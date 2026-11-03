package service

import (
	"encoding/json"
	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestPluginInstanceBindingValidation(t *testing.T) {
	m := testPluginGeneralManifest()
	b := PluginGroupBinding{InstanceID: 11, GroupID: 7, Stage: contract.Input, Entry: "inspect", Protocol: "openai.chat_completions.v1", Enabled: true, CanReplace: true}
	require.NoError(t, ValidatePluginGroupBindings(m, 11, []PluginGroupBinding{b}))
	for _, edit := range []func(*PluginGroupBinding){func(b *PluginGroupBinding) { b.InstanceID = 12 }, func(b *PluginGroupBinding) { b.GroupID = 0 }, func(b *PluginGroupBinding) { b.Stage = "other" }, func(b *PluginGroupBinding) { b.Entry = "missing" }, func(b *PluginGroupBinding) { b.Protocol = "unknown" }} {
		c := b
		edit(&c)
		require.ErrorIs(t, ValidatePluginGroupBindings(m, 11, []PluginGroupBinding{c}), ErrPluginInstanceInvalid)
	}
	require.ErrorIs(t, ValidatePluginGroupBindings(m, 11, []PluginGroupBinding{b, b}), ErrPluginInstanceInvalid)
	m.General.Hooks[0].CanReplace = false
	require.ErrorIs(t, ValidatePluginGroupBindings(m, 11, []PluginGroupBinding{b}), ErrPluginInstanceInvalid)
	b.CanReplace = false
	require.NoError(t, ValidatePluginGroupBindings(m, 11, []PluginGroupBinding{b}))
}
func TestPluginInstanceSnapshotOwnershipAndProjection(t *testing.T) {
	s := PluginInstanceSnapshot{Instance: PluginInstance{ID: 11, PluginID: 3}, Revision: PluginInstanceRevision{InstanceID: 11, Revision: 1, ConfigEncrypted: "secret"}, Installation: PluginInstallation{Manifest: testPluginGeneralManifest()}, Bindings: []PluginGroupBinding{{InstanceID: 11, GroupID: 7}}}
	c, err := clonePluginInstanceSnapshot(&s)
	require.NoError(t, err)
	c.Installation.Manifest.General.Hooks[0].Protocols[0] = "edited"
	c.Bindings[0].GroupID = 99
	require.NotEqual(t, "edited", s.Installation.Manifest.General.Hooks[0].Protocols[0])
	require.EqualValues(t, 7, s.Bindings[0].GroupID)
	data, err := json.Marshal(s)
	require.NoError(t, err)
	require.NotContains(t, string(data), "secret")
	require.NotContains(t, string(data), "config_encrypted")
}
