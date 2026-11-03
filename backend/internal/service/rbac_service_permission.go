package service

import "context"

func (s *RBACService) HasPermission(ctx context.Context, userID int64, permission string) (bool, error) {
	roles, err := s.repo.GetRolePermissions(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, role := range roles {
		if role.SuperAdmin || HasPermission(role, permission) {
			return true, nil
		}
	}
	return false, nil
}
