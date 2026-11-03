//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbrole "github.com/Wei-Shaw/sub2api/ent/role"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type rbacFailingUserUpdate struct{ service.UserRepository }

func (r rbacFailingUserUpdate) Update(ctx context.Context, u *service.User, f service.UserUpdateFields) error {
	if err := r.UserRepository.Update(ctx, u, f); err != nil {
		return err
	}
	return errors.New("forced failure after user update")
}
func rbacIntegrationAdminService(client *dbent.Client, repo service.UserRepository) service.AdminService {
	return service.NewAdminService(nil, repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, client, nil, nil, nil, nil, nil, nil, nil, nil, nil)
}

func TestRBACLegacyUserMutationIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client := testEntClient(t)
	require.NoError(t, SyncRBAC(ctx, integrationDB))
	prefix := fmt.Sprintf("rbac_user_guard_%d", time.Now().UnixNano())
	var userIDs, roleIDs []int64
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if len(userIDs) > 0 {
			_, err := integrationDB.ExecContext(cleanup, "DELETE FROM users WHERE id=ANY($1)", pq.Array(userIDs))
			require.NoError(t, err)
		}
		if len(roleIDs) > 0 {
			_, err := integrationDB.ExecContext(cleanup, "DELETE FROM roles WHERE id=ANY($1)", pq.Array(roleIDs))
			require.NoError(t, err)
		}
	})
	for i := 0; i < 4; i++ {
		u, err := client.User.Create().SetEmail(fmt.Sprintf("%s_%d@example.com", prefix, i)).SetPasswordHash("original-hash").Save(ctx)
		require.NoError(t, err)
		userIDs = append(userIDs, u.ID)
	}
	superID, err := client.Role.Query().Where(dbrole.CodeEQ("super_admin")).OnlyID(ctx)
	require.NoError(t, err)
	_, err = client.UserRole.Create().SetUserID(userIDs[0]).SetRoleID(superID).Save(ctx)
	require.NoError(t, err)
	management := service.NewRBACManagementService(NewRBACManagementRepository(client))
	root := service.RBACActor{UserID: userIDs[0]}
	role, err := management.CreateRole(ctx, root, service.CreateRBACRoleInput{Code: prefix, Name: "User operator"})
	require.NoError(t, err)
	roleIDs = append(roleIDs, role.ID)
	require.NoError(t, management.SetRolePermissions(ctx, root, role.ID, []string{"users.create", "users.update", "users.delete", "users.read"}))
	require.NoError(t, management.SetUserRoles(ctx, root, userIDs[1], []int64{role.ID}))
	elevated, err := management.CreateRole(ctx, root, service.CreateRBACRoleInput{Code: prefix + "_higher", Name: "Settings operator"})
	require.NoError(t, err)
	roleIDs = append(roleIDs, elevated.ID)
	require.NoError(t, management.SetRolePermissions(ctx, root, elevated.ID, []string{"settings.update"}))
	require.NoError(t, management.SetUserRoles(ctx, root, userIDs[3], []int64{elevated.ID}))
	repo := NewUserRepository(client, integrationDB)
	svc := rbacIntegrationAdminService(client, repo)
	actorCtx := service.WithRBACActor(ctx, service.RBACActor{UserID: userIDs[1]})
	rootCtx := service.WithRBACActor(ctx, root)
	_, err = svc.UpdateUser(actorCtx, userIDs[0], &service.UpdateUserInput{Password: "forbidden-password"})
	require.ErrorIs(t, err, service.ErrRBACProtectedUser)
	_, err = svc.UpdateUser(actorCtx, userIDs[3], &service.UpdateUserInput{Email: prefix + "_takeover@example.com"})
	require.ErrorIs(t, err, service.ErrRBACPermissionCeiling)
	_, err = svc.UpdateUser(actorCtx, userIDs[1], &service.UpdateUserInput{Role: service.RoleAdmin})
	require.ErrorIs(t, err, service.ErrRBACForbidden)
	_, err = svc.CreateUser(actorCtx, &service.CreateUserInput{Email: prefix + "_admin@example.com", Password: "test-password", Role: service.RoleAdmin})
	require.ErrorIs(t, err, service.ErrRBACForbidden)
	_, err = svc.BindUserAuthIdentity(actorCtx, userIDs[0], service.AdminBindAuthIdentityInput{})
	require.ErrorIs(t, err, service.ErrRBACProtectedUser)
	require.ErrorIs(t, svc.DeleteUser(rootCtx, userIDs[0]), service.ErrRBACProtectedUser)
	_, err = svc.UpdateUser(rootCtx, userIDs[0], &service.UpdateUserInput{Status: service.StatusDisabled})
	require.ErrorIs(t, err, service.ErrRBACProtectedUser)
	_, err = svc.UpdateUser(actorCtx, userIDs[2], &service.UpdateUserInput{Password: "changed-password"})
	require.NoError(t, err)
	user, err := repo.GetByID(ctx, userIDs[2])
	require.NoError(t, err)
	require.True(t, user.CheckPassword("changed-password"))
	before := user.PasswordHash
	failing := rbacIntegrationAdminService(client, rbacFailingUserUpdate{repo})
	_, err = failing.UpdateUser(actorCtx, userIDs[2], &service.UpdateUserInput{Password: "rolled-back-password"})
	require.EqualError(t, err, "forced failure after user update")
	user, err = repo.GetByID(ctx, userIDs[2])
	require.NoError(t, err)
	require.Equal(t, before, user.PasswordHash, "external transaction must own credential update")
	created, err := svc.CreateUser(rootCtx, &service.CreateUserInput{Email: prefix + "_newadmin@example.com", Password: "test-password", Role: service.RoleAdmin})
	require.NoError(t, err)
	userIDs = append(userIDs, created.ID)
	snapshot, err := service.NewRBACService(NewRBACRepository(client)).Snapshot(ctx, created.ID)
	require.NoError(t, err)
	require.True(t, snapshot.IsSuperAdmin, "legacy admin promotion binds immediately, not only on restart")
	require.NoError(t, management.SetRolePermissions(ctx, root, role.ID, []string{"users.read"}))
	_, err = svc.UpdateUser(actorCtx, userIDs[2], &service.UpdateUserInput{Password: "revoked-password"})
	require.ErrorIs(t, err, service.ErrRBACForbidden)
}

type rbacPausedUserRead struct {
	service.UserRepository
	entered chan struct{}
	release chan struct{}
}

func (r rbacPausedUserRead) GetByID(ctx context.Context, id int64) (*service.User, error) {
	close(r.entered)
	select {
	case <-r.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return r.UserRepository.GetByID(ctx, id)
}

func TestRBACUserEmailLockOrderIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	client := testEntClient(t)
	require.NoError(t, SyncRBAC(ctx, integrationDB))
	prefix := fmt.Sprintf("rbac_lock_%d", time.Now().UnixNano())
	u, err := client.User.Create().SetEmail(prefix + "@example.com").SetPasswordHash("original").Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM users WHERE id=$1", u.ID) })
	repo := NewUserRepository(client, integrationDB)
	entered, release := make(chan struct{}), make(chan struct{})
	svc := rbacIntegrationAdminService(client, rbacPausedUserRead{repo, entered, release})
	adminResult := make(chan error, 1)
	go func() {
		_, err := svc.UpdateUser(service.WithRBACActor(ctx, service.RBACActor{UserID: u.ID, AdminAPIKey: true}), u.ID, &service.UpdateUserInput{Email: prefix + "_new@example.com"})
		adminResult <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("admin authorization did not reach mutation")
	}
	selfResult := make(chan error, 1)
	go func() {
		tx, err := client.Tx(ctx)
		if err != nil {
			selfResult <- err
			return
		}
		defer tx.Rollback()
		err = repo.(*userRepository).UpdateEmailWithAliasGuard(dbent.NewTxContext(ctx, tx), u.ID, prefix+"_new@example.com", "self-password-hash")
		if err == nil {
			err = tx.Commit()
		}
		selfResult <- err
	}()
	select {
	case err := <-selfResult:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		cancel()
		close(release)
		<-adminResult
		t.Fatal("admin pre-locked user row before email mutex; self-service email update blocked")
	}
	close(release)
	require.NoError(t, <-adminResult)
}

type rbacDeleteKeys struct{ service.APIKeyRepository }

func (rbacDeleteKeys) ListByUserID(context.Context, int64, pagination.PaginationParams, service.APIKeyListFilters) ([]service.APIKey, *pagination.PaginationResult, error) {
	return []service.APIKey{{ID: 99, Key: "test-old-key"}}, nil, nil
}
func (rbacDeleteKeys) DeleteWithAudit(context.Context, int64) error { return nil }

type rbacDeleteCache struct {
	targetID  int64
	keys      []string
	committed []bool
}

func (c *rbacDeleteCache) check(ctx context.Context) {
	var deleted bool
	err := integrationDB.QueryRowContext(ctx, "SELECT deleted_at IS NOT NULL FROM users WHERE id=$1", c.targetID).Scan(&deleted)
	c.committed = append(c.committed, err == nil && deleted && dbent.TxFromContext(ctx) == nil)
}
func (c *rbacDeleteCache) InvalidateAuthCacheByKey(ctx context.Context, key string) {
	c.keys = append(c.keys, key)
	c.check(ctx)
}
func (c *rbacDeleteCache) InvalidateAuthCacheByUserID(ctx context.Context, _ int64) { c.check(ctx) }
func (c *rbacDeleteCache) InvalidateAuthCacheByGroupID(context.Context, int64)      {}

func TestRBACUserDeleteInvalidatesOnlyAfterCommitIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := testEntClient(t)
	require.NoError(t, SyncRBAC(ctx, integrationDB))
	prefix := fmt.Sprintf("rbac_delete_cache_%d", time.Now().UnixNano())
	var ids []int64
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM users WHERE id=ANY($1)", pq.Array(ids))
	})
	for i := 0; i < 2; i++ {
		u, err := client.User.Create().SetEmail(fmt.Sprintf("%s_%d@example.com", prefix, i)).SetPasswordHash("original").Save(ctx)
		require.NoError(t, err)
		ids = append(ids, u.ID)
	}
	cache := &rbacDeleteCache{targetID: ids[1]}
	repo := NewUserRepository(client, integrationDB)
	svc := service.NewAdminService(nil, repo, nil, nil, nil, rbacDeleteKeys{}, nil, nil, nil, nil, nil, nil, cache, client, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	require.NoError(t, svc.DeleteUser(service.WithRBACActor(ctx, service.RBACActor{UserID: ids[0], AdminAPIKey: true}), ids[1]))
	require.Equal(t, []string{"test-old-key"}, cache.keys, "preserve raw keys collected before deletion")
	require.Equal(t, []bool{true, true}, cache.committed, "both cache invalidations must observe committed deletion outside transaction")
}
