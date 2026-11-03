package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
	"github.com/stretchr/testify/require"
)

func testPluginGeneralManifest() PluginManifest {
	m := testPluginManifest(nil)
	m.SchemaVersion = 2
	m.ID = "com.example.general"
	m.Requires.TransportAPI = 0
	m.Requires.GeneralAPI = PluginGeneralAPIVersion
	m.Requires.UIBridge = 0
	m.UI = PluginUIManifest{}
	delete(m.Files, "ui/index.html")
	m.Capabilities = []PluginCapability{{ID: PluginCapabilityRequestHook}, {ID: PluginCapabilityIndependentTask}}
	m.General = &PluginGeneralManifest{
		ConfigVersion:    1,
		Hooks:            []PluginHookEntry{{Name: "inspect", Stages: []contract.Stage{contract.Input, contract.Output}, Protocols: []string{"openai.chat_completions.v1"}, CanReplace: true}},
		Tasks:            []PluginTaskEntry{{Name: "sync_prices"}},
		HostCapabilities: []string{"channels.pricing.write", "storage.kv.read"},
	}
	return m
}

func TestPluginGeneralManifestValidModes(t *testing.T) {
	for _, mode := range []string{"mixed", "hook", "task", "ui"} {
		t.Run(mode, func(t *testing.T) {
			m := testPluginGeneralManifest()
			switch mode {
			case "hook":
				m.Capabilities = m.Capabilities[:1]
				m.General.Tasks = nil
			case "task":
				m.Capabilities = m.Capabilities[1:]
				m.General.Hooks = nil
			case "ui":
				legacy := testPluginManifest(nil)
				m.UI = legacy.UI
				m.Requires.UIBridge = legacy.Requires.UIBridge
				m.Files[legacy.UI.Entrypoint] = legacy.Files[legacy.UI.Entrypoint]
			}
			require.NoError(t, m.Validate())
			raw, err := m.MarshalJSONBytes()
			require.NoError(t, err)
			var restored PluginManifest
			require.NoError(t, json.Unmarshal(raw, &restored))
			require.NoError(t, restored.Validate())
			require.Equal(t, m, restored)
		})
	}
}

func TestPluginGeneralManifestRejectsInvalidDeclarations(t *testing.T) {
	tests := []struct {
		name string
		edit func(*PluginManifest)
	}{
		{"missing general", func(m *PluginManifest) { m.General = nil }},
		{"missing general api", func(m *PluginManifest) { m.Requires.GeneralAPI = 0 }},
		{"future general api", func(m *PluginManifest) { m.Requires.GeneralAPI = 2 }},
		{"transport mixed", func(m *PluginManifest) { m.Requires.TransportAPI = 1 }},
		{"protocol mismatch", func(m *PluginManifest) { m.Requires.PluginProtocol = 2 }},
		{"headless bridge", func(m *PluginManifest) { m.Requires.UIBridge = 1 }},
		{"config zero", func(m *PluginManifest) { m.General.ConfigVersion = 0 }},
		{"config negative", func(m *PluginManifest) { m.General.ConfigVersion = -1 }},
		{"no capability", func(m *PluginManifest) { m.Capabilities = nil }},
		{"unknown capability", func(m *PluginManifest) { m.Capabilities[0].ID = "business.detector.v1" }},
		{"transport capability", func(m *PluginManifest) { m.Capabilities[0].ID = PluginCapabilityOpenAIOAuthOutbound }},
		{"duplicate capability", func(m *PluginManifest) { m.Capabilities[1] = m.Capabilities[0] }},
		{"platform mixed", func(m *PluginManifest) { m.Capabilities[0].Platform = PlatformOpenAI }},
		{"account mixed", func(m *PluginManifest) { m.Capabilities[0].AccountType = AccountTypeOAuth }},
		{"missing hooks", func(m *PluginManifest) { m.General.Hooks = nil }},
		{"missing tasks", func(m *PluginManifest) { m.General.Tasks = nil }},
		{"undeclared task", func(m *PluginManifest) { m.Capabilities = m.Capabilities[:1] }},
		{"undeclared hook", func(m *PluginManifest) { m.Capabilities = m.Capabilities[1:] }},
		{"duplicate hook", func(m *PluginManifest) { m.General.Hooks = append(m.General.Hooks, m.General.Hooks[0]) }},
		{"duplicate task", func(m *PluginManifest) { m.General.Tasks = append(m.General.Tasks, m.General.Tasks[0]) }},
		{"cross mode name", func(m *PluginManifest) { m.General.Tasks[0].Name = m.General.Hooks[0].Name }},
		{"empty entry", func(m *PluginManifest) { m.General.Hooks[0].Name = "" }},
		{"invalid entry", func(m *PluginManifest) { m.General.Tasks[0].Name = "Bad Name" }},
		{"long entry", func(m *PluginManifest) { m.General.Tasks[0].Name = strings.Repeat("a", 65) }},
		{"missing stages", func(m *PluginManifest) { m.General.Hooks[0].Stages = nil }},
		{"unknown stage", func(m *PluginManifest) { m.General.Hooks[0].Stages = []contract.Stage{"task"} }},
		{"duplicate stage", func(m *PluginManifest) { m.General.Hooks[0].Stages = []contract.Stage{contract.Input, contract.Input} }},
		{"missing protocols", func(m *PluginManifest) { m.General.Hooks[0].Protocols = nil }},
		{"bad protocol", func(m *PluginManifest) { m.General.Hooks[0].Protocols = []string{"*"} }},
		{"duplicate protocol", func(m *PluginManifest) {
			m.General.Hooks[0].Protocols = []string{"openai.responses.v1", "openai.responses.v1"}
		}},
		{"long protocol", func(m *PluginManifest) { m.General.Hooks[0].Protocols = []string{"a." + strings.Repeat("a", 159)} }},
		{"bad host capability", func(m *PluginManifest) { m.General.HostCapabilities = []string{"*"} }},
		{"duplicate host capability", func(m *PluginManifest) { m.General.HostCapabilities = []string{"storage.kv.read", "storage.kv.read"} }},
		{"long host capability", func(m *PluginManifest) { m.General.HostCapabilities = []string{"a." + strings.Repeat("a", 159)} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { m := testPluginGeneralManifest(); tt.edit(&m); require.Error(t, m.Validate()) })
	}
}

func TestPluginGeneralManifestLimits(t *testing.T) {
	m := testPluginGeneralManifest()
	m.General.Hooks[0].Name = strings.Repeat("a", 64)
	m.General.HostCapabilities = nil
	for i := 0; i < 32; i++ {
		m.General.HostCapabilities = append(m.General.HostCapabilities, fmt.Sprintf("cap.v%d", i))
	}
	require.NoError(t, m.Validate())
	m.General.HostCapabilities = append(m.General.HostCapabilities, "cap.extra")
	require.Error(t, m.Validate())
	m = testPluginGeneralManifest()
	m.General.Hooks = nil
	for i := 0; i < 32; i++ {
		m.General.Hooks = append(m.General.Hooks, PluginHookEntry{Name: fmt.Sprintf("hook_%d", i), Stages: []contract.Stage{contract.Input}, Protocols: []string{"openai.responses.v1"}})
	}
	require.NoError(t, m.Validate())
	m.General.Hooks = append(m.General.Hooks, PluginHookEntry{Name: "extra", Stages: []contract.Stage{contract.Input}, Protocols: []string{"openai.responses.v1"}})
	require.Error(t, m.Validate())
	m = testPluginGeneralManifest()
	m.General.Tasks = nil
	for i := 0; i < 32; i++ {
		m.General.Tasks = append(m.General.Tasks, PluginTaskEntry{Name: fmt.Sprintf("task_%d", i)})
	}
	require.NoError(t, m.Validate())
	m.General.Tasks = append(m.General.Tasks, PluginTaskEntry{Name: "extra"})
	require.Error(t, m.Validate())
	m = testPluginGeneralManifest()
	m.General.Hooks[0].Protocols = nil
	for i := 0; i < 16; i++ {
		m.General.Hooks[0].Protocols = append(m.General.Hooks[0].Protocols, fmt.Sprintf("protocol.v%d", i))
	}
	require.NoError(t, m.Validate())
	m.General.Hooks[0].Protocols = append(m.General.Hooks[0].Protocols, "protocol.extra")
	require.Error(t, m.Validate())
}

func TestPluginGeneralManifestDoesNotDowngradeToLegacy(t *testing.T) {
	legacy := testPluginManifest(nil)
	require.NoError(t, legacy.Validate())
	raw, err := json.Marshal(legacy)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "general_api")
	require.NotContains(t, string(raw), "\"general\"")
	legacy.General = testPluginGeneralManifest().General
	require.Error(t, legacy.Validate())
	legacy.General = nil
	legacy.Requires.GeneralAPI = 1
	require.Error(t, legacy.Validate())
	legacy = testPluginManifest(nil)
	legacy.SchemaVersion = 3
	require.Error(t, legacy.Validate())
}

func TestPluginGeneralCompatibilityNeverAdvertisesRuntimeSupport(t *testing.T) {
	m := testPluginGeneralManifest()
	result := EvaluatePluginCompatibility(m, PluginHostInfo{Version: "0.1.179"})
	require.False(t, result.Compatible)
	require.Equal(t, "incompatible", result.Status)
	require.Equal(t, "general_runtime_unavailable", result.ReasonCode)
	require.Equal(t, PluginGeneralAPIVersion, result.GeneralAPI)
	m.General = nil
	result = EvaluatePluginCompatibility(m, PluginHostInfo{Version: "0.1.179"})
	require.False(t, result.Compatible)
	require.Equal(t, "invalid_manifest", result.ReasonCode)
	m = testPluginManifest(nil)
	m.SchemaVersion = 3
	result = EvaluatePluginCompatibility(m, PluginHostInfo{Version: "0.1.179"})
	require.False(t, result.Compatible)
}

func TestPluginGeneralUIAndHostVersionBoundaries(t *testing.T) {
	for _, name := range []string{"missing hash", "outside ui", "bridge mismatch"} {
		t.Run(name, func(t *testing.T) {
			m := testPluginGeneralManifest()
			legacy := testPluginManifest(nil)
			m.UI = legacy.UI
			m.Requires.UIBridge = legacy.Requires.UIBridge
			m.Files[legacy.UI.Entrypoint] = legacy.Files[legacy.UI.Entrypoint]
			switch name {
			case "missing hash":
				delete(m.Files, m.UI.Entrypoint)
			case "outside ui":
				m.UI.Entrypoint = "bin/plugin"
			case "bridge mismatch":
				m.Requires.UIBridge = 0
			}
			require.Error(t, m.Validate())
		})
	}
	m := testPluginGeneralManifest()
	result := EvaluatePluginCompatibility(m, PluginHostInfo{Version: "0.2.0"})
	require.False(t, result.Compatible)
	require.Equal(t, "host_version_mismatch", result.ReasonCode)
	legacy := testPluginManifest(nil)
	legacy.Requires.GeneralAPI = 1
	require.False(t, EvaluatePluginCompatibility(legacy, PluginHostInfo{Version: "0.1.179"}).Compatible)
}
