package service

import (
	"context"
	"reflect"
	"testing"
)

func TestRBACSnapshotReturnsSortedRolesPermissionUnionAndSuperAdmin(t *testing.T) {
	svc := NewRBACService(&permissionSnapshotRepoStub{roles: []RolePermissions{
		{Code: "analyst", Name: "Analyst", Permissions: []string{"users.read", "roles.read"}},
		{Code: "operator", Name: "Operator", Permissions: []string{"users.read", "groups.update"}},
	}})
	got, err := svc.Snapshot(context.Background(), 17)
	if err != nil {
		t.Fatal(err)
	}
	if got.IsSuperAdmin {
		t.Fatal("unexpected super admin")
	}
	if !reflect.DeepEqual(got.Permissions, []string{"groups.update", "roles.read", "users.read"}) {
		t.Fatalf("permissions=%v", got.Permissions)
	}
	if len(got.Roles) != 2 || got.Roles[0].Code != "analyst" || got.Roles[1].Code != "operator" {
		t.Fatalf("roles=%v", got.Roles)
	}
}

func TestRBACSnapshotPreservesSuperAdminStatus(t *testing.T) {
	svc := NewRBACService(&permissionSnapshotRepoStub{roles: []RolePermissions{{Code: "super_admin", SuperAdmin: true}}})
	got, err := svc.Snapshot(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsSuperAdmin {
		t.Fatal("super admin status lost")
	}
}
