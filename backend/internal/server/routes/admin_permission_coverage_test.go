package routes

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type boundaryChecker struct{ seen []string }

func (s *boundaryChecker) HasPermission(_ context.Context, _ int64, code string) (bool, error) {
	s.seen = append(s.seen, code)
	return false, nil
}
func (s *boundaryChecker) Snapshot(context.Context, int64) (service.RBACSnapshot, error) {
	s.seen = append(s.seen, "super_admin")
	return service.RBACSnapshot{}, nil
}

func boundaryRouter(t *testing.T, checker middleware.PermissionChecker) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	next := func(c *gin.Context) { c.Next() }
	auth := middleware.AdminAuthMiddleware(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
		c.Next()
	})
	h := &handler.Handlers{Admin: &handler.AdminHandlers{}}
	v1 := r.Group("/api/v1")
	RegisterAdminRoutes(v1, h, auth, middleware.AuditLogMiddleware(next), middleware.StepUpAuthMiddleware(next), nil, nil, checker)
	RegisterPaymentRoutes(v1, nil, nil, nil, middleware.JWTAuthMiddleware(next), auth, middleware.AuditLogMiddleware(next), nil, nil, nil, checker)
	handler.RegisterPageRoutes(v1, t.TempDir(), next, gin.HandlerFunc(auth), nil, checker)
	return r
}

func TestAdminBoundaryProductionInventoryAndDenials(t *testing.T) {
	checker := &boundaryChecker{}
	r := boundaryRouter(t, checker)
	policies := middleware.AdminRoutePolicies()
	covered := map[string]bool{}
	for _, route := range r.Routes() {
		if !strings.HasPrefix(route.Path, "/api/v1/admin/") && route.Path != "/api/v1/pages" {
			continue
		}
		key := route.Method + " " + route.Path
		policy, ok := policies[key]
		require.True(t, ok, "missing policy: %s", key)
		covered[key] = true
		t.Run(key, func(t *testing.T) {
			checker.seen = nil
			path := route.Path
			// Substitute all Gin parameters without depending on handler inputs.
			parts := strings.Split(path, "/")
			for i, p := range parts {
				if strings.HasPrefix(p, ":") {
					parts[i] = "1"
				}
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(route.Method, strings.Join(parts, "/"), nil))
			require.Equal(t, 403, w.Code, "denial must precede business handlers: %s", key)
			expected := policy.Permission
			if policy.SuperAdminOnly {
				expected = "super_admin"
			}
			require.Equal(t, []string{expected}, checker.seen, "exact permission contract: %s", key)
		})
	}
	require.Greater(t, len(covered), 400, "inventory unexpectedly shrank")
	for key := range policies {
		require.True(t, covered[key], "stale policy: %s", key)
	}
	t.Logf("verified %d production management routes", len(covered))
}

func TestAdminBoundaryProductionNilChecker(t *testing.T) {
	r := boundaryRouter(t, nil)
	for _, path := range []string{"/admin/dashboard/stats", "/admin/accounts", "/admin/payment/config", "/pages", "/admin/settings/admin-api-key"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1"+path, nil))
		require.Equal(t, 503, w.Code, path)
	}
}

func TestAdminBoundaryLegacyUserHelperFailsClosed(t *testing.T) {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
		c.Next()
	})
	r.GET("/test", permissionFor(nil, "users.read"), func(c *gin.Context) { c.Status(204) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/test", nil))
	require.Equal(t, 503, w.Code)
}

type boundaryBrokenBody struct{}

func (boundaryBrokenBody) Read([]byte) (int, error) { return 0, errors.New("test body failure") }
func (boundaryBrokenBody) Close() error             { return nil }

func TestAdminBoundaryPaymentPublicAndUserRoutesUnchanged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	checker := &boundaryChecker{}
	jwt := middleware.JWTAuthMiddleware(func(c *gin.Context) { c.AbortWithStatus(418) })
	admin := middleware.AdminAuthMiddleware(func(c *gin.Context) { t.Fatal("admin auth ran on a non-admin route") })
	next := middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })
	RegisterPaymentRoutes(r.Group("/api/v1"), nil, nil, nil, jwt, admin, next, nil, nil, redisClient, checker)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/payment/config", nil))
	require.Equal(t, 418, w.Code)
	for _, path := range []string{"/api/v1/payment/public/orders/verify", "/api/v1/payment/public/orders/resolve"} {
		w = httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader("{")))
		require.Equal(t, 400, w.Code)
	}
	// Body-read error exercises the real webhook handler before service IO.
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/payment/webhook/stripe", boundaryBrokenBody{}))
	require.Equal(t, 400, w.Code)
	require.Contains(t, w.Body.String(), "failed to read body")
	require.Empty(t, checker.seen)
}

func TestAdminBoundaryPreservesStepUpForSuperAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	calls := 0
	auth := middleware.AdminAuthMiddleware(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
		c.Set(string(middleware.ContextKeySuperAdmin), true)
		c.Next()
	})
	next := middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	step := middleware.StepUpAuthMiddleware(func(c *gin.Context) { calls++; c.AbortWithStatus(418) })
	RegisterAdminRoutes(r.Group("/api/v1"), &handler.Handlers{Admin: &handler.AdminHandlers{}}, auth, next, step, nil, nil, allowAllRoutePermissionChecker{})
	for _, tc := range []struct{ method, path string }{{"GET", "/accounts/data"}, {"GET", "/proxies/data"}, {"POST", "/backups/1/restore"}, {"POST", "/plugins/upload"}} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, "/api/v1/admin"+tc.path, nil))
		require.Equal(t, 418, w.Code, tc.path)
	}
	require.Equal(t, 4, calls, "RBAC bypass must not bypass existing step-up")
}

func TestAdminPluginInstanceRoutesRegisteredAndDenied(t *testing.T) {
	checker := &boundaryChecker{}
	router := boundaryRouter(t, checker)
	for _, route := range []struct{ method, path, permission string }{
		{"GET", "/plugin-instances", "plugin_instances.read"},
		{"GET", "/plugin-instances/1", "plugin_instances.read"},
		{"GET", "/plugin-instances/1/revisions", "plugin_instances.read"},
		{"GET", "/plugin-instances/1/status", "plugin_instances.read"},
		{"POST", "/plugin-instances", "plugin_instances.create"},
		{"PUT", "/plugin-instances/1/config", "plugin_instances.update"},
		{"POST", "/plugin-instances/1/publish", "plugin_instances.publish"},
		{"POST", "/plugin-instances/1/disable", "plugin_instances.disable"},
		{"DELETE", "/plugin-instances/1", "plugin_instances.delete"},
		{"PUT", "/plugin-instances/1/bindings", "plugin_instances.bindings.update"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			checker.seen = nil
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(route.method, "/api/v1/admin"+route.path, nil))
			require.Equal(t, 403, response.Code, "route must exist and deny before its business handler")
			require.Equal(t, []string{route.permission}, checker.seen)
		})
	}
}

func TestAdminPluginInstanceWritesPreserveStepUpForSuperAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	auth := middleware.AdminAuthMiddleware(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
		c.Set(string(middleware.ContextKeySuperAdmin), true)
		c.Next()
	})
	next := middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	stepCalls := 0
	step := middleware.StepUpAuthMiddleware(func(c *gin.Context) {
		stepCalls++
		c.AbortWithStatus(418)
	})
	RegisterAdminRoutes(router.Group("/api/v1"), &handler.Handlers{Admin: &handler.AdminHandlers{}}, auth, next, step, nil, nil, allowAllRoutePermissionChecker{})
	for _, route := range []struct{ method, path string }{
		{"POST", "/plugin-instances"},
		{"PUT", "/plugin-instances/1/config"},
		{"POST", "/plugin-instances/1/publish"},
		{"POST", "/plugin-instances/1/disable"},
		{"DELETE", "/plugin-instances/1"},
		{"PUT", "/plugin-instances/1/bindings"},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(route.method, "/api/v1/admin"+route.path, nil))
		require.Equal(t, 418, response.Code, route.method+" "+route.path)
	}
	require.Equal(t, 6, stepCalls, "super-admin RBAC bypass must preserve step-up on all instance writes")
}

type boundaryPluginInstanceReader struct{ boundaryChecker }

func (s *boundaryPluginInstanceReader) HasPermission(_ context.Context, _ int64, code string) (bool, error) {
	s.seen = append(s.seen, code)
	return code == "plugin_instances.read", nil
}

func TestAdminPluginInstanceStatusReadDoesNotRequireStepUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	checker := &boundaryPluginInstanceReader{}
	auth := middleware.AdminAuthMiddleware(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
		c.Next()
	})
	next := middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	stepCalls := 0
	step := middleware.StepUpAuthMiddleware(func(c *gin.Context) { stepCalls++; c.AbortWithStatus(418) })
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{PluginInstance: &adminhandler.PluginInstanceHandler{}}}
	RegisterAdminRoutes(router.Group("/api/v1"), handlers, auth, next, step, nil, nil, checker)
	w := httptest.NewRecorder()
	// An invalid ID proves the real handler was reached, without needing a DB.
	router.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/admin/plugin-instances/0/status", nil))
	require.Equal(t, 400, w.Code)
	require.Contains(t, w.Body.String(), "PLUGIN_INSTANCE_ID_INVALID")
	require.Equal(t, []string{"plugin_instances.read"}, checker.seen)
	require.Zero(t, stepCalls, "read permission alone must suffice for status")
}
