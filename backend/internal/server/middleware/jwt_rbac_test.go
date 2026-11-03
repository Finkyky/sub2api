//go:build unit

package middleware

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type jwtCompatRBACRepo struct {
	roles []service.RolePermissions
	err   error
	ids   []int64
}

func (r *jwtCompatRBACRepo) GetRolePermissions(_ context.Context, id int64) ([]service.RolePermissions, error) {
	r.ids = append(r.ids, id)
	return r.roles, r.err
}

func TestJWTAuthRBACLiveSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user := &service.User{ID: 42, Role: service.RoleUser, Status: service.StatusActive}
	users := &stubJWTUserRepo{users: map[int64]*service.User{42: user}}
	auth := service.NewAuthService(nil, users, nil, nil, &config.Config{JWT: config.JWTConfig{Secret: "test-secret", ExpireHour: 1}}, nil, nil, nil, nil, nil, nil, nil, nil)
	userSvc := service.NewUserService(users, nil, nil, nil)
	token, err := auth.GenerateToken(context.Background(), user)
	require.NoError(t, err)
	repo := &jwtCompatRBACRepo{}
	for _, tc := range []struct {
		name          string
		roles         []service.RolePermissions
		err           error
		nilService    bool
		access, super bool
		status        int
	}{
		{name: "ordinary RBAC", roles: []service.RolePermissions{{Permissions: []string{"pages.read"}}}, access: true, status: 200},
		{name: "super", roles: []service.RolePermissions{{SuperAdmin: true}}, access: true, super: true, status: 200},
		{name: "revoked", status: 200},
		{name: "empty role", roles: []service.RolePermissions{{Code: "empty"}}, status: 200},
		{name: "unknown permission", roles: []service.RolePermissions{{Permissions: []string{"retired.unknown"}}}, status: 200},
		{name: "database error", err: errors.New("db unavailable"), status: 500},
		{name: "nil service", nilService: true, status: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo.roles, repo.err = tc.roles, tc.err
			rbac := service.NewRBACService(repo)
			if tc.nilService {
				rbac = nil
			}
			r := gin.New()
			r.Use(gin.HandlerFunc(NewJWTAuthMiddleware(auth, userSvc, nil, nil, rbac)))
			r.GET("/", func(c *gin.Context) {
				access, exists := c.Get("admin_access")
				require.True(t, exists)
				require.Equal(t, tc.access, access)
				super, exists := c.Get(string(ContextKeySuperAdmin))
				require.True(t, exists)
				require.Equal(t, tc.super, super)
				role, _ := GetUserRoleFromContext(c)
				require.Equal(t, service.RoleUser, role)
				c.Status(200)
			})
			r.GET("/legacy-sensitive", AdminOnly(), func(c *gin.Context) { c.Status(200) })
			var checker PermissionChecker
			if rbac != nil {
				checker = rbac
			}
			r.GET("/ungranted", RequirePermission(checker, "users.read"), func(c *gin.Context) { c.Status(200) })
			for _, path := range []string{"/", "/legacy-sensitive", "/ungranted"} {
				w := httptest.NewRecorder()
				req := httptest.NewRequest("GET", path, nil)
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("X-Admin-Access", "true")
				req.Header.Set("X-Super-Admin", "true")
				r.ServeHTTP(w, req)
				want := tc.status
				if want == 200 && path == "/legacy-sensitive" {
					want = 403
				}
				if want == 200 && path == "/ungranted" && !tc.super {
					if tc.nilService {
						want = 503
					} else {
						want = 403
					}
				}
				require.Equal(t, want, w.Code, w.Body.String())
			}
			require.Equal(t, service.RoleUser, user.Role)
		})
	}
	for _, id := range repo.ids {
		require.Equal(t, int64(42), id)
	}
}

func TestJWTOptionalAndAPIKeyDoNotGrantAdminVisibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user := &service.User{ID: 42, Role: service.RoleAdmin, Status: service.StatusActive}
	users := &stubJWTUserRepo{users: map[int64]*service.User{42: user}}
	auth := service.NewAuthService(nil, users, nil, nil, &config.Config{JWT: config.JWTConfig{Secret: "test-secret", ExpireHour: 1}}, nil, nil, nil, nil, nil, nil, nil, nil)
	token, err := auth.GenerateToken(context.Background(), user)
	require.NoError(t, err)
	r := gin.New()
	r.Use(gin.HandlerFunc(NewOptionalJWTAuthMiddleware(auth, service.NewUserService(users, nil, nil, nil), nil, nil)))
	r.GET("/", func(c *gin.Context) {
		require.False(t, HasAdminAccessFromContext(c))
		super, exists := c.Get(string(ContextKeySuperAdmin))
		require.True(t, exists)
		require.Equal(t, false, super)
		c.Status(200)
	})
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Admin-Access", "true")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/?admin_access=true", nil)
	c.Set(string(ContextKeyUser), AuthSubject{UserID: user.ID})
	c.Set(string(ContextKeyUserRole), service.RoleAdmin)
	c.Set(string(ContextKeyAPIKey), &service.APIKey{User: user})
	c.Request.Header.Set("X-Super-Admin", "true")
	require.False(t, HasAdminAccessFromContext(c), "API key owner role is not an RBAC snapshot")
	BackendModeUserGuard(newBackendModeSettingService(t, "true"))(c)
	require.True(t, c.IsAborted())
	require.Equal(t, 403, c.Writer.Status())
}
