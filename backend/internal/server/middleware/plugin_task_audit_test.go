package middleware

import (
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPluginTaskAuditOmitsBodiesAndSensitiveReads(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ method, path, action string }{
		{"POST", "/api/v1/admin/plugin-tasks", "admin.plugin_tasks.create"}, {"PUT", "/api/v1/admin/plugin-tasks/:id", "admin.plugin_tasks.update"}, {"DELETE", "/api/v1/admin/plugin-tasks/:id", "admin.plugin_tasks.delete"}, {"POST", "/api/v1/admin/plugin-tasks/:id/run", "admin.plugin_tasks.run"}, {"GET", "/api/v1/admin/plugin-runs/:id/sensitive", "admin.plugin_runs.sensitive"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			repo := &auditCaptureRepository{}
			audit := service.NewAuditLogService(repo, nil)
			audit.Start()
			defer audit.Stop()
			r := gin.New()
			r.Use(gin.HandlerFunc(NewAuditLogMiddleware(audit)))
			r.Handle(tc.method, tc.path, func(c *gin.Context) {
				SetAuditAction(c, "SECRET-ACTION")
				SetAuditExtra(c, map[string]any{"result": "success", "task_id": int64(11), "run_id": int64(22), "instance_id": int64(33), "generation": int64(3), "status": "succeeded"})
				c.Set(auditCtxKeyExtra, map[string]any{"result": "success", "task_id": int64(11), "run_id": int64(22), "instance_id": int64(33), "status": "succeeded", "raw": "SECRET-EXTRA"})
				c.Status(200)
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, strings.ReplaceAll(tc.path, ":id", "SECRET-ID")+"?secret=SECRET-QUERY", strings.NewReader(`{"input":{"arbitrary":"SECRET-BODY"}}`)))
			audit.Stop()
			repo.mu.Lock()
			logs := append([]*service.AuditLog{}, repo.logs...)
			repo.mu.Unlock()
			require.Len(t, logs, 1)
			require.Equal(t, tc.action, logs[0].Action)
			require.EqualValues(t, 11, logs[0].Extra["task_id"])
			raw, err := json.Marshal(logs[0])
			require.NoError(t, err)
			require.NotContains(t, string(raw), "SECRET")
		})
	}
}
