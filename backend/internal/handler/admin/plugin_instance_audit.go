package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// This HTTP-only adapter projects verified request fields and committed
// operation results. It adds no observation IO or service-gate acquisition.
// Unknown package/revision fields are omitted, not inferred or read separately.
type pluginInstanceAudit struct{ c *gin.Context }

func beginPluginInstanceAudit(c *gin.Context, action string) *pluginInstanceAudit {
	middleware.SetAuditAction(c, action)
	middleware.SetAuditExtra(c, map[string]any{"result": "failed"})
	return &pluginInstanceAudit{c: c}
}
func (a *pluginInstanceAudit) attempt(id, pluginID, expected, revision int64) {
	fields := map[string]any{}
	for key, value := range map[string]int64{"instance_id": id, "plugin_id": pluginID, "expected_generation": expected, "revision": revision} {
		if value > 0 {
			fields[key] = value
		}
	}
	middleware.SetAuditExtra(a.c, fields)
}
func (a *pluginInstanceAudit) success(snapshot *service.PluginInstanceSnapshot) {
	a.attempt(snapshot.Instance.ID, snapshot.Instance.PluginID, 0, snapshot.Revision.Revision)
	middleware.SetAuditExtra(a.c, map[string]any{"result": "success", "new_generation": snapshot.Instance.Generation})
}
func (a *pluginInstanceAudit) deleted() {
	middleware.SetAuditExtra(a.c, map[string]any{"result": "success", "deleted": true})
}
func pluginInstanceAuditError(c *gin.Context, code string) {
	middleware.SetAuditExtra(c, map[string]any{"result": "failed", "error_code": code})
}
