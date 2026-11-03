package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type rbacRouteChecker struct {
	allowed bool
	seen    *string
}

func (s rbacRouteChecker) HasPermission(_ context.Context, _ int64, code string) (bool, error) {
	if s.seen != nil {
		*s.seen = code
	}
	return s.allowed, nil
}

func TestRBACRoutesRegisterExpectedContracts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
		c.Next()
	})
	registerRBACRoutes(r.Group("/api/v1/admin"), &handler.Handlers{Admin: &handler.AdminHandlers{}}, []middleware.PermissionChecker{rbacRouteChecker{}})
	routes := map[string]bool{}
	for _, route := range r.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	for _, route := range []string{
		"GET /api/v1/admin/roles", "POST /api/v1/admin/roles", "GET /api/v1/admin/roles/:id", "PUT /api/v1/admin/roles/:id", "DELETE /api/v1/admin/roles/:id",
		"GET /api/v1/admin/permissions", "GET /api/v1/admin/roles/:id/permissions", "PUT /api/v1/admin/roles/:id/permissions",
		"GET /api/v1/admin/users/:id/roles", "PUT /api/v1/admin/users/:id/roles",
	} {
		require.True(t, routes[route], "missing route %s", route)
	}
}

func TestRBACRoutesRequireOperationPermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ method, path, permission string }{
		{http.MethodGet, "/roles", "roles.read"}, {http.MethodPost, "/roles", "roles.create"}, {http.MethodPut, "/roles/9", "roles.update"}, {http.MethodDelete, "/roles/9", "roles.delete"},
		{http.MethodGet, "/roles/9", "roles.read"}, {http.MethodGet, "/roles/9/permissions", "roles.read"}, {http.MethodGet, "/permissions", "permissions.read"}, {http.MethodPut, "/roles/9/permissions", "roles.permissions.assign"}, {http.MethodGet, "/users/9/roles", "users.read"}, {http.MethodPut, "/users/9/roles", "users.roles.assign"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			seen := ""
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
				c.Next()
			})
			registerRBACRoutes(r.Group("/api/v1/admin"), &handler.Handlers{Admin: &handler.AdminHandlers{}}, []middleware.PermissionChecker{rbacRouteChecker{allowed: false, seen: &seen}})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, "/api/v1/admin"+tc.path, nil))
			require.Equal(t, http.StatusForbidden, w.Code, "permission %s", tc.permission)
			require.Equal(t, tc.permission, seen)
		})
	}
}

func TestRBACRoutesFailClosedWithoutPermissionChecker(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
		c.Next()
	})
	registerRBACRoutes(r.Group("/api/v1/admin"), &handler.Handlers{Admin: &handler.AdminHandlers{}}, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/admin/roles", nil))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestRBACRoutesAreInProductionAdminRegistration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	adminAuth := middleware.AdminAuthMiddleware(func(c *gin.Context) { middleware.AbortWithError(c, 401, "UNAUTHORIZED", "no credentials") })
	RegisterAdminRoutes(r.Group("/api/v1"), &handler.Handlers{Admin: &handler.AdminHandlers{}}, adminAuth, middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }), nil, nil, nil, allowAllRoutePermissionChecker{})
	for _, path := range []string{"/roles", "/roles/7", "/permissions", "/roles/7/permissions", "/users/7/roles"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/admin"+path, nil))
		require.Equal(t, 401, w.Code, path)
	}
}
func TestRBACRoutesNoIdentityAndGrantedPermission(t *testing.T) {
	for _, identity := range []bool{false, true} {
		r := gin.New()
		if identity {
			r.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
				c.Next()
			})
		}
		registerRBACRoutes(r.Group("/api/v1/admin"), &handler.Handlers{Admin: &handler.AdminHandlers{}}, []middleware.PermissionChecker{rbacRouteChecker{allowed: true}})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/admin/roles", nil))
		if identity {
			require.Equal(t, 503, w.Code, "authorization reaches the missing handler, which fails closed")
		} else {
			require.Equal(t, 401, w.Code)
		}
	}
}
