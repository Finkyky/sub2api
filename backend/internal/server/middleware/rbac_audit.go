package middleware

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// SetRBACAuditChange is a typed channel for transaction-produced RBAC snapshots.
// It deliberately does not widen SetAuditExtra's generic scalar allowlist.
func SetRBACAuditChange(c *gin.Context, change service.RBACMutationAudit) {
	SetAuditAction(c, change.Action)
	extra := map[string]any{}
	if existing, ok := c.Get(auditCtxKeyExtra); ok {
		if fields, ok := existing.(map[string]any); ok {
			for k, v := range fields {
				extra[k] = v
			}
		}
	}
	extra["target_type"] = change.TargetType
	extra["target_id"] = change.TargetID
	extra["before"] = change.Before
	extra["after"] = change.After
	c.Set(auditCtxKeyExtra, extra)
}
