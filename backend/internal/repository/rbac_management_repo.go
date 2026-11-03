package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbpermission "github.com/Wei-Shaw/sub2api/ent/permission"
	dbrole "github.com/Wei-Shaw/sub2api/ent/role"
	dbrolepermission "github.com/Wei-Shaw/sub2api/ent/rolepermission"
	dbuser "github.com/Wei-Shaw/sub2api/ent/user"
	dbuserrole "github.com/Wei-Shaw/sub2api/ent/userrole"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type rbacManagementRepository struct{ client *dbent.Client }
type rbacManagementTx struct{ *rbacManagementRepository }

var _ service.RBACManagementRepository = (*rbacManagementRepository)(nil)
var _ service.RBACManagementTx = (*rbacManagementTx)(nil)

func NewRBACManagementRepository(client *dbent.Client) service.RBACManagementRepository {
	return &rbacManagementRepository{client: client}
}

func (r *rbacManagementRepository) WithTransaction(ctx context.Context, fn func(service.RBACManagementTx) error) error {
	tx, err := r.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin RBAC management: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	// Acquire the same first row lock as SyncRBAC before loading actor authority.
	// All RBAC mutations serialize here: checks and writes observe one consistent
	// permission ceiling, including when another request revokes actor privileges.
	if _, err := tx.Role.Query().Where(dbrole.CodeEQ("super_admin")).ForUpdate().Only(ctx); err != nil {
		return fmt.Errorf("lock RBAC system role: %w", err)
	}
	if err := fn(&rbacManagementTx{&rbacManagementRepository{client: tx.Client()}}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit RBAC management: %w", err)
	}
	return nil
}

func mapRBACRole(row *dbent.Role) service.RBACRole {
	out := service.RBACRole{ID: row.ID, Code: row.Code, Name: row.Name, IsSystem: row.IsSystem, IsSuperAdmin: row.IsSuperAdmin, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Permissions: make([]string, 0, len(row.Edges.Permissions))}
	if row.Description != nil {
		out.Description = *row.Description
	}
	for _, p := range row.Edges.Permissions {
		out.Permissions = append(out.Permissions, p.Code)
	}
	sort.Strings(out.Permissions)
	return out
}
func (r *rbacManagementRepository) ListRoles(ctx context.Context) ([]service.RBACRole, error) {
	rows, err := r.client.Role.Query().WithPermissions().Order(dbrole.ByCode()).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]service.RBACRole, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapRBACRole(row))
	}
	return out, nil
}
func (r *rbacManagementRepository) GetRole(ctx context.Context, id int64) (*service.RBACRole, error) {
	row, err := r.client.Role.Query().Where(dbrole.IDEQ(id)).WithPermissions().Only(ctx)
	if dbent.IsNotFound(err) {
		return nil, service.ErrRBACRoleNotFound
	}
	if err != nil {
		return nil, err
	}
	out := mapRBACRole(row)
	return &out, nil
}
func (r *rbacManagementRepository) GetUserRoles(ctx context.Context, id int64) ([]service.RBACRole, error) {
	query := r.client.User.Query().Where(dbuser.IDEQ(id), dbuser.DeletedAtIsNil())
	exists, err := query.Exist(ctx)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, service.ErrUserNotFound
	}
	rows, err := query.QueryRoles().WithPermissions().Order(dbrole.ByCode()).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]service.RBACRole, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapRBACRole(row))
	}
	return out, nil
}
func (t *rbacManagementTx) GetUser(ctx context.Context, id int64) (*service.RBACManagedUser, error) {
	// User status/legacy role may also be changed by existing non-RBAC services.
	// Lock the row so those updates cannot race a management authorization check.
	row, err := t.client.User.Query().Where(dbuser.IDEQ(id), dbuser.DeletedAtIsNil()).
		Select(dbuser.FieldID, dbuser.FieldStatus, dbuser.FieldRole).ForUpdate().Only(ctx)
	if dbent.IsNotFound(err) {
		return nil, service.ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return &service.RBACManagedUser{ID: row.ID, Status: row.Status, LegacyAdmin: row.Role == service.RoleAdmin}, nil
}
func (t *rbacManagementTx) CreateRole(ctx context.Context, role *service.RBACRole) error {
	row, err := t.client.Role.Create().SetCode(role.Code).SetName(role.Name).SetDescription(role.Description).Save(ctx)
	if isUniqueViolation(err) {
		return service.ErrRBACRoleExists
	}
	if err != nil {
		return err
	}
	*role = mapRBACRole(row)
	return nil
}
func (t *rbacManagementTx) UpdateRole(ctx context.Context, role *service.RBACRole) error {
	row, err := t.client.Role.UpdateOneID(role.ID).SetName(role.Name).SetDescription(role.Description).Save(ctx)
	if dbent.IsNotFound(err) {
		return service.ErrRBACRoleNotFound
	}
	if err != nil {
		return err
	}
	role.Name = row.Name
	role.UpdatedAt = row.UpdatedAt
	return nil
}
func (t *rbacManagementTx) DeleteRole(ctx context.Context, id int64) error {
	err := t.client.Role.DeleteOneID(id).Exec(ctx)
	if dbent.IsNotFound(err) {
		return service.ErrRBACRoleNotFound
	}
	return err
}
func (t *rbacManagementTx) SetRolePermissions(ctx context.Context, id int64, codes []string) error {
	var ids []int64
	if len(codes) > 0 {
		var err error
		ids, err = t.client.Permission.Query().Where(dbpermission.CodeIn(codes...)).Order(dbpermission.ByCode()).IDs(ctx)
		if err != nil {
			return err
		}
		if len(ids) != len(codes) {
			return service.ErrRBACUnknownPermission
		}
	}
	if _, err := t.client.RolePermission.Delete().Where(dbrolepermission.RoleIDEQ(id)).Exec(ctx); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	creates := make([]*dbent.RolePermissionCreate, 0, len(ids))
	for _, permissionID := range ids {
		creates = append(creates, t.client.RolePermission.Create().SetRoleID(id).SetPermissionID(permissionID))
	}
	return t.client.RolePermission.CreateBulk(creates...).Exec(ctx)
}
func (t *rbacManagementTx) SetUserRoles(ctx context.Context, id int64, roleIDs []int64) error {
	if len(roleIDs) > 0 {
		ids, err := t.client.Role.Query().Where(dbrole.IDIn(roleIDs...)).IDs(ctx)
		if err != nil {
			return err
		}
		if len(ids) != len(roleIDs) {
			return service.ErrRBACRoleNotFound
		}
	}
	if _, err := t.client.UserRole.Delete().Where(dbuserrole.UserIDEQ(id)).Exec(ctx); err != nil {
		return err
	}
	if len(roleIDs) == 0 {
		return nil
	}
	creates := make([]*dbent.UserRoleCreate, 0, len(roleIDs))
	for _, roleID := range roleIDs {
		creates = append(creates, t.client.UserRole.Create().SetUserID(id).SetRoleID(roleID))
	}
	return t.client.UserRole.CreateBulk(creates...).Exec(ctx)
}
