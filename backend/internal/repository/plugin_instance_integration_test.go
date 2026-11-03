//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
	"github.com/stretchr/testify/require"
)

func piIntegrationInstallation(t *testing.T) *service.PluginInstallation {
	t.Helper()
	key := fmt.Sprintf("com.example.p2c.%d", time.Now().UnixNano())
	hash := strings.Repeat("a", 64)
	m := service.PluginManifest{SchemaVersion: 2, ID: key, Name: "P2c fixture", Version: "0.1.0", Requires: service.PluginRequirements{Sub2API: ">=0.1.0", PluginProtocol: 1, GeneralAPI: 1}, Capabilities: []service.PluginCapability{{ID: service.PluginCapabilityRequestHook}, {ID: service.PluginCapabilityIndependentTask}}, Files: map[string]string{"bin/plugin": hash}, General: &service.PluginGeneralManifest{ConfigVersion: 1, Hooks: []service.PluginHookEntry{{Name: "inspect", Stages: []contract.Stage{contract.Input, contract.Output}, Protocols: []string{"example.text.v1", "other.text.v1"}, CanReplace: true}}, Tasks: []service.PluginTaskEntry{{Name: "sync_prices"}}}}
	m.Runtimes = map[string]service.PluginRuntime{m.RuntimeKey(): {Path: "bin/plugin"}}
	require.NoError(t, m.Validate())
	p, e := NewPluginRepository(integrationDB).Install(context.Background(), &service.PluginInstallation{PluginKey: key, Name: m.Name, Version: m.Version, Manifest: m, BinarySHA256: hash, SignatureStatus: service.PluginSignatureUnsigned, State: service.PluginStateIncompatible}, nil)
	require.NoError(t, e)
	return p
}
func piIntegrationGroup(t *testing.T) int64 {
	t.Helper()
	var id int64
	e := integrationDB.QueryRow(`INSERT INTO groups(name,platform) VALUES($1,'openai') RETURNING id`, fmt.Sprintf("p2c-group-%d", time.Now().UnixNano())).Scan(&id)
	require.NoError(t, e)
	return id
}
func TestPluginInstanceRoundTripIntegration(t *testing.T) {
	ctx := context.Background()
	p := piIntegrationInstallation(t)
	r := NewPluginInstanceRepository(integrationDB)
	s, e := r.Create(ctx, p.ID, "alpha instance", "ciphertext-alpha")
	require.NoError(t, e)
	require.False(t, s.Instance.Enabled)
	require.EqualValues(t, 1, s.Instance.Generation)
	require.EqualValues(t, 1, s.Revision.Revision)
	id := s.Instance.ID
	draft, e := r.SaveRevision(ctx, id, 1, "ciphertext-beta")
	require.NoError(t, e)
	require.EqualValues(t, 2, draft.Revision.Revision)
	require.EqualValues(t, 1, draft.Instance.CurrentRevision)
	alpha, e := r.Snapshot(ctx, id, 1)
	require.NoError(t, e)
	require.Equal(t, "ciphertext-alpha", alpha.Revision.ConfigEncrypted)
	published, e := r.Publish(ctx, id, draft.Instance.Generation, 2, true)
	require.NoError(t, e)
	require.EqualValues(t, 2, published.Instance.CurrentRevision)
	groupID := piIntegrationGroup(t)
	binding := service.PluginGroupBinding{InstanceID: id, GroupID: groupID, Stage: contract.Input, Entry: "inspect", Protocol: "example.text.v1", Enabled: true, CanReplace: true, Order: 8}
	bound, e := r.ReplaceBindings(ctx, id, published.Instance.Generation, []service.PluginGroupBinding{binding})
	require.NoError(t, e)
	require.Len(t, bound.Bindings, 1)
	require.Positive(t, bound.Bindings[0].ID)
	selected, e := r.GroupSnapshots(ctx, groupID, contract.Input, "example.text.v1")
	require.NoError(t, e)
	require.Len(t, selected, 1)
	require.True(t, selected[0].Instance.Enabled)
	require.EqualValues(t, 2, selected[0].Revision.Revision)
	empty, e := r.GroupSnapshots(ctx, groupID+1000000, contract.Input, "example.text.v1")
	require.NoError(t, e)
	require.Empty(t, empty)
	_, e = r.GroupSnapshots(ctx, groupID, contract.Input, "unsupported.protocol.v1")
	require.ErrorIs(t, e, service.ErrPluginInstanceUnavailable)
	back, e := r.Publish(ctx, id, bound.Instance.Generation, 1, true)
	require.NoError(t, e)
	require.EqualValues(t, 1, back.Instance.CurrentRevision)
	require.Greater(t, back.Instance.Generation, bound.Instance.Generation)
	disabled, e := r.Publish(ctx, id, back.Instance.Generation, 1, false)
	require.NoError(t, e)
	require.False(t, disabled.Instance.Enabled)
	selected, e = r.GroupSnapshots(ctx, groupID, contract.Input, "example.text.v1")
	require.NoError(t, e)
	require.Len(t, selected, 1)
	require.False(t, selected[0].Instance.Enabled, "disabled instance must remain visible behind its live binding")
	data, e := json.Marshal(selected)
	require.NoError(t, e)
	require.NotContains(t, string(data), "ciphertext")
	t.Log("DB_ROUNDTRIP draft=2 published=2 rollback=1 disabled_binding_visible=true")
}
func TestPluginInstanceConcurrentPublicationIntegration(t *testing.T) {
	ctx := context.Background()
	p := piIntegrationInstallation(t)
	r := NewPluginInstanceRepository(integrationDB)
	s, e := r.Create(ctx, p.ID, "concurrent", "cipher-alpha")
	require.NoError(t, e)
	draft, e := r.SaveRevision(ctx, s.Instance.ID, 1, "cipher-beta")
	require.NoError(t, e)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, rev := range []int64{1, 2} {
		wg.Add(1)
		go func(rev int64) {
			defer wg.Done()
			<-start
			_, e := r.Publish(ctx, s.Instance.ID, draft.Instance.Generation, rev, true)
			results <- e
		}(rev)
	}
	close(start)
	wg.Wait()
	close(results)
	ok, conflict := 0, 0
	for e := range results {
		if e == nil {
			ok++
		} else {
			require.ErrorIs(t, e, service.ErrPluginInstanceConflict)
			conflict++
		}
	}
	require.Equal(t, 1, ok)
	require.Equal(t, 1, conflict)
	t.Log("DB_CAS success=1 conflict=1")
}
func TestPluginInstanceFreezesInstallationIntegration(t *testing.T) {
	ctx := context.Background()
	p := piIntegrationInstallation(t)
	r := NewPluginInstanceRepository(integrationDB)
	_, e := r.Create(ctx, p.ID, "frozen", "cipher-alpha")
	require.NoError(t, e)
	replacement := *p
	replacement.Version = "0.2.0"
	replacement.Manifest.Version = "0.2.0"
	_, e = NewPluginRepository(integrationDB).Install(ctx, &replacement, nil)
	require.ErrorIs(t, e, service.ErrPluginStateChanged)
	e = NewPluginRepository(integrationDB).Delete(ctx, p.ID, p.BinarySHA256)
	require.ErrorIs(t, e, service.ErrPluginStateChanged)
	stored, e := NewPluginRepository(integrationDB).GetByID(ctx, p.ID)
	require.NoError(t, e)
	require.Equal(t, "0.1.0", stored.Version)
	t.Log("DB_INSTALLATION upgrade=rejected delete=rejected version=0.1.0")
}
func TestPluginInstanceMigrationIdempotentIntegration(t *testing.T) {
	ctx := context.Background()
	tx, e := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, e)
	defer tx.Rollback()
	b, e := migrations.FS.ReadFile("253_general_plugin_instances.sql")
	require.NoError(t, e)
	for i := 0; i < 2; i++ {
		_, e = tx.ExecContext(ctx, string(b))
		require.NoError(t, e)
	}
	var count int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_constraint WHERE conname='sub2api_plugin_instances_current_revision_fk' AND conrelid='sub2api_plugin_instances'::regclass`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestPluginInstanceEncryptedConfigIntegration(t *testing.T) {
	ctx := context.Background()
	p := piIntegrationInstallation(t)
	r := NewPluginInstanceRepository(integrationDB)
	cfg := &config.Config{}
	cfg.Totp.EncryptionKey = strings.Repeat("b", 64)
	cipher, e := NewAESEncryptor(cfg)
	require.NoError(t, e)
	controller := service.NewPluginInstanceService(r, cipher, nil, time.Second)
	defer controller.Close()
	plain := json.RawMessage(`{"api_key":"fixture-secret","rule":"alpha"}`)
	first, e := controller.Create(ctx, p.ID, "encrypted", plain)
	require.NoError(t, e)
	require.NotContains(t, first.Revision.ConfigEncrypted, "fixture-secret")
	decrypted, e := cipher.Decrypt(first.Revision.ConfigEncrypted)
	require.NoError(t, e)
	require.JSONEq(t, string(plain), decrypted)
	draft, e := controller.SaveConfig(ctx, first.Instance.ID, 1, plain)
	require.NoError(t, e)
	require.NotEqual(t, first.Revision.ConfigEncrypted, draft.Revision.ConfigEncrypted)
	var stored string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT config_encrypted FROM sub2api_plugin_instance_revisions WHERE instance_id=$1 AND revision=2`, first.Instance.ID).Scan(&stored))
	require.Equal(t, draft.Revision.ConfigEncrypted, stored)
	require.NotContains(t, stored, "fixture-secret")
	data, e := json.Marshal(draft)
	require.NoError(t, e)
	require.NotContains(t, string(data), "fixture-secret")
	require.NotContains(t, string(data), stored)
	t.Log("DB_CONFIG encrypted_at_rest=true random_nonce=true response_secrets=false")
}

func TestPluginInstanceProtocolCoveragePerStepIntegration(t *testing.T) {
	ctx := context.Background()
	r := NewPluginInstanceRepository(integrationDB)
	groupID := piIntegrationGroup(t)
	a, e := r.Create(ctx, piIntegrationInstallation(t).ID, "protocol-a", "cipher-a")
	require.NoError(t, e)
	a, e = r.Publish(ctx, a.Instance.ID, a.Instance.Generation, 1, true)
	require.NoError(t, e)
	b, e := r.Create(ctx, piIntegrationInstallation(t).ID, "protocol-b", "cipher-b")
	require.NoError(t, e)
	bind := func(id int64, protocol string) service.PluginGroupBinding {
		return service.PluginGroupBinding{InstanceID: id, GroupID: groupID, Stage: contract.Input, Entry: "inspect", Protocol: protocol, Enabled: true}
	}
	// Two protocols of one entry are alternatives, not two required calls.
	a, e = r.ReplaceBindings(ctx, a.Instance.ID, a.Instance.Generation, []service.PluginGroupBinding{bind(a.Instance.ID, "example.text.v1"), bind(a.Instance.ID, "other.text.v1")})
	require.NoError(t, e)
	selected, e := r.GroupSnapshots(ctx, groupID, contract.Input, "example.text.v1")
	require.NoError(t, e)
	require.Len(t, selected, 1)
	require.Len(t, selected[0].Bindings, 1)
	b, e = r.ReplaceBindings(ctx, b.Instance.ID, b.Instance.Generation, []service.PluginGroupBinding{bind(b.Instance.ID, "other.text.v1")})
	require.NoError(t, e)
	// B is disabled, but its enabled binding must not disappear just because A matches.
	_, e = r.GroupSnapshots(ctx, groupID, contract.Input, "example.text.v1")
	require.ErrorIs(t, e, service.ErrPluginInstanceUnavailable)
	b, e = r.Publish(ctx, b.Instance.ID, b.Instance.Generation, 1, true)
	require.NoError(t, e)
	_, e = r.GroupSnapshots(ctx, groupID, contract.Input, "example.text.v1")
	require.ErrorIs(t, e, service.ErrPluginInstanceUnavailable)
	t.Log("DB_PROTOCOL alternate_protocols=accepted missing_bound_step=rejected")
}
