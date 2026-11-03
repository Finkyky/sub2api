package middleware

import "github.com/gin-gonic/gin"

var pluginTaskAuditActions = map[string]string{
	"POST /api/v1/admin/plugin-tasks":             "admin.plugin_tasks.create",
	"PUT /api/v1/admin/plugin-tasks/:id":          "admin.plugin_tasks.update",
	"DELETE /api/v1/admin/plugin-tasks/:id":       "admin.plugin_tasks.delete",
	"POST /api/v1/admin/plugin-tasks/:id/run":     "admin.plugin_tasks.run",
	"GET /api/v1/admin/plugin-runs/:id/sensitive": "admin.plugin_runs.sensitive",
}

func projectPluginTaskAuditExtra(c *gin.Context, status int) map[string]any {
	fields := map[string]any{}
	if v, ok := c.Get(auditCtxKeyExtra); ok {
		if x, ok := v.(map[string]any); ok {
			fields = x
		}
	}
	out := map[string]any{"http_status": status, "result": "failed", "error_code": "PLUGIN_TASK_REQUEST_DENIED"}
	for _, key := range []string{"task_id", "run_id", "instance_id", "generation"} {
		if n, ok := fields[key].(int64); ok && n > 0 {
			out[key] = n
		}
	}
	if fields["result"] == "success" && status >= 200 && status < 300 {
		out["result"] = "success"
		out["error_code"] = "OK"
		if fields["deleted"] == true {
			out["deleted"] = true
		}
	} else {
		delete(out, "generation")
		if code, ok := fields["error_code"].(string); ok {
			switch code {
			case "PLUGIN_TASK_INVALID", "PLUGIN_TASK_NOT_FOUND", "PLUGIN_TASK_CONFLICT", "PLUGIN_TASK_DISABLED", "PLUGIN_TASK_CAPABILITY_DENIED", "PLUGIN_TASK_UNAVAILABLE":
				out["error_code"] = code
			}
		}
	}
	if state, ok := fields["status"].(string); ok {
		switch state {
		case "running", "succeeded", "failed", "lost":
			out["status"] = state
		}
	}
	return out
}
