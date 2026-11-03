//go:build integration

package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/server/routes"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type piHTTPIntegrationUsers struct{ service.UserRepository }

func (piHTTPIntegrationUsers) GetByID(context.Context, int64) (*service.User, error) {
	return &service.User{ID: 41, TotpEnabled: true}, nil
}
func (piHTTPIntegrationUsers) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

type piHTTPIntegrationGrants struct {
	service.TotpCache
	allowed bool
	session string
}

func (s *piHTTPIntegrationGrants) HasStepUpGrant(_ context.Context, _ int64, key string) (bool, error) {
	s.session = key
	return s.allowed, nil
}

// Real routes, permission/step-up middleware, Totp/User services, audit queue,
// PostgreSQL and child processes. Identity/user/grant storage remain fixtures.
func TestPluginInstanceHTTPRuntimeErrorMatrixIntegration(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{}
	cfg.Totp.EncryptionKey = strings.Repeat("b", 64)
	cipher, err := NewAESEncryptor(cfg)
	require.NoError(t, err)
	p := piAuditRealInstallation(t, cfg)
	repo := NewPluginInstanceRepository(integrationDB)
	manager := service.NewPluginManager(NewPluginRepository(integrationDB), cipher, cfg, service.PluginHostInfo{Version: "0.1.179"}, nil)
	instances := service.NewPluginInstanceService(repo, cipher, manager, time.Second)
	defer instances.Close()
	auditRepo := NewAuditLogRepository(integrationDB)
	audit := service.NewAuditLogService(auditRepo, nil)
	audit.Start()
	defer audit.Stop()
	checker := &piDeleteIntegrationChecker{permission: "none"}
	grants := &piHTTPIntegrationGrants{}
	users := piHTTPIntegrationUsers{}
	step := middleware.NewStepUpAuthMiddleware(service.NewTotpService(users, nil, grants, nil, nil, nil), service.NewUserService(users, nil, nil, nil), nil)
	email := fmt.Sprintf("http-matrix-%d@example.com", time.Now().UnixNano())
	prefix := fmt.Sprintf("http-matrix-%d", time.Now().UnixNano())
	gin.SetMode(gin.TestMode)
	router := gin.New()
	auth := middleware.AdminAuthMiddleware(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 41})
		c.Set(string(middleware.ContextKeyUserRole), "admin")
		c.Set(middleware.ContextKeyAuthEmail, email)
		c.Set("auth_method", service.AuditAuthMethodJWT)
		c.Set(middleware.ContextKeySessionID, "pg-http-session")
		c.Next()
	})
	h := &handler.Handlers{Admin: &handler.AdminHandlers{PluginInstance: adminhandler.NewPluginInstanceHandler(instances)}}
	routes.RegisterAdminRoutes(router.Group("/api/v1", nil), h, auth, middleware.NewAuditLogMiddleware(audit), step, nil, nil, piAuditIntegrationPermission{checker: checker})
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	ciphertexts := []string{}
	type expectedEntry struct {
		id, action string
		status     int
	}
	expected := []expectedEntry{}
	request := func(method, path, body, action string, status int, reason string) *httptest.ResponseRecorder {
		t.Helper()
		id := prefix + "-" + strconv.Itoa(len(expected))
		req := httptest.NewRequest(method, "/api/v1/admin"+path+"?note=query-http-canary", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req = req.WithContext(context.WithValue(req.Context(), ctxkey.RequestID, id))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, status, w.Code, w.Body.String())
		if reason != "" {
			require.Contains(t, w.Body.String(), `"`+reason+`"`)
		}
		for _, secret := range append(append([]string{}, ciphertexts...), "http-pg-private-config-canary", p.InstallPath, p.ArtifactPath, "config_encrypted", "install_path") {
			require.NotContains(t, w.Body.String(), secret)
		}
		if action != "" {
			expected = append(expected, expectedEntry{id, action, status})
		}
		return w
	}
	decode := func(w *httptest.ResponseRecorder) service.PluginInstance {
		t.Helper()
		var v struct {
			Data struct {
				Instance service.PluginInstance `json:"instance"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))
		return v.Data.Instance
	}
	create := fmt.Sprintf(`{"plugin_id":%d,"name":"pg-http","config":{"marker":"http-pg-private-config-canary"}}`, p.ID)
	request("POST", "/plugin-instances", create, middleware.AuditActionPluginInstanceCreate, 403, "FORBIDDEN")
	checker.permission = "all"
	request("POST", "/plugin-instances", create, middleware.AuditActionPluginInstanceCreate, 403, "STEP_UP_REQUIRED")
	rows, err := repo.List(ctx, p.ID)
	require.NoError(t, err)
	require.Empty(t, rows)
	grants.allowed = true
	request("POST", "/plugin-instances", `{"bad":"http-pg-private-config-canary"}`, middleware.AuditActionPluginInstanceCreate, 400, "PLUGIN_INSTANCE_REQUEST_INVALID")
	created := decode(request("POST", "/plugin-instances", create, middleware.AuditActionPluginInstanceCreate, 201, ""))
	id := created.ID
	path := "/plugin-instances/" + strconv.FormatInt(id, 10)
	snapshot, err := repo.Snapshot(ctx, id, 0)
	require.NoError(t, err)
	ciphertexts = append(ciphertexts, snapshot.Revision.ConfigEncrypted)
	request("GET", path, "", "", 200, "")
	published := decode(request("POST", path+"/publish", `{"expected_generation":1,"revision":1}`, middleware.AuditActionPluginInstancePublish, 200, ""))
	old, err := instances.Acquire(ctx, id)
	require.NoError(t, err)
	defer old.Release()
	rejectedDraft := decode(request("PUT", path+"/config", fmt.Sprintf(`{"expected_generation":%d,"config":{"marker":"http-pg-private-config-canary","reject":true}}`, published.Generation), middleware.AuditActionPluginInstanceConfig, 200, ""))
	snapshot, err = repo.Snapshot(ctx, id, rejectedDraft.LatestRevision)
	require.NoError(t, err)
	ciphertexts = append(ciphertexts, snapshot.Revision.ConfigEncrypted)
	request("POST", path+"/publish", fmt.Sprintf(`{"expected_generation":%d,"revision":999}`, rejectedDraft.Generation), middleware.AuditActionPluginInstancePublish, 404, "PLUGIN_INSTANCE_NOT_FOUND")
	request("POST", path+"/publish", `{"expected_generation":2,"revision":2}`, middleware.AuditActionPluginInstancePublish, 409, "PLUGIN_INSTANCE_CONFLICT")
	request("POST", path+"/publish", fmt.Sprintf(`{"expected_generation":%d,"revision":0}`, rejectedDraft.Generation), middleware.AuditActionPluginInstancePublish, 400, "PLUGIN_INSTANCE_INVALID")
	request("POST", path+"/publish", fmt.Sprintf(`{"expected_generation":%d,"revision":2}`, rejectedDraft.Generation), middleware.AuditActionPluginInstancePublish, 422, "PLUGIN_INSTANCE_UNAVAILABLE")
	unchanged, err := repo.Snapshot(ctx, id, 0)
	require.NoError(t, err)
	require.Equal(t, rejectedDraft.Generation, unchanged.Instance.Generation)
	require.EqualValues(t, 1, unchanged.Instance.CurrentRevision)
	require.True(t, unchanged.Instance.Enabled)
	state, err := instances.Status(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "runtime_start_failed", state.LastError)
	require.Len(t, state.Runtimes, 1)
	require.False(t, state.Closed)
	result, err := old.RunTask(ctx, "sync_prices", "old-still-live", json.RawMessage(`{}`))
	require.NoError(t, err)
	require.Contains(t, string(result.Output), "http-pg-private-config-canary")
	fresh, err := instances.Acquire(ctx, id)
	require.NoError(t, err)
	result, err = fresh.RunTask(ctx, "sync_prices", "fresh-still-old", json.RawMessage(`{}`))
	require.NoError(t, err)
	require.Contains(t, string(result.Output), "http-pg-private-config-canary")
	fresh.Release()
	request("GET", path+"/status", "", "", 200, "")
	repaired := decode(request("PUT", path+"/config", fmt.Sprintf(`{"expected_generation":%d,"config":{"marker":"http-pg-private-config-canary-repaired"}}`, rejectedDraft.Generation), middleware.AuditActionPluginInstanceConfig, 200, ""))
	snapshot, err = repo.Snapshot(ctx, id, repaired.LatestRevision)
	require.NoError(t, err)
	ciphertexts = append(ciphertexts, snapshot.Revision.ConfigEncrypted)
	replaced := decode(request("POST", path+"/publish", fmt.Sprintf(`{"expected_generation":%d,"revision":%d}`, repaired.Generation, repaired.LatestRevision), middleware.AuditActionPluginInstancePublish, 200, ""))
	disabled := decode(request("POST", path+"/disable", fmt.Sprintf(`{"expected_generation":%d}`, replaced.Generation), middleware.AuditActionPluginInstanceDisable, 200, ""))
	request("DELETE", path, fmt.Sprintf(`{"expected_generation":%d}`, disabled.Generation), middleware.AuditActionPluginInstanceDelete, 409, "PLUGIN_INSTANCE_BUSY")
	old.Release()
	request("DELETE", path, fmt.Sprintf(`{"expected_generation":%d}`, disabled.Generation), middleware.AuditActionPluginInstanceDelete, 200, "")
	request("DELETE", path, fmt.Sprintf(`{"expected_generation":%d}`, disabled.Generation), middleware.AuditActionPluginInstanceDelete, 404, "PLUGIN_INSTANCE_NOT_FOUND")
	request("GET", path, "", "", 404, "PLUGIN_INSTANCE_NOT_FOUND")
	instances.Close()
	request("POST", "/plugin-instances", create, middleware.AuditActionPluginInstanceCreate, 503, "PLUGIN_INSTANCE_UNAVAILABLE")
	require.Equal(t, "pg-http-session", grants.session)
	audit.Stop()
	entries, err := auditRepo.List(ctx, &service.AuditLogFilter{ActorEmail: email, PageSize: 100})
	require.NoError(t, err)
	require.Equal(t, len(expected), entries.Total)
	require.Len(t, entries.Logs, len(expected))
	require.Len(t, expected, 17)
	actual := map[string]*service.AuditLog{}
	for _, listed := range entries.Logs {
		entry, err := auditRepo.GetByID(ctx, listed.ID)
		require.NoError(t, err)
		actual[entry.RequestID] = entry
	}
	for _, want := range expected {
		entry := actual[want.id]
		require.NotNil(t, entry)
		require.Equal(t, want.action, entry.Action)
		require.Equal(t, want.status, entry.StatusCode)
		require.EqualValues(t, want.status, entry.Extra["http_status"])
		require.Equal(t, "<credential-bearing body omitted>", entry.RequestBody)
		if want.status >= 400 {
			require.Equal(t, "failed", entry.Extra["result"])
			require.NotContains(t, entry.Extra, "new_generation")
			require.NotContains(t, entry.Extra, "deleted")
		}
		if want.status == 422 {
			require.Equal(t, "PLUGIN_INSTANCE_UNAVAILABLE", entry.Extra["error_code"])
			require.Equal(t, false, entry.Extra["conflict"])
		}
		encoded, err := json.Marshal(entry)
		require.NoError(t, err)
		for _, secret := range append(append([]string{}, ciphertexts...), "http-pg-private-config-canary", p.InstallPath, p.ArtifactPath, "query-http-canary") {
			require.NotContains(t, string(encoded), secret)
			require.NotContains(t, logs.String(), secret)
		}
	}
	require.NoError(t, NewPluginRepository(integrationDB).Delete(ctx, p.ID, p.BinarySHA256))
	t.Log("HTTP_MATRIX_PG real_configure_rejection=422 closed=503 missing_step_up=403 persisted_writes=17 failed_publish_generation_unchanged=true old_and_fresh_leases_still_work=true response_log_audit_canaries_absent=true")
}
