package examples_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	examples "github.com/Wei-Shaw/sub2api/examples/general-plugins"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	general "github.com/Wei-Shaw/sub2api/pkg/pluginapi/general/v1"
	"github.com/hashicorp/go-hclog"
	hcplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

var packages = flag.String("packages", "", "directory of package.py output; required for real process/install tests")

func TestSourceManifestAndConfigSchema(t *testing.T) {
	for _, name := range examples.Names {
		t.Run(name, func(t *testing.T) {
			raw, e := examples.Source(name, "manifest.source.json")
			if e != nil {
				t.Fatal(e)
			}
			var m service.PluginManifest
			if e = json.Unmarshal(raw, &m); e != nil {
				t.Fatal(e)
			}
			info, e := examples.Info(name)
			if e != nil || info.PluginId != m.ID || info.ConfigVersion != int64(m.General.ConfigVersion) {
				t.Fatalf("%v %v", info, e)
			}
			if m.SchemaVersion != 2 || m.Requires.TransportAPI != 0 || m.Requires.UIBridge != 0 || m.UI.Entrypoint != "" {
				t.Fatal("mixed API")
			}
			for _, file := range []string{"config.schema.json", "config.example.json"} {
				raw, e = examples.Source(name, file)
				if e != nil || !json.Valid(raw) {
					t.Fatal(file, e)
				}
			}
		})
	}
}
func TestPackagedInstallValidateAndRealGRPC(t *testing.T) {
	if *packages == "" {
		t.Skip("set -args -packages=<output directory> to run real installation and process tests")
	}
	for _, name := range examples.Names {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(*packages, name+"-1.0.0.s2plugin")
			data, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			z, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if e != nil {
				t.Fatal(e)
			}
			var m service.PluginManifest
			payload := map[string][]byte{}
			for _, f := range z.File {
				r, e := f.Open()
				if e != nil {
					t.Fatal(e)
				}
				b, e := io.ReadAll(r)
				r.Close()
				if e != nil {
					t.Fatal(e)
				}
				if f.Name == "manifest.json" {
					if e = json.Unmarshal(b, &m); e != nil {
						t.Fatal(e)
					}
				} else {
					payload[f.Name] = b
				}
			}
			if e = m.Validate(); e != nil {
				t.Fatal(e)
			}
			if len(m.Runtimes) != 2 {
				t.Fatalf("want Windows and Linux: %v", m.Runtimes)
			}
			if len(payload) != len(m.Files) {
				t.Fatal("undeclared file")
			}
			for p, want := range m.Files {
				sum := sha256.Sum256(payload[p])
				if hex.EncodeToString(sum[:]) != want {
					t.Fatal("bad SHA", p)
				}
			}
			cfg := &config.Config{Plugins: config.PluginConfig{DataDir: t.TempDir(), AllowUnsigned: true, TrustedPublishers: map[string]string{}, MaxUploadBytes: 64 * 1024 * 1024, MaxUncompressedBytes: 128 * 1024 * 1024, StartTimeoutSeconds: 10}}
			installer := service.NewPluginPackageInstaller(cfg, service.PluginHostInfo{Version: "0.1.179"})
			installed, e := installer.Install(context.Background(), bytes.NewReader(data), nil)
			if e != nil {
				t.Fatal(e)
			}
			if e = installed.Manifest.Validate(); e != nil {
				t.Fatal(e)
			}
			for p, want := range m.Files {
				b, e := os.ReadFile(filepath.Join(installed.InstallPath, filepath.FromSlash(p)))
				if e != nil {
					t.Fatal(e)
				}
				sum := sha256.Sum256(b)
				if hex.EncodeToString(sum[:]) != want {
					t.Fatal("installed SHA mismatch", p)
				}
			}
			checksum, e := hex.DecodeString(installed.BinarySHA256)
			if e != nil {
				t.Fatal(e)
			}
			client := hcplugin.NewClient(&hcplugin.ClientConfig{HandshakeConfig: general.HandshakeConfig, Plugins: general.ClientPluginMap(), Cmd: exec.Command(installed.BinaryPath), AllowedProtocols: []hcplugin.Protocol{hcplugin.ProtocolGRPC}, StartTimeout: 10 * time.Second, SecureConfig: &hcplugin.SecureConfig{Checksum: checksum, Hash: sha256.New()}, Logger: hclog.NewNullLogger(), SyncStdout: io.Discard, SyncStderr: io.Discard, SkipHostEnv: true, GRPCDialOptions: []grpc.DialOption{grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(general.MaxMessageBytes), grpc.MaxCallSendMsgSize(general.MaxMessageBytes))}})
			defer client.Kill()
			rpc, e := client.Client()
			if e != nil {
				t.Fatal(e)
			}
			rawAPI, e := rpc.Dispense(general.PluginName)
			if e != nil {
				t.Fatal(e)
			}
			api := rawAPI.(general.GeneralPluginClient)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			info, e := api.GetInfo(ctx, &general.InfoRequest{})
			if e != nil {
				t.Fatal(e)
			}
			want, e := examples.Info(name)
			if e != nil || !proto.Equal(want, info) {
				t.Fatalf("runtime/source mismatch %v %v", info, e)
			}
			compareManifestInfo(t, m, info)
			configJSON, e := examples.Source(name, "config.example.json")
			if e != nil {
				t.Fatal(e)
			}
			r, e := api.Configure(ctx, &general.ConfigureRequest{InstanceId: 7, ConfigRevision: 2, ConfigVersion: 1, ConfigJson: configJSON})
			if e != nil || !r.Accepted || r.InstanceId != 7 || r.ConfigRevision != 2 {
				t.Fatalf("%v %v", r, e)
			}
			if name == "pricing-sync" {
				body := `{"input":{},"host":{"kv":{},"network":[{"url":"https://prices.example.test/v1","body":{"prices":{"model-a":{"input_price":1.2}}}}],"pricing":[{"channel_id":10,"pricing_id":20,"fields":{"input_price":1}}]}}`
				taskReq := &general.TaskRequest{InstanceId: 7, ConfigRevision: 2, Entry: name, InputJson: []byte(body)}
				out, e := api.RunTask(ctx, taskReq)
				if e != nil || out.Status != "succeeded" || !out.Changed {
					t.Fatalf("%v %v", out, e)
				}
				if !strings.Contains(string(out.OutputJson), `"input_price":1.2`) {
					t.Fatalf("wrong proposal: %s", out.OutputJson)
				}
				taskReq.InputJson = []byte(`{"input":{"prices":{"model-a":{"input_price":1.2}}},"host":{"kv":{},"network":[],"pricing":[{"channel_id":10,"pricing_id":20,"fields":{"input_price":1}}]}}`)
				out, e = api.RunTask(ctx, taskReq)
				if e != nil || out.Status != "succeeded" || !out.Changed || !strings.Contains(string(out.OutputJson), `"input_price":1.2`) {
					t.Fatalf("offline RPC: %v %v", out, e)
				}
				taskReq.InputJson = []byte(`{"input":{},"host":{"kv":{},"network":[],"pricing":[]}}`)
				out, e = api.RunTask(ctx, taskReq)
				if e != nil || out.Status != "failed" || out.ErrorCode != "pricing_source_missing" || out.Changed || len(out.OutputJson) != 0 {
					t.Fatalf("%v %v", out, e)
				}
			} else {
				for _, protocol := range info.Hooks[0].Protocols {
					var content *general.Content
					if name == "credential-input" {
						body := inputFixture(protocol, "sk-abcdefghijklmnop123456")
						if e = service.ValidatePluginGatewayInput(protocol, body, body); e != nil {
							t.Fatal("fixture disagrees with M4", e)
						}
						content = &general.Content{MediaType: "application/json", Data: body}
					} else {
						normalized, e := service.PluginGatewayOutputContent(protocol, "application/json", outputFixture(protocol))
						if e != nil {
							t.Fatal("M4 normalization", e)
						}
						content = &general.Content{MediaType: normalized.MediaType, Data: normalized.Data}
					}
					req := &general.HookRequest{InstanceId: 7, ConfigRevision: 2, Entry: name, Stage: info.Hooks[0].Stages[0], Protocol: protocol, Content: content}
					out, e := api.Check(ctx, req)
					if e != nil || out.Decision != "block" || out.Rejection == nil || out.Content != nil {
						t.Fatalf("%v %v", out, e)
					}
					if strings.Contains(out.String(), "sk-abcdefghijklmnop123456") {
						t.Fatal("secret escaped")
					}
					canceled, stop := context.WithCancel(ctx)
					stop()
					_, e = api.Check(canceled, req)
					if status.Code(e) != codes.Canceled {
						t.Fatal(e)
					}
					expired, stop := context.WithDeadline(ctx, time.Now().Add(-time.Second))
					_, e = api.Check(expired, req)
					stop()
					if status.Code(e) != codes.DeadlineExceeded {
						t.Fatal(e)
					}
					req.Content.Data = bytes.Repeat([]byte("x"), 1024*1024+1)
					_, e = api.Check(ctx, req)
					if status.Code(e) != codes.ResourceExhausted {
						t.Fatal(e)
					}
				}
			}
			t.Logf("INSTALL/Validate/EXTRACT-SHA/REAL-GRPC PASS %s runtimes=%v", name, sortedKeys(m.Runtimes))
		})
	}
}
func sortedKeys(m map[string]service.PluginRuntime) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func compareManifestInfo(t *testing.T, m service.PluginManifest, i *general.InfoResponse) {
	t.Helper()
	var caps []string
	for _, c := range m.Capabilities {
		caps = append(caps, c.ID)
	}
	if !reflect.DeepEqual(caps, i.Capabilities) || !reflect.DeepEqual(m.General.HostCapabilities, i.HostCapabilities) || len(m.General.Hooks) != len(i.Hooks) || len(m.General.Tasks) != len(i.Tasks) {
		t.Fatal("GetInfo declaration mismatch")
	}
	for n, h := range m.General.Hooks {
		stages := []string{}
		for _, s := range h.Stages {
			stages = append(stages, string(s))
		}
		if h.Name != i.Hooks[n].Name || !reflect.DeepEqual(stages, i.Hooks[n].Stages) || !reflect.DeepEqual(h.Protocols, i.Hooks[n].Protocols) || h.CanReplace != i.Hooks[n].CanReplace {
			t.Fatal("hook mismatch")
		}
	}
	for n, task := range m.General.Tasks {
		if task.Name != i.Tasks[n].Name {
			t.Fatal("task mismatch")
		}
	}
}
func inputFixture(protocol, text string) []byte {
	switch protocol {
	case "openai.chat_completions.v1":
		return []byte(`{"model":"m","messages":[{"role":"user","content":"` + text + `"}]}`)
	case "openai.responses.v1":
		return []byte(`{"model":"m","input":"` + text + `"}`)
	case "anthropic.messages.v1":
		return []byte(`{"model":"m","max_tokens":20,"messages":[{"role":"user","content":[{"type":"text","text":"` + text + `"}]}]}`)
	default:
		return []byte(`{"contents":[{"role":"user","parts":[{"text":"` + text + `"}]}]}`)
	}
}
func outputFixture(protocol string) []byte {
	switch protocol {
	case "openai.chat_completions.v1":
		return []byte(`{"id":"a","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"guaranteed profit"},"finish_reason":"stop"}]}`)
	case "openai.responses.v1":
		return []byte(`{"id":"a","object":"response","model":"m","status":"completed","output":[{"id":"b","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"guaranteed profit","annotations":[]}]}]}`)
	case "anthropic.messages.v1":
		return []byte(`{"id":"a","type":"message","model":"m","role":"assistant","content":[{"type":"text","text":"guaranteed profit"}],"stop_reason":"end_turn"}`)
	default:
		return []byte(`{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"guaranteed profit"}]},"finishReason":"STOP"}]}`)
	}
}
