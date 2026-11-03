//go:build integration

package repository_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dbrole "github.com/Wei-Shaw/sub2api/ent/role"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Real JWT authentication -> permission middleware -> handler -> service -> Ent
// transactions -> PostgreSQL, plus the production async audit queue/repository.
func TestRBACAPIIntegrationAuthenticatedLifecycleAndAudit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, client := repository.IntegrationTestDatabase(t)
	require.NoError(t, repository.SyncRBAC(ctx, db))
	prefix := fmt.Sprintf("rbac_http_%d", time.Now().UnixNano())
	var users, roles []int64
	t.Cleanup(func() {
		_, err := db.Exec("DELETE FROM users WHERE id = ANY($1)", pq.Array(users))
		require.NoError(t, err)
		_, err = db.Exec("DELETE FROM roles WHERE id = ANY($1)", pq.Array(roles))
		require.NoError(t, err)
	})
	userRepo := repository.NewUserRepository(client, db)
	auth := service.NewAuthService(nil, nil, nil, nil, &config.Config{JWT: config.JWTConfig{Secret: "isolated-rbac-http-integration-secret", ExpireHour: 1}}, nil, nil, nil, nil, nil, nil, nil, nil)
	var tokens []string
	for i := 0; i < 3; i++ {
		user, err := client.User.Create().SetEmail(fmt.Sprintf("%s_%d@example.com", prefix, i)).SetPasswordHash("fixture-not-a-credential").Save(ctx)
		require.NoError(t, err)
		users = append(users, user.ID)
		loaded, err := userRepo.GetByID(ctx, user.ID)
		require.NoError(t, err)
		token, err := auth.GenerateToken(ctx, loaded)
		require.NoError(t, err)
		tokens = append(tokens, token)
	}
	superID, err := client.Role.Query().Where(dbrole.CodeEQ("super_admin")).OnlyID(ctx)
	require.NoError(t, err)
	_, err = client.UserRole.Create().SetUserID(users[0]).SetRoleID(superID).Save(ctx)
	require.NoError(t, err)
	management := service.NewRBACManagementService(repository.NewRBACManagementRepository(client))
	root := service.RBACActor{UserID: users[0]}
	manager, err := management.CreateRole(ctx, root, service.CreateRBACRoleInput{Code: prefix + "_manager", Name: "Manager"})
	require.NoError(t, err)
	roles = append(roles, manager.ID)
	powers := []string{"roles.read", "roles.create", "roles.update", "roles.delete", "roles.permissions.assign", "users.roles.assign", "users.read"}
	require.NoError(t, management.SetRolePermissions(ctx, root, manager.ID, powers))
	require.NoError(t, management.SetUserRoles(ctx, root, users[1], []int64{manager.ID}))
	rbac := service.NewRBACService(repository.NewRBACRepository(client))
	audit := service.NewAuditLogService(repository.NewAuditLogRepository(db), nil)
	audit.Start()
	defer audit.Stop()
	handler := admin.NewRBACHandler(management)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.HandlerFunc(middleware.NewAdminAuthMiddleware(auth, service.NewUserService(userRepo, nil, nil, nil), nil, audit, rbac)))
	// Check the actual authenticated request.Context, not a test-injected actor.
	router.Use(func(c *gin.Context) {
		actor, ok := service.RBACActorFromContext(c.Request.Context())
		require.True(t, ok)
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		require.True(t, ok)
		require.Equal(t, subject.UserID, actor.UserID)
		require.False(t, actor.AdminAPIKey)
		c.Next()
	})
	router.Use(gin.HandlerFunc(middleware.NewAuditLogMiddleware(audit)))
	router.POST("/api/v1/admin/roles", middleware.RequirePermission(rbac, "roles.create"), handler.CreateRole)
	router.GET("/api/v1/admin/roles/:id", middleware.RequirePermission(rbac, "roles.read"), handler.GetRole)
	router.PUT("/api/v1/admin/roles/:id", middleware.RequirePermission(rbac, "roles.update"), handler.UpdateRole)
	router.DELETE("/api/v1/admin/roles/:id", middleware.RequirePermission(rbac, "roles.delete"), handler.DeleteRole)
	router.PUT("/api/v1/admin/roles/:id/permissions", middleware.RequirePermission(rbac, "roles.permissions.assign"), handler.SetRolePermissions)
	router.PUT("/api/v1/admin/users/:id/roles", middleware.RequirePermission(rbac, "users.roles.assign"), handler.SetUserRoles)
	request := func(token, method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		router.ServeHTTP(w, req)
		require.Equal(t, status, w.Code, w.Body.String())
		return w
	}
	w := request(tokens[1], "POST", "/api/v1/admin/roles", fmt.Sprintf(`{"code":%q,"name":"Reader"}`, prefix+"_reader"), http.StatusCreated)
	var created struct {
		Data service.RBACRole `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	require.Positive(t, created.Data.ID)
	roles = append(roles, created.Data.ID)
	path := fmt.Sprintf("/api/v1/admin/roles/%d", created.Data.ID)
	request(tokens[1], "POST", "/api/v1/admin/roles", fmt.Sprintf(`{"code":%q,"name":"Duplicate"}`, prefix+"_reader"), 409)
	request(tokens[1], "PUT", path, `{"name":"Updated reader","description":"database-backed change"}`, 200)
	request(tokens[1], "PUT", path+"/permissions", `{"permissions":["users.read","users.read"]}`, 200)
	// A database commit failure must roll back the mutation and must not emit a
	// transaction-produced success snapshot into the asynchronous audit queue.
	functionName := pq.QuoteIdentifier(prefix + "_reject")
	triggerName := pq.QuoteIdentifier(prefix + "_commit")
	_, err = db.Exec("CREATE FUNCTION " + functionName + "() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected HTTP commit failure'; END $$")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := db.Exec("DROP TRIGGER IF EXISTS " + triggerName + " ON roles")
		require.NoError(t, err)
		_, err = db.Exec("DROP FUNCTION IF EXISTS " + functionName + "()")
		require.NoError(t, err)
	})
	_, err = db.Exec(fmt.Sprintf("CREATE CONSTRAINT TRIGGER %s AFTER UPDATE ON roles DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.id = %d) EXECUTE FUNCTION %s()", triggerName, created.Data.ID, functionName))
	require.NoError(t, err)
	request(tokens[1], "PUT", path, `{"name":"must-rollback-at-commit"}`, 500)
	unchanged, err := management.GetRole(ctx, created.Data.ID)
	require.NoError(t, err)
	require.Equal(t, "Updated reader", unchanged.Name)
	_, err = db.Exec("DROP TRIGGER " + triggerName + " ON roles")
	require.NoError(t, err)

	request(tokens[1], "PUT", fmt.Sprintf("/api/v1/admin/users/%d/roles", users[2]), fmt.Sprintf(`{"role_ids":[%d,%d]}`, created.Data.ID, created.Data.ID), 200)
	snap, err := rbac.Snapshot(ctx, users[2])
	require.NoError(t, err)
	require.Equal(t, []string{"users.read"}, snap.Permissions)
	require.Len(t, snap.Roles, 1)
	request(tokens[1], "PUT", path+"/permissions", `{"permissions":["payments.refund"]}`, 403)
	request(tokens[1], "PUT", path+"/permissions", `{"permissions":["not.real"]}`, 400)
	request(tokens[1], "PUT", path, `{"name":"forged","admin_api_key":true,"password":"must-not-leak"}`, 400)
	request(tokens[0], "DELETE", fmt.Sprintf("/api/v1/admin/roles/%d", superID), "", 403)
	// Revocation is visible with the same JWT on the very next request.
	require.NoError(t, management.SetRolePermissions(ctx, root, manager.ID, []string{"roles.read", "roles.delete", "users.read"}))
	request(tokens[1], "PUT", path, `{"name":"must-not-commit"}`, 403)
	stored, err := management.GetRole(ctx, created.Data.ID)
	require.NoError(t, err)
	require.Equal(t, "Updated reader", stored.Name)
	require.Equal(t, []string{"users.read"}, stored.Permissions)
	request(tokens[1], "GET", path, "", 200)
	request(tokens[1], "DELETE", path, "", 200)
	snap, err = rbac.Snapshot(ctx, users[2])
	require.NoError(t, err)
	require.Empty(t, snap.Roles)
	// Token version and active status are reloaded from the real user repository.
	require.NoError(t, client.User.UpdateOneID(users[1]).SetPasswordHash("changed-password-fingerprint").Exec(ctx))
	request(tokens[1], "GET", path, "", 401)
	request("invalid-jwt", "POST", "/api/v1/admin/roles", `{}`, 401)
	audit.Stop() // deterministically drain production queue before inspecting DB
	rows, err := db.QueryContext(ctx, `SELECT actor_user_id,auth_method,action,status_code,request_body,extra FROM audit_logs WHERE actor_user_id=ANY($1) ORDER BY id`, pq.Array(users))
	require.NoError(t, err)
	defer rows.Close()
	actions := map[string]int{}
	failures := 0
	for rows.Next() {
		var actor int64
		var method, action, body string
		var status int
		var raw []byte
		require.NoError(t, rows.Scan(&actor, &method, &action, &status, &body, &raw))
		require.Equal(t, "jwt", method)
		require.Contains(t, users, actor)
		require.NotContains(t, body, "must-not-leak")
		var extra map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &extra))
		if status >= 400 {
			failures++
			require.NotContains(t, extra, "before")
			require.NotContains(t, extra, "after")
			continue
		}
		require.Equal(t, users[1], actor)
		actions[action]++
		if action == "role.read" {
			require.NotContains(t, extra, "before")
			require.NotContains(t, extra, "after")
			continue
		}
		require.Contains(t, extra, "before")
		require.Contains(t, extra, "after")
		require.Contains(t, extra, "target_id")
		switch action {
		case "role.created":
			require.JSONEq(t, `null`, string(extra["before"]))
			require.Contains(t, string(extra["after"]), "Reader")
		case "role.updated":
			require.Contains(t, string(extra["before"]), "Reader")
			require.Contains(t, string(extra["after"]), "Updated reader")
		case "role.permissions_updated":
			require.Contains(t, string(extra["after"]), "users.read")
		case "user.roles_updated":
			require.Contains(t, string(extra["after"]), "Updated reader")
		case "role.deleted":
			require.Contains(t, string(extra["before"]), "Updated reader")
			require.JSONEq(t, `null`, string(extra["after"]))
		}
	}
	require.NoError(t, rows.Err())
	require.Equal(t, map[string]int{"role.created": 1, "role.updated": 1, "role.permissions_updated": 1, "user.roles_updated": 1, "role.deleted": 1, "role.read": 1}, actions)
	require.Equal(t, 7, failures)
}
