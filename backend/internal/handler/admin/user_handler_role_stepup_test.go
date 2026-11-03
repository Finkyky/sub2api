package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 角色提升为管理员的 step-up 门控条件测试。
// 测试环境不注入认证上下文，因此门控一旦触发会以 401 中止；
// 借此区分「触发了 step-up 校验」与「直接放行到业务层（200）」。
func setupRoleStepUpRouter(t *testing.T, handlers ...gin.HandlerFunc) (*gin.Engine, *roleStepUpAdminService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(handlers...)
	adminSvc := &roleStepUpAdminService{stubAdminService: newStubAdminService()}
	// 追加一个已是管理员的目标用户，验证「目标已是 admin 不触发门控」。
	adminSvc.users = append(adminSvc.users, service.User{
		ID:     2,
		Email:  "admin@example.com",
		Role:   service.RoleAdmin,
		Status: service.StatusActive,
	})

	h := NewUserHandler(adminSvc, nil, nil, nil, nil, nil, nil)
	router.POST("/api/v1/admin/users", h.Create)
	router.PUT("/api/v1/admin/users/:id", h.Update)
	return router, adminSvc
}

func doJSON(t *testing.T, router *gin.Engine, method, path string, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	return rec
}

func TestUpdateUserPromoteToAdminRequiresStepUp(t *testing.T) {
	router, _ := setupRoleStepUpRouter(t)

	rec := doJSON(t, router, http.MethodPut, "/api/v1/admin/users/1", map[string]any{"role": "admin"})
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUpdateUserKeepAdminRoleSkipsStepUp(t *testing.T) {
	router, _ := setupRoleStepUpRouter(t)

	rec := doJSON(t, router, http.MethodPut, "/api/v1/admin/users/2", map[string]any{"role": "admin"})
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestUpdateUserRegularRoleSkipsStepUp(t *testing.T) {
	router, _ := setupRoleStepUpRouter(t)

	rec := doJSON(t, router, http.MethodPut, "/api/v1/admin/users/1", map[string]any{"role": "user", "email": "u@example.com"})
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCreateAdminUserRequiresStepUp(t *testing.T) {
	router, _ := setupRoleStepUpRouter(t)

	rec := doJSON(t, router, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"email": "new-admin@example.com", "password": "pass123", "role": "admin",
	})
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestCreateRegularUserSkipsStepUp(t *testing.T) {
	router, _ := setupRoleStepUpRouter(t)

	rec := doJSON(t, router, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"email": "new-user@example.com", "password": "pass123", "role": "user",
	})
	require.Equal(t, http.StatusOK, rec.Code)
}

// Record whether the handler reached the mutation without changing the shared stub.
type roleStepUpAdminService struct {
	*stubAdminService
	updateCalls int
	updateID    int64
	updateInput *service.UpdateUserInput
}

func (s *roleStepUpAdminService) UpdateUser(ctx context.Context, id int64, input *service.UpdateUserInput) (*service.User, error) {
	s.updateCalls++
	s.updateID = id
	s.updateInput = input
	return s.stubAdminService.UpdateUser(ctx, id, input)
}

func TestUpdateUserSelfRoleProtection(t *testing.T) {
	for _, tc := range []struct {
		name          string
		currentRole   string
		requestedRole string
		getUserErr    error
		wantStatus    int
		wantError     string
	}{
		{name: "RBAC user keeps user role", currentRole: service.RoleUser, requestedRole: service.RoleUser, wantStatus: http.StatusOK},
		{name: "legacy admin cannot demote self", currentRole: service.RoleAdmin, requestedRole: service.RoleUser, wantStatus: http.StatusBadRequest, wantError: "cannot demote yourself from admin"},
		{name: "RBAC user omits role", currentRole: service.RoleUser, wantStatus: http.StatusOK},
		{name: "legacy admin omits role", currentRole: service.RoleAdmin, wantStatus: http.StatusOK},
		{name: "role lookup failure stops update", currentRole: service.RoleUser, requestedRole: service.RoleUser, getUserErr: service.ErrUserNotFound, wantStatus: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router, svc := setupRoleStepUpRouter(t, func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
				// Admin authentication exposes a request capability, not the stored role.
				c.Set(string(middleware.ContextKeyUserRole), service.RoleAdmin)
				c.Request = c.Request.WithContext(service.WithRBACActor(c.Request.Context(), service.RBACActor{UserID: 1}))
				c.Next()
			})
			svc.users[0].Role = tc.currentRole
			svc.getUserErr = tc.getUserErr
			payload := map[string]any{"username": "updated-name"}
			if tc.requestedRole != "" {
				payload["role"] = tc.requestedRole
			}
			rec := doJSON(t, router, http.MethodPut, "/api/v1/admin/users/1", payload)
			require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			if tc.wantError != "" {
				require.Contains(t, rec.Body.String(), tc.wantError)
			}
			if tc.wantStatus != http.StatusOK {
				require.Zero(t, svc.updateCalls)
				return
			}
			require.Equal(t, 1, svc.updateCalls)
			require.Equal(t, int64(1), svc.updateID)
			require.Equal(t, int64(1), svc.updateInput.ActorAdminID)
			require.Equal(t, tc.requestedRole, svc.updateInput.Role)
			require.NotNil(t, svc.updateInput.Username)
			require.Equal(t, "updated-name", *svc.updateInput.Username)
		})
	}
}
