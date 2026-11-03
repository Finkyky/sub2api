package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func rcWriteFixture(t *testing.T) (*groupRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	c := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = c.Close() })
	return newGroupRepositoryWithSQL(c, db), m
}
func rcWriteGroup() *service.Group {
	return &service.Group{ID: 7, Name: "cache-group", Platform: service.PlatformOpenAI, RateMultiplier: 1, Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard, ResponseResultCacheConfig: service.GroupResponseResultCacheConfig{Enabled: true, TTLSeconds: 30, MaxResponseBytes: 2097152, Revision: 4}}
}
func TestGroupResultCacheNilWrites(t *testing.T) {
	require.NotPanics(t, func() { require.EqualError(t, createGroupRecord(context.Background(), nil, nil), "group is nil") })
	require.NotPanics(t, func() { require.EqualError(t, (&groupRepository{}).Update(context.Background(), nil), "group is nil") })
}
func TestGroupResultCacheFailedWritePreservesRevision(t *testing.T) {
	t.Run("database failure and retry", func(t *testing.T) {
		repo, m := rcWriteFixture(t)
		g := rcWriteGroup()
		before := g.ResponseResultCacheConfig
		for attempt := 0; attempt < 2; attempt++ {
			m.ExpectBegin()
			m.ExpectExec(`(?s)UPDATE "groups".*COALESCE.*revision`).WillReturnError(errors.New("database write failed"))
			m.ExpectRollback()
			err := repo.Update(context.Background(), g)
			require.ErrorContains(t, err, "database write failed")
			require.Equal(t, before, g.ResponseResultCacheConfig, "a failed attempt must not advance the caller's CAS revision")
		}
		require.NoError(t, m.ExpectationsWereMet())
		t.Logf("FAILED_WRITE input_revision=%d retry_revision=%d", before.Revision, g.ResponseResultCacheConfig.Revision)
	})
	t.Run("CAS conflict", func(t *testing.T) {
		repo, m := rcWriteFixture(t)
		g := rcWriteGroup()
		before := g.ResponseResultCacheConfig
		m.ExpectBegin()
		m.ExpectExec(`(?s)UPDATE "groups".*COALESCE.*revision`).WillReturnResult(sqlmock.NewResult(0, 0))
		m.ExpectQuery(`(?s)SELECT.*FROM "groups"`).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		m.ExpectRollback()
		err := repo.Update(context.Background(), g)
		require.ErrorIs(t, err, service.ErrResultCachePolicyConflict)
		require.Equal(t, before, g.ResponseResultCacheConfig)
		require.NoError(t, m.ExpectationsWereMet())
	})
}
func TestGroupResultCacheCreateFailurePreservesInput(t *testing.T) {
	repo, m := rcWriteFixture(t)
	g := rcWriteGroup()
	g.ResponseResultCacheConfig.Revision = 99
	g.ResponseResultCacheConfig.TTLSeconds = 0
	before := g.ResponseResultCacheConfig
	m.ExpectQuery(`INSERT INTO "groups"`).WillReturnError(errors.New("create failed"))
	require.ErrorContains(t, createGroupRecord(context.Background(), repo.client, g), "create failed")
	require.Equal(t, before, g.ResponseResultCacheConfig)
	require.NoError(t, m.ExpectationsWereMet())
}
func TestGroupResultCacheCreateOwnsRevision(t *testing.T) {
	repo, m := rcWriteFixture(t)
	g := rcWriteGroup()
	g.ResponseResultCacheConfig.Revision = 99
	g.ResponseResultCacheConfig.TTLSeconds = 0
	m.ExpectQuery(`INSERT INTO "groups"`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(8)))
	require.NoError(t, createGroupRecord(context.Background(), repo.client, g))
	require.EqualValues(t, 1, g.ResponseResultCacheConfig.Revision)
	require.Equal(t, 30, g.ResponseResultCacheConfig.TTLSeconds)
	require.EqualValues(t, 8, g.ID)
	require.NoError(t, m.ExpectationsWereMet())
}
func TestGroupResultCacheSuccessfulWritePublishesRevision(t *testing.T) {
	repo, m := rcWriteFixture(t)
	g := rcWriteGroup()
	updatedAt := time.Now().UTC()
	m.ExpectBegin()
	m.ExpectExec(`(?s)UPDATE "groups".*COALESCE.*revision`).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery(`(?s)SELECT.*FROM "groups"`).WillReturnRows(sqlmock.NewRows([]string{"id", "updated_at"}).AddRow(int64(7), updatedAt))
	m.ExpectCommit()
	m.ExpectExec(`INSERT INTO scheduler_outbox`).WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, repo.Update(context.Background(), g))
	require.EqualValues(t, 5, g.ResponseResultCacheConfig.Revision)
	require.Equal(t, updatedAt, g.UpdatedAt)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestGroupResultCacheCommitFailurePreservesRevision(t *testing.T) {
	repo, m := rcWriteFixture(t)
	g := rcWriteGroup()
	before := g.ResponseResultCacheConfig
	m.ExpectBegin()
	m.ExpectExec(`(?s)UPDATE "groups".*COALESCE.*revision`).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery(`(?s)SELECT.*FROM "groups"`).WillReturnRows(sqlmock.NewRows([]string{"id", "updated_at"}).AddRow(int64(7), time.Now().UTC()))
	m.ExpectCommit().WillReturnError(errors.New("commit failed"))
	require.ErrorContains(t, repo.Update(context.Background(), g), "commit failed")
	require.Equal(t, before, g.ResponseResultCacheConfig)
	require.NoError(t, m.ExpectationsWereMet())
}
