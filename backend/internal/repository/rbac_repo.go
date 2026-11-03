package repository

import (
	"context"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbuser "github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type rbacRepository struct {
	client *dbent.Client
}

var _ service.RBACRolePermissionRepository = (*rbacRepository)(nil)

func NewRBACRepository(client *dbent.Client) service.RBACRolePermissionRepository {
	return &rbacRepository{client: client}
}

func (r *rbacRepository) GetRolePermissions(ctx context.Context, userID int64) ([]service.RolePermissions, error) {
	roles, err := r.client.User.Query().
		Where(dbuser.IDEQ(userID)).
		QueryRoles().
		WithPermissions().
		All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]service.RolePermissions, 0, len(roles))
	for _, role := range roles {
		permissions := make([]string, 0, len(role.Edges.Permissions))
		for _, permission := range role.Edges.Permissions {
			permissions = append(permissions, permission.Code)
		}
		result = append(result, service.RolePermissions{
			ID:          role.ID,
			Code:        role.Code,
			Name:        role.Name,
			SuperAdmin:  role.IsSuperAdmin,
			Permissions: permissions,
		})
	}
	return result, nil
}
