package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/server/routes"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type piHTTPRepo struct{ *piAuditRepo }

func (r *piHTTPRepo) Snapshot(ctx context.Context, id, revision int64) (*service.PluginInstanceSnapshot, error) {
	if r.fail != nil {
		return nil, r.fail
	}
	if revision != 0 && revision != 1 && revision != 2 {
		return nil, service.ErrPluginInstanceNotFound
	}
	return r.piAuditRepo.Snapshot(ctx, id, revision)
}
func (r *piHTTPRepo) Create(ctx context.Context, id int64, name, cipher string) (*service.PluginInstanceSnapshot, error) {
	if id <= 0 || strings.TrimSpace(name) == "" {
		return nil, service.ErrPluginInstanceInvalid
	}
	return r.piAuditRepo.Create(ctx, id, name, cipher)
}
func (r *piHTTPRepo) check(id, gen int64) error {
	if id <= 0 || gen <= 0 {
		return service.ErrPluginInstanceInvalid
	}
	if id != 11 {
		return service.ErrPluginInstanceNotFound
	}
	if gen != r.s.Instance.Generation {
		return service.ErrPluginInstanceConflict
	}
	return nil
}
func (r *piHTTPRepo) SaveRevision(ctx context.Context, id, gen int64, cipher string) (*service.PluginInstanceSnapshot, error) {
	if err := r.check(id, gen); err != nil {
		return nil, err
	}
	return r.piAuditRepo.SaveRevision(ctx, id, gen, cipher)
}
func (r *piHTTPRepo) Delete(ctx context.Context, id, gen int64) error {
	if err := r.check(id, gen); err != nil {
		return err
	}
	if r.s.Instance.Enabled {
		return service.ErrPluginInstanceEnabled
	}
	return r.piAuditRepo.Delete(ctx, id, gen)
}
func (r *piHTTPRepo) ReplaceBindings(ctx context.Context, id, gen int64, b []service.PluginGroupBinding) (*service.PluginInstanceSnapshot, error) {
	if err := r.check(id, gen); err != nil {
		return nil, err
	}
	return r.piAuditRepo.ReplaceBindings(ctx, id, gen, b)
}
func (r *piHTTPRepo) Publish(ctx context.Context, id, gen, revision int64, enabled bool) (*service.PluginInstanceSnapshot, error) {
	if err := r.check(id, gen); err != nil {
		return nil, err
	}
	return r.piAuditRepo.Publish(ctx, id, gen, revision, enabled)
}

type piHTTPUsers struct {
	service.UserRepository
	enabled bool
	err     error
}

func (r *piHTTPUsers) GetByID(context.Context, int64) (*service.User, error) {
	return &service.User{ID: 77, TotpEnabled: r.enabled}, r.err
}
func (*piHTTPUsers) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

type piHTTPGrants struct {
	service.TotpCache
	allowed bool
	err     error
	calls   int
	user    int64
	session string
}

func (s *piHTTPGrants) HasStepUpGrant(_ context.Context, id int64, key string) (bool, error) {
	s.calls++
	s.user = id
	s.session = key
	return s.allowed, s.err
}

type piHTTPPermission struct {
	allowed bool
	err     error
	seen    []string
}

func (s *piHTTPPermission) HasPermission(_ context.Context, _ int64, code string) (bool, error) {
	s.seen = append(s.seen, code)
	return s.allowed, s.err
}

type piHTTPFixture struct {
	router        *gin.Engine
	repo          *piHTTPRepo
	users         *piHTTPUsers
	grants        *piHTTPGrants
	permission    *piHTTPPermission
	instances     *service.PluginInstanceService
	audit         *service.AuditLogService
	capture       *piAuditCapture
	authenticated bool
	authMethod    string
}

func newPIHTTPFixture(t *testing.T) *piHTTPFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	f := &piHTTPFixture{repo: &piHTTPRepo{newPIAuditRepo()}, users: &piHTTPUsers{enabled: true}, grants: &piHTTPGrants{allowed: true}, permission: &piHTTPPermission{allowed: true}, capture: &piAuditCapture{}, authenticated: true, authMethod: service.AuditAuthMethodJWT}
	f.instances = service.NewPluginInstanceService(f.repo, piAuditCipher{}, nil, time.Second)
	f.audit = service.NewAuditLogService(f.capture, nil)
	f.audit.Start()
	t.Cleanup(f.audit.Stop)
	t.Cleanup(f.instances.Close)
	f.router = gin.New()
	missingAuth := middleware.NewAdminAuthMiddleware(nil, nil, nil, nil, nil)
	auth := middleware.AdminAuthMiddleware(func(c *gin.Context) {
		if !f.authenticated {
			gin.HandlerFunc(missingAuth)(c)
			return
		}
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 77})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
		c.Set(middleware.ContextKeyAuthEmail, "http-operator@example.com")
		c.Set("auth_method", f.authMethod)
		c.Set(middleware.ContextKeySessionID, "pi-http-session")
		c.Next()
	})
	users := service.NewUserService(f.users, nil, nil, nil)
	totp := service.NewTotpService(f.users, nil, f.grants, nil, nil, nil)
	step := middleware.NewStepUpAuthMiddleware(totp, users, nil)
	h := &handler.Handlers{Admin: &handler.AdminHandlers{Account: &adminhandler.AccountHandler{}, PluginInstance: adminhandler.NewPluginInstanceHandler(f.instances)}}
	routes.RegisterAdminRoutes(f.router.Group("/api/v1"), h, auth, middleware.NewAuditLogMiddleware(f.audit), step, nil, nil, f.permission)
	return f
}

type piHTTPOperation struct {
	name, method, path, body, permission, action string
	success                                      int
}

var piHTTPOperations = []piHTTPOperation{
	{"list", "GET", "/plugin-instances", "", "plugin_instances.read", "", 200},
	{"get", "GET", "/plugin-instances/11", "", "plugin_instances.read", "", 200},
	{"revisions", "GET", "/plugin-instances/11/revisions", "", "plugin_instances.read", "", 200},
	{"status", "GET", "/plugin-instances/11/status", "", "plugin_instances.read", "", 200},
	{"create", "POST", "/plugin-instances", `{"plugin_id":3,"name":"http-test","config":{"marker":"config-canary"}}`, "plugin_instances.create", middleware.AuditActionPluginInstanceCreate, 201},
	{"config", "PUT", "/plugin-instances/11/config", `{"expected_generation":4,"config":{"marker":"config-canary"}}`, "plugin_instances.update", middleware.AuditActionPluginInstanceConfig, 200},
	{"publish", "POST", "/plugin-instances/11/publish", `{"expected_generation":4,"revision":1}`, "plugin_instances.publish", middleware.AuditActionPluginInstancePublish, 422},
	{"disable", "POST", "/plugin-instances/11/disable", `{"expected_generation":4}`, "plugin_instances.disable", middleware.AuditActionPluginInstanceDisable, 200},
	{"bindings", "PUT", "/plugin-instances/11/bindings", `{"expected_generation":4,"bindings":[]}`, "plugin_instances.bindings.update", middleware.AuditActionPluginInstanceBindings, 200},
	{"delete", "DELETE", "/plugin-instances/11", `{"expected_generation":4}`, "plugin_instances.delete", middleware.AuditActionPluginInstanceDelete, 200},
}

func (f *piHTTPFixture) request(t *testing.T, op piHTTPOperation, status int, reason string) {
	t.Helper()
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	separator := "?"
	if strings.Contains(op.path, "?") {
		separator = "&"
	}
	req := httptest.NewRequest(op.method, "/api/v1/admin"+op.path+separator+"note=query-canary", bytes.NewBufferString(op.body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	f.audit.Stop()
	require.Equal(t, status, w.Code, w.Body.String())
	if reason != "" {
		require.Contains(t, w.Body.String(), `"`+reason+`"`)
	}
	for _, secret := range []string{"config-canary", "cipher-canary", "install-path-canary", "artifact-path-canary", "private-error-canary", "private-reason-canary", "private-metadata-canary", "config_encrypted", "install_path", "artifact_data"} {
		require.NotContains(t, w.Body.String(), secret)
		require.NotContains(t, logs.String(), secret)
	}
	if op.action == "" || !f.authenticated {
		require.Empty(t, f.capture.logs)
	} else {
		require.Len(t, f.capture.logs, 1)
		entry := f.capture.logs[0]
		require.Equal(t, op.action, entry.Action)
		require.EqualValues(t, status, entry.Extra["http_status"])
		require.Equal(t, "<credential-bearing body omitted>", entry.RequestBody)
		encoded, err := json.Marshal(entry)
		require.NoError(t, err)
		for _, secret := range []string{"config-canary", "cipher-canary", "install-path-canary", "private-error-canary", "query-canary", "raw-id-canary"} {
			require.NotContains(t, string(encoded), secret)
		}
		if status >= 400 {
			require.Equal(t, "failed", entry.Extra["result"])
			require.NotContains(t, entry.Extra, "new_generation")
			require.NotContains(t, entry.Extra, "deleted")
		}
	}
	require.Equal(t, []string{op.permission}, f.permission.seen)
}
func TestPluginInstanceHTTPProductionBoundaryMatrix(t *testing.T) {
	for _, op := range piHTTPOperations {
		for _, boundary := range []string{"success", "unauthenticated", "permission", "closed", "step-up", "totp-disabled", "machine-key", "grant-unavailable"} {
			if op.action == "" && boundary != "success" && boundary != "unauthenticated" && boundary != "permission" && boundary != "closed" {
				continue
			}
			t.Run(op.name+"/"+boundary, func(t *testing.T) {
				f := newPIHTTPFixture(t)
				status, reason := op.success, ""
				switch boundary {
				case "unauthenticated":
					f.authenticated = false
					w := httptest.NewRecorder()
					f.router.ServeHTTP(w, httptest.NewRequest(op.method, "/api/v1/admin"+op.path, bytes.NewBufferString(op.body)))
					f.audit.Stop()
					require.Equal(t, 401, w.Code)
					require.Contains(t, w.Body.String(), "UNAUTHORIZED")
					require.Empty(t, f.permission.seen)
					require.Zero(t, f.grants.calls)
					require.Zero(t, f.repo.writes)
					require.Empty(t, f.capture.logs)
					return
				case "permission":
					f.permission.allowed = false
					status, reason = 403, "FORBIDDEN"
				case "closed":
					f.instances.Close()
					status, reason = 503, "PLUGIN_INSTANCE_UNAVAILABLE"
				case "step-up":
					f.grants.allowed = false
					status, reason = 403, "STEP_UP_REQUIRED"
				case "totp-disabled":
					f.users.enabled = false
					status, reason = 403, "STEP_UP_TOTP_NOT_ENABLED"
				case "machine-key":
					f.authMethod = service.AuditAuthMethodAdminAPIKey
					status, reason = 403, "STEP_UP_ADMIN_API_KEY_FORBIDDEN"
				case "grant-unavailable":
					f.grants.err = errors.New("private-error-canary")
					status, reason = 503, "STEP_UP_UNAVAILABLE"
				}
				f.request(t, op, status, reason)
				if boundary != "success" {
					require.Zero(t, f.repo.writes)
					require.Zero(t, f.repo.snapshotReads)
				}
				if op.action == "" || boundary == "permission" || boundary == "totp-disabled" || boundary == "machine-key" {
					require.Zero(t, f.grants.calls)
				} else {
					require.Equal(t, 1, f.grants.calls)
					require.EqualValues(t, 77, f.grants.user)
					require.Equal(t, "pi-http-session", f.grants.session)
				}
			})
		}
	}
}
func TestPluginInstanceHTTPInvalidRequestsMatrix(t *testing.T) {
	for _, op := range piHTTPOperations {
		if op.action != "" {
			for _, body := range []string{"{", op.body + " {}", `{"unknown":"private-error-canary"}`} {
				t.Run(op.name+"/json/"+fmt.Sprint(len(body)), func(t *testing.T) {
					f := newPIHTTPFixture(t)
					op := op
					op.body = body
					f.request(t, op, 400, "PLUGIN_INSTANCE_REQUEST_INVALID")
					require.Zero(t, f.repo.writes)
				})
			}
		}
		if op.name != "list" && op.name != "create" {
			for _, id := range []string{"0", "-1", "9223372036854775808", "raw-id-canary"} {
				t.Run(op.name+"/id/"+id, func(t *testing.T) {
					f := newPIHTTPFixture(t)
					op := op
					op.path = strings.Replace(op.path, "/11", "/"+id, 1)
					f.request(t, op, 400, "PLUGIN_INSTANCE_ID_INVALID")
					require.Zero(t, f.repo.writes)
				})
			}
		}
	}
	for _, tc := range []struct {
		index int
		body  string
	}{{4, `{"plugin_id":3,"name":"http-test"}`}, {5, `{"expected_generation":4}`}, {4, `{"plugin_id":3,"name":"http-test","config":null}`}, {5, `{"expected_generation":4,"config":[]}`}} {
		t.Run(fmt.Sprintf("config/%d/%s", tc.index, tc.body), func(t *testing.T) {
			f := newPIHTTPFixture(t)
			op := piHTTPOperations[tc.index]
			op.body = tc.body
			f.request(t, op, 400, "")
			require.Zero(t, f.repo.writes)
		})
	}
	for _, raw := range []string{"-1", "9223372036854775808", "invalid"} {
		t.Run("filter/"+raw, func(t *testing.T) {
			f := newPIHTTPFixture(t)
			op := piHTTPOperations[0]
			op.path += "?plugin_id=" + raw
			f.request(t, op, 400, "PLUGIN_INSTANCE_PLUGIN_ID_INVALID")
		})
	}
}
func TestPluginInstanceHTTPMissingAndCASMatrix(t *testing.T) {
	for _, op := range piHTTPOperations {
		if op.name != "list" && op.name != "create" {
			t.Run(op.name+"/missing-instance", func(t *testing.T) {
				f := newPIHTTPFixture(t)
				op := op
				op.path = strings.Replace(op.path, "/11", "/999", 1)
				f.request(t, op, 404, "PLUGIN_INSTANCE_NOT_FOUND")
				require.Zero(t, f.repo.writes)
			})
		}
		if op.action != "" && op.name != "create" {
			t.Run(op.name+"/stale", func(t *testing.T) {
				f := newPIHTTPFixture(t)
				op := op
				op.body = strings.Replace(op.body, `"expected_generation":4`, `"expected_generation":3`, 1)
				f.request(t, op, 409, "PLUGIN_INSTANCE_CONFLICT")
				require.Zero(t, f.repo.writes)
				require.Equal(t, true, f.capture.logs[0].Extra["conflict"])
			})
		}
	}
	t.Run("missing-revision", func(t *testing.T) {
		f := newPIHTTPFixture(t)
		op := piHTTPOperations[6]
		op.body = `{"expected_generation":4,"revision":999}`
		f.request(t, op, 404, "PLUGIN_INSTANCE_NOT_FOUND")
		require.Zero(t, f.repo.writes)
	})
	t.Run("enabled-delete", func(t *testing.T) {
		f := newPIHTTPFixture(t)
		f.repo.s.Instance.Enabled = true
		f.request(t, piHTTPOperations[9], 409, "PLUGIN_INSTANCE_ENABLED")
		require.Zero(t, f.repo.writes)
	})
	t.Run("busy-delete", func(t *testing.T) {
		f := newPIHTTPFixture(t)
		f.repo.fail = service.ErrPluginInstanceBusy
		f.request(t, piHTTPOperations[9], 409, "PLUGIN_INSTANCE_BUSY")
		require.False(t, f.capture.logs[0].Extra["conflict"].(bool))
	})
}
func TestPluginInstanceHTTPPrivateFailureMatrix(t *testing.T) {
	for _, op := range piHTTPOperations {
		for _, app := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/app=%t", op.name, app), func(t *testing.T) {
				f := newPIHTTPFixture(t)
				f.repo.fail = errors.New("private-error-canary cipher-canary install-path-canary config-canary")
				if app {
					f.repo.fail = infraerrors.Conflict("private-reason-canary", "private-error-canary").WithMetadata(map[string]string{"secret": "private-metadata-canary"})
				}
				status, reason := 503, "PLUGIN_INSTANCE_OPERATION_UNAVAILABLE"
				if op.action == "" {
					status, reason = 500, "PLUGIN_INSTANCE_READ_FAILED"
					if app {
						status = 409
					}
					if op.name == "status" {
						status, reason = 503, "PLUGIN_INSTANCE_STATUS_UNAVAILABLE"
					}
				}
				if op.name == "delete" {
					reason = "PLUGIN_INSTANCE_DELETE_UNAVAILABLE"
				}
				f.request(t, op, status, reason)
			})
		}
	}
	t.Run("closed-priority", func(t *testing.T) {
		f := newPIHTTPFixture(t)
		f.repo.fail = errors.Join(service.ErrPluginInstanceUnavailable, service.ErrGeneralClosed, errors.New("private-error-canary"))
		f.request(t, piHTTPOperations[5], 503, "PLUGIN_INSTANCE_UNAVAILABLE")
	})
}

func (r *piHTTPRepo) RevisionSummaries(ctx context.Context, id int64) ([]service.PluginInstanceRevisionSummary, error) {
	if id != 11 {
		return nil, service.ErrPluginInstanceNotFound
	}
	return r.piAuditRepo.RevisionSummaries(ctx, id)
}
func TestPluginInstanceHTTPInvalidPublishArguments(t *testing.T) {
	for _, body := range []string{`{"expected_generation":0,"revision":1}`, `{"expected_generation":-1,"revision":1}`, `{"expected_generation":4,"revision":0}`, `{"expected_generation":4,"revision":-1}`, `{"expected_generation":4}`} {
		t.Run(body, func(t *testing.T) {
			f := newPIHTTPFixture(t)
			op := piHTTPOperations[6]
			op.body = body
			f.request(t, op, 400, "PLUGIN_INSTANCE_INVALID")
			require.Zero(t, f.repo.snapshotReads)
			require.Zero(t, f.repo.writes)
		})
	}
}

func TestPluginInstanceHTTPWrappedApplicationErrorDoesNotLeak5xxDetails(t *testing.T) {
	for _, op := range piHTTPOperations {
		t.Run(op.name, func(t *testing.T) {
			f := newPIHTTPFixture(t)
			cause := errors.New("cipher-canary install-path-canary config-canary")
			app := infraerrors.ServiceUnavailable("private-reason-canary", "private-error-canary").WithMetadata(map[string]string{"secret": "private-metadata-canary"}).WithCause(cause)
			f.repo.fail = fmt.Errorf("private-error-canary wrapper: %w", app)
			reason := "PLUGIN_INSTANCE_OPERATION_UNAVAILABLE"
			if op.action == "" {
				reason = "PLUGIN_INSTANCE_READ_FAILED"
			}
			if op.name == "status" {
				reason = "PLUGIN_INSTANCE_STATUS_UNAVAILABLE"
			}
			if op.name == "delete" {
				reason = "PLUGIN_INSTANCE_DELETE_UNAVAILABLE"
			}
			f.request(t, op, 503, reason)
		})
	}
}
