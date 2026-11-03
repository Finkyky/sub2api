package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
	"time"
)

var (
	ErrPluginTaskInvalid    = errors.New("invalid plugin task")
	ErrPluginTaskNotFound   = errors.New("plugin task not found")
	ErrPluginTaskConflict   = errors.New("plugin task generation or lease changed")
	ErrPluginTaskDisabled   = errors.New("plugin task or instance disabled")
	ErrPluginTaskCapability = errors.New("plugin task capability denied")
)

type PluginTaskScope struct {
	ChannelIDs    []int64  `json:"channel_ids"`
	PricingIDs    []int64  `json:"pricing_ids,omitempty"`
	PricingFields []string `json:"pricing_fields"`
	NetworkURLs   []string `json:"network_urls"`
	KVKeys        []string `json:"kv_keys"`
}
type PluginTask struct {
	ID                  int64           `json:"id"`
	InstanceID          int64           `json:"instance_id"`
	Name                string          `json:"name"`
	Entry               string          `json:"entry"`
	Cron                string          `json:"cron"`
	Enabled             bool            `json:"enabled"`
	TimeoutSeconds      int             `json:"timeout_seconds"`
	MaxRetries          int             `json:"max_retries"`
	RetryBackoffSeconds int             `json:"retry_backoff_seconds"`
	Generation          int64           `json:"generation"`
	Input               json.RawMessage `json:"input"`
	Scope               PluginTaskScope `json:"scope"`
	NextRunAt           *time.Time      `json:"next_run_at,omitempty"`
}
type PluginTaskRun struct {
	ID              int64      `json:"id"`
	TaskID          int64      `json:"task_id"`
	InstanceID      int64      `json:"instance_id"`
	ConfigRevision  int64      `json:"config_revision"`
	TaskGeneration  int64      `json:"task_generation"`
	Trigger         string     `json:"trigger"`
	IdempotencyKey  string     `json:"idempotency_key"`
	Attempt         int        `json:"attempt"`
	FencingToken    int64      `json:"fencing_token"`
	Status          string     `json:"status"`
	Changed         bool       `json:"changed"`
	ErrorCode       string     `json:"error_code,omitempty"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	DurationMS      int64      `json:"duration_ms"`
	ResultEncrypted string     `json:"-"`
}
type PluginTaskClaim struct {
	Task               PluginTask
	Run                PluginTaskRun
	Owner              string
	LeaseUntil         time.Time
	InstanceGeneration int64
}
type PluginTaskRepository interface {
	List(context.Context, int64) ([]PluginTask, error)
	Get(context.Context, int64) (*PluginTask, error)
	Create(context.Context, *PluginTask) (*PluginTask, error)
	Update(context.Context, *PluginTask, int64) (*PluginTask, error)
	Delete(context.Context, int64, int64) error
	Due(context.Context, int) ([]int64, error)
	Claim(context.Context, int64, string, string, string, time.Duration) (*PluginTaskClaim, error)
	Finish(context.Context, *PluginTaskClaim, PluginTaskRun, func(*sql.Tx) error) error
	Runs(context.Context, int64, int) ([]PluginTaskRun, error)
	Run(context.Context, int64) (*PluginTaskRun, error)
}
type PluginTaskBroker interface {
	Prepare(context.Context, *PluginTaskClaim, *PluginInstanceSnapshot) (json.RawMessage, error)
	Commit(context.Context, *sql.Tx, *PluginTaskClaim, contract.TaskResult) error
	AfterCommit()
}
