package middleware

import "github.com/gin-gonic/gin"

// ContextKeyAdminAccess is set only from a live server-side RBAC snapshot.
// It allows backend-mode sign-in and admin-visible pages, not sensitive operations.
const ContextKeyAdminAccess ContextKey = "admin_access"

// HasAdminAccessFromContext never falls back to legacy user.role or request data.
func HasAdminAccessFromContext(c *gin.Context) bool {
	subject, ok := GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		return false
	}
	value, exists := c.Get(string(ContextKeyAdminAccess))
	allowed, ok := value.(bool)
	return exists && ok && allowed
}
