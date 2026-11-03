package service

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func TestPluginTaskCronUTC(t *testing.T) {
	after := time.Date(2026, 10, 11, 23, 59, 30, 0, time.FixedZone("other", 8*3600))
	for _, spec := range []string{"* * * * *", "@every 1m", "0 0 * * *", "0 0 1 JAN *"} {
		next, err := PluginTaskNextRun(spec, after)
		require.NoError(t, err, spec)
		require.Equal(t, time.UTC, next.Location())
		require.True(t, next.After(after))
	}
	for _, spec := range []string{"@every 59s", "@every 0s", "@hourly", "CRON_TZ=Asia/Shanghai * * * * *", "* * * * * *", "0 0 30 2 *", "invalid"} {
		_, err := PluginTaskNextRun(spec, after)
		require.ErrorIs(t, err, ErrPluginTaskInvalid, spec)
	}
}
func TestPluginTaskDefinitionValidation(t *testing.T) {
	m := testPluginGeneralManifest()
	m.General.HostCapabilities = nil
	task := PluginTask{InstanceID: 11, Name: "task", Entry: "sync_prices", Cron: "@every 1m", TimeoutSeconds: 30, RetryBackoffSeconds: 1, MaxRetries: 2, Input: json.RawMessage(`{"secret":"input-canary"}`)}
	require.NoError(t, ValidatePluginTask(task, m))
	task.Entry = "undeclared"
	require.ErrorIs(t, ValidatePluginTask(task, m), ErrPluginTaskInvalid)
	task.Entry = "sync_prices"
	task.TimeoutSeconds = 0
	require.ErrorIs(t, ValidatePluginTask(task, m), ErrPluginTaskInvalid)
	task.TimeoutSeconds = 30
	task.Scope.KVKeys = []string{"secret", "secret"}
	require.ErrorIs(t, ValidatePluginTask(task, m), ErrPluginTaskInvalid)
}

func TestPluginTaskDefinitionInputBudgetAndScopeDeclaration(t *testing.T) {
	task := ptTestTask()
	m := testPluginGeneralManifest()
	task.Input = json.RawMessage(`{"data":"` + strings.Repeat("x", 32*1024) + `"}`)
	require.ErrorIs(t, ValidatePluginTask(task, m), ErrPluginTaskInvalid)
	task.Input = json.RawMessage(`{"data":"public"}`)
	task.Scope.KVKeys = []string{"state"}
	require.ErrorIs(t, ValidatePluginTask(task, m), ErrPluginTaskCapability)
	m.General.HostCapabilities = []string{"host.kv.v1"}
	require.NoError(t, ValidatePluginTask(task, m))
	task.Scope.NetworkURLs = []string{"https://example.org/data"}
	require.ErrorIs(t, ValidatePluginTask(task, m), ErrPluginTaskCapability)
	m.General.HostCapabilities = append(m.General.HostCapabilities, "host.network.read.v1")
	require.NoError(t, ValidatePluginTask(task, m))
	task.Scope.NetworkURLs = []string{"http://example.org/data"}
	require.Error(t, ValidatePluginTask(task, m))
	task.Scope.NetworkURLs = []string{"https://example.org/data"}
	task.Scope.ChannelIDs = []int64{1}
	task.Scope.PricingFields = []string{"input_price"}
	require.ErrorIs(t, ValidatePluginTask(task, m), ErrPluginTaskCapability)
	m.General.HostCapabilities = append(m.General.HostCapabilities, "host.channel.pricing.write.v1")
	require.NoError(t, ValidatePluginTask(task, m))
	task.Scope.PricingFields = []string{"secret_config"}
	require.ErrorIs(t, ValidatePluginTask(task, m), ErrPluginTaskCapability)
}
