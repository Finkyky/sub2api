package middleware

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

var piAuditRouteCases = []struct{ method, path, action string }{
	{"POST", "/api/v1/admin/plugin-instances", "admin.plugin_instances.create"},
	{"PUT", "/api/v1/admin/plugin-instances/:id/config", "admin.plugin_instances.config.update"},
	{"POST", "/api/v1/admin/plugin-instances/:id/publish", "admin.plugin_instances.publish"},
	{"POST", "/api/v1/admin/plugin-instances/:id/disable", "admin.plugin_instances.disable"},
	{"PUT", "/api/v1/admin/plugin-instances/:id/bindings", "admin.plugin_instances.bindings.update"},
	{"DELETE", "/api/v1/admin/plugin-instances/:id", "admin.plugin_instances.delete"},
}

func TestPluginInstanceAuditRoutesHaveExplicitActionsAndOmittedBodies(t *testing.T) {
	for _, tc := range piAuditRouteCases {
		key := tc.method + " " + tc.path
		require.Equal(t, tc.action, auditActionOverrides[key], key)
		_, ok := auditBodyOmittedRoutes[key]
		require.True(t, ok, key)
	}
}
func TestPluginInstanceAuditMiddlewareProjectsOnlyTrustedScalars(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range piAuditRouteCases {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			repository := &auditCaptureRepository{}
			audit := service.NewAuditLogService(repository, nil)
			audit.Start()
			defer audit.Stop()
			router := gin.New()
			router.Use(gin.HandlerFunc(NewAuditLogMiddleware(audit)))
			router.Handle(tc.method, tc.path, func(c *gin.Context) {
				SetAuditAction(c, "action-canary")
				SetAuditExtra(c, map[string]any{"result": "success", "instance_id": int64(11), "plugin_id": int64(3), "expected_generation": int64(8), "new_generation": int64(9), "revision": int64(2), "observed_generation": int64(8), "conflict": false, "name": "name-canary", "config": "config-canary", "install_path": "path-canary"})
				// Bypass the setter deliberately: the final projection must also enforce
				// types/fields, not trust arbitrary maps previously attached to context.
				value, _ := c.Get(auditCtxKeyExtra)
				fields := value.(map[string]any)
				fields["unsafe_nested"] = map[string]any{"secret": "nested-canary"}
				fields["deleted"] = "typed-field-canary"
				c.Status(200)
			})
			path := strings.ReplaceAll(tc.path, ":id", "raw-id-canary") + "?note=query-canary"
			req := httptest.NewRequest(tc.method, path, bytes.NewBufferString(`{"name":"name-canary","config":{"marker":"body-canary"},"config_encrypted":"cipher-canary","install_path":"path-canary"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			audit.Stop()
			repository.mu.Lock()
			logs := append([]*service.AuditLog(nil), repository.logs...)
			repository.mu.Unlock()
			require.Len(t, logs, 1)
			entry := logs[0]
			require.Equal(t, "<credential-bearing body omitted>", entry.RequestBody)
			require.Equal(t, tc.action, entry.Action)
			for key, want := range map[string]int64{"instance_id": 11, "plugin_id": 3, "expected_generation": 8, "new_generation": 9, "revision": 2} {
				require.EqualValues(t, want, entry.Extra[key], key)
			}
			require.Equal(t, "success", entry.Extra["result"])
			require.Equal(t, "OK", entry.Extra["error_code"])
			require.EqualValues(t, 200, entry.Extra["http_status"])
			require.NotContains(t, entry.Extra, "deleted")
			require.NotContains(t, entry.Extra, "unsafe_nested")
			require.NotContains(t, entry.Extra, "observed_generation")
			require.NotContains(t, entry.Extra, "params")
			require.NotContains(t, entry.Extra, "query")
			data, err := json.Marshal(entry)
			require.NoError(t, err)
			for _, secret := range []string{"name-canary", "config-canary", "path-canary", "nested-canary", "typed-field-canary", "raw-id-canary", "query-canary", "body-canary", "cipher-canary", "action-canary"} {
				require.NotContains(t, string(data), secret)
			}
		})
	}
}
func TestPluginInstanceAuditMiddlewareDenialDoesNotReadOrRecordBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repository := &auditCaptureRepository{}
	audit := service.NewAuditLogService(repository, nil)
	audit.Start()
	defer audit.Stop()
	router := gin.New()
	router.Use(gin.HandlerFunc(NewAuditLogMiddleware(audit)))
	router.DELETE("/api/v1/admin/plugin-instances/:id", func(c *gin.Context) { c.AbortWithStatus(403) })
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/v1/admin/plugin-instances/11?note=query-canary", bytes.NewBufferString("invalid-body-canary")))
	audit.Stop()
	repository.mu.Lock()
	defer repository.mu.Unlock()
	require.Len(t, repository.logs, 1)
	entry := repository.logs[0]
	require.Equal(t, "admin.plugin_instances.delete", entry.Action)
	require.Equal(t, "failed", entry.Extra["result"])
	require.Equal(t, "PLUGIN_INSTANCE_REQUEST_DENIED", entry.Extra["error_code"])
	require.EqualValues(t, 11, entry.Extra["instance_id"])
	require.EqualValues(t, 403, entry.Extra["http_status"])
	require.NotContains(t, entry.Extra, "expected_generation")
	require.Equal(t, "<credential-bearing body omitted>", entry.RequestBody)
	require.NotContains(t, entry.Extra, "query")
}

func TestPluginInstanceAuditFinalProjectionRejectsInvalidTypesAndErrorText(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Params = gin.Params{{Key: "id", Value: "11"}}
	c.Set(auditCtxKeyExtra, map[string]any{
		"instance_id": "identity-canary", "plugin_id": float64(3), "expected_generation": int64(-1),
		"new_generation": int64(9), "revision": map[string]any{"secret": "nested-canary"},
		"result": "secret-result-canary", "error_code": "private-error-canary", "deleted": true,
		"http_status": "status-canary", "conflict": true,
	})
	got := projectPluginInstanceAuditExtra(c, 409)
	require.Equal(t, map[string]any{"instance_id": int64(11), "http_status": 409, "result": "failed", "error_code": "PLUGIN_INSTANCE_REQUEST_DENIED", "conflict": false}, got)
	c.Set(auditCtxKeyExtra, map[string]any{"expected_generation": int64(4), "error_code": "PLUGIN_INSTANCE_CONFLICT", "new_generation": int64(5), "deleted": true})
	got = projectPluginInstanceAuditExtra(c, 409)
	require.Equal(t, true, got["conflict"])
	require.Equal(t, "PLUGIN_INSTANCE_CONFLICT", got["error_code"])
	require.Equal(t, int64(4), got["expected_generation"])
	require.NotContains(t, got, "new_generation")
	require.NotContains(t, got, "deleted")
}
