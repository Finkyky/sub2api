//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/lib/pq"
	"net/url"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestRBACSyncIntegrationRepairsAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	// All fixture DDL/data, including this private schema, roll back with testTx.
	// Use the real RBAC migration rather than a second handwritten table schema.
	schema := fmt.Sprintf("rbac_sync_%d", time.Now().UnixNano())
	_, err := tx.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "SET LOCAL search_path TO "+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `CREATE TABLE users (id BIGINT PRIMARY KEY, role TEXT NOT NULL, deleted_at TIMESTAMPTZ)`)
	require.NoError(t, err)
	source, err := migrations.FS.ReadFile("250_add_rbac_foundation.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(source))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
 INSERT INTO users (id, role, deleted_at) VALUES (101, 'admin', NULL), (102, 'user', NULL), (103, 'admin', NOW());
 INSERT INTO roles (code, name) VALUES ('auditor', 'Custom auditor');
 INSERT INTO role_permissions (role_id, permission_id)
 SELECT r.id, p.id FROM roles r, permissions p WHERE r.code = 'auditor' AND p.code = 'users.read';
 INSERT INTO user_roles (user_id, role_id) SELECT 102, id FROM roles WHERE code = 'auditor';
 UPDATE roles SET name = 'Corrupt', is_system = FALSE, is_super_admin = FALSE WHERE code = 'super_admin';
 UPDATE permissions SET name = 'Stale', resource = 'stale', action = 'stale', description = 'stale' WHERE code = 'users.read';
 DELETE FROM permissions WHERE code = 'roles.read';
 INSERT INTO permissions (code, name, resource, action) VALUES ('retired.read', 'Retired', 'retired', 'read');`)
	require.NoError(t, err)

	auditMigration, err := migrations.FS.ReadFile("180_audit_logs.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(auditMigration))
	require.NoError(t, err)
	var previousCounts []int
	for iteration := 0; iteration < 2; iteration++ {
		require.NoError(t, syncRBAC(ctx, tx))
		var auditCount int
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT count(*) FROM audit_logs WHERE action = 'permission_catalog.synced'`).Scan(&auditCount))
		require.Equal(t, iteration+1, auditCount, "one audit event per successful sync, even when state is unchanged")
		var name string
		var system, super bool
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT name, is_system, is_super_admin FROM roles WHERE code = 'super_admin'`).Scan(&name, &system, &super))
		require.Equal(t, "Super Administrator", name)
		require.True(t, system)
		require.True(t, super)
		for _, p := range service.PermissionCatalog() {
			var got service.PermissionDefinition
			require.NoError(t, tx.QueryRowContext(ctx, `SELECT code, name, resource, action, COALESCE(description, '') FROM permissions WHERE code = $1`, p.Code).Scan(&got.Code, &got.Name, &got.Resource, &got.Action, &got.Description))
			require.Equal(t, p, got)
		}
		var roleCount, permissionCount, grantCount, bindingCount int
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM roles), (SELECT COUNT(*) FROM permissions), (SELECT COUNT(*) FROM role_permissions), (SELECT COUNT(*) FROM user_roles)`).Scan(&roleCount, &permissionCount, &grantCount, &bindingCount))
		counts := []int{roleCount, permissionCount, grantCount, bindingCount}
		require.Equal(t, []int{2, len(service.PermissionCatalog()) + 1, len(service.PermissionCatalog()) + 2, 2}, counts)
		if iteration > 0 {
			require.Equal(t, previousCounts, counts)
		}
		previousCounts = counts
		var userID int64
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT ur.user_id FROM user_roles ur JOIN roles r ON r.id = ur.role_id WHERE r.code = 'super_admin'`).Scan(&userID))
		require.EqualValues(t, 101, userID, "ordinary and soft-deleted users must not be promoted")
		var customPermission string
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT p.code FROM role_permissions rp JOIN roles r ON r.id = rp.role_id JOIN permissions p ON p.id = rp.permission_id WHERE r.code = 'auditor'`).Scan(&customPermission))
		require.Equal(t, "users.read", customPermission)
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT ur.user_id FROM user_roles ur JOIN roles r ON r.id = ur.role_id WHERE r.code = 'auditor'`).Scan(&userID))
		require.EqualValues(t, 102, userID)
	}
}

// This fixture uses the real migrations and independent committed transactions,
// unlike testTx. Its private schema is dropped only inside the disposable DB.
func rbacSyncDatabase(t *testing.T) *sql.DB {
	t.Helper()
	schema := fmt.Sprintf("rbac_atomic_%d", time.Now().UnixNano())
	_, err := integrationDB.Exec("CREATE SCHEMA " + pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	// The harness emits URL DSNs for both Docker and external mode.
	u, err := url.Parse(integrationDSN)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	connector, err := pq.NewConnector(u.String())
	require.NoError(t, err)
	db := sql.OpenDB(connector)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
		_, err := integrationDB.Exec("DROP SCHEMA " + pq.QuoteIdentifier(schema) + " CASCADE")
		require.NoError(t, err)
	})
	_, err = db.Exec(`CREATE TABLE users (id BIGINT PRIMARY KEY, role TEXT NOT NULL, deleted_at TIMESTAMPTZ);`)
	require.NoError(t, err)
	for _, file := range []string{"250_add_rbac_foundation.sql", "180_audit_logs.sql"} {
		data, err := migrations.FS.ReadFile(file)
		require.NoError(t, err)
		_, err = db.Exec(string(data))
		require.NoError(t, err)
	}
	_, err = db.Exec(`INSERT INTO users VALUES (1, 'admin', NULL), (2, 'user', NULL), (3, 'admin', NOW())`)
	require.NoError(t, err)
	return db
}

func TestRBACSyncIntegrationCommittedAuditAndConcurrency(t *testing.T) {
	db := rbacSyncDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	const starts = 4
	var wg sync.WaitGroup
	errs := make(chan error, starts)
	for i := 0; i < starts; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- SyncRBAC(ctx, db) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var roles, permissions, grants, bindings, audits int
	require.NoError(t, db.QueryRow(`SELECT (SELECT count(*) FROM roles), (SELECT count(*) FROM permissions),
 (SELECT count(*) FROM role_permissions), (SELECT count(*) FROM user_roles), (SELECT count(*) FROM audit_logs)`).Scan(&roles, &permissions, &grants, &bindings, &audits))
	require.Equal(t, []int{1, len(service.PermissionCatalog()), len(service.PermissionCatalog()), 1, starts}, []int{roles, permissions, grants, bindings, audits})
	rows, err := db.Query(`SELECT actor_user_id, actor_email, actor_role, auth_method, credential_masked,
 action, method, path, request_body, status_code, extra FROM audit_logs ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()
	codes := make([]string, 0, len(service.PermissionCatalog()))
	for code := range service.PermissionCatalog() {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for rows.Next() {
		var actor sql.NullInt64
		var email, role, auth, credential, action, method, path, body string
		var status int
		var raw []byte
		require.NoError(t, rows.Scan(&actor, &email, &role, &auth, &credential, &action, &method, &path, &body, &status, &raw))
		require.False(t, actor.Valid)
		require.Equal(t, []string{"", "system", "system", "", "permission_catalog.synced", "", "", ""}, []string{email, role, auth, credential, action, method, path, body})
		require.Equal(t, 200, status)
		var extra struct {
			Source string   `json:"source"`
			Count  int      `json:"permission_count"`
			Codes  []string `json:"permission_codes"`
			RoleID int64    `json:"super_admin_role_id"`
		}
		require.NoError(t, json.Unmarshal(raw, &extra))
		require.Equal(t, "startup", extra.Source)
		require.Equal(t, len(codes), extra.Count)
		require.Equal(t, codes, extra.Codes)
		require.Positive(t, extra.RoleID)
	}
	require.NoError(t, rows.Err())
}

func TestRBACSyncIntegrationFailureIsAtomic(t *testing.T) {
	for _, stage := range []string{"permission", "audit", "commit"} {
		t.Run(stage, func(t *testing.T) {
			db := rbacSyncDatabase(t)
			_, err := db.Exec(`UPDATE roles SET name='stale', is_system=FALSE WHERE code='super_admin';
    DELETE FROM permissions WHERE code='roles.read'; DELETE FROM user_roles; DELETE FROM role_permissions;`)
			require.NoError(t, err)
			var originalPermissions int
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM permissions`).Scan(&originalPermissions))
			table, timing := "audit_logs", "BEFORE INSERT"
			if stage == "permission" {
				table = "permissions"
			}
			if stage == "commit" {
				timing = "AFTER INSERT"
			}
			_, err = db.Exec(`CREATE FUNCTION reject_sync() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected sync failure'; END $$`)
			require.NoError(t, err)
			trigger := "CREATE TRIGGER fail_sync " + timing + " ON " + table + " FOR EACH ROW EXECUTE FUNCTION reject_sync()"
			if stage == "commit" {
				trigger = "CREATE CONSTRAINT TRIGGER fail_sync AFTER INSERT ON audit_logs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_sync()"
			}
			_, err = db.Exec(trigger)
			require.NoError(t, err)
			err = SyncRBAC(context.Background(), db)
			require.ErrorContains(t, err, "injected sync failure")
			if stage == "commit" {
				require.ErrorContains(t, err, "commit RBAC sync")
			}
			var name string
			var system bool
			var permissions, grants, bindings, audits int
			require.NoError(t, db.QueryRow(`SELECT name,is_system FROM roles WHERE code='super_admin'`).Scan(&name, &system))
			require.Equal(t, "stale", name)
			require.False(t, system)
			require.NoError(t, db.QueryRow(`SELECT (SELECT count(*) FROM permissions),(SELECT count(*) FROM role_permissions),(SELECT count(*) FROM user_roles),(SELECT count(*) FROM audit_logs)`).Scan(&permissions, &grants, &bindings, &audits))
			require.Equal(t, []int{originalPermissions, 0, 0, 0}, []int{permissions, grants, bindings, audits})
			_, err = db.Exec("DROP TRIGGER fail_sync ON " + table)
			require.NoError(t, err)
			require.NoError(t, SyncRBAC(context.Background(), db))
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM audit_logs`).Scan(&audits))
			require.Equal(t, 1, audits)
		})
	}
}
