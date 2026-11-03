package service

import "testing"

func TestPermissionCatalogContainsCoreAdminPermissions(t *testing.T) {
	catalog := PermissionCatalog()
	for _, code := range []string{"users.read", "users.roles.assign", "roles.permissions.assign", "permissions.read"} {
		if _, ok := catalog[code]; !ok {
			t.Fatalf("catalog missing %q", code)
		}
	}
}

func TestPermissionCatalogEntriesMatchCodes(t *testing.T) {
	for code, entry := range PermissionCatalog() {
		if entry.Code != code || entry.Resource == "" || entry.Action == "" {
			t.Fatalf("invalid catalog entry for %q: %#v", code, entry)
		}
	}
}
