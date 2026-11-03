package service

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestPluginGeneralPackageSignedInstall(t *testing.T) {
	for _, withUI := range []bool{false, true} {
		t.Run(map[bool]string{false: "headless", true: "ui"}[withUI], func(t *testing.T) {
			pub, priv, err := ed25519.GenerateKey(rand.Reader)
			require.NoError(t, err)
			cfg := testPluginConfig(t.TempDir(), false)
			cfg.Plugins.TrustedPublishers["general-test"] = base64.StdEncoding.EncodeToString(pub)
			m := testPluginGeneralManifest()
			if withUI {
				legacy := testPluginManifest(nil)
				m.UI = legacy.UI
				m.Requires.UIBridge = legacy.Requires.UIBridge
				m.Files[legacy.UI.Entrypoint] = legacy.Files[legacy.UI.Entrypoint]
			}
			archive := buildPluginArchive(t, m, priv, "general-test", nil)
			installer := NewPluginPackageInstaller(cfg, PluginHostInfo{Version: "0.1.179"})
			installed, err := installer.Install(context.Background(), bytes.NewReader(archive), nil)
			require.NoError(t, err)
			require.Equal(t, PluginSignatureTrusted, installed.SignatureStatus)
			require.Equal(t, PluginStateIncompatible, installed.State)
			require.Equal(t, "general_runtime_unavailable", installed.Compatibility.ReasonCode)
			require.Equal(t, m, installed.Manifest)
			require.FileExists(t, installed.BinaryPath)
			require.FileExists(t, installed.ArtifactPath)
			require.Equal(t, archive, installed.ArtifactData)
			require.Empty(t, installed.Bindings)
		})
	}
}

func TestPluginGeneralPackageSecurityChecksStillApply(t *testing.T) {
	t.Run("unsigned", func(t *testing.T) {
		cfg := testPluginConfig(t.TempDir(), false)
		_, err := NewPluginPackageInstaller(cfg, PluginHostInfo{Version: "0.1.179"}).Install(context.Background(), bytes.NewReader(buildPluginArchive(t, testPluginGeneralManifest(), nil, "", nil)), nil)
		require.Error(t, err)
	})
	t.Run("hash mismatch", func(t *testing.T) {
		m := testPluginGeneralManifest()
		m.Files["bin/plugin"] = strings.Repeat("0", 64)
		_, err := NewPluginPackageInstaller(testPluginConfig(t.TempDir(), true), PluginHostInfo{Version: "0.1.179"}).Install(context.Background(), bytes.NewReader(buildPluginArchive(t, m, nil, "", nil)), nil)
		require.Error(t, err)
	})
	t.Run("wrong signer", func(t *testing.T) {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
		cfg := testPluginConfig(t.TempDir(), false)
		cfg.Plugins.TrustedPublishers["test"] = base64.StdEncoding.EncodeToString(pub)
		_, err = NewPluginPackageInstaller(cfg, PluginHostInfo{Version: "0.1.179"}).Install(context.Background(), bytes.NewReader(buildPluginArchive(t, testPluginGeneralManifest(), priv, "test", nil)), nil)
		require.Error(t, err)
	})
	t.Run("traversal", func(t *testing.T) {
		archive := buildPluginArchive(t, testPluginGeneralManifest(), nil, "", map[string][]byte{"../escape": []byte("x")})
		_, err := NewPluginPackageInstaller(testPluginConfig(t.TempDir(), true), PluginHostInfo{Version: "0.1.179"}).Install(context.Background(), bytes.NewReader(archive), nil)
		require.Error(t, err)
	})
	t.Run("unknown declaration field", func(t *testing.T) {
		m := testPluginGeneralManifest()
		raw, err := json.Marshal(m)
		require.NoError(t, err)
		raw = bytes.Replace(raw, []byte(`"general":{`), []byte(`"general":{"grant_all":true,`), 1)
		var buf bytes.Buffer
		writer := zip.NewWriter(&buf)
		writeZipEntry(t, writer, pluginManifestFilename, raw)
		writeZipEntry(t, writer, "bin/plugin", []byte("binary"))
		require.NoError(t, writer.Close())
		_, err = NewPluginPackageInstaller(testPluginConfig(t.TempDir(), true), PluginHostInfo{Version: "0.1.179"}).Install(context.Background(), bytes.NewReader(buf.Bytes()), nil)
		require.ErrorContains(t, err, "unknown field")
	})
}

type pluginGeneralInstallRepository struct {
	PluginRepository
	stored            *PluginInstallation
	installedBindings []PluginBinding
	stateUpdates      int
	beginCalls        int
}

func (r *pluginGeneralInstallRepository) GetByKey(context.Context, string) (*PluginInstallation, error) {
	return nil, sql.ErrNoRows
}
func (r *pluginGeneralInstallRepository) Install(_ context.Context, p *PluginInstallation, bindings []PluginBinding) (*PluginInstallation, error) {
	copy := *p
	copy.ID = 77
	copy.Bindings = append([]PluginBinding(nil), bindings...)
	r.stored = &copy
	r.installedBindings = append([]PluginBinding(nil), bindings...)
	return r.GetByID(context.Background(), 77)
}
func (r *pluginGeneralInstallRepository) GetByID(context.Context, int64) (*PluginInstallation, error) {
	if r.stored == nil {
		return nil, sql.ErrNoRows
	}
	copy := *r.stored
	return &copy, nil
}
func (r *pluginGeneralInstallRepository) UpdateState(_ context.Context, _ int64, state, lastError string, _ *time.Time, _, _ string) error {
	r.stateUpdates++
	r.stored.State = state
	r.stored.LastError = lastError
	return nil
}
func (r *pluginGeneralInstallRepository) BeginEnable(context.Context, int64, string, string) error {
	r.beginCalls++
	return errors.New("unexpected transport enable")
}

func TestPluginGeneralManagerInstallNeverCreatesTransportBindings(t *testing.T) {
	for _, general := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "general"}[general], func(t *testing.T) {
			repo := &pluginGeneralInstallRepository{}
			manager := NewPluginManager(repo, pluginTokenEncryptor{}, testPluginConfig(t.TempDir(), true), PluginHostInfo{Version: "0.1.179"}, nil)
			m := testPluginManifest(nil)
			if general {
				m = testPluginGeneralManifest()
			}
			installed, err := manager.Install(context.Background(), bytes.NewReader(buildPluginArchive(t, m, nil, "", nil)), nil)
			require.NoError(t, err)
			if general {
				require.Empty(t, repo.installedBindings)
				require.Empty(t, installed.Bindings)
				require.Equal(t, PluginStateIncompatible, installed.State)
				require.Equal(t, "general_runtime_unavailable", installed.Compatibility.ReasonCode)
				enabled, enableErr := manager.Enable(context.Background(), installed.ID, true, 100)
				require.Nil(t, enabled)
				require.ErrorContains(t, enableErr, ErrPluginGeneralRuntimeUnavailable.Error())
				testResult, testErr := manager.Test(context.Background(), installed.ID)
				require.Nil(t, testResult)
				require.ErrorIs(t, testErr, ErrPluginGeneralRuntimeUnavailable)
				configResult, configErr := manager.SaveConfig(context.Background(), installed.ID, json.RawMessage(`{"threshold":0.9}`))
				require.Nil(t, configResult)
				require.ErrorIs(t, configErr, ErrPluginGeneralRuntimeUnavailable)
				require.Empty(t, manager.runtimes)
				require.False(t, manager.ShouldRouteOpenAIOAuth(&Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}))
				require.Equal(t, 0, repo.beginCalls)
				require.Equal(t, 1, repo.stateUpdates)
			} else {
				require.Len(t, repo.installedBindings, 1)
				require.Equal(t, PluginCapabilityOpenAIOAuthOutbound, repo.installedBindings[0].Capability)
				require.Equal(t, PlatformOpenAI, repo.installedBindings[0].Platform)
				require.Equal(t, AccountTypeOAuth, repo.installedBindings[0].AccountType)
				require.False(t, repo.installedBindings[0].Enabled)
				require.Equal(t, 100, repo.installedBindings[0].RolloutPercent)
				require.Equal(t, PluginStateDisabled, installed.State)
			}
		})
	}
}

func TestPluginGeneralRuntimeRejectedBeforeBinaryOrProcessAccess(t *testing.T) {
	installation := &PluginInstallation{Manifest: testPluginGeneralManifest(), BinaryPath: "must-not-execute", BinarySHA256: "invalid"}
	runtime, err := startPluginRuntime(context.Background(), installation, time.Second, t.TempDir(), nil)
	require.Nil(t, runtime)
	require.ErrorIs(t, err, ErrPluginGeneralRuntimeUnavailable)
}

// Counts every transport RPC so stale-cache regressions prove zero dispatch,
// rather than merely observing an eventual error from the old process.
type pluginGeneralCountingClient struct {
	normalizingPluginClient
	calls int
}

func (c *pluginGeneralCountingClient) ValidateConfig(ctx context.Context, req *pluginv1.ValidateConfigRequest, opts ...grpc.CallOption) (*pluginv1.ValidateConfigResponse, error) {
	c.calls++
	return c.normalizingPluginClient.ValidateConfig(ctx, req, opts...)
}
func (c *pluginGeneralCountingClient) ApplyConfig(ctx context.Context, req *pluginv1.ApplyConfigRequest, opts ...grpc.CallOption) (*pluginv1.ApplyConfigResponse, error) {
	c.calls++
	return c.normalizingPluginClient.ApplyConfig(ctx, req, opts...)
}
func (c *pluginGeneralCountingClient) TestConfig(context.Context, *pluginv1.TestConfigRequest, ...grpc.CallOption) (*pluginv1.TestConfigResponse, error) {
	c.calls++
	return &pluginv1.TestConfigResponse{Success: true}, nil
}

func TestPluginGeneralManagerRejectsStaleCachedTransport(t *testing.T) {
	for _, kind := range []string{"general", "changed transport package"} {
		for _, operation := range []string{"save", "test"} {
			t.Run(kind+"/"+operation, func(t *testing.T) {
				current := &PluginInstallation{ID: 9, PluginKey: "com.example.old", Version: "0.1.0", Manifest: testPluginManifest(nil), BinarySHA256: strings.Repeat("a", 64)}
				old := *current
				if kind == "general" {
					current.Manifest = testPluginGeneralManifest()
				} else {
					current.BinarySHA256 = strings.Repeat("b", 64)
				}
				repo := &pluginConfigRepository{installation: current}
				client := &pluginGeneralCountingClient{normalizingPluginClient: normalizingPluginClient{normalized: []byte(`{"normalized":true}`)}}
				manager := &PluginManager{repo: repo, encryptor: pluginTokenEncryptor{}, runtimes: map[int64]*pluginRuntime{9: {installation: &old, api: client}}}
				var err error
				if operation == "save" {
					_, err = manager.SaveConfig(context.Background(), 9, []byte(`{"threshold":0.9}`))
				} else {
					_, err = manager.Test(context.Background(), 9)
				}
				if kind == "general" {
					require.ErrorIs(t, err, ErrPluginGeneralRuntimeUnavailable)
				} else {
					require.ErrorIs(t, err, ErrPluginStateChanged)
				}
				require.Zero(t, client.calls)
				require.Empty(t, repo.encrypted)
				require.Empty(t, client.applied)
			})
		}
	}
}
