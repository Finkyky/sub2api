package service

import (
	"fmt"
	"sort"
)

// RolePermissions is the authorization snapshot for one role.
type RolePermissions struct {
	ID          int64
	Code        string
	Name        string
	SuperAdmin  bool
	Permissions []string
}

// EffectivePermissions returns the sorted union of permissions granted by roles.
// Super-admin status is kept on RolePermissions and evaluated by HasPermission.
func EffectivePermissions(roles []RolePermissions) []string {
	set := make(map[string]struct{})
	for _, role := range roles {
		for _, permission := range role.Permissions {
			if permission != "" {
				set[permission] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(set))
	for permission := range set {
		result = append(result, permission)
	}
	sort.Strings(result)
	return result
}

// HasPermission applies the RBAC union rule and the super-admin bypass.
func HasPermission(role RolePermissions, permission string) bool {
	if role.SuperAdmin {
		return true
	}
	for _, candidate := range role.Permissions {
		if candidate == permission {
			return true
		}
	}
	return false
}

// ValidatePermissionCeiling ensures an actor only grants permissions already in
// their effective set. Super-admin actors have no ceiling.
func ValidatePermissionCeiling(actor RolePermissions, permissions []string) error {
	if actor.SuperAdmin {
		return nil
	}
	allowed := make(map[string]struct{}, len(actor.Permissions))
	for _, permission := range actor.Permissions {
		allowed[permission] = struct{}{}
	}
	for _, permission := range permissions {
		if _, ok := allowed[permission]; !ok {
			return fmt.Errorf("permission %q exceeds actor permission ceiling", permission)
		}
	}
	return nil
}
