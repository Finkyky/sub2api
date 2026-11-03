package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbrole "github.com/Wei-Shaw/sub2api/ent/role"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func newRBACManagementSQLMock(t *testing.T) (service.RBACManagementRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	return NewRBACManagementRepository(client), mock
}
func expectRBACManagementLock(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT .* FROM "roles" WHERE .*code.* FOR UPDATE`).
		WillReturnRows(sqlmock.NewRows(dbrole.Columns).AddRow(30, time.Now(), time.Now(), "super_admin", "Root", nil, true, true))
}

func TestRBACManagementRepositoryTransactionLifecycle(t *testing.T) {
	for _, stage := range []string{"success", "begin", "lock", "callback", "commit"} {
		t.Run(stage, func(t *testing.T) {
			repo, mock := newRBACManagementSQLMock(t)
			cause := errors.New("injected " + stage)
			switch stage {
			case "begin":
				mock.ExpectBegin().WillReturnError(cause)
			case "lock":
				mock.ExpectBegin()
				mock.ExpectQuery(`SELECT .* FOR UPDATE`).WillReturnError(cause)
				mock.ExpectRollback()
			default:
				expectRBACManagementLock(mock)
				if stage == "callback" {
					mock.ExpectRollback()
				} else if stage == "commit" {
					mock.ExpectCommit().WillReturnError(cause)
				} else {
					mock.ExpectCommit()
				}
			}
			called := false
			err := repo.WithTransaction(context.Background(), func(tx service.RBACManagementTx) error {
				called = true
				require.NotNil(t, tx)
				if stage == "callback" {
					return cause
				}
				return nil
			})
			if stage == "success" {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, cause)
			}
			require.Equal(t, stage != "begin" && stage != "lock", called)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRBACManagementRepositoryMissingLockRoleFailsClosed(t *testing.T) {
	repo, mock := newRBACManagementSQLMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT .* FOR UPDATE`).WillReturnRows(sqlmock.NewRows(dbrole.Columns))
	mock.ExpectRollback()
	called := false
	err := repo.WithTransaction(context.Background(), func(service.RBACManagementTx) error { called = true; return nil })
	require.Error(t, err)
	require.False(t, called)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRBACManagementRepositoryReplacePermissions(t *testing.T) {
	repo, mock := newRBACManagementSQLMock(t)
	expectRBACManagementLock(mock)
	mock.ExpectQuery(`SELECT .* FROM "permissions"`).WithArgs("roles.read", "users.read").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(2).AddRow(5))
	mock.ExpectExec(`DELETE FROM "role_permissions"`).WithArgs(int64(20)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`INSERT INTO "role_permissions"`).WithArgs(int64(2), int64(20), int64(5), int64(20)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(100).AddRow(101))
	mock.ExpectCommit()
	require.NoError(t, repo.WithTransaction(context.Background(), func(tx service.RBACManagementTx) error {
		return tx.SetRolePermissions(context.Background(), 20, []string{"roles.read", "users.read"})
	}))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRBACManagementRepositoryMissingPermissionDoesNotDeleteBindings(t *testing.T) {
	repo, mock := newRBACManagementSQLMock(t)
	expectRBACManagementLock(mock)
	mock.ExpectQuery(`SELECT .* FROM "permissions"`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()
	err := repo.WithTransaction(context.Background(), func(tx service.RBACManagementTx) error {
		return tx.SetRolePermissions(context.Background(), 20, []string{"users.read"})
	})
	require.ErrorIs(t, err, service.ErrRBACUnknownPermission)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRBACManagementRepositoryAssignmentInsertFailureRollsBack(t *testing.T) {
	for _, kind := range []string{"permission", "user_role"} {
		t.Run(kind, func(t *testing.T) {
			repo, mock := newRBACManagementSQLMock(t)
			expectRBACManagementLock(mock)
			cause := errors.New("assignment insert failed")
			if kind == "permission" {
				mock.ExpectQuery(`SELECT .* FROM "permissions"`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(5))
				mock.ExpectExec(`DELETE FROM "role_permissions"`).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectQuery(`INSERT INTO "role_permissions"`).WillReturnError(cause)
			} else {
				mock.ExpectQuery(`SELECT .* FROM "roles"`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(20))
				mock.ExpectExec(`DELETE FROM "user_roles"`).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectQuery(`INSERT INTO "user_roles"`).WillReturnError(cause)
			}
			mock.ExpectRollback()
			err := repo.WithTransaction(context.Background(), func(tx service.RBACManagementTx) error {
				if kind == "permission" {
					return tx.SetRolePermissions(context.Background(), 20, []string{"users.read"})
				}
				return tx.SetUserRoles(context.Background(), 2, []int64{20})
			})
			require.ErrorIs(t, err, cause)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRBACManagementRepositoryClearAssignments(t *testing.T) {
	for _, kind := range []string{"permission", "user_role"} {
		t.Run(kind, func(t *testing.T) {
			repo, mock := newRBACManagementSQLMock(t)
			expectRBACManagementLock(mock)
			table := "role_permissions"
			if kind == "user_role" {
				table = "user_roles"
			}
			mock.ExpectExec(`DELETE FROM "` + table + `"`).WillReturnResult(sqlmock.NewResult(0, 2))
			mock.ExpectCommit()
			require.NoError(t, repo.WithTransaction(context.Background(), func(tx service.RBACManagementTx) error {
				if kind == "permission" {
					return tx.SetRolePermissions(context.Background(), 20, nil)
				}
				return tx.SetUserRoles(context.Background(), 2, nil)
			}))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestRBACManagementRepositoryCreateDuplicateRole(t *testing.T) {
	repo, mock := newRBACManagementSQLMock(t)
	expectRBACManagementLock(mock)
	mock.ExpectQuery(`INSERT INTO "roles"`).WillReturnError(&pq.Error{Code: "23505"})
	mock.ExpectRollback()
	err := repo.WithTransaction(context.Background(), func(tx service.RBACManagementTx) error {
		return tx.CreateRole(context.Background(), &service.RBACRole{Code: "reader", Name: "Reader"})
	})
	require.ErrorIs(t, err, service.ErrRBACRoleExists)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRBACManagementRepositoryMapsRolePermissions(t *testing.T) {
	got := mapRBACRole(&dbent.Role{ID: 20, Code: "reader", Name: "Reader", Edges: dbent.RoleEdges{Permissions: []*dbent.Permission{{Code: "users.read"}, {Code: "roles.read"}}}})
	require.Equal(t, []string{"roles.read", "users.read"}, got.Permissions)
	empty := mapRBACRole(&dbent.Role{})
	require.NotNil(t, empty.Permissions)
}

type rbacTxOptionsDriver struct {
	*entsql.Driver
	options *sql.TxOptions
}

func (d *rbacTxOptionsDriver) BeginTx(ctx context.Context, opts *sql.TxOptions) (dialect.Tx, error) {
	d.options = opts
	return d.Driver.BeginTx(ctx, opts)
}
func TestRBACManagementRepositoryExplicitReadCommitted(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	driver := &rbacTxOptionsDriver{Driver: entsql.OpenDB(dialect.Postgres, db)}
	client := dbent.NewClient(dbent.Driver(driver))
	defer client.Close()
	expectRBACManagementLock(mock)
	mock.ExpectCommit()
	require.NoError(t, NewRBACManagementRepository(client).WithTransaction(context.Background(), func(service.RBACManagementTx) error { return nil }))
	require.NotNil(t, driver.options, "authorization reads must not inherit a stale repeatable-read transaction snapshot")
	require.Equal(t, sql.LevelReadCommitted, driver.options.Isolation)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRBACManagementRepositoryReplaceUserRoles(t *testing.T) {
	repo, mock := newRBACManagementSQLMock(t)
	expectRBACManagementLock(mock)
	mock.ExpectQuery(`SELECT .* FROM "roles"`).WithArgs(int64(20), int64(40)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(20).AddRow(40))
	mock.ExpectExec(`DELETE FROM "user_roles"`).WithArgs(int64(2)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`INSERT INTO "user_roles"`).WithArgs(int64(20), int64(2), int64(40), int64(2)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(10).AddRow(11))
	mock.ExpectCommit()
	require.NoError(t, repo.WithTransaction(context.Background(), func(tx service.RBACManagementTx) error {
		return tx.SetUserRoles(context.Background(), 2, []int64{20, 40})
	}))
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestRBACManagementRepositoryMissingRoleDoesNotDeleteBindings(t *testing.T) {
	repo, mock := newRBACManagementSQLMock(t)
	expectRBACManagementLock(mock)
	mock.ExpectQuery(`SELECT .* FROM "roles"`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()
	err := repo.WithTransaction(context.Background(), func(tx service.RBACManagementTx) error { return tx.SetUserRoles(context.Background(), 2, []int64{404}) })
	require.ErrorIs(t, err, service.ErrRBACRoleNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestRBACManagementRepositoryReadMissingRole(t *testing.T) {
	repo, mock := newRBACManagementSQLMock(t)
	mock.ExpectQuery(`SELECT .* FROM "roles" WHERE .*id`).WillReturnRows(sqlmock.NewRows(dbrole.Columns))
	role, err := repo.GetRole(context.Background(), 404)
	require.Nil(t, role)
	require.ErrorIs(t, err, service.ErrRBACRoleNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestRBACManagementRepositoryLocksOnlyNonDeletedUsers(t *testing.T) {
	repo, mock := newRBACManagementSQLMock(t)
	expectRBACManagementLock(mock)
	mock.ExpectQuery(`SELECT .* FROM "users" WHERE .*deleted_at.*IS NULL.*FOR UPDATE`).WillReturnRows(sqlmock.NewRows([]string{"id", "status", "role"}).AddRow(7, service.StatusActive, service.RoleAdmin))
	mock.ExpectCommit()
	require.NoError(t, repo.WithTransaction(context.Background(), func(tx service.RBACManagementTx) error {
		user, err := tx.GetUser(context.Background(), 7)
		if err != nil {
			return err
		}
		require.EqualValues(t, 7, user.ID)
		require.True(t, user.LegacyAdmin)
		return nil
	}))
	require.NoError(t, mock.ExpectationsWereMet())
}
