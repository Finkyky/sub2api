//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	dbrole "github.com/Wei-Shaw/sub2api/ent/role"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestRBACManagementIntegrationLifecycleAndRevocation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := testEntClient(t)
	require.NoError(t, SyncRBAC(ctx, integrationDB))
	rootRoleID, err := client.Role.Query().Where(dbrole.CodeEQ("super_admin")).OnlyID(ctx)
	require.NoError(t, err)
	prefix := fmt.Sprintf("rbac_mgmt_%d", time.Now().UnixNano())
	var userIDs, roleIDs []int64
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if len(userIDs) > 0 {
			_, err := integrationDB.ExecContext(cleanupCtx, "DELETE FROM users WHERE id = ANY($1)", pq.Array(userIDs))
			require.NoError(t, err)
		}
		if len(roleIDs) > 0 {
			_, err := integrationDB.ExecContext(cleanupCtx, "DELETE FROM roles WHERE id = ANY($1)", pq.Array(roleIDs))
			require.NoError(t, err)
		}
	})
	for i := 0; i < 3; i++ {
		user, err := client.User.Create().SetEmail(fmt.Sprintf("%s_%d@example.com", prefix, i)).SetPasswordHash("test-hash").Save(ctx)
		require.NoError(t, err)
		userIDs = append(userIDs, user.ID)
	}
	_, err = client.UserRole.Create().SetUserID(userIDs[0]).SetRoleID(rootRoleID).Save(ctx)
	require.NoError(t, err)
	repo := NewRBACManagementRepository(client)
	svc := service.NewRBACManagementService(repo)
	root := service.RBACActor{UserID: userIDs[0]}
	actor := service.RBACActor{UserID: userIDs[1]}
	target := userIDs[2]
	createRole := func(suffix string) *service.RBACRole {
		role, err := svc.CreateRole(ctx, root, service.CreateRBACRoleInput{Code: prefix + suffix, Name: suffix})
		require.NoError(t, err)
		roleIDs = append(roleIDs, role.ID)
		return role
	}
	manager := createRole("_manager")
	reader := createRole("_reader")
	observer := createRole("_observer")
	require.NoError(t, svc.SetRolePermissions(ctx, root, manager.ID, []string{"roles.read", "roles.create", "roles.update", "roles.delete", "roles.permissions.assign", "users.roles.assign", "users.read"}))
	require.NoError(t, svc.SetUserRoles(ctx, root, actor.UserID, []int64{manager.ID}))
	require.NoError(t, svc.SetRolePermissions(ctx, actor, reader.ID, []string{"users.read"}))
	require.NoError(t, svc.SetRolePermissions(ctx, actor, observer.ID, []string{"roles.read"}))
	require.NoError(t, svc.SetUserRoles(ctx, actor, target, []int64{reader.ID, observer.ID, reader.ID}))
	snapshotService := service.NewRBACService(NewRBACRepository(client))
	snapshot, err := snapshotService.Snapshot(ctx, target)
	require.NoError(t, err)
	require.Equal(t, []string{"roles.read", "users.read"}, snapshot.Permissions)
	require.Len(t, snapshot.Roles, 2)
	require.ErrorIs(t, svc.SetRolePermissions(ctx, actor, reader.ID, []string{"payments.refund"}), service.ErrRBACPermissionCeiling)
	require.ErrorIs(t, svc.SetUserRoles(ctx, actor, target, []int64{rootRoleID}), service.ErrRBACPermissionCeiling)
	require.ErrorIs(t, svc.SetUserRoles(ctx, root, root.UserID, nil), service.ErrRBACProtectedUser)
	require.ErrorIs(t, svc.DeleteRole(ctx, root, rootRoleID), service.ErrRBACProtectedRole)
	updated, err := svc.UpdateRole(ctx, actor, reader.ID, service.UpdateRBACRoleInput{Name: "Updated reader", Description: "Metadata"})
	require.NoError(t, err)
	require.Equal(t, reader.Code, updated.Code)
	readBack, err := svc.GetRole(ctx, reader.ID)
	require.NoError(t, err)
	require.Equal(t, "Updated reader", readBack.Name)
	require.Equal(t, []string{"users.read"}, readBack.Permissions)

	cause := errors.New("force rollback after assignment delete")
	require.ErrorIs(t, repo.WithTransaction(ctx, func(tx service.RBACManagementTx) error {
		if err := tx.SetUserRoles(ctx, target, nil); err != nil {
			return err
		}
		return cause
	}), cause)
	snapshot, err = snapshotService.Snapshot(ctx, target)
	require.NoError(t, err)
	require.Len(t, snapshot.Roles, 2)
	require.NoError(t, svc.SetRolePermissions(ctx, actor, reader.ID, nil))
	snapshot, err = snapshotService.Snapshot(ctx, target)
	require.NoError(t, err)
	require.Equal(t, []string{"roles.read"}, snapshot.Permissions)
	// A committed revocation must affect the very next management transaction.
	require.NoError(t, svc.SetRolePermissions(ctx, root, manager.ID, []string{"users.read"}))
	_, err = svc.UpdateRole(ctx, actor, reader.ID, service.UpdateRBACRoleInput{Name: "Denied"})
	require.ErrorIs(t, err, service.ErrRBACForbidden)
	require.NoError(t, svc.DeleteRole(ctx, root, reader.ID))
	snapshot, err = snapshotService.Snapshot(ctx, target)
	require.NoError(t, err)
	require.Len(t, snapshot.Roles, 1)
	require.Equal(t, observer.ID, snapshot.Roles[0].ID)
	// Exercise cascading deletion with both permission and user edges present.
	require.NoError(t, svc.DeleteRole(ctx, root, observer.ID))
	snapshot, err = snapshotService.Snapshot(ctx, target)
	require.NoError(t, err)
	require.Empty(t, snapshot.Roles)
	require.Empty(t, snapshot.Permissions)
	var remaining int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM role_permissions WHERE role_id = ANY($1)", pq.Array([]int64{reader.ID, observer.ID})).Scan(&remaining))
	require.Zero(t, remaining)
	user, err := client.User.Get(ctx, target)
	require.NoError(t, err)
	require.Equal(t, service.RoleUser, user.Role)
}

func TestRBACManagementIntegrationSerializesWithStartup(t *testing.T) {
	require.NoError(t, SyncRBAC(context.Background(), integrationDB))
	repo := NewRBACManagementRepository(testEntClient(t))
	held, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	outerCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		done <- repo.WithTransaction(outerCtx, func(service.RBACManagementTx) error {
			close(held)
			select {
			case <-release:
				return nil
			case <-outerCtx.Done():
				return outerCtx.Err()
			}
		})
	}()
	select {
	case <-held:
	case err := <-done:
		t.Fatalf("lock holder stopped: %v", err)
	case <-outerCtx.Done():
		t.Fatal(outerCtx.Err())
	}
	for _, kind := range []string{"management", "startup"} {
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		called := false
		var err error
		if kind == "management" {
			err = repo.WithTransaction(ctx, func(service.RBACManagementTx) error { called = true; return nil })
		} else {
			err = SyncRBAC(ctx, integrationDB)
		}
		cancel()
		require.Error(t, err, "second transaction must wait for the shared lock")
		require.False(t, called)
	}
	unblock()
	require.NoError(t, <-done)
	require.NoError(t, repo.WithTransaction(context.Background(), func(service.RBACManagementTx) error { return nil }))
}
