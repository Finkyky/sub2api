//go:build unit

package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestBackendModeGuardRBACContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, role string
		access     any
		subject    bool
		want       int
	}{
		{"ordinary rbac", "user", true, true, 200},
		{"revoked legacy admin", "admin", false, true, 403},
		{"missing snapshot", "admin", nil, true, 403},
		{"malformed snapshot", "admin", "true", true, 403},
		{"missing subject", "user", true, false, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set(string(ContextKeyUserRole), tc.role)
				if tc.subject {
					c.Set(string(ContextKeyUser), AuthSubject{UserID: 42})
				}
				if tc.access != nil {
					c.Set("admin_access", tc.access)
				}
				c.Next()
			})
			r.Use(BackendModeUserGuard(newBackendModeSettingService(t, "true")))
			r.GET("/", func(c *gin.Context) { c.Status(200) })
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("X-Admin-Access", "true")
			r.ServeHTTP(w, req)
			require.Equal(t, tc.want, w.Code)
		})
	}
}
