package setup

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestInsertBootstrapAdminAssignsSuperAdminRoleAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	userQuery := mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO users (email, password_hash, role, balance, concurrency, status, created_at, updated_at)"))
	userQuery.WithArgs("root@example.com", "hash", service.RoleAdmin, 0.0, 5, service.StatusActive, sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(42))
	roleExec := mock.ExpectExec(regexp.QuoteMeta("INSERT INTO user_roles (user_id, role_id) SELECT $1, id FROM roles WHERE code = 'super_admin' ON CONFLICT (user_id, role_id) DO NOTHING"))
	roleExec.WithArgs(int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	admin := &service.User{Email: "root@example.com", PasswordHash: "hash", Role: service.RoleAdmin, Status: service.StatusActive, Concurrency: 5}
	if err := insertBootstrapAdmin(context.Background(), db, admin); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestInsertBootstrapAdminRejectsMissingSuperAdminRole(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO users").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(42))
	mock.ExpectExec("INSERT INTO user_roles").WithArgs(int64(42)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	err = insertBootstrapAdmin(context.Background(), db, &service.User{})
	require.ErrorContains(t, err, "super_admin role assignment")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestInsertBootstrapAdminRollsBackWriteFailure(t *testing.T) {
	for _, stage := range []string{"user", "role", "rows_affected"} {
		t.Run(stage, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			cause := errors.New("injected " + stage + " failure")
			mock.ExpectBegin()
			user := mock.ExpectQuery("INSERT INTO users")
			if stage == "user" {
				user.WillReturnError(cause)
			} else {
				user.WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(42))
				role := mock.ExpectExec("INSERT INTO user_roles").WithArgs(int64(42))
				if stage == "role" {
					role.WillReturnError(cause)
				} else {
					role.WillReturnResult(sqlmock.NewErrorResult(cause))
				}
			}
			mock.ExpectRollback()
			require.ErrorIs(t, insertBootstrapAdmin(context.Background(), db, &service.User{}), cause)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
