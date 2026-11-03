package service

import "testing"

func TestEffectivePermissionsUnionsRoles(t *testing.T) {
	roles := []RolePermissions{
		{SuperAdmin: false, Permissions: []string{"users.read", "users.update"}},
		{SuperAdmin: false, Permissions: []string{"users.read", "roles.read"}},
	}
	got := EffectivePermissions(roles)
	want := []string{"roles.read", "users.read", "users.update"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestHasPermissionSuperAdminBypassesCatalog(t *testing.T) {
	if !HasPermission(RolePermissions{SuperAdmin: true}, "future.permission") {
		t.Fatal("super admin should have every permission")
	}
}

func TestHasPermissionRejectsMissingPermission(t *testing.T) {
	if HasPermission(RolePermissions{Permissions: []string{"users.read"}}, "users.delete") {
		t.Fatal("missing permission should be rejected")
	}
}

func TestValidatePermissionCeilingRejectsNewPermission(t *testing.T) {
	actor := RolePermissions{Permissions: []string{"users.read"}}
	if err := ValidatePermissionCeiling(actor, []string{"users.read", "users.delete"}); err == nil {
		t.Fatal("permission outside actor ceiling should be rejected")
	}
}
