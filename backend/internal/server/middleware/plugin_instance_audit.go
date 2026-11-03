package middleware

import (
	"strconv"

	"github.com/gin-gonic/gin"
)

const (
	AuditActionPluginInstanceCreate   = "admin.plugin_instances.create"
	AuditActionPluginInstanceConfig   = "admin.plugin_instances.config.update"
	AuditActionPluginInstancePublish  = "admin.plugin_instances.publish"
	AuditActionPluginInstanceDisable  = "admin.plugin_instances.disable"
	AuditActionPluginInstanceBindings = "admin.plugin_instances.bindings.update"
	AuditActionPluginInstanceDelete   = "admin.plugin_instances.delete"
)

func isPluginInstanceAuditWrite(routeKey string) bool {
	switch auditActionOverrides[routeKey] {
	case AuditActionPluginInstanceCreate, AuditActionPluginInstanceConfig, AuditActionPluginInstancePublish, AuditActionPluginInstanceDisable, AuditActionPluginInstanceBindings, AuditActionPluginInstanceDelete:
		return true
	default:
		return false
	}
}

var pluginInstanceAuditCodes = map[string]bool{
	"PLUGIN_INSTANCE_INVALID": true, "PLUGIN_INSTANCE_NOT_FOUND": true,
	"PLUGIN_INSTANCE_CONFLICT": true, "PLUGIN_INSTANCE_ENABLED": true, "PLUGIN_INSTANCE_BUSY": true,
	"PLUGIN_INSTANCE_UNAVAILABLE": true, "PLUGIN_INSTANCE_OPERATION_UNAVAILABLE": true,
	"PLUGIN_INSTANCE_DELETE_UNAVAILABLE": true, "PLUGIN_INSTANCE_REQUEST_INVALID": true,
	"PLUGIN_INSTANCE_ID_INVALID": true,
}

// A final projection is needed in addition to SetAuditExtra: earlier middleware
// may attach arbitrary maps directly. Plugin payloads, query strings and raw ID
// params are deliberately never copied into a persisted management audit.
func projectPluginInstanceAuditExtra(c *gin.Context, status int) map[string]any {
	fields := map[string]any{}
	if value, ok := c.Get(auditCtxKeyExtra); ok {
		if extra, ok := value.(map[string]any); ok {
			fields = extra
		}
	}
	out := map[string]any{"http_status": status, "result": "failed", "error_code": "PLUGIN_INSTANCE_OPERATION_FAILED", "conflict": false}
	for _, key := range []string{"instance_id", "plugin_id", "expected_generation", "new_generation", "revision"} {
		if value, ok := fields[key].(int64); ok && value > 0 {
			out[key] = value
		}
	}
	if _, ok := out["instance_id"]; !ok {
		if id, err := strconv.ParseInt(c.Param("id"), 10, 64); err == nil && id > 0 {
			out["instance_id"] = id
		}
	}
	if result, ok := fields["result"].(string); ok && result == "success" && status >= 200 && status < 300 {
		out["result"] = "success"
		out["error_code"] = "OK"
		if deleted, ok := fields["deleted"].(bool); ok && deleted {
			out["deleted"] = true
		}
	} else {
		// Failed/denied writes never claim a committed generation or deletion.
		delete(out, "new_generation")
		if status >= 400 {
			out["error_code"] = "PLUGIN_INSTANCE_REQUEST_DENIED"
			if code, ok := fields["error_code"].(string); ok && pluginInstanceAuditCodes[code] {
				out["error_code"] = code
				out["conflict"] = code == "PLUGIN_INSTANCE_CONFLICT"
			}
		}
	}
	return out
}
