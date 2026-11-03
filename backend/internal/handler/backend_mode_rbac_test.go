package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type compatRBACRepo struct {
	roles []service.RolePermissions
	err   error
	ids   []int64
}

func (r *compatRBACRepo) GetRolePermissions(_ context.Context, id int64) ([]service.RolePermissions, error) {
	r.ids = append(r.ids, id)
	return r.roles, r.err
}

func TestAuthBackendModeRBACLiveBoundary(t *testing.T) {
	settings := service.NewSettingService(&passkeySwitchSettingRepo{value: "true", values: map[string]string{service.SettingKeyBackendModeEnabled: "true"}}, &config.Config{})
	user := &service.User{ID: 42, Role: service.RoleUser, Status: service.StatusActive}
	repo := &compatRBACRepo{roles: []service.RolePermissions{{Permissions: []string{"audit_logs.read"}}}}
	h := &AuthHandler{settingSvc: settings, rbacService: service.NewRBACService(repo)}
	require.NoError(t, h.ensureBackendModeAllowsUser(context.Background(), user))
	require.Equal(t, service.RoleUser, user.Role)
	repo.roles = nil
	require.Error(t, h.ensureBackendModeAllowsUser(context.Background(), user))
	user.Role = service.RoleAdmin
	require.Error(t, h.ensureBackendModeAllowsUser(context.Background(), user), "legacy role must not bypass revocation")
	repo.roles = []service.RolePermissions{{SuperAdmin: true}}
	require.NoError(t, h.ensureBackendModeAllowsUser(context.Background(), user))
	repo.err = errors.New("database down")
	require.Error(t, h.ensureBackendModeAllowsUser(context.Background(), user))
	h.rbacService = nil
	require.Error(t, h.ensureBackendModeAllowsUser(context.Background(), user))
	require.Error(t, h.ensureBackendModeAllowsUser(context.Background(), nil))
	require.NotEmpty(t, repo.ids)
	for _, id := range repo.ids {
		require.Equal(t, int64(42), id)
	}
}

func TestPageAdminVisibilityRequiresTrustedRBAC(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := service.NewSettingService(&passkeySwitchSettingRepo{value: `[{"page_slug":"guide","visibility":"admin"}]`}, &config.Config{})
	h := NewPageHandler(t.TempDir(), settings)
	for _, tc := range []struct {
		name, role string
		access     any
		subject    bool
		want       bool
	}{
		{"rbac user", "user", true, true, true},
		{"revoked legacy admin", "admin", false, true, false},
		{"missing snapshot", "admin", nil, true, false},
		{"malformed snapshot", "admin", "true", true, false},
		{"no subject", "user", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("GET", "/pages/guide", nil)
			c.Set(string(middleware.ContextKeyUserRole), tc.role)
			if tc.subject {
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
			}
			if tc.access != nil {
				c.Set("admin_access", tc.access)
			}
			c.Request.Header.Set("X-Admin-Access", "true")
			require.Equal(t, tc.want, h.checkSlugVisibility(c, "guide"))
			require.False(t, h.checkImageSlugVisibility(c, "guide"))
		})
	}
}

func TestPasskeyBackendModeRBACLiveBoundary(t *testing.T) {
	settings := service.NewSettingService(&passkeySwitchSettingRepo{value: "true"}, &config.Config{})
	require.NoError(t, settings.UpdateSettings(context.Background(), &service.SystemSettings{BackendModeEnabled: true}))
	t.Cleanup(func() {
		require.NoError(t, settings.UpdateSettings(context.Background(), &service.SystemSettings{BackendModeEnabled: false}))
	})
	repo := &compatRBACRepo{roles: []service.RolePermissions{{Permissions: []string{"pages.read"}}}}
	h := NewPasskeyHandler(nil, nil, settings, service.NewRBACService(repo))
	user := &service.User{ID: 42, Role: service.RoleUser, Status: service.StatusActive}
	require.NoError(t, h.ensureBackendModeAllowsUser(context.Background(), user))
	require.Equal(t, service.RoleUser, user.Role)
	repo.roles = nil
	require.Error(t, h.ensureBackendModeAllowsUser(context.Background(), user))
	user.Role = service.RoleAdmin
	require.Error(t, h.ensureBackendModeAllowsUser(context.Background(), user))
	repo.roles = []service.RolePermissions{{Permissions: []string{"unknown.retired"}}}
	require.Error(t, h.ensureBackendModeAllowsUser(context.Background(), user))
	repo.roles = []service.RolePermissions{{SuperAdmin: true}}
	require.NoError(t, h.ensureBackendModeAllowsUser(context.Background(), user))
	repo.err = errors.New("db unavailable")
	require.Error(t, h.ensureBackendModeAllowsUser(context.Background(), user))
	h = NewPasskeyHandler(nil, nil, settings, nil)
	require.Error(t, h.ensureBackendModeAllowsUser(context.Background(), user))
	require.Error(t, h.ensureBackendModeAllowsUser(context.Background(), nil))
	user.Status = service.StatusDisabled
	require.Error(t, h.ensureBackendModeAllowsUser(context.Background(), user))
}

func TestPasswordAndOAuthBackendModeRBAC(t *testing.T) {
	for _, kind := range []string{"password", "oauth"} {
		t.Run(kind, func(t *testing.T) {
			h, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{settingValues: map[string]string{service.SettingKeyBackendModeEnabled: "true"}})
			repo := &compatRBACRepo{roles: []service.RolePermissions{{Permissions: []string{"pages.read"}}}}
			h.SetRBACService(service.NewRBACService(repo))
			hash, err := h.authService.HashPassword("secret-123")
			require.NoError(t, err)
			user, err := client.User.Create().SetEmail("rbac@example.com").SetUsername("rbac").SetPasswordHash(hash).SetRole(service.RoleUser).SetStatus(service.StatusActive).Save(context.Background())
			require.NoError(t, err)
			for i, want := range []int{200, 403} {
				if i == 1 {
					repo.roles = nil
				}
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				if kind == "password" {
					c.Request = httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"email":"rbac@example.com","password":"secret-123","role":"admin","is_super_admin":true}`))
					c.Request.Header.Set("Content-Type", "application/json")
					h.Login(c)
				} else {
					token := []string{"allowed-session", "revoked-session"}[i]
					_, err := client.PendingAuthSession.Create().SetSessionToken(token).SetIntent("login").SetProviderType("linuxdo").SetProviderKey("linuxdo").SetProviderSubject("rbac-subject").SetTargetUserID(user.ID).SetResolvedEmail(user.Email).SetBrowserSessionKey("browser-session").SetLocalFlowState(map[string]any{oauthCompletionResponseKey: map[string]any{"access_token": "issued-token", "redirect": "/dashboard"}}).SetExpiresAt(time.Now().UTC().Add(10 * time.Minute)).Save(context.Background())
					require.NoError(t, err)
					c.Request = httptest.NewRequest("POST", "/api/v1/auth/oauth/pending/exchange", nil)
					c.Request.AddCookie(&http.Cookie{Name: oauthPendingSessionCookieName, Value: encodeCookieValue(token)})
					c.Request.AddCookie(&http.Cookie{Name: oauthPendingBrowserCookieName, Value: encodeCookieValue("browser-session")})
					h.ExchangePendingOAuthCompletion(c)
				}
				require.Equal(t, want, w.Code, w.Body.String())
				if want == 200 {
					require.Contains(t, w.Body.String(), "access_token")
				} else {
					require.NotContains(t, w.Body.String(), "access_token")
				}
				stored, err := client.User.Get(context.Background(), user.ID)
				require.NoError(t, err)
				require.Equal(t, service.RoleUser, stored.Role)
			}
		})
	}
}

type compatUserRepo struct {
	service.UserRepository
	user *service.User
}

func (r *compatUserRepo) GetByID(_ context.Context, id int64) (*service.User, error) {
	if id != r.user.ID {
		return nil, service.ErrUserNotFound
	}
	return r.user, nil
}
func (r *compatUserRepo) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}
func (r *compatUserRepo) UpdateUserLastActiveAt(context.Context, int64, time.Time) error { return nil }

type compatRefreshCache struct {
	service.RefreshTokenCache
	tokens map[string]*service.RefreshTokenData
}

func (r *compatRefreshCache) StoreRefreshToken(_ context.Context, hash string, data *service.RefreshTokenData, _ time.Duration) error {
	r.tokens[hash] = data
	return nil
}
func (r *compatRefreshCache) GetRefreshToken(_ context.Context, hash string) (*service.RefreshTokenData, error) {
	data, ok := r.tokens[hash]
	if !ok {
		return nil, service.ErrRefreshTokenNotFound
	}
	return data, nil
}
func (r *compatRefreshCache) DeleteRefreshToken(_ context.Context, hash string) error {
	delete(r.tokens, hash)
	return nil
}
func (r *compatRefreshCache) AddToUserTokenSet(context.Context, int64, string, time.Duration) error {
	return nil
}
func (r *compatRefreshCache) AddToFamilyTokenSet(context.Context, string, string, time.Duration) error {
	return nil
}

func TestRefreshBackendModeRBAC(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name       string
		legacyRole string
		roles      []service.RolePermissions
		err        error
		nilRBAC    bool
		want       int
	}{
		{name: "ordinary role", roles: []service.RolePermissions{{Permissions: []string{"pages.read"}}}, want: 200},
		{name: "super", roles: []service.RolePermissions{{SuperAdmin: true}}, want: 200},
		{name: "revoked legacy admin", legacyRole: service.RoleAdmin, want: 403},
		{name: "ordinary non rbac", want: 403},
		{name: "retired permission", roles: []service.RolePermissions{{Permissions: []string{"retired.unknown"}}}, want: 403},
		{name: "rbac failure", err: errors.New("db unavailable"), want: 500},
		{name: "nil rbac", nilRBAC: true, want: 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := &service.User{ID: 42, Email: "refresh@example.com", Role: service.RoleUser, Status: service.StatusActive}
			if tc.legacyRole != "" {
				user.Role = tc.legacyRole
			}
			cache := &compatRefreshCache{tokens: map[string]*service.RefreshTokenData{}}
			auth := service.NewAuthService(nil, &compatUserRepo{user: user}, nil, cache, &config.Config{JWT: config.JWTConfig{Secret: "test-secret", ExpireHour: 1, RefreshTokenExpireDays: 7}}, nil, nil, nil, nil, nil, nil, nil, nil)
			repo := &compatRBACRepo{roles: tc.roles, err: tc.err}
			h := &AuthHandler{authService: auth, settingSvc: service.NewSettingService(&passkeySwitchSettingRepo{value: "true", values: map[string]string{service.SettingKeyBackendModeEnabled: "true"}}, &config.Config{}), rbacService: service.NewRBACService(repo)}
			if tc.nilRBAC {
				h.rbacService = nil
			}
			pair, err := auth.GenerateTokenPair(context.Background(), user, "")
			require.NoError(t, err)
			send := func(token string) *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest("POST", "/api/v1/auth/refresh", strings.NewReader(`{"refresh_token":"`+token+`","user_id":1,"role":"admin","is_super_admin":true}`))
				c.Request.Header.Set("Content-Type", "application/json")
				c.Request.Header.Set("X-Admin-Access", "true")
				h.RefreshToken(c)
				return w
			}
			w := send(pair.RefreshToken)
			require.Equal(t, tc.want, w.Code, w.Body.String())
			if tc.want == 200 {
				var body struct {
					Data RefreshTokenResponse `json:"data"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
				require.NotEmpty(t, body.Data.AccessToken)
				repo.roles = nil
				w = send(body.Data.RefreshToken)
				require.Equal(t, 403, w.Code, w.Body.String())
			}
			require.NotContains(t, w.Body.String(), "access_token")
			require.NotContains(t, w.Body.String(), "refresh_token")
			require.Empty(t, cache.tokens, "denied rotation must revoke the newly issued refresh token")
			for _, id := range repo.ids {
				require.Equal(t, int64(42), id)
			}
		})
	}
}

func TestPageBackendModeJWTLiveRBAC(t *testing.T) {
	gin.SetMode(gin.TestMode)
	user := &service.User{ID: 42, Role: service.RoleUser, Status: service.StatusActive}
	users := &compatUserRepo{user: user}
	auth := service.NewAuthService(nil, users, nil, nil, &config.Config{JWT: config.JWTConfig{Secret: "test-secret", ExpireHour: 1}}, nil, nil, nil, nil, nil, nil, nil, nil)
	userSvc := service.NewUserService(users, nil, nil, nil)
	repo := &compatRBACRepo{roles: []service.RolePermissions{{Permissions: []string{"audit_logs.read"}}}}
	settings := service.NewSettingService(&passkeySwitchSettingRepo{value: `[{"page_slug":"guide","visibility":"admin"}]`}, &config.Config{})
	require.NoError(t, settings.UpdateSettings(context.Background(), &service.SystemSettings{BackendModeEnabled: true}))
	t.Cleanup(func() {
		require.NoError(t, settings.UpdateSettings(context.Background(), &service.SystemSettings{BackendModeEnabled: false}))
	})
	dir := t.TempDir()
	h := NewPageHandler(dir, settings)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pages", "guide.md"), []byte("# Admin guide"), 0600))
	r := gin.New()
	r.Use(gin.HandlerFunc(middleware.NewJWTAuthMiddleware(auth, userSvc, nil, nil, service.NewRBACService(repo))))
	r.GET("/pages/:slug", h.GetPageContent)
	r.GET("/backend/pages/:slug", middleware.BackendModeUserGuard(settings), h.GetPageContent)
	token, err := auth.GenerateToken(context.Background(), user)
	require.NoError(t, err)
	for _, tc := range []struct {
		name          string
		roles         []service.RolePermissions
		err           error
		page, backend int
	}{
		{"ordinary RBAC", repo.roles, nil, 200, 200},
		{"revoked", nil, nil, 404, 403},
		{"error", nil, errors.New("db down"), 500, 500},
		{"super", []service.RolePermissions{{SuperAdmin: true}}, nil, 200, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo.roles, repo.err = tc.roles, tc.err
			for _, path := range []string{"/pages/guide", "/backend/pages/guide"} {
				req := httptest.NewRequest("GET", path, nil)
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("X-Admin-Access", "true")
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				want := tc.page
				if strings.HasPrefix(path, "/backend") {
					want = tc.backend
				}
				require.Equal(t, want, w.Code, w.Body.String())
			}
			require.Equal(t, service.RoleUser, user.Role)
		})
	}
}

func TestPageUserVisibilityPreservedWithoutAdminSnapshot(t *testing.T) {
	h := NewPageHandler(t.TempDir(), service.NewSettingService(&passkeySwitchSettingRepo{value: `[{"page_slug":"guide","visibility":"user"}]`}, &config.Config{}))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/pages/guide", nil)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
	c.Set(string(middleware.ContextKeyUserRole), service.RoleUser)
	require.True(t, h.checkSlugVisibility(c, "guide"))
	require.True(t, h.checkImageSlugVisibility(c, "guide"))
	require.False(t, h.checkSlugVisibility(c, "unknown"))
	require.False(t, h.checkImageSlugVisibility(c, "unknown"))
}
