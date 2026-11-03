package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/server/routes"
	"github.com/Wei-Shaw/sub2api/internal/service"
	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type piAuditCipher struct{}

func (piAuditCipher) Encrypt(string) (string, error) { return "cipher-canary", nil }
func (piAuditCipher) Decrypt(string) (string, error) { return `{"marker":"config-canary"}`, nil }

type piAuditRepo struct {
	service.PluginInstanceRepository
	s                 service.PluginInstanceSnapshot
	fail, observeFail error
	writes            int
	snapshotReads     int
	waitSnapshot      bool
}

func newPIAuditRepo() *piAuditRepo {
	return &piAuditRepo{s: service.PluginInstanceSnapshot{
		Instance:     service.PluginInstance{ID: 11, PluginID: 3, Name: "name-canary", Generation: 4, CurrentRevision: 2, LatestRevision: 3},
		Revision:     service.PluginInstanceRevision{InstanceID: 11, Revision: 2, ConfigVersion: 1, ConfigEncrypted: "cipher-canary"},
		Installation: service.PluginInstallation{InstallPath: "install-path-canary", ArtifactPath: "artifact-path-canary", ArtifactData: []byte("artifact-canary"), Manifest: service.PluginManifest{SchemaVersion: 2, General: &service.PluginGeneralManifest{ConfigVersion: 1, Hooks: []service.PluginHookEntry{{Name: "inspect", Stages: []contract.Stage{contract.Input}, Protocols: []string{"example.text.v1"}}}}}},
	}}
}
func (r *piAuditRepo) Snapshot(ctx context.Context, id, revision int64) (*service.PluginInstanceSnapshot, error) {
	r.snapshotReads++
	if r.waitSnapshot {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if r.observeFail != nil {
		return nil, r.observeFail
	}
	if id != 11 {
		return nil, service.ErrPluginInstanceNotFound
	}
	s := r.s
	if revision > 0 {
		s.Revision.Revision = revision
	}
	return &s, nil
}
func (r *piAuditRepo) Create(context.Context, int64, string, string) (*service.PluginInstanceSnapshot, error) {
	r.writes++
	if r.fail != nil {
		return nil, r.fail
	}
	r.s.Instance.Generation = 1
	r.s.Instance.CurrentRevision = 1
	r.s.Revision.Revision = 1
	return &r.s, nil
}
func (r *piAuditRepo) SaveRevision(context.Context, int64, int64, string) (*service.PluginInstanceSnapshot, error) {
	r.writes++
	if r.fail != nil {
		return nil, r.fail
	}
	r.s.Instance.Generation = 5
	r.s.Revision.Revision = 4
	return &r.s, nil
}
func (r *piAuditRepo) Publish(_ context.Context, _ int64, _ int64, revision int64, enabled bool) (*service.PluginInstanceSnapshot, error) {
	r.writes++
	if r.fail != nil {
		return nil, r.fail
	}
	r.s.Instance.Generation = 5
	r.s.Instance.Enabled = enabled
	r.s.Revision.Revision = revision
	return &r.s, nil
}
func (r *piAuditRepo) ReplaceBindings(context.Context, int64, int64, []service.PluginGroupBinding) (*service.PluginInstanceSnapshot, error) {
	r.writes++
	if r.fail != nil {
		return nil, r.fail
	}
	r.s.Instance.Generation = 5
	return &r.s, nil
}
func (r *piAuditRepo) Delete(context.Context, int64, int64) error { r.writes++; return r.fail }

type piAuditCapture struct {
	mu   sync.Mutex
	logs []*service.AuditLog
}

func (r *piAuditCapture) BatchInsert(_ context.Context, logs []*service.AuditLog) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, logs...)
	return int64(len(logs)), nil
}
func (r *piAuditCapture) Insert(ctx context.Context, e *service.AuditLog) error {
	_, err := r.BatchInsert(ctx, []*service.AuditLog{e})
	return err
}
func (r *piAuditCapture) List(context.Context, *service.AuditLogFilter) (*service.AuditLogList, error) {
	return &service.AuditLogList{}, nil
}
func (r *piAuditCapture) GetByID(context.Context, int64) (*service.AuditLog, error) {
	return nil, service.ErrAuditLogNotFound
}
func (r *piAuditCapture) Count(context.Context) (int64, error)                        { return 0, nil }
func (r *piAuditCapture) TruncateAll(context.Context) error                           { return nil }
func (r *piAuditCapture) DeleteBefore(context.Context, time.Time, int) (int64, error) { return 0, nil }

type piAuditPermission struct{ allow bool }

func (s *piAuditPermission) HasPermission(context.Context, int64, string) (bool, error) {
	return s.allow, nil
}

type piAuditFixture struct {
	router      *gin.Engine
	repo        *piAuditRepo
	capture     *piAuditCapture
	audit       *service.AuditLogService
	instances   *service.PluginInstanceService
	permission  *piAuditPermission
	stepAllowed bool
}

func newPIAuditFixture(t *testing.T) *piAuditFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	f := &piAuditFixture{repo: newPIAuditRepo(), capture: &piAuditCapture{}, permission: &piAuditPermission{allow: true}, stepAllowed: true}
	f.instances = service.NewPluginInstanceService(f.repo, piAuditCipher{}, nil, time.Second)
	f.audit = service.NewAuditLogService(f.capture, nil)
	f.audit.Start()
	t.Cleanup(f.audit.Stop)
	t.Cleanup(f.instances.Close)
	f.router = gin.New()
	auth := middleware.AdminAuthMiddleware(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 77})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
		c.Set(middleware.ContextKeyAuthEmail, "operator@example.com")
		c.Next()
	})
	step := middleware.StepUpAuthMiddleware(func(c *gin.Context) {
		if !f.stepAllowed {
			c.AbortWithStatus(428)
			return
		}
		c.Next()
	})
	h := &handler.Handlers{Admin: &handler.AdminHandlers{Account: &adminhandler.AccountHandler{}, PluginInstance: adminhandler.NewPluginInstanceHandler(f.instances)}}
	routes.RegisterAdminRoutes(f.router.Group("/api/v1"), h, auth, middleware.NewAuditLogMiddleware(f.audit), step, nil, nil, f.permission)
	return f
}
func (f *piAuditFixture) request(t *testing.T, method, path, body string, status int) *service.AuditLog {
	t.Helper()
	req := httptest.NewRequest(method, "/api/v1/admin"+path+"?note=query-canary", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	require.Equal(t, status, w.Code, w.Body.String())
	f.audit.Stop()
	f.capture.mu.Lock()
	defer f.capture.mu.Unlock()
	require.Len(t, f.capture.logs, 1)
	entry := f.capture.logs[0]
	require.EqualValues(t, 77, *entry.ActorUserID)
	require.Equal(t, "operator@example.com", entry.ActorEmail)
	require.Equal(t, "<credential-bearing body omitted>", entry.RequestBody)
	data, err := json.Marshal(entry)
	require.NoError(t, err)
	for _, secret := range []string{"config-canary", "cipher-canary", "name-canary", "install-path-canary", "artifact-path-canary", "artifact-canary", "query-canary", "raw-id-canary", "unknown-field-canary"} {
		require.NotContains(t, string(data), secret)
	}
	require.NotContains(t, entry.Extra, "params")
	require.NotContains(t, entry.Extra, "query")
	require.EqualValues(t, status, entry.Extra["http_status"])
	return entry
}

var piAuditOperations = []struct {
	name, method, path, body, action string
	status                           int
	newGen, revision                 int64
}{
	{"create", "POST", "/plugin-instances", `{"plugin_id":3,"name":"name-canary","config":{"marker":"config-canary"}}`, "admin.plugin_instances.create", 201, 1, 1},
	{"config", "PUT", "/plugin-instances/11/config", `{"expected_generation":4,"config":{"marker":"config-canary"}}`, "admin.plugin_instances.config.update", 200, 5, 4},
	{"disable", "POST", "/plugin-instances/11/disable", `{"expected_generation":4}`, "admin.plugin_instances.disable", 200, 5, 2},
	{"bindings", "PUT", "/plugin-instances/11/bindings", `{"expected_generation":4,"bindings":[]}`, "admin.plugin_instances.bindings.update", 200, 5, 2},
	{"delete", "DELETE", "/plugin-instances/11", `{"expected_generation":4}`, "admin.plugin_instances.delete", 200, 0, 0},
	{"publish", "POST", "/plugin-instances/11/publish", `{"expected_generation":4,"revision":1}`, "admin.plugin_instances.publish", 422, 0, 1},
}

func TestPluginInstanceAuditHandlersRecordStructuredOutcomes(t *testing.T) {
	for _, tc := range piAuditOperations {
		t.Run(tc.name, func(t *testing.T) {
			f := newPIAuditFixture(t)
			entry := f.request(t, tc.method, tc.path, tc.body, tc.status)
			require.Equal(t, tc.action, entry.Action)
			require.EqualValues(t, 11, entry.Extra["instance_id"])
			if tc.newGen > 0 {
				require.EqualValues(t, 3, entry.Extra["plugin_id"])
			} else {
				require.NotContains(t, entry.Extra, "plugin_id")
			}
			if tc.revision > 0 {
				require.EqualValues(t, tc.revision, entry.Extra["revision"])
			} else {
				require.NotContains(t, entry.Extra, "revision")
			}
			if tc.name != "create" {
				require.EqualValues(t, 4, entry.Extra["expected_generation"])
				require.NotContains(t, entry.Extra, "observed_generation")
			}
			if tc.newGen > 0 {
				require.EqualValues(t, tc.newGen, entry.Extra["new_generation"])
			} else {
				require.NotContains(t, entry.Extra, "new_generation")
			}
			if tc.status < 400 {
				require.Equal(t, "success", entry.Extra["result"])
				require.Equal(t, "OK", entry.Extra["error_code"])
			} else {
				require.Equal(t, "failed", entry.Extra["result"])
				require.Equal(t, "PLUGIN_INSTANCE_UNAVAILABLE", entry.Extra["error_code"])
			}
			if tc.name == "delete" {
				require.Equal(t, true, entry.Extra["deleted"])
			}
		})
	}
}
func TestPluginInstanceAuditHandlersDoNotInventSuccessfulGenerations(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		status   int
		code     string
		conflict bool
	}{
		{"conflict", service.ErrPluginInstanceConflict, 409, "PLUGIN_INSTANCE_CONFLICT", true},
		{"missing", service.ErrPluginInstanceNotFound, 404, "PLUGIN_INSTANCE_NOT_FOUND", false},
		{"invalid", service.ErrPluginInstanceInvalid, 400, "PLUGIN_INSTANCE_INVALID", false},
		{"private", errors.New("install-path-canary cipher-canary config-canary"), 503, "PLUGIN_INSTANCE_OPERATION_UNAVAILABLE", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPIAuditFixture(t)
			f.repo.fail = tc.err
			var logs bytes.Buffer
			old := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(old)
			entry := f.request(t, "PUT", "/plugin-instances/11/config", `{"expected_generation":4,"config":{"marker":"config-canary"}}`, tc.status)
			require.Equal(t, "failed", entry.Extra["result"])
			require.Equal(t, tc.code, entry.Extra["error_code"])
			require.Equal(t, tc.conflict, entry.Extra["conflict"])
			require.NotContains(t, entry.Extra, "new_generation")
			require.EqualValues(t, 4, entry.Extra["expected_generation"])
			for _, secret := range []string{"install-path-canary", "cipher-canary", "config-canary"} {
				require.NotContains(t, logs.String(), secret)
			}
		})
	}
}
func TestPluginInstanceAuditRejectedRequestsNeverPersistUnvalidatedInput(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body string
		status                   int
	}{
		{"missing-create-config", "POST", "/plugin-instances", `{"plugin_id":3,"name":"name-canary"}`, 400},
		{"missing-save-config", "PUT", "/plugin-instances/11/config", `{"expected_generation":4}`, 400},
		{"raw-id", "DELETE", "/plugin-instances/raw-id-canary", `{"expected_generation":4}`, 400},
		{"unknown-field", "POST", "/plugin-instances/11/disable", `{"expected_generation":4,"unsafe":"unknown-field-canary"}`, 400},
		{"invalid-json", "PUT", "/plugin-instances/11/bindings", `{"secret":"config-canary"`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPIAuditFixture(t)
			entry := f.request(t, tc.method, tc.path, tc.body, tc.status)
			require.Equal(t, "failed", entry.Extra["result"])
			require.NotContains(t, entry.Extra, "new_generation")
			require.Zero(t, f.repo.writes)
			if tc.name == "missing-create-config" {
				require.EqualValues(t, 3, entry.Extra["plugin_id"])
			}
			if tc.name == "missing-save-config" {
				require.EqualValues(t, 4, entry.Extra["expected_generation"])
			}
		})
	}
}
func TestPluginInstanceAuditDenialsAndClosedService(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{{"permission", 403}, {"step-up", 428}, {"closed", 503}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPIAuditFixture(t)
			if tc.name == "permission" {
				f.permission.allow = false
			}
			if tc.name == "step-up" {
				f.stepAllowed = false
			}
			if tc.name == "closed" {
				f.instances.Close()
			}
			entry := f.request(t, "DELETE", "/plugin-instances/11", `{"expected_generation":4}`, tc.status)
			require.Equal(t, "admin.plugin_instances.delete", entry.Action)
			require.Equal(t, "failed", entry.Extra["result"])
			require.EqualValues(t, 11, entry.Extra["instance_id"])
			require.NotContains(t, entry.Extra, "new_generation")
			require.Zero(t, f.repo.writes)
			if tc.name != "closed" {
				require.NotContains(t, entry.Extra, "expected_generation")
			}
		})
	}
}
func TestPluginInstanceAuditReadStatusRemainsUnaudited(t *testing.T) {
	f := newPIAuditFixture(t)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/admin/plugin-instances/11/status", nil))
	require.Equal(t, 200, w.Code)
	f.audit.Stop()
	require.Empty(t, f.capture.logs)
	require.Zero(t, f.repo.writes)
}

func (r *piAuditRepo) List(context.Context, int64) ([]service.PluginInstance, error) {
	return nil, r.fail
}
func (r *piAuditRepo) RevisionSummaries(context.Context, int64) ([]service.PluginInstanceRevisionSummary, error) {
	return nil, r.fail
}

func TestPluginInstanceAuditDoesNotAddObservationIOOrConsumeWriteDeadline(t *testing.T) {
	for _, tc := range []struct{ method, path, body string }{
		{"PUT", "/plugin-instances/11/config", `{"expected_generation":4,"config":{}}`},
		{"DELETE", "/plugin-instances/11", `{"expected_generation":4}`},
	} {
		t.Run(tc.method, func(t *testing.T) {
			f := newPIAuditFixture(t)
			f.repo.waitSnapshot = true
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			req := httptest.NewRequest(tc.method, "/api/v1/admin"+tc.path, bytes.NewBufferString(tc.body)).WithContext(ctx)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			f.router.ServeHTTP(w, req)
			require.Equal(t, 200, w.Code, w.Body.String())
			require.Equal(t, 1, f.repo.writes)
			require.Zero(t, f.repo.snapshotReads, "audit must not add repository reads or acquire service gate")
		})
	}
}
func TestPluginInstanceAuditPreservesReadErrorStatusesWithoutRawDetails(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		reason string
	}{
		{"unknown", errors.New("ordinary-read-failure"), 500, "PLUGIN_INSTANCE_READ_FAILED"},
		{"application", infraerrors.Conflict("READ_CONFLICT", "read conflict"), 409, "PLUGIN_INSTANCE_READ_FAILED"},
	} {
		for _, path := range []string{"/plugin-instances", "/plugin-instances/11", "/plugin-instances/11/revisions"} {
			t.Run(tc.name+path, func(t *testing.T) {
				f := newPIAuditFixture(t)
				f.repo.fail = tc.err
				f.repo.observeFail = tc.err
				w := httptest.NewRecorder()
				f.router.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/admin"+path, nil))
				require.Equal(t, tc.status, w.Code, w.Body.String())
				require.Contains(t, w.Body.String(), tc.reason)
				require.NotContains(t, w.Body.String(), "ordinary-read-failure")
				require.NotContains(t, w.Body.String(), "READ_CONFLICT")
				f.audit.Stop()
				require.Empty(t, f.capture.logs)
			})
		}
	}
}
