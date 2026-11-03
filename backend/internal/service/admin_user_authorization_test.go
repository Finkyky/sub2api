package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRBACLegacyUserMutationAuthority(t *testing.T) {
	manager := RolePermissions{Permissions: []string{"users.update", "users.create", "users.delete", "users.read"}}
	super := RolePermissions{SuperAdmin: true}
	ordinary := &RBACManagedUser{ID: 2, Status: StatusActive}
	for _, tc := range []struct {
		name            string
		actor           RolePermissions
		target          *RBACManagedUser
		targetAuthority RolePermissions
		change          adminUserMutation
		want            error
	}{
		{"ordinary create", manager, nil, RolePermissions{}, adminUserMutation{permission: "users.create", role: "user"}, nil},
		{"legacy promotion denied", manager, ordinary, RolePermissions{}, adminUserMutation{permission: "users.update", role: "admin"}, ErrRBACForbidden},
		{"legacy admin creation denied", manager, nil, RolePermissions{}, adminUserMutation{permission: "users.create", role: "admin"}, ErrRBACForbidden},
		{"super creation", super, nil, RolePermissions{}, adminUserMutation{permission: "users.create", role: "admin"}, nil},
		{"missing operation", RolePermissions{Permissions: []string{"users.read"}}, ordinary, RolePermissions{}, adminUserMutation{permission: "users.update"}, ErrRBACForbidden},
		{"super password protected", manager, ordinary, super, adminUserMutation{permission: "users.update"}, ErrRBACProtectedUser},
		{"legacy protected", manager, &RBACManagedUser{ID: 2, LegacyAdmin: true}, RolePermissions{}, adminUserMutation{permission: "users.update"}, ErrRBACProtectedUser},
		{"higher role protected", manager, ordinary, RolePermissions{Permissions: []string{"settings.update"}}, adminUserMutation{permission: "users.update"}, ErrRBACPermissionCeiling},
		{"lower role editable", manager, ordinary, RolePermissions{Permissions: []string{"users.read"}}, adminUserMutation{permission: "users.update"}, nil},
		{"super may update super", super, ordinary, super, adminUserMutation{permission: "users.update"}, nil},
		{"super cannot delete super", super, ordinary, super, adminUserMutation{permission: "users.delete", remove: true}, ErrRBACProtectedUser},
		{"super cannot disable super", super, ordinary, super, adminUserMutation{permission: "users.update", status: "disabled"}, ErrRBACProtectedUser},
		{"super cannot demote legacy", super, &RBACManagedUser{ID: 2, LegacyAdmin: true}, super, adminUserMutation{permission: "users.update", role: "user"}, ErrRBACProtectedUser},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAdminUserMutation(tc.actor, tc.target, tc.targetAuthority, tc.change)
			if tc.want == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.want)
			}
		})
	}
}

func TestRBACLegacyMutationFailsClosedWithoutDatabase(t *testing.T) {
	s := &adminServiceImpl{}
	ctx := WithRBACActor(context.Background(), RBACActor{UserID: 7})
	_, err := s.UpdateUser(ctx, 8, &UpdateUserInput{Password: "password"})
	require.Error(t, err)
	_, err = s.CreateUser(ctx, &CreateUserInput{Email: "x@example.com", Password: "password"})
	require.Error(t, err)
	require.Error(t, s.DeleteUser(ctx, 8))
	_, err = s.BindUserAuthIdentity(ctx, 8, AdminBindAuthIdentityInput{})
	require.Error(t, err)
}

func TestRBACSnapshotAdminEntry(t *testing.T) {
	require.True(t, (RBACSnapshot{IsSuperAdmin: true}).HasAdminAccess())
	require.True(t, (RBACSnapshot{Permissions: []string{"users.read"}}).HasAdminAccess())
	require.False(t, (RBACSnapshot{Roles: []RBACRoleSummary{{Code: "empty"}}}).HasAdminAccess())
	require.False(t, (RBACSnapshot{Permissions: []string{"retired.unknown"}}).HasAdminAccess())
}
