package middleware

import (
	"context"
	"errors"

	"github.com/gin-gonic/gin"
)

type PermissionChecker interface {
	HasPermission(ctx context.Context, userID int64, permission string) (bool, error)
}

func RequirePermission(checker PermissionChecker, permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		subject, ok := GetAuthSubjectFromContext(c)
		if !ok || subject.UserID <= 0 {
			AbortWithError(c, 401, "UNAUTHORIZED", "User not found in context")
			return
		}
		if superAdmin, exists := c.Get(string(ContextKeySuperAdmin)); exists {
			if isSuperAdmin, ok := superAdmin.(bool); ok && isSuperAdmin {
				c.Next()
				return
			}
		}
		if checker == nil {
			AbortWithError(c, 503, "RBAC_UNAVAILABLE", "Permission checker unavailable")
			return
		}
		allowed, err := checker.HasPermission(c.Request.Context(), subject.UserID, permission)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				AbortWithError(c, 499, "REQUEST_CANCELED", "Request canceled")
			} else {
				AbortWithError(c, 500, "INTERNAL_ERROR", "Failed to evaluate permission")
			}
			return
		}
		if !allowed {
			AbortWithError(c, 403, "FORBIDDEN", "Permission required: "+permission)
			return
		}
		c.Next()
	}
}
