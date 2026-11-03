package repository

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

const testRBACRoleUpsert = `INSERT INTO roles (code, name, description, is_system, is_super_admin)
 VALUES ($1, $2, $3, TRUE, TRUE)
 ON CONFLICT (code) DO UPDATE SET name = EXCLUDED.name, description = EXCLUDED.description,
 is_system = TRUE, is_super_admin = TRUE, updated_at = NOW() RETURNING id`
const testRBACPermissionUpsert = `INSERT INTO permissions (code, name, resource, action, description)
 VALUES ($1, $2, $3, $4, $5)
 ON CONFLICT (code) DO UPDATE SET name = EXCLUDED.name, resource = EXCLUDED.resource,
 action = EXCLUDED.action, description = EXCLUDED.description, updated_at = NOW()`
const testRBACGrant = `INSERT INTO role_permissions (role_id, permission_id)
 SELECT $1, id FROM permissions ON CONFLICT (role_id, permission_id) DO NOTHING`
const testRBACBind = `INSERT INTO user_roles (user_id, role_id)
 SELECT id, $1 FROM users WHERE role = $2 AND deleted_at IS NULL
 ON CONFLICT (user_id, role_id) DO NOTHING`

const testRBACSyncAudit = `INSERT INTO audit_logs (actor_role, auth_method, action, status_code, extra)
 VALUES ($1, $2, $3, $4, $5)`

// Expectations independently specify the conflict keys and legacy-admin filter.
// A failure at any write must stop subsequent writes and roll back the transaction.
func expectRBACSync(mock sqlmock.Sqlmock, failAt string, cause error) {
	if failAt == "begin" {
		mock.ExpectBegin().WillReturnError(cause)
		return
	}
	mock.ExpectBegin()
	role := mock.ExpectQuery(regexp.QuoteMeta(testRBACRoleUpsert)).WithArgs("super_admin", "Super Administrator", "Built-in role with all administration permissions")
	if failAt == "role" {
		role.WillReturnError(cause)
		mock.ExpectRollback()
		return
	}
	role.WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(10))
	catalog := service.PermissionCatalog()
	codes := make([]string, 0, len(catalog))
	for code := range catalog {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for i, code := range codes {
		p := catalog[code]
		write := mock.ExpectExec(regexp.QuoteMeta(testRBACPermissionUpsert)).WithArgs(p.Code, p.Name, p.Resource, p.Action, p.Description)
		if failAt == "permission" && i == 1 {
			write.WillReturnError(cause)
			mock.ExpectRollback()
			return
		}
		write.WillReturnResult(sqlmock.NewResult(0, 1))
	}
	grant := mock.ExpectExec(regexp.QuoteMeta(testRBACGrant)).WithArgs(int64(10))
	if failAt == "grant" {
		grant.WillReturnError(cause)
		mock.ExpectRollback()
		return
	}
	grant.WillReturnResult(sqlmock.NewResult(0, 0))
	bind := mock.ExpectExec(regexp.QuoteMeta(testRBACBind)).WithArgs(int64(10), service.RoleAdmin)
	if failAt == "bind" {
		bind.WillReturnError(cause)
		mock.ExpectRollback()
		return
	}
	bind.WillReturnResult(sqlmock.NewResult(0, 0))
	extra, _ := json.Marshal(map[string]any{
		"source": "startup", "permission_count": len(codes),
		"permission_codes": codes, "super_admin_role_id": int64(10),
	})
	audit := mock.ExpectExec(regexp.QuoteMeta(testRBACSyncAudit)).WithArgs("system", "system", "permission_catalog.synced", 200, string(extra))
	if failAt == "audit" {
		audit.WillReturnError(cause)
		mock.ExpectRollback()
		return
	}
	audit.WillReturnResult(sqlmock.NewResult(1, 1))
	if failAt == "commit" {
		mock.ExpectCommit().WillReturnError(cause)
		return
	}
	mock.ExpectCommit()
}

func TestRBACSyncCatalogAndCompatibilityTwice(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	for i := 0; i < 2; i++ {
		expectRBACSync(mock, "", nil)
		require.NoError(t, SyncRBAC(context.Background(), db))
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRBACSyncFailureRollsBack(t *testing.T) {
	for _, stage := range []string{"begin", "role", "permission", "grant", "bind", "audit", "commit"} {
		t.Run(stage, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			cause := errors.New("injected " + stage + " failure")
			expectRBACSync(mock, stage, cause)
			err = SyncRBAC(context.Background(), db)
			require.ErrorIs(t, err, cause)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRBACSyncRejectsNilDatabase(t *testing.T) {
	require.ErrorContains(t, SyncRBAC(context.Background(), nil), "nil sql db")
}

func TestRBACSyncHonorsCanceledContext(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, SyncRBAC(ctx, db), context.Canceled)
	require.NoError(t, mock.ExpectationsWereMet())
}
