package service

import (
	"context"
	"sort"
)

type RBACRoleSummary struct {
	ID           int64  `json:"id"`
	Code         string `json:"code"`
	Name         string `json:"name"`
	IsSuperAdmin bool   `json:"is_super_admin"`
}

type RBACSnapshot struct {
	Roles        []RBACRoleSummary `json:"roles"`
	Permissions  []string          `json:"permissions"`
	IsSuperAdmin bool              `json:"is_super_admin"`
}

// Snapshot loads authorization from the database on every request, so role and
// permission changes take effect on the next request without stale grants.
func (s *RBACService) Snapshot(ctx context.Context, userID int64) (RBACSnapshot, error) {
	roles, err := s.repo.GetRolePermissions(ctx, userID)
	if err != nil {
		return RBACSnapshot{}, err
	}
	out := RBACSnapshot{
		Roles:       make([]RBACRoleSummary, 0, len(roles)),
		Permissions: EffectivePermissions(roles),
	}
	for _, role := range roles {
		out.Roles = append(out.Roles, RBACRoleSummary{ID: role.ID, Code: role.Code, Name: role.Name, IsSuperAdmin: role.SuperAdmin})
		out.IsSuperAdmin = out.IsSuperAdmin || role.SuperAdmin
	}
	sort.Slice(out.Roles, func(i, j int) bool { return out.Roles[i].Code < out.Roles[j].Code })
	return out, nil
}

// HasAdminAccess admits only current, code-owned administrative grants. An empty
// or retired-only role does not itself grant access to the administration surface.
func (s RBACSnapshot) HasAdminAccess() bool {
	if s.IsSuperAdmin {
		return true
	}
	catalog := PermissionCatalog()
	for _, code := range s.Permissions {
		if _, ok := catalog[code]; ok {
			return true
		}
	}
	return false
}
