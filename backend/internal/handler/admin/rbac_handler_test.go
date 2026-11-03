package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type rbacHandlerRepo struct {
	role        *service.RBACRole
	roles       []service.RBACRole
	user        service.RBACManagedUser
	permissions map[string]bool
	assigned    []int64
}
type rbacHandlerTx struct{ r *rbacHandlerRepo }

func (r *rbacHandlerRepo) WithTransaction(_ context.Context, fn func(service.RBACManagementTx) error) error {
	return fn(&rbacHandlerTx{r})
}
func (r *rbacHandlerRepo) ListRoles(context.Context) ([]service.RBACRole, error) { return r.roles, nil }
func (r *rbacHandlerRepo) GetRole(_ context.Context, id int64) (*service.RBACRole, error) {
	if r.role != nil && r.role.ID == id {
		out := *r.role
		out.Permissions = append([]string{}, r.role.Permissions...)
		return &out, nil
	}
	return nil, service.ErrRBACRoleNotFound
}
func (r *rbacHandlerRepo) GetUserRoles(context.Context, int64) ([]service.RBACRole, error) {
	return r.roles, nil
}
func (tx *rbacHandlerTx) GetUser(context.Context, int64) (*service.RBACManagedUser, error) {
	u := tx.r.user
	return &u, nil
}
func (tx *rbacHandlerTx) GetRole(ctx context.Context, id int64) (*service.RBACRole, error) {
	return tx.r.GetRole(ctx, id)
}
func (tx *rbacHandlerTx) GetUserRoles(context.Context, int64) ([]service.RBACRole, error) {
	return tx.r.roles, nil
}
func (tx *rbacHandlerTx) CreateRole(_ context.Context, r *service.RBACRole) error {
	r.ID = 7
	tx.r.role = r
	return nil
}
func (tx *rbacHandlerTx) UpdateRole(_ context.Context, role *service.RBACRole) error {
	tx.r.role = role
	return nil
}
func (tx *rbacHandlerTx) DeleteRole(context.Context, int64) error { tx.r.role = nil; return nil }
func (tx *rbacHandlerTx) SetRolePermissions(_ context.Context, id int64, codes []string) error {
	tx.r.role.Permissions = codes
	return nil
}
func (tx *rbacHandlerTx) SetUserRoles(_ context.Context, _ int64, ids []int64) error {
	tx.r.assigned = ids
	return nil
}

func rbacHandlerRequest(t *testing.T, h *RBACHandler, method, path, body string) (*httptest.ResponseRecorder, *gin.Context) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Params = gin.Params{{Key: "id", Value: "7"}}
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
	c.Set("auth_method", service.AuditAuthMethodJWT)
	switch {
	case method == http.MethodPost:
		h.CreateRole(c)
	case method == http.MethodPut && strings.HasSuffix(path, "/permissions"):
		h.SetRolePermissions(c)
	default:
		h.ListRoles(c)
	}
	return w, c
}
func TestRBACHandlerRoleCreateValidatesAndReturnsCreated(t *testing.T) {
	repo := &rbacHandlerRepo{user: service.RBACManagedUser{ID: 1, Status: service.StatusActive}, roles: []service.RBACRole{{ID: 1, Permissions: []string{"roles.create"}}}}
	h := NewRBACHandler(service.NewRBACManagementService(repo))
	w, _ := rbacHandlerRequest(t, h, http.MethodPost, "/admin/roles", `{"code":"auditor","name":"Auditor","description":"Read only"}`)
	require.Equal(t, http.StatusCreated, w.Code)
	require.Equal(t, "auditor", repo.role.Code)
	require.Empty(t, repo.role.Permissions)
}
func TestRBACHandlerRejectsInvalidJSONWithoutMutation(t *testing.T) {
	repo := &rbacHandlerRepo{user: service.RBACManagedUser{ID: 1, Status: service.StatusActive}, roles: []service.RBACRole{{ID: 1, Permissions: []string{"roles.create"}}}}
	h := NewRBACHandler(service.NewRBACManagementService(repo))
	w, _ := rbacHandlerRequest(t, h, http.MethodPost, "/admin/roles", `{"code":"bad code"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Nil(t, repo.role)
}
func TestRBACHandlerPermissionReplacementAuditsAction(t *testing.T) {
	repo := &rbacHandlerRepo{user: service.RBACManagedUser{ID: 1, Status: service.StatusActive}, role: &service.RBACRole{ID: 7, Code: "auditor", Name: "Auditor"}, roles: []service.RBACRole{{ID: 1, Permissions: []string{"roles.permissions.assign", "users.read"}}}}
	h := NewRBACHandler(service.NewRBACManagementService(repo))
	w, c := rbacHandlerRequest(t, h, http.MethodPut, "/admin/roles/7/permissions", `{"permissions":["users.read"]}`)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, []string{"users.read"}, repo.role.Permissions)
	require.Equal(t, "role.permissions_updated", c.GetString("audit_action"))
}

func TestRBACHandlerRejectsMalformedAndPrivilegedFields(t *testing.T) {
	for _, body := range []string{
		`null`, `[]`, `{}`, `{"code":"reader","name":"Reader","is_super_admin":true}`,
		`{"code":"reader","name":"Reader","admin_api_key":true}`,
		`{"code":"reader","name":"Reader","permissions":["users.delete"]}`,
		`{"code":"reader","name":"Reader"} {}`,
		`{"code":"reader","name":"Reader","description":"` + strings.Repeat("x", 65536) + `"}`,
	} {
		t.Run(body[:min(len(body), 65)], func(t *testing.T) {
			repo := &rbacHandlerRepo{user: service.RBACManagedUser{ID: 1, Status: service.StatusActive}, roles: []service.RBACRole{{Permissions: []string{"roles.create"}}}}
			w, _ := rbacHandlerRequest(t, NewRBACHandler(service.NewRBACManagementService(repo)), http.MethodPost, "/admin/roles", body)
			require.Equal(t, 400, w.Code)
			require.Nil(t, repo.role)
		})
	}
}
func TestRBACHandlerExplicitArrayRequiredAndUnknownPermission(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
	}{{`{}`, 400}, {`null`, 400}, {`{"permissions":null}`, 400}, {`{"permissions":[]}`, 200}, {`{"permissions":["unknown"]}`, 400}} {
		repo := &rbacHandlerRepo{user: service.RBACManagedUser{ID: 1, Status: service.StatusActive}, role: &service.RBACRole{ID: 7, Code: "reader"}, roles: []service.RBACRole{{Permissions: []string{"roles.permissions.assign"}}}}
		w, _ := rbacHandlerRequest(t, NewRBACHandler(service.NewRBACManagementService(repo)), http.MethodPut, "/admin/roles/7/permissions", tc.body)
		require.Equal(t, tc.status, w.Code, tc.body)
	}
}
func TestRBACHandlerReadEndpointsAndInvalidIDs(t *testing.T) {
	repo := &rbacHandlerRepo{role: &service.RBACRole{ID: 7, Code: "reader", Permissions: []string{"users.read"}}, roles: []service.RBACRole{{ID: 7, Code: "reader"}}}
	h := NewRBACHandler(service.NewRBACManagementService(repo))
	for _, endpoint := range []gin.HandlerFunc{h.GetRole, h.GetRolePermissions, h.GetUserRoles} {
		for _, id := range []string{"-1", "0", "bad", "9223372036854775808", "7"} {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/", nil)
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
			c.Params = gin.Params{{Key: "id", Value: id}}
			endpoint(c)
			if id == "7" {
				require.Equal(t, 200, w.Code)
			} else {
				require.Equal(t, 400, w.Code)
			}
		}
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
	h.ListPermissions(c)
	require.Equal(t, 200, w.Code)
	var body struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.NotEmpty(t, body.Data)
	require.Contains(t, body.Data[0], "code")
	require.NotContains(t, body.Data[0], "Code")
}
func TestRBACHandlerOnlyTrustsAuthenticatedAPIKeyContext(t *testing.T) {
	for _, tc := range []struct {
		name, method string
		marker       bool
		status       int
	}{
		{"header alone", "jwt", false, 403}, {"method alone", "admin_api_key", false, 403}, {"marker alone", "jwt", true, 403}, {"both trusted fields", "admin_api_key", true, 201},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &rbacHandlerRepo{user: service.RBACManagedUser{ID: 1, Status: service.StatusActive}}
			h := NewRBACHandler(service.NewRBACManagementService(repo))
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/", strings.NewReader(`{"code":"reader","name":"Reader"}`))
			c.Request.Header.Set("x-api-key", "untrusted")
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
			c.Set("auth_method", tc.method)
			c.Set(string(middleware.ContextKeySuperAdmin), tc.marker)
			h.CreateRole(c)
			require.Equal(t, tc.status, w.Code)
		})
	}
}
func TestRBACHandlerMissingIdentityAndDependency(t *testing.T) {
	for _, actor := range []int64{0, 1} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/", nil)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: actor})
		var h *RBACHandler
		h.ListRoles(c)
		if actor == 0 {
			require.Equal(t, 401, w.Code)
		} else {
			require.Equal(t, 503, w.Code)
		}
	}
}
func TestRBACHandlerUpdateDeleteAndUserAssignments(t *testing.T) {
	repo := &rbacHandlerRepo{user: service.RBACManagedUser{ID: 1, Status: service.StatusActive}, role: &service.RBACRole{ID: 7, Code: "reader", Name: "Before"}, roles: []service.RBACRole{{Permissions: []string{"roles.update", "roles.delete", "users.roles.assign"}}}}
	h := NewRBACHandler(service.NewRBACManagementService(repo))
	run := func(method, body string, endpoint gin.HandlerFunc) (*httptest.ResponseRecorder, *gin.Context) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(method, "/", strings.NewReader(body))
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		c.Params = gin.Params{{Key: "id", Value: "7"}}
		endpoint(c)
		return w, c
	}
	w, c := run("PUT", `{"name":"After","description":"New"}`, h.UpdateRole)
	require.Equal(t, 200, w.Code)
	extra := c.MustGet("audit_extra").(map[string]any)
	require.Equal(t, "Before", extra["before"].(*service.RBACAuditState).Role.Name)
	require.Equal(t, "After", extra["after"].(*service.RBACAuditState).Role.Name)
	for _, body := range []string{`{}`, `{"role_ids":null}`, `{"role_ids":[-1]}`} {
		w, _ = run("PUT", body, h.SetUserRoles)
		require.Equal(t, 400, w.Code)
	}
	w, c = run("PUT", `{"role_ids":[]}`, h.SetUserRoles)
	require.Equal(t, 200, w.Code)
	require.Empty(t, repo.assigned)
	require.Equal(t, "user.roles_updated", c.GetString("audit_action"))
	w, c = run("DELETE", "", h.DeleteRole)
	require.Equal(t, 200, w.Code)
	require.Nil(t, repo.role)
	require.Equal(t, "role.deleted", c.GetString("audit_action"))
	w, _ = run("GET", "", h.GetRole)
	require.Equal(t, 404, w.Code)
}
