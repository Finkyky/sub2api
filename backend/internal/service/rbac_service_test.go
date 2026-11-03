package service

import (
	"context"
	"testing"
)

type permissionSnapshotRepoStub struct {
	roles []RolePermissions
}

func (s *permissionSnapshotRepoStub) GetRolePermissions(context.Context, int64) ([]RolePermissions, error) {
	return s.roles, nil
}

func TestRBACServiceLoadsEffectivePermissions(t *testing.T) {
	svc := NewRBACService(&permissionSnapshotRepoStub{roles: []RolePermissions{{Permissions: []string{"users.read"}}, {Permissions: []string{"roles.read"}}}})
	got, err := svc.EffectivePermissions(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if !HasPermission(RolePermissions{Permissions: got}, "roles.read") || !HasPermission(RolePermissions{Permissions: got}, "users.read") {
		t.Fatalf("unexpected permissions: %v", got)
	}
}
