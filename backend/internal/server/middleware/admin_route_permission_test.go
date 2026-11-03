package middleware

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type routePolicyChecker struct {
	allow bool
	super bool
	err   error
	seen  []string
}

func (s *routePolicyChecker) HasPermission(_ context.Context, _ int64, code string) (bool, error) {
	s.seen = append(s.seen, code)
	return s.allow, s.err
}
func (s *routePolicyChecker) Snapshot(context.Context, int64) (service.RBACSnapshot, error) {
	s.seen = append(s.seen, "super_admin")
	return service.RBACSnapshot{IsSuperAdmin: s.super}, s.err
}

func TestAdminRoutePolicyCatalogAndExactSemantics(t *testing.T) {
	policies := AdminRoutePolicies()
	catalog := service.PermissionCatalog()
	for key, p := range policies {
		require.NotEmpty(t, key)
		if p.SuperAdminOnly {
			require.Empty(t, p.Permission, key)
			continue
		}
		_, ok := catalog[p.Permission]
		require.True(t, ok, "unknown catalog code %s: %s", p.Permission, key)
	}
	for key, want := range map[string]string{
		"GET /api/v1/admin/accounts/:id/usage":               "accounts.update",
		"POST /api/v1/admin/accounts/usage/batch":            "accounts.update",
		"GET /api/v1/admin/users/:id/api-keys":               "api_keys.read",
		"POST /api/v1/admin/dashboard/users-usage":           "dashboard.read",
		"POST /api/v1/admin/dashboard/aggregation/backfill":  "ops.manage",
		"GET /api/v1/admin/channels/pricing/sync-models":     "channels.read",
		"POST /api/v1/admin/payment/orders/:id/refund/query": "payments.refund",
		"POST /api/v1/admin/payment/orders/:id/retry":        "payments.update",
		"GET /api/v1/admin/payment/providers":                "payments.read",
		"PUT /api/v1/admin/payment/providers/:id":            "payments.configure",
		"GET /api/v1/admin/accounts/data":                    "accounts.export",
		"POST /api/v1/admin/accounts/batch-delete":           "accounts.delete",
		"POST /api/v1/admin/groups/:id/composite-routes":     "groups.update",
		"GET /api/v1/admin/groups/:id/subscriptions":         "subscriptions.read",
		"GET /api/v1/pages":                                  "pages.read",
		"PUT /api/v1/admin/users/:id/roles":                  "users.roles.assign",
		"POST /api/v1/admin/audit-logs/clear":                "audit_logs.delete",
	} {
		require.Equal(t, want, policies[key].Permission, key)
	}
	for _, key := range []string{"GET /api/v1/admin/settings/admin-api-key", "POST /api/v1/admin/settings/admin-api-key/regenerate", "DELETE /api/v1/admin/settings/admin-api-key", "POST /api/v1/admin/backups/:id/restore", "POST /api/v1/admin/plugins/upload", "POST /api/v1/admin/system/update"} {
		require.True(t, policies[key].SuperAdminOnly, key)
	}
	delete(policies, "GET /api/v1/pages")
	require.Contains(t, AdminRoutePolicies(), "GET /api/v1/pages", "caller must not mutate live policy")
}

func TestAdminRoutePolicyFailClosedAndSuperAdmin(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		identity   int64
		trusted    bool
		checker    PermissionChecker
		status     int
	}{
		{"missing identity", "/api/v1/admin/accounts", 0, false, &routePolicyChecker{allow: true}, 401},
		{"negative identity", "/api/v1/admin/accounts", -1, true, &routePolicyChecker{}, 401},
		{"nil checker", "/api/v1/admin/accounts", 9, false, nil, 503},
		{"denied", "/api/v1/admin/accounts", 9, false, &routePolicyChecker{}, 403},
		{"granted", "/api/v1/admin/accounts", 9, false, &routePolicyChecker{allow: true}, 204},
		{"checker error", "/api/v1/admin/accounts", 9, false, &routePolicyChecker{err: errors.New("db down")}, 500},
		{"canceled", "/api/v1/admin/accounts", 9, false, &routePolicyChecker{err: context.Canceled}, 499},
		{"trusted api key", "/api/v1/admin/accounts", 9, true, nil, 204},
		{"unmapped ordinary", "/api/v1/admin/future", 9, false, &routePolicyChecker{allow: true}, 403},
		{"unmapped super", "/api/v1/admin/future", 9, true, nil, 403},
		{"superonly ordinary", "/api/v1/admin/settings/admin-api-key", 9, false, &routePolicyChecker{allow: true}, 403},
		{"superonly database super", "/api/v1/admin/settings/admin-api-key", 9, false, &routePolicyChecker{super: true}, 204},
		{"superonly trusted api key", "/api/v1/admin/settings/admin-api-key", 9, true, nil, 204},
		{"superonly no checker", "/api/v1/admin/settings/admin-api-key", 9, false, nil, 503},
		{"superonly wrong checker", "/api/v1/admin/settings/admin-api-key", 9, false, permissionCheckerStub{allowed: true}, 503},
		{"superonly lookup error", "/api/v1/admin/settings/admin-api-key", 9, false, &routePolicyChecker{err: errors.New("db down")}, 500},
		{"superonly canceled", "/api/v1/admin/settings/admin-api-key", 9, false, &routePolicyChecker{err: context.Canceled}, 499},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.Use(func(c *gin.Context) {
				if tc.identity != 0 {
					c.Set(string(ContextKeyUser), AuthSubject{UserID: tc.identity})
				}
				c.Set(string(ContextKeySuperAdmin), tc.trusted)
				c.Next()
			})
			r.GET(tc.path, RequireAdminRoutePermission(tc.checker), func(c *gin.Context) { c.Status(204) })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
			require.Equal(t, tc.status, w.Code)
		})
	}
}

func TestAdminRoutePolicyAllContractsReachOnlyGrantedHandlers(t *testing.T) {
	for key, p := range AdminRoutePolicies() {
		t.Run(key, func(t *testing.T) {
			method, path, _ := strings.Cut(key, " ")
			checker := &routePolicyChecker{allow: true, super: p.SuperAdminOnly}
			r := gin.New()
			r.Use(func(c *gin.Context) { c.Set(string(ContextKeyUser), AuthSubject{UserID: 9}); c.Next() })
			r.Handle(method, path, RequireAdminRoutePermission(checker), func(c *gin.Context) { c.Status(204) })
			parts := strings.Split(path, "/")
			for i, p := range parts {
				if strings.HasPrefix(p, ":") {
					parts[i] = "7"
				}
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(method, strings.Join(parts, "/"), nil))
			require.Equal(t, 204, w.Code)
			expected := p.Permission
			if p.SuperAdminOnly {
				expected = "super_admin"
			}
			require.Equal(t, []string{expected}, checker.seen)
		})
	}
}

func TestAdminRoutePolicyDoesNotUseRawURLForMatching(t *testing.T) {
	checker := &routePolicyChecker{allow: true}
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(string(ContextKeyUser), AuthSubject{UserID: 9}); c.Next() })
	r.GET("/api/v1/admin/accounts/:id", RequireAdminRoutePermission(checker), func(c *gin.Context) { c.Status(204) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/admin/accounts/123?permission=ops.manage", nil))
	require.Equal(t, 204, w.Code)
	require.Equal(t, []string{"accounts.read"}, checker.seen)
	r.POST("/api/v1/admin/accounts/:id", RequireAdminRoutePermission(checker), func(c *gin.Context) { c.Status(204) })
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/admin/accounts/123", nil))
	require.Equal(t, 403, w.Code)
}
