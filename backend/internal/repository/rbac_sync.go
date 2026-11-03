package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// SyncRBAC synchronizes the code-owned permission catalog and repairs the built-in
// super-admin role after migrations, before serving requests. Custom roles and
// their assignments are left untouched. Legacy admin compatibility is retained
// while users.role remains the administration entry gate.
func SyncRBAC(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errors.New("sync RBAC: nil sql db")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin RBAC sync: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := syncRBAC(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit RBAC sync: %w", err)
	}
	return nil
}

func syncRBAC(ctx context.Context, tx *sql.Tx) error {
	// Every instance locks/upserts the same role first. PostgreSQL retains that
	// row lock until commit, serializing concurrent startup synchronizations.
	var superAdminID int64
	err := tx.QueryRowContext(ctx, `INSERT INTO roles (code, name, description, is_system, is_super_admin)
 VALUES ($1, $2, $3, TRUE, TRUE)
 ON CONFLICT (code) DO UPDATE SET name = EXCLUDED.name, description = EXCLUDED.description,
 is_system = TRUE, is_super_admin = TRUE, updated_at = NOW() RETURNING id`,
		"super_admin", "Super Administrator", "Built-in role with all administration permissions").Scan(&superAdminID)
	if err != nil {
		return fmt.Errorf("sync super_admin role: %w", err)
	}

	catalog := service.PermissionCatalog()
	codes := make([]string, 0, len(catalog))
	for code := range catalog {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		p := catalog[code]
		_, err := tx.ExecContext(ctx, `INSERT INTO permissions (code, name, resource, action, description)
 VALUES ($1, $2, $3, $4, $5)
 ON CONFLICT (code) DO UPDATE SET name = EXCLUDED.name, resource = EXCLUDED.resource,
 action = EXCLUDED.action, description = EXCLUDED.description, updated_at = NOW()`,
			p.Code, p.Name, p.Resource, p.Action, p.Description)
		if err != nil {
			return fmt.Errorf("sync permission %q: %w", code, err)
		}
	}
	// Keep older catalog rows during rolling upgrades. Never delete custom role
	// bindings; super-admin always retains all permissions, including new entries.
	if _, err := tx.ExecContext(ctx, `INSERT INTO role_permissions (role_id, permission_id)
 SELECT $1, id FROM permissions ON CONFLICT (role_id, permission_id) DO NOTHING`, superAdminID); err != nil {
		return fmt.Errorf("sync super_admin permissions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_roles (user_id, role_id)
 SELECT id, $1 FROM users WHERE role = $2 AND deleted_at IS NULL
 ON CONFLICT (user_id, role_id) DO NOTHING`, superAdminID, service.RoleAdmin); err != nil {
		return fmt.Errorf("sync legacy admin roles: %w", err)
	}
	// Record startup success atomically with the synchronized state. The HTTP
	// audit queue is not running yet, and a separate repository transaction would
	// either lose this event or commit it for a startup that later rolled back.
	// One event per successful sync is intentional; catalog/assignment data stays
	// idempotent. Only code-owned permission metadata is recorded, never credentials.
	extra, err := json.Marshal(map[string]any{
		"source": "startup", "permission_count": len(codes),
		"permission_codes": codes, "super_admin_role_id": superAdminID,
	})
	if err != nil {
		return fmt.Errorf("encode RBAC sync audit: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_logs (actor_role, auth_method, action, status_code, extra)
 VALUES ($1, $2, $3, $4, $5)`, "system", "system", "permission_catalog.synced", 200, string(extra)); err != nil {
		return fmt.Errorf("audit RBAC sync: %w", err)
	}
	return nil
}
