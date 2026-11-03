package service

import "context"

type RBACRolePermissionRepository interface {
	GetRolePermissions(ctx context.Context, userID int64) ([]RolePermissions, error)
}

type RBACService struct {
	repo RBACRolePermissionRepository
}

func NewRBACService(repo RBACRolePermissionRepository) *RBACService {
	return &RBACService{repo: repo}
}

func (s *RBACService) EffectivePermissions(ctx context.Context, userID int64) ([]string, error) {
	roles, err := s.repo.GetRolePermissions(ctx, userID)
	if err != nil {
		return nil, err
	}
	return EffectivePermissions(roles), nil
}
