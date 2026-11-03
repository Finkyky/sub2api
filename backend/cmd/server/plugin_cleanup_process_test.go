package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
	"github.com/stretchr/testify/require"
)

type pluginCleanupCipher struct{}

func (pluginCleanupCipher) Encrypt(value string) (string, error) { return value, nil }
func (pluginCleanupCipher) Decrypt(value string) (string, error) { return value, nil }

type pluginCleanupInstanceRepo struct {
	service.PluginInstanceRepository
	mu           sync.Mutex
	instance     service.PluginInstance
	installation service.PluginInstallation
	configs      map[int64]string
	block        atomic.Bool
	canceled     atomic.Bool
	entered      chan struct{}
	once         sync.Once
}

func (r *pluginCleanupInstanceRepo) Snapshot(ctx context.Context, id, revision int64) (*service.PluginInstanceSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if id != r.instance.ID {
		return nil, service.ErrPluginInstanceNotFound
	}
	if revision == 0 {
		revision = r.instance.CurrentRevision
	}
	encrypted, ok := r.configs[revision]
	if !ok {
		return nil, service.ErrPluginInstanceNotFound
	}
	return &service.PluginInstanceSnapshot{Instance: r.instance, Installation: r.installation, Revision: service.PluginInstanceRevision{InstanceID: id, Revision: revision, ConfigVersion: 1, ConfigEncrypted: encrypted}}, nil
}
func (r *pluginCleanupInstanceRepo) List(ctx context.Context, _ int64) ([]service.PluginInstance, error) {
	if r.block.Load() {
		r.once.Do(func() { close(r.entered) })
		<-ctx.Done()
		r.canceled.Store(true)
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return []service.PluginInstance{r.instance}, nil
}
func (r *pluginCleanupInstanceRepo) Publish(ctx context.Context, id, generation, revision int64, enabled bool) (*service.PluginInstanceSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	if id != r.instance.ID || r.instance.Generation != generation {
		r.mu.Unlock()
		return nil, service.ErrPluginInstanceConflict
	}
	r.instance.Generation++
	r.instance.CurrentRevision = revision
	r.instance.Enabled = enabled
	r.mu.Unlock()
	return r.Snapshot(ctx, id, revision)
}
func pluginCleanupInstallation(t *testing.T, cfg *config.Config) service.PluginInstallation {
	t.Helper()
	cfg.Plugins.DataDir = t.TempDir()
	cfg.Plugins.StartTimeoutSeconds = 10
	cfg.Plugins.InstanceReconcileIntervalSeconds = 1
	cfg.Plugins.InstanceReconcileTimeoutSeconds = 30
	installed := filepath.Join(cfg.Plugins.DataDir, "installed")
	binary := filepath.Join(installed, "bin", "sub2api-lifecycle-fixture.exe")
	require.NoError(t, os.MkdirAll(filepath.Dir(binary), 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(cfg.Plugins.DataDir, "runtime"), 0700))
	output, err := exec.Command("go", "build", "-o", binary, "../../internal/service/testdata/general-plugin").CombinedOutput()
	require.NoError(t, err, string(output))
	data, err := os.ReadFile(binary)
	require.NoError(t, err)
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	manifest := service.PluginManifest{SchemaVersion: 2, ID: "com.example.general", Name: "lifecycle fixture", Version: "0.1.0", Requires: service.PluginRequirements{Sub2API: ">=0.1.0", PluginProtocol: 1, GeneralAPI: 1}, Capabilities: []service.PluginCapability{{ID: service.PluginCapabilityRequestHook}, {ID: service.PluginCapabilityIndependentTask}}, Files: map[string]string{"bin/sub2api-lifecycle-fixture.exe": hash}, General: &service.PluginGeneralManifest{ConfigVersion: 1, Hooks: []service.PluginHookEntry{{Name: "inspect", Stages: []contract.Stage{contract.Input, contract.Output}, Protocols: []string{"example.text.v1"}, CanReplace: true}}, Tasks: []service.PluginTaskEntry{{Name: "sync_prices"}}}}
	manifest.Runtimes = map[string]service.PluginRuntime{manifest.RuntimeKey(): {Path: "bin/sub2api-lifecycle-fixture.exe"}}
	require.NoError(t, manifest.Validate())
	return service.PluginInstallation{ID: 3, PluginKey: manifest.ID, Version: manifest.Version, Manifest: manifest, BinarySHA256: hash, BinaryPath: binary, InstallPath: installed, SignatureStatus: service.PluginSignatureUnsigned, State: service.PluginStateIncompatible}
}
func pluginCleanupProcessIDs(t *testing.T, binary string) []int {
	t.Helper()
	var command *exec.Cmd
	if runtime.GOOS == "windows" {
		literal := "'" + strings.ReplaceAll(binary, "'", "''") + "'"
		command = exec.Command("powershell.exe", "-NoProfile", "-Command", "$ErrorActionPreference = 'Stop'; Get-Process | Where-Object { $_.ProcessName -eq 'sub2api-lifecycle-fixture' -and $_.Path -eq "+literal+" } | Select-Object -ExpandProperty Id")
	} else {
		command = exec.Command("ps", "-eo", "pid=,args=")
	}
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	var ids []int
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if runtime.GOOS != "windows" && (len(fields) < 2 || fields[1] != binary) {
			continue
		}
		id, err := strconv.Atoi(fields[0])
		require.NoError(t, err)
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}
func TestPluginCleanupRealProcesses(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle_worker", true: "cancel_active_cycle"}[active], func(t *testing.T) {
			ctx := context.Background()
			cfg := &config.Config{}
			installation := pluginCleanupInstallation(t, cfg)
			repo := &pluginCleanupInstanceRepo{instance: service.PluginInstance{ID: 11, PluginID: 3, Name: "cleanup", Generation: 1, CurrentRevision: 1, LatestRevision: 2}, installation: installation, configs: map[int64]string{1: `{"marker":"alpha"}`, 2: `{"marker":"beta"}`}, entered: make(chan struct{})}
			manager := service.NewPluginManager(nil, pluginCleanupCipher{}, cfg, service.PluginHostInfo{Version: "0.1.179"}, nil)
			instances := service.ProvidePluginInstanceService(repo, pluginCleanupCipher{}, manager)
			t.Cleanup(func() { instances.Close(); manager.Stop() })
			first, err := instances.Publish(ctx, 11, 1, 1)
			require.NoError(t, err)
			old, err := instances.Acquire(ctx, 11)
			require.NoError(t, err)
			defer old.Release()
			_, err = instances.Publish(ctx, 11, first.Instance.Generation, 2)
			require.NoError(t, err)
			current, err := instances.Acquire(ctx, 11)
			require.NoError(t, err)
			defer current.Release()
			marker := func(lease *service.PluginInstanceLease, want string) {
				t.Helper()
				result, err := lease.RunTask(ctx, "sync_prices", "cleanup", json.RawMessage(`{}`))
				require.NoError(t, err)
				require.JSONEq(t, `{"marker":"`+want+`"}`, string(result.Output))
			}
			marker(old, "alpha")
			marker(current, "beta")
			status, err := instances.Status(ctx, 11)
			require.NoError(t, err)
			require.Len(t, status.Runtimes, 2)
			require.True(t, status.Retired)
			require.Equal(t, 2, status.LeaseRefs)
			before, err := repo.Snapshot(ctx, 11, 0)
			require.NoError(t, err)
			pids := pluginCleanupProcessIDs(t, installation.BinaryPath)
			require.Len(t, pids, 2, "both current and retired children must really exist before shutdown")
			if active {
				repo.block.Store(true)
				select {
				case <-repo.entered:
				case <-time.After(3 * time.Second):
					t.Fatal("reconciler did not enter the active database read")
				}
			}
			cleanup := pluginCleanupFixture(t, instances, manager)
			t.Cleanup(cleanup)
			app := &Application{Cleanup: cleanup, PluginManager: manager}
			done := make(chan struct{})
			began := time.Now()
			go func() { app.Cleanup(); close(done) }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("application cleanup did not cancel and join plugin shutdown")
			}
			cleanupElapsed := time.Since(began)
			app.Cleanup()
			if active {
				require.True(t, repo.canceled.Load(), "cleanup must cancel the actual gate-owning cycle")
			}
			_, err = old.RunTask(ctx, "sync_prices", "old-after-close", json.RawMessage(`{}`))
			require.ErrorIs(t, err, service.ErrGeneralClosed)
			_, err = current.RunTask(ctx, "sync_prices", "new-after-close", json.RawMessage(`{}`))
			require.ErrorIs(t, err, service.ErrGeneralClosed)
			require.ErrorIs(t, instances.Reconcile(ctx), service.ErrGeneralClosed)
			require.ErrorIs(t, instances.StartReconciler(ctx, time.Millisecond, time.Second), service.ErrGeneralClosed)
			after, err := repo.Snapshot(ctx, 11, 0)
			require.NoError(t, err)
			require.Equal(t, before, after, "shutdown is not a durable disable or publication")
			remaining := pluginCleanupProcessIDs(t, installation.BinaryPath)
			require.Empty(t, remaining, "cleanup must wait for OS process exit, not only close lease state")
			t.Logf("REAL_APP_CLEANUP active_cycle=%t before_pids=%v after_pids=%v cleanup_elapsed=%s elapsed_with_probe=%s authority_unchanged=true", active, pids, remaining, cleanupElapsed, time.Since(began))
		})
	}
}
