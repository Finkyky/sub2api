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

type rbacAuthUserRepo struct {
	service.UserRepository
	user *service.User
}

func (r *rbacAuthUserRepo) GetByID(context.Context, int64) (*service.User, error) {
	out := *r.user
	return &out, nil
}

type rbacAuthRolesRepo struct {
	roles []service.RolePermissions
	err   error
}

func (r *rbacAuthRolesRepo) GetRolePermissions(context.Context, int64) ([]service.RolePermissions, error) {
	return r.roles, r.err
}

func TestRBACAdminEntryAndImmediateRevocation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "rbac-test-secret", ExpireHour: 1}}
	auth := service.NewAuthService(nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	user := &service.User{ID: 9, Role: service.RoleUser, Email: "operator@example.com", Status: service.StatusActive, Concurrency: 1}
	users := service.NewUserService(&rbacAuthUserRepo{user: user}, nil, nil, nil)
	roles := &rbacAuthRolesRepo{roles: []service.RolePermissions{{Code: "reader", Permissions: []string{"users.read"}}}}
	checker := service.NewRBACService(roles)
	token, err := auth.GenerateToken(context.Background(), user)
	require.NoError(t, err)
	r := gin.New()
	r.Use(gin.HandlerFunc(NewAdminAuthMiddleware(auth, users, nil, nil, checker)))
	r.GET("/api/v1/admin/users", RequireAdminRoutePermission(checker), func(c *gin.Context) {
		actor, ok := service.RBACActorFromContext(c.Request.Context())
		require.True(t, ok)
		require.Equal(t, int64(9), actor.UserID)
		require.False(t, actor.AdminAPIKey)
		c.Status(204)
	})
	for _, ws := range []bool{false, true} {
		send := func() int {
			req := httptest.NewRequest("GET", "/api/v1/admin/users", nil)
			if ws {
				req.Header.Set("Upgrade", "websocket")
				req.Header.Set("Connection", "Upgrade")
				req.Header.Set("Sec-WebSocket-Protocol", "sub2api-admin, jwt."+token)
			} else {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			return w.Code
		}
		roles.roles = []service.RolePermissions{{Code: "reader", Permissions: []string{"users.read"}}}
		require.Equal(t, 204, send())
		roles.roles = nil
		require.Equal(t, 403, send(), "revocation applies to next request")
		user.Role = service.RoleAdmin
		require.Equal(t, 403, send(), "legacy field does not bypass missing RBAC grants")
		roles.err = errors.New("database offline")
		require.Equal(t, 500, send())
		roles.err = nil
		roles.roles = []service.RolePermissions{{SuperAdmin: true}}
		require.Equal(t, 204, send())
		user.Status = service.StatusDisabled
		require.Equal(t, 401, send())
		user.Status = service.StatusActive
	}
}

func TestRBACAdminEntryMissingCheckerFailsClosed(t *testing.T) {
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "rbac-nil-secret", ExpireHour: 1}}
	auth := service.NewAuthService(nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	user := &service.User{ID: 1, Role: service.RoleAdmin, Status: service.StatusActive}
	users := service.NewUserService(&rbacAuthUserRepo{user: user}, nil, nil, nil)
	token, err := auth.GenerateToken(context.Background(), user)
	require.NoError(t, err)
	r := gin.New()
	r.Use(gin.HandlerFunc(NewAdminAuthMiddleware(auth, users, nil, nil, nil)))
	r.GET("/admin", func(c *gin.Context) { c.Status(204) })
	req := httptest.NewRequest("GET", "/admin", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 503, w.Code)
}

func (r *rbacAuthUserRepo) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}
