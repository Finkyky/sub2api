package admin

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestProjectPluginInstanceOmitsEncryptedConfiguration(t *testing.T) {
	snapshot := &service.PluginInstanceSnapshot{
		Instance:     service.PluginInstance{ID: 11, PluginID: 3, Name: "check", Generation: 4, CurrentRevision: 2, LatestRevision: 2, Enabled: true},
		Revision:     service.PluginInstanceRevision{InstanceID: 11, Revision: 2, ConfigVersion: 1, ConfigEncrypted: "ciphertext-secret"},
		Installation: service.PluginInstallation{Manifest: service.PluginManifest{SchemaVersion: 2, General: &service.PluginGeneralManifest{ConfigVersion: 1}}},
	}
	data, err := json.Marshal(projectPluginInstance(snapshot))
	require.NoError(t, err)
	require.NotContains(t, string(data), "ciphertext-secret")
	require.NotContains(t, string(data), "config_encrypted")
	require.Contains(t, string(data), `"current_revision":2`)
}
