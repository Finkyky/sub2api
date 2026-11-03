package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// sqlmock v1.5.2 does not expose TxOptions expectations. Capture them at the
// real BeginTx boundary; MaxOpenConns(1) additionally proves every reader uses
// that transaction's connection rather than issuing reads through the parent DB.
type rcChannelTxOptions struct {
	channelDB
	options sql.TxOptions
}

func (d *rcChannelTxOptions) BeginTx(ctx context.Context, o *sql.TxOptions) (*sql.Tx, error) {
	d.options = *o
	return d.channelDB.BeginTx(ctx, o)
}

type rcChannelSnapshotQuery struct {
	name, pattern string
	rows          *sqlmock.Rows
}

func rcChannelSnapshotQueries() []rcChannelSnapshotQuery {
	now := time.Now()
	return []rcChannelSnapshotQuery{
		{"policy", regexp.QuoteMeta(resultCachePolicyCurrentSQL), sqlmock.NewRows([]string{"config", "status", "version"}).AddRow(`{"enabled":true,"ttl_seconds":60,"max_response_bytes":2097152,"revision":2}`, "active", "snapshot-v2")},
		{"channels", "FROM channels ORDER BY id", sqlmock.NewRows([]string{"id", "name", "description", "status", "mapping", "billing", "restrict", "features", "feature_config", "stats", "created", "updated"}).AddRow(1, "channel", "", "active", `{"openai":{"m":"new"}}`, "channel_mapped", true, "[]", `{}`, true, now, now)},
		{"groups", "FROM channel_groups", sqlmock.NewRows([]string{"channel_id", "group_id"}).AddRow(1, 42)},
		{"pricing", "FROM channel_model_pricing", sqlmock.NewRows([]string{"id", "channel_id", "platform", "models", "billing_mode", "input", "output", "cw", "cw1h", "cr", "fast", "flex", "max", "ii", "io", "per", "time", "created", "updated"}).AddRow(10, 1, "openai", `["new"]`, "token", 2.0, 3.0, nil, nil, nil, nil, nil, nil, nil, nil, nil, `{"timezone":"UTC","periods":[]}`, now, now)},
		{"intervals", "FROM channel_pricing_intervals", sqlmock.NewRows([]string{"id", "pricing_id", "min", "max", "tier", "in", "out", "cw", "cw1h", "cr", "im", "om", "cwm", "crm", "per", "sort", "created", "updated"}).AddRow(11, 10, 0, nil, "base", 4.0, nil, nil, nil, nil, nil, nil, nil, nil, nil, 0, now, now)},
		{"stats", "FROM channel_account_stats_pricing_rules", sqlmock.NewRows([]string{"id", "channel_id", "name", "groups", "accounts", "sort", "created", "updated"}).AddRow(20, 1, "rule", "{42}", "{99}", 1, now, now)},
		{"stats_pricing", "FROM channel_account_stats_model_pricing", sqlmock.NewRows([]string{"id", "rule_id", "platform", "models", "mode", "in", "out", "cw", "cw1h", "cr", "reasoning_effort_multipliers", "io", "per", "created", "updated"}).AddRow(30, 20, "openai", `["new"]`, "token", 5.0, nil, nil, nil, nil, `{"high":2}`, nil, nil, now, now)},
		{"stats_intervals", "FROM channel_account_stats_pricing_intervals", sqlmock.NewRows([]string{"id", "pricing_id", "min", "max", "tier", "in", "out", "cw", "cw1h", "cr", "per", "sort", "created", "updated"}).AddRow(31, 30, 0, nil, "base", 6.0, nil, nil, nil, nil, nil, 0, now, now)},
		{"platforms", "SELECT id, platform FROM groups", sqlmock.NewRows([]string{"id", "platform"}).AddRow(42, service.PlatformOpenAI)},
	}
}
func TestResultCacheChannelSnapshotRepository(t *testing.T) {
	stages := []string{"success", "begin", "policy", "channels", "groups", "pricing", "intervals", "stats", "stats_pricing", "stats_intervals", "platforms", "commit"}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			db, m, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			db.SetMaxOpenConns(1)
			capture := &rcChannelTxOptions{channelDB: db}
			repo := &channelRepository{db: capture}
			failure := errors.New("snapshot stage failed")
			if stage == "begin" {
				m.ExpectBegin().WillReturnError(failure)
			} else {
				m.ExpectBegin()
				for _, q := range rcChannelSnapshotQueries() {
					expected := m.ExpectQuery(q.pattern)
					if q.name == "policy" {
						expected.WithArgs(int64(42))
					}
					if q.name == stage {
						expected.WillReturnError(failure)
						break
					}
					expected.WillReturnRows(q.rows).RowsWillBeClosed()
				}
				if stage == "success" {
					m.ExpectCommit()
				} else if stage == "commit" {
					m.ExpectCommit().WillReturnError(failure)
				} else {
					m.ExpectRollback()
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			channels, platforms, p, err := repo.LoadResultCacheSnapshot(ctx, 42)
			require.Equal(t, sql.LevelRepeatableRead, capture.options.Isolation)
			require.True(t, capture.options.ReadOnly)
			if stage == "success" {
				require.NoError(t, err)
				require.Equal(t, "snapshot-v2", p.Version)
				require.Len(t, channels, 1)
				require.Equal(t, service.PlatformOpenAI, platforms[42])
				ch := channels[0]
				require.Equal(t, "new", ch.ModelMapping[service.PlatformOpenAI]["m"])
				require.Equal(t, []int64{42}, ch.GroupIDs)
				require.Equal(t, 2.0, *ch.ModelPricing[0].InputPrice)
				require.Equal(t, "UTC", ch.ModelPricing[0].TimePricing.Timezone)
				require.Equal(t, 4.0, *ch.ModelPricing[0].Intervals[0].InputPrice)
				require.Equal(t, []int64{99}, ch.AccountStatsPricingRules[0].AccountIDs)
				require.Equal(t, 5.0, *ch.AccountStatsPricingRules[0].Pricing[0].InputPrice)
				require.Equal(t, map[string]float64{"high": 2}, ch.AccountStatsPricingRules[0].Pricing[0].ReasoningEffortMultipliers)
				require.Equal(t, 6.0, *ch.AccountStatsPricingRules[0].Pricing[0].Intervals[0].InputPrice)
			} else {
				require.ErrorIs(t, err, failure)
				require.Nil(t, channels)
				require.Nil(t, platforms)
				require.Empty(t, p.Version)
			}
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}
func TestResultCacheChannelSnapshotRepositoryNoChannel(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	m.ExpectBegin()
	q := rcChannelSnapshotQueries()[0]
	m.ExpectQuery(q.pattern).WithArgs(int64(42)).WillReturnRows(q.rows)
	m.ExpectQuery("FROM channels ORDER BY id").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "description", "status", "mapping", "billing", "restrict", "features", "feature_config", "stats", "created", "updated"}))
	m.ExpectCommit()
	channels, platforms, p, err := NewChannelRepository(db).(service.ResultCacheChannelSnapshotRepository).LoadResultCacheSnapshot(context.Background(), 42)
	require.NoError(t, err)
	require.Empty(t, channels)
	require.Empty(t, platforms)
	require.Equal(t, "snapshot-v2", p.Version)
	require.NoError(t, m.ExpectationsWereMet())
}
