package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Both *sql.DB and the read-only snapshot adapter reuse the existing channel
// scanners, including intervals/time-pricing/account-stat rules, without a
// second hand-maintained projection. Constructors still accept *sql.DB.
type channelDB interface {
	dbExec
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}
type channelSnapshotDB struct{ *sql.Tx }

func (channelSnapshotDB) BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error) {
	return nil, fmt.Errorf("nested transaction in read-only channel snapshot")
}

var _ service.ResultCacheChannelSnapshotRepository = (*channelRepository)(nil)

// LoadResultCacheSnapshot ties all data to the SAME authority snapshot; policy
// v3 SQL/material remains unchanged. It is not a shared cache refresh.
func (r *channelRepository) LoadResultCacheSnapshot(ctx context.Context, groupID int64) ([]service.Channel, map[int64]string, service.ResultCachePolicy, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, nil, service.ResultCachePolicy{}, fmt.Errorf("begin channel snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	policy, err := readResultCachePolicy(ctx, tx, groupID)
	if err != nil {
		return nil, nil, service.ResultCachePolicy{}, fmt.Errorf("read channel snapshot authority: %w", err)
	}
	reader := &channelRepository{db: channelSnapshotDB{tx}}
	channels, err := reader.ListAll(ctx)
	if err != nil {
		return nil, nil, service.ResultCachePolicy{}, err
	}
	var groupIDs []int64
	for i := range channels {
		groupIDs = append(groupIDs, channels[i].GroupIDs...)
	}
	platforms, err := reader.GetGroupPlatforms(ctx, groupIDs)
	if err != nil {
		return nil, nil, service.ResultCachePolicy{}, err
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, service.ResultCachePolicy{}, fmt.Errorf("commit channel snapshot: %w", err)
	}
	return channels, platforms, policy, nil
}
