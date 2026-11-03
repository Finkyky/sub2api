package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRBACAuditCapturesTransactionSnapshotsAfterCommit(t *testing.T) {
	for _, operation := range []string{"create", "update", "delete", "permissions", "user_roles"} {
		t.Run(operation, func(t *testing.T) {
			repo := newRBACManagementMemory()
			svc := NewRBACManagementService(repo)
			var changes []RBACMutationAudit
			ctx := WithRBACMutationAudit(context.Background(), func(change RBACMutationAudit) {
				if operation == "update" {
					require.Equal(t, "Changed", repo.roles[20].Name, "emit only after commit")
				}
				changes = append(changes, change)
			})
			actor := RBACActor{UserID: 1}
			var err error
			switch operation {
			case "create":
				_, err = svc.CreateRole(ctx, actor, CreateRBACRoleInput{Code: "new", Name: "New"})
			case "update":
				_, err = svc.UpdateRole(ctx, actor, 20, UpdateRBACRoleInput{Name: "Changed"})
			case "delete":
				err = svc.DeleteRole(ctx, actor, 20)
			case "permissions":
				err = svc.SetRolePermissions(ctx, actor, 20, []string{"roles.read"})
			case "user_roles":
				err = svc.SetUserRoles(ctx, actor, 2, []int64{10, 20})
			}
			require.NoError(t, err)
			require.Len(t, changes, 1)
			change := changes[0]
			require.NotEmpty(t, change.Action)
			require.Positive(t, change.TargetID)
			switch operation {
			case "create":
				require.Equal(t, "role.created", change.Action)
				require.Nil(t, change.Before)
				require.Equal(t, "new", change.After.Role.Code)
			case "update":
				require.Equal(t, "Reader", change.Before.Role.Name)
				require.Equal(t, "Changed", change.After.Role.Name)
			case "delete":
				require.Equal(t, "reader", change.Before.Role.Code)
				require.Nil(t, change.After)
			case "permissions":
				require.Equal(t, []string{"users.read"}, change.Before.Role.Permissions)
				require.Equal(t, []string{"roles.read"}, change.After.Role.Permissions)
			case "user_roles":
				require.Len(t, change.Before.Roles, 1)
				require.Len(t, change.After.Roles, 2)
			}
			// Captured snapshots cannot alias subsequent repository changes.
			repo.roles[20] = RBACRole{ID: 20, Name: "Later", Permissions: []string{"other"}}
			if change.Before != nil && change.Before.Role != nil {
				require.Equal(t, "Reader", change.Before.Role.Name)
			}
		})
	}
}
func TestRBACAuditDoesNotEmitOnFailure(t *testing.T) {
	for _, stage := range []string{"write", "commit", "denied"} {
		t.Run(stage, func(t *testing.T) {
			repo := newRBACManagementMemory()
			svc := NewRBACManagementService(repo)
			count := 0
			ctx := WithRBACMutationAudit(context.Background(), func(RBACMutationAudit) { count++ })
			actor := RBACActor{UserID: 1}
			cause := errors.New("injected")
			if stage == "write" {
				repo.writeErr = cause
			}
			if stage == "commit" {
				repo.commitErr = cause
			}
			if stage == "denied" {
				actor.UserID = 2
			}
			err := svc.SetRolePermissions(ctx, actor, 20, nil)
			require.Error(t, err)
			require.Zero(t, count)
		})
	}
}

// Inject a snapshot read failure after the actual role write. The transaction
// must roll back the write and suppress the audit event, not report success.
type rbacAuditReadFailureRepo struct {
	*rbacManagementMemory
	cause error
}
type rbacAuditReadFailureTx struct {
	RBACManagementTx
	cause error
	reads int
}

func (tx *rbacAuditReadFailureTx) GetRole(ctx context.Context, id int64) (*RBACRole, error) {
	tx.reads++
	if tx.reads == 3 {
		return nil, tx.cause
	}
	return tx.RBACManagementTx.GetRole(ctx, id)
}
func (repo *rbacAuditReadFailureRepo) WithTransaction(ctx context.Context, fn func(RBACManagementTx) error) error {
	return repo.rbacManagementMemory.WithTransaction(ctx, func(tx RBACManagementTx) error {
		return fn(&rbacAuditReadFailureTx{RBACManagementTx: tx, cause: repo.cause})
	})
}
func TestRBACAuditSnapshotFailureRollsBackMutation(t *testing.T) {
	cause := errors.New("after snapshot failed")
	repo := &rbacAuditReadFailureRepo{rbacManagementMemory: newRBACManagementMemory(), cause: cause}
	svc := NewRBACManagementService(repo)
	count := 0
	ctx := WithRBACMutationAudit(context.Background(), func(RBACMutationAudit) { count++ })
	err := svc.SetRolePermissions(ctx, RBACActor{UserID: 1}, 20, nil)
	require.ErrorIs(t, err, cause)
	require.Zero(t, count)
	require.Equal(t, []string{"users.read"}, repo.roles[20].Permissions)
}
