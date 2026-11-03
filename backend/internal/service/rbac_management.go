package service

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrRBACUnauthenticated   = infraerrors.Unauthorized("RBAC_UNAUTHENTICATED", "authenticated actor required")
	ErrRBACForbidden         = infraerrors.Forbidden("RBAC_FORBIDDEN", "RBAC operation not permitted")
	ErrRBACInvalidInput      = infraerrors.BadRequest("RBAC_INVALID_INPUT", "invalid role metadata or identifier")
	ErrRBACUnknownPermission = infraerrors.BadRequest("RBAC_UNKNOWN_PERMISSION", "unknown permission code")
	ErrRBACRoleNotFound      = infraerrors.NotFound("RBAC_ROLE_NOT_FOUND", "role not found")
	ErrRBACRoleExists        = infraerrors.Conflict("RBAC_ROLE_EXISTS", "role code already exists")
	ErrRBACProtectedRole     = infraerrors.Forbidden("RBAC_PROTECTED_ROLE", "system and super-admin roles are immutable")
	ErrRBACProtectedUser     = infraerrors.Forbidden("RBAC_PROTECTED_USER", "super-admin user assignments are protected")
	ErrRBACPermissionCeiling = infraerrors.Forbidden("RBAC_PERMISSION_CEILING", "role permissions exceed actor authority")
)

// RBACActor is populated only from trusted server authentication context.
// AdminAPIKey must never be decoded from a request body or arbitrary JWT claim.
type RBACActor struct {
	UserID      int64
	AdminAPIKey bool
}

type RBACRole struct {
	ID           int64     `json:"id"`
	Code         string    `json:"code"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	IsSystem     bool      `json:"is_system"`
	IsSuperAdmin bool      `json:"is_super_admin"`
	Permissions  []string  `json:"permissions"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type RBACManagedUser struct {
	ID          int64
	Status      string
	LegacyAdmin bool
}

type CreateRBACRoleInput struct{ Code, Name, Description string }
type UpdateRBACRoleInput struct{ Name, Description string }

// RBACManagementTx exposes only operations used after authorization is loaded
// inside the same transaction. Implementations serialize all RBAC writes with
// startup synchronization before invoking the callback.
type RBACManagementTx interface {
	GetUser(context.Context, int64) (*RBACManagedUser, error)
	GetRole(context.Context, int64) (*RBACRole, error)
	GetUserRoles(context.Context, int64) ([]RBACRole, error)
	CreateRole(context.Context, *RBACRole) error
	UpdateRole(context.Context, *RBACRole) error
	DeleteRole(context.Context, int64) error
	SetRolePermissions(context.Context, int64, []string) error
	SetUserRoles(context.Context, int64, []int64) error
}

type RBACManagementRepository interface {
	ListRoles(context.Context) ([]RBACRole, error)
	GetRole(context.Context, int64) (*RBACRole, error)
	GetUserRoles(context.Context, int64) ([]RBACRole, error)
	WithTransaction(context.Context, func(RBACManagementTx) error) error
}

type RBACManagementService struct{ repo RBACManagementRepository }

func NewRBACManagementService(repo RBACManagementRepository) *RBACManagementService {
	return &RBACManagementService{repo: repo}
}

// Read authorization is enforced at the HTTP route boundary. Mutations also
// re-check operation permissions below, so revocation between requests is seen.
func (s *RBACManagementService) ListRoles(ctx context.Context) ([]RBACRole, error) {
	return s.repo.ListRoles(ctx)
}
func (s *RBACManagementService) GetRole(ctx context.Context, id int64) (*RBACRole, error) {
	if id <= 0 {
		return nil, ErrRBACInvalidInput
	}
	return s.repo.GetRole(ctx, id)
}
func (s *RBACManagementService) GetUserRoles(ctx context.Context, id int64) ([]RBACRole, error) {
	if id <= 0 {
		return nil, ErrRBACInvalidInput
	}
	return s.repo.GetUserRoles(ctx, id)
}
func (s *RBACManagementService) ListPermissions() []PermissionDefinition {
	catalog := PermissionCatalog()
	out := make([]PermissionDefinition, 0, len(catalog))
	for _, p := range catalog {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

func (s *RBACManagementService) authorized(ctx context.Context, actor RBACActor, permission string, fn func(RBACManagementTx, RolePermissions) error) error {
	if actor.UserID <= 0 {
		return ErrRBACUnauthenticated
	}
	sink, _ := ctx.Value(rbacMutationAuditKey{}).(rbacMutationAuditSink)
	var changes []RBACMutationAudit
	err := s.repo.WithTransaction(ctx, func(tx RBACManagementTx) error {
		if sink != nil {
			tx = &rbacAuditedTx{RBACManagementTx: tx, changes: &changes}
		}
		user, err := tx.GetUser(ctx, actor.UserID)
		if err != nil {
			return err
		}
		if !actor.AdminAPIKey && user.Status != StatusActive {
			return ErrRBACForbidden
		}
		roles, err := tx.GetUserRoles(ctx, actor.UserID)
		if err != nil {
			return err
		}
		snapshots := make([]RolePermissions, 0, len(roles))
		effective := RolePermissions{SuperAdmin: actor.AdminAPIKey}
		for _, role := range roles {
			snapshots = append(snapshots, RolePermissions{Permissions: role.Permissions})
			effective.SuperAdmin = effective.SuperAdmin || role.IsSuperAdmin
		}
		effective.Permissions = EffectivePermissions(snapshots)
		if !HasPermission(effective, permission) {
			return ErrRBACForbidden
		}
		return fn(tx, effective)
	})
	if err != nil {
		return err
	}
	if sink != nil {
		for _, change := range changes {
			sink(change)
		}
	}
	return nil
}

var rbacRoleCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,99}$`)

func validRBACRoleName(name string) bool { return name != "" && utf8.RuneCountInString(name) <= 150 }
func protectedRBACRole(role *RBACRole) bool {
	return role.IsSystem || role.IsSuperAdmin || role.Code == "super_admin"
}
func checkRBACRoleCeiling(actor RolePermissions, roles ...RBACRole) error {
	for _, role := range roles {
		if (role.IsSuperAdmin || role.Code == "super_admin") && !actor.SuperAdmin {
			return ErrRBACPermissionCeiling
		}
		if err := ValidatePermissionCeiling(actor, role.Permissions); err != nil {
			return ErrRBACPermissionCeiling.WithCause(err)
		}
	}
	return nil
}
func mutableRBACRole(ctx context.Context, tx RBACManagementTx, actor RolePermissions, id int64) (*RBACRole, error) {
	role, err := tx.GetRole(ctx, id)
	if err != nil {
		return nil, err
	}
	if protectedRBACRole(role) {
		return nil, ErrRBACProtectedRole
	}
	if err := checkRBACRoleCeiling(actor, *role); err != nil {
		return nil, err
	}
	return role, nil
}

func (s *RBACManagementService) CreateRole(ctx context.Context, actor RBACActor, input CreateRBACRoleInput) (*RBACRole, error) {
	input.Code = strings.TrimSpace(input.Code)
	input.Name = strings.TrimSpace(input.Name)
	if !rbacRoleCodePattern.MatchString(input.Code) || !validRBACRoleName(input.Name) {
		return nil, ErrRBACInvalidInput
	}
	if input.Code == "super_admin" {
		return nil, ErrRBACProtectedRole
	}
	role := &RBACRole{Code: input.Code, Name: input.Name, Description: strings.TrimSpace(input.Description), Permissions: []string{}}
	err := s.authorized(ctx, actor, "roles.create", func(tx RBACManagementTx, _ RolePermissions) error { return tx.CreateRole(ctx, role) })
	if err != nil {
		return nil, err
	}
	return role, nil
}
func (s *RBACManagementService) UpdateRole(ctx context.Context, actor RBACActor, id int64, input UpdateRBACRoleInput) (*RBACRole, error) {
	input.Name = strings.TrimSpace(input.Name)
	if id <= 0 || !validRBACRoleName(input.Name) {
		return nil, ErrRBACInvalidInput
	}
	var result *RBACRole
	err := s.authorized(ctx, actor, "roles.update", func(tx RBACManagementTx, authority RolePermissions) error {
		role, err := mutableRBACRole(ctx, tx, authority, id)
		if err != nil {
			return err
		}
		role.Name = input.Name
		role.Description = strings.TrimSpace(input.Description)
		if err := tx.UpdateRole(ctx, role); err != nil {
			return err
		}
		result = role
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
func (s *RBACManagementService) DeleteRole(ctx context.Context, actor RBACActor, id int64) error {
	if id <= 0 {
		return ErrRBACInvalidInput
	}
	return s.authorized(ctx, actor, "roles.delete", func(tx RBACManagementTx, authority RolePermissions) error {
		if _, err := mutableRBACRole(ctx, tx, authority, id); err != nil {
			return err
		}
		return tx.DeleteRole(ctx, id)
	})
}
func (s *RBACManagementService) SetRolePermissions(ctx context.Context, actor RBACActor, id int64, codes []string) error {
	if id <= 0 {
		return ErrRBACInvalidInput
	}
	catalog := PermissionCatalog()
	seen := map[string]bool{}
	normalized := make([]string, 0, len(codes))
	for _, code := range codes {
		if _, ok := catalog[code]; !ok {
			return ErrRBACUnknownPermission.WithMetadata(map[string]string{"permission": code})
		}
		if !seen[code] {
			seen[code] = true
			normalized = append(normalized, code)
		}
	}
	sort.Strings(normalized)
	return s.authorized(ctx, actor, "roles.permissions.assign", func(tx RBACManagementTx, authority RolePermissions) error {
		if _, err := mutableRBACRole(ctx, tx, authority, id); err != nil {
			return err
		}
		if err := ValidatePermissionCeiling(authority, normalized); err != nil {
			return ErrRBACPermissionCeiling.WithCause(err)
		}
		return tx.SetRolePermissions(ctx, id, normalized)
	})
}
func (s *RBACManagementService) SetUserRoles(ctx context.Context, actor RBACActor, userID int64, roleIDs []int64) error {
	if userID <= 0 {
		return ErrRBACInvalidInput
	}
	seen := map[int64]bool{}
	normalized := make([]int64, 0, len(roleIDs))
	for _, id := range roleIDs {
		if id <= 0 {
			return ErrRBACInvalidInput
		}
		if !seen[id] {
			seen[id] = true
			normalized = append(normalized, id)
		}
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i] < normalized[j] })
	return s.authorized(ctx, actor, "users.roles.assign", func(tx RBACManagementTx, authority RolePermissions) error {
		target, err := tx.GetUser(ctx, userID)
		if err != nil {
			return err
		}
		current, err := tx.GetUserRoles(ctx, userID)
		if err != nil {
			return err
		}
		if target.LegacyAdmin {
			return ErrRBACProtectedUser
		}
		for _, role := range current {
			if role.IsSuperAdmin || role.Code == "super_admin" {
				return ErrRBACProtectedUser
			}
		}
		if err := checkRBACRoleCeiling(authority, current...); err != nil {
			return err
		}
		for _, id := range normalized {
			role, err := tx.GetRole(ctx, id)
			if err != nil {
				return err
			}
			if err := checkRBACRoleCeiling(authority, *role); err != nil {
				return err
			}
		}
		// Never rewrite users.role: doing so would promote ordinary roles on restart.
		return tx.SetUserRoles(ctx, userID, normalized)
	})
}
