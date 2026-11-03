package service

import (
	"context"
	"database/sql"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbrole "github.com/Wei-Shaw/sub2api/ent/role"
	dbuser "github.com/Wei-Shaw/sub2api/ent/user"
	dbuserrole "github.com/Wei-Shaw/sub2api/ent/userrole"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type rbacActorContextKey struct{}
type adminUserEffectsKey struct{}
type adminUserEffects struct{ afterCommit []func(context.Context) }

func afterAdminUserCommit(ctx context.Context, effect func(context.Context)) {
	if effects, ok := ctx.Value(adminUserEffectsKey{}).(*adminUserEffects); ok {
		effects.afterCommit = append(effects.afterCommit, effect)
		return
	}
	effect(ctx)
}

// WithRBACActor is set by successful server-side admin authentication, not by
// request data. Internal non-HTTP maintenance callers may omit an actor.
func WithRBACActor(ctx context.Context, actor RBACActor) context.Context {
	return context.WithValue(ctx, rbacActorContextKey{}, actor)
}
func RBACActorFromContext(ctx context.Context) (RBACActor, bool) {
	actor, ok := ctx.Value(rbacActorContextKey{}).(RBACActor)
	return actor, ok
}

type adminUserMutation struct {
	permission   string
	targetID     int64
	role, status string
	remove       bool
}

func validateAdminUserMutation(actor RolePermissions, target *RBACManagedUser, targetAuthority RolePermissions, change adminUserMutation) error {
	if !HasPermission(actor, change.permission) {
		return ErrRBACForbidden
	}
	role := strings.ToLower(strings.TrimSpace(change.role))
	if role == RoleAdmin && !actor.SuperAdmin {
		return ErrRBACForbidden
	}
	if target == nil {
		return nil
	}
	protected := target.LegacyAdmin || targetAuthority.SuperAdmin
	if protected {
		// A super administrator may rotate another super administrator's credentials,
		// but no actor may remove, disable or demote the protected administrator.
		if change.remove || change.status == StatusDisabled || (target.LegacyAdmin && role == RoleUser) {
			return ErrRBACProtectedUser
		}
		if !actor.SuperAdmin {
			return ErrRBACProtectedUser
		}
	}
	if !actor.SuperAdmin {
		if err := ValidatePermissionCeiling(actor, targetAuthority.Permissions); err != nil {
			return ErrRBACPermissionCeiling
		}
	}
	return nil
}

func userMutationAuthority(row *dbent.User) RolePermissions {
	var roles []RolePermissions
	out := RolePermissions{}
	for _, role := range row.Edges.Roles {
		p := RolePermissions{SuperAdmin: role.IsSuperAdmin}
		for _, permission := range role.Edges.Permissions {
			p.Permissions = append(p.Permissions, permission.Code)
		}
		roles = append(roles, p)
		out.SuperAdmin = out.SuperAdmin || p.SuperAdmin
	}
	out.Permissions = EffectivePermissions(roles)
	return out
}

// withAdminUserAuthorization serializes sensitive legacy user writes with RBAC
// assignment and startup sync. Authority and target protection are checked in
// the same transaction as the write, so role changes cannot race a password
// reset, identity binding, deletion or legacy-admin promotion.
func (s *adminServiceImpl) withAdminUserAuthorization(ctx context.Context, change adminUserMutation, fn func(context.Context) error) error {
	actor, present := RBACActorFromContext(ctx)
	if !present {
		return fn(ctx)
	} // Existing trusted internal service callers.
	if actor.UserID <= 0 {
		return ErrRBACUnauthenticated
	}
	if s.entClient == nil {
		return infraerrors.New(503, "RBAC_UNAVAILABLE", "User mutation authorization unavailable")
	}
	tx, err := s.entClient.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Role.Query().Where(dbrole.CodeEQ("super_admin")).ForUpdate().Only(ctx); err != nil {
		return err
	}
	// The system-role lock serializes all role/legacy-role authorization writes.
	// Do NOT pre-lock user rows here: repository email updates acquire the email
	// mutex/advisory lock before their row write, as does self-service email binding.
	// Reversing that order creates an application/SQL deadlock. User writes obtain
	// row locks inside the repository, after any email uniqueness locks.
	load := func(id int64) (*dbent.User, error) {
		row, err := tx.User.Query().Where(dbuser.IDEQ(id), dbuser.DeletedAtIsNil()).WithRoles(func(q *dbent.RoleQuery) { q.WithPermissions() }).Only(ctx)
		if dbent.IsNotFound(err) {
			return nil, ErrUserNotFound
		}
		return row, err
	}
	actorRow, err := load(actor.UserID)
	if err != nil {
		return err
	}
	if !actor.AdminAPIKey && actorRow.Status != StatusActive {
		return ErrRBACForbidden
	}
	authority := userMutationAuthority(actorRow)
	authority.SuperAdmin = authority.SuperAdmin || actor.AdminAPIKey
	var target *RBACManagedUser
	var targetAuthority RolePermissions
	if change.targetID > 0 {
		row, err := load(change.targetID)
		if err != nil {
			return err
		}
		target = &RBACManagedUser{ID: row.ID, Status: row.Status, LegacyAdmin: row.Role == RoleAdmin}
		targetAuthority = userMutationAuthority(row)
	}
	if err = validateAdminUserMutation(authority, target, targetAuthority, change); err != nil {
		return err
	}
	effects := &adminUserEffects{}
	writeCtx := context.WithValue(dbent.NewTxContext(ctx, tx), adminUserEffectsKey{}, effects)
	if err = fn(writeCtx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	for _, effect := range effects.afterCommit {
		effect(ctx)
	}
	return nil
}

func bindLegacySuperAdmin(ctx context.Context, user *User) error {
	if user == nil || user.Role != RoleAdmin {
		return nil
	}
	tx := dbent.TxFromContext(ctx)
	if tx == nil {
		return nil
	}
	id, err := tx.Role.Query().Where(dbrole.CodeEQ("super_admin")).OnlyID(ctx)
	if err != nil {
		return err
	}
	exists, err := tx.UserRole.Query().Where(dbuserrole.UserIDEQ(user.ID), dbuserrole.RoleIDEQ(id)).Exist(ctx)
	if err != nil || exists {
		return err
	}
	return tx.UserRole.Create().SetUserID(user.ID).SetRoleID(id).Exec(ctx)
}

func (s *adminServiceImpl) CreateUser(ctx context.Context, input *CreateUserInput) (out *User, err error) {
	err = s.withAdminUserAuthorization(ctx, adminUserMutation{permission: "users.create", role: input.Role}, func(ctx context.Context) error {
		var e error
		out, e = s.createUserAuthorized(ctx, input)
		if e != nil {
			return e
		}
		return bindLegacySuperAdmin(ctx, out)
	})
	if _, scoped := RBACActorFromContext(ctx); scoped && err == nil {
		s.assignDefaultSubscriptions(ctx, out.ID)
	}
	return
}
func (s *adminServiceImpl) UpdateUser(ctx context.Context, id int64, input *UpdateUserInput) (out *User, err error) {
	err = s.withAdminUserAuthorization(ctx, adminUserMutation{permission: "users.update", targetID: id, role: input.Role, status: input.Status}, func(ctx context.Context) error {
		var e error
		out, e = s.updateUserAuthorized(ctx, id, input)
		if e != nil {
			return e
		}
		return bindLegacySuperAdmin(ctx, out)
	})
	// Invalidate once more after commit; pre-commit invalidation must not leave a
	// concurrently reloaded cache holding the previous committed user settings.
	if _, scoped := RBACActorFromContext(ctx); scoped && err == nil && s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, id)
	}
	return
}
func (s *adminServiceImpl) DeleteUser(ctx context.Context, id int64) error {
	return s.withAdminUserAuthorization(ctx, adminUserMutation{permission: "users.delete", targetID: id, remove: true}, func(ctx context.Context) error { return s.deleteUserAuthorized(ctx, id) })
}
func (s *adminServiceImpl) BindUserAuthIdentity(ctx context.Context, id int64, input AdminBindAuthIdentityInput) (out *AdminBoundAuthIdentity, err error) {
	err = s.withAdminUserAuthorization(ctx, adminUserMutation{permission: "users.update", targetID: id}, func(ctx context.Context) error {
		var e error
		out, e = s.bindUserAuthIdentityAuthorized(ctx, id, input)
		return e
	})
	return
}
