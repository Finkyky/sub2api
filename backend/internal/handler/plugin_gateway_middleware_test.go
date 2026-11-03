package handler

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/securityaudit"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type pgmLease struct {
	snapshot        service.PluginInstanceSnapshot
	check           func(context.Context, contract.HookRequest) (contract.HookResult, error)
	mu              sync.Mutex
	calls, releases int
}

func (l *pgmLease) Snapshot() (*service.PluginInstanceSnapshot, error) { return &l.snapshot, nil }
func (l *pgmLease) Check(ctx context.Context, _ int64, r contract.HookRequest) (contract.HookResult, error) {
	l.mu.Lock()
	l.calls++
	l.mu.Unlock()
	return l.check(ctx, r)
}
func (l *pgmLease) Release() { l.mu.Lock(); l.releases++; l.mu.Unlock() }

type pgmSource struct {
	leases map[int64]map[contract.Stage][]service.PluginGatewayLease
	err    error
}

func (s *pgmSource) AcquireGroup(_ context.Context, id int64, stage contract.Stage, _ string) ([]service.PluginGatewayLease, error) {
	return s.leases[id][stage], s.err
}
func pgmNewLease(stage contract.Stage, id, group int64, protocol string, check func(context.Context, contract.HookRequest) (contract.HookResult, error)) *pgmLease {
	return &pgmLease{snapshot: service.PluginInstanceSnapshot{Instance: service.PluginInstance{ID: id, Enabled: true, CurrentRevision: 1}, Revision: service.PluginInstanceRevision{Revision: 1}, Installation: service.PluginInstallation{Version: "1.0.0"}, Bindings: []service.PluginGroupBinding{{ID: id, InstanceID: id, GroupID: group, Stage: stage, Protocol: protocol, Entry: "inspect", Enabled: true, CanReplace: true}}}, check: check}
}
func pgmRouter(h *PluginGatewayHandler, group *service.Group, path string, next gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		id := group.ID
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{GroupID: &id, Group: group})
		c.Next()
	})
	r.POST(path, h.Middleware, next)
	return r
}

const pgmBody = `{"model":"m","messages":[{"role":"user","content":"question"}],"stream":false}`
const pgmResponse = `{"id":"r","model":"m","choices":[{"message":{"role":"assistant","content":"PRIVATE"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`

func TestPluginGatewayMiddlewareInputBlockAndUnavailable(t *testing.T) {
	for _, kind := range []string{"block", "unavailable", "unknown_protocol"} {
		t.Run(kind, func(t *testing.T) {
			protocol := service.PluginProtocolOpenAIChat
			path := "/v1/chat/completions"
			if kind == "unknown_protocol" {
				protocol = "unsupported.gateway"
				path = "/v1/images/generations"
			}
			input := pgmNewLease(contract.Input, 1, 42, protocol, func(context.Context, contract.HookRequest) (contract.HookResult, error) {
				return contract.HookResult{Decision: contract.Block, Rejection: &contract.Rejection{Code: "deny", Message: "PRIVATE CONFIG"}}, nil
			})
			source := &pgmSource{leases: map[int64]map[contract.Stage][]service.PluginGatewayLease{42: {contract.Input: {input}}}}
			if kind == "unavailable" {
				source.err = errors.New("PRIVATE CONFIG")
			}
			h := NewPluginGatewayHandlerWithService(service.NewPluginGatewayServiceWithSource(source), nil)
			calls := 0
			r := pgmRouter(h, &service.Group{ID: 42}, path, func(c *gin.Context) { calls++; c.String(200, "PRIVATE") })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("POST", path, bytes.NewBufferString(pgmBody)))
			want := 403
			if kind == "unavailable" {
				want = 503
			}
			if kind == "unknown_protocol" {
				want = 422
			}
			require.Equal(t, want, w.Code)
			require.Zero(t, calls)
			require.NotContains(t, w.Body.String(), "PRIVATE")
		})
	}
}
func TestPluginGatewayMiddlewareStrictZeroLeakUntilDecision(t *testing.T) {
	for _, decision := range []contract.Decision{contract.Pass, contract.Block, contract.Replace} {
		t.Run(string(decision), func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var inspected string
			output := pgmNewLease(contract.Output, 2, 42, service.PluginProtocolOpenAIChat, func(ctx context.Context, r contract.HookRequest) (contract.HookResult, error) {
				inspected = string(r.Content.Data)
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return contract.HookResult{}, ctx.Err()
				}
				if decision == contract.Block {
					return contract.HookResult{Decision: decision, Rejection: &contract.Rejection{Code: "deny", Message: "PRIVATE"}}, nil
				}
				if decision == contract.Replace {
					return contract.HookResult{Decision: decision, Content: &contract.Content{MediaType: "text/plain", Data: []byte("approved")}}, nil
				}
				return contract.HookResult{Decision: decision}, nil
			})
			source := &pgmSource{leases: map[int64]map[contract.Stage][]service.PluginGatewayLease{42: {contract.Output: {output}}}}
			h := NewPluginGatewayHandlerWithService(service.NewPluginGatewayServiceWithSource(source), nil)
			r := pgmRouter(h, &service.Group{ID: 42}, "/v1/chat/completions", func(c *gin.Context) {
				c.Header("Content-Type", "application/json")
				c.Header("X-Private", "PRIVATE")
				c.Writer.WriteHeaderNow()
				_, _ = c.Writer.Write([]byte(pgmResponse))
				c.Writer.Flush()
			})
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				r.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(pgmBody)))
				close(done)
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("output plugin not reached")
			}
			require.Zero(t, w.Body.Len())
			require.Empty(t, w.Header().Get("X-Private"))
			require.Equal(t, "PRIVATE", inspected)
			close(release)
			<-done
			if decision == contract.Block {
				require.Equal(t, 403, w.Code)
				require.NotContains(t, w.Body.String(), "PRIVATE")
				require.Empty(t, w.Header().Get("X-Private"))
			} else if decision == contract.Replace {
				require.Equal(t, 200, w.Code)
				require.Contains(t, w.Body.String(), "approved")
				require.NotContains(t, w.Body.String(), "PRIVATE")
				require.Contains(t, w.Body.String(), `"prompt_tokens":2`)
			} else {
				require.Equal(t, 200, w.Code)
				require.Equal(t, pgmResponse, w.Body.String())
			}
			require.Equal(t, 1, output.releases)
		})
	}
}
func TestPluginGatewayMiddlewareInputReplacementAndUnbound(t *testing.T) {
	for _, bound := range []bool{false, true} {
		t.Run(map[bool]string{false: "unbound", true: "replacement"}[bound], func(t *testing.T) {
			input := pgmNewLease(contract.Input, 1, 42, service.PluginProtocolOpenAIChat, func(context.Context, contract.HookRequest) (contract.HookResult, error) {
				return contract.HookResult{Decision: contract.Replace, Content: &contract.Content{MediaType: "application/json", Data: []byte(`{"model":"m","messages":[{"role":"user","content":"approved"}],"stream":false}`)}}, nil
			})
			source := &pgmSource{leases: map[int64]map[contract.Stage][]service.PluginGatewayLease{42: {}}}
			if bound {
				source.leases[42][contract.Input] = []service.PluginGatewayLease{input}
			}
			h := NewPluginGatewayHandlerWithService(service.NewPluginGatewayServiceWithSource(source), nil)
			r := pgmRouter(h, &service.Group{ID: 42}, "/v1/chat/completions", func(c *gin.Context) { b, _ := io.ReadAll(c.Request.Body); c.Data(200, "application/json", b) })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(pgmBody)))
			require.Equal(t, 200, w.Code)
			if bound {
				require.Contains(t, w.Body.String(), "approved")
				require.Equal(t, 1, input.calls)
			} else {
				require.Equal(t, pgmBody, w.Body.String())
				require.Zero(t, input.calls)
			}
		})
	}
}
func TestPluginGatewayMiddlewareResourceLimitAndDeadline(t *testing.T) {
	for _, kind := range []string{"bytes", "deadline", "upstream_error"} {
		t.Run(kind, func(t *testing.T) {
			output := pgmNewLease(contract.Output, 2, 42, service.PluginProtocolOpenAIChat, func(context.Context, contract.HookRequest) (contract.HookResult, error) {
				return contract.HookResult{Decision: contract.Pass}, nil
			})
			source := &pgmSource{leases: map[int64]map[contract.Stage][]service.PluginGatewayLease{42: {contract.Output: {output}}}}
			cfg := &config.Config{}
			cfg.Plugins.StrictOutputMaxBytes = 8
			if kind != "bytes" {
				cfg.Plugins.StrictOutputMaxBytes = 1024
			}
			cfg.Plugins.StrictOutputTimeoutSeconds = 1
			h := NewPluginGatewayHandlerWithService(service.NewPluginGatewayServiceWithSource(source), cfg)
			r := pgmRouter(h, &service.Group{ID: 42}, "/v1/chat/completions", func(c *gin.Context) {
				if kind == "deadline" {
					<-c.Request.Context().Done()
				}
				if kind == "upstream_error" {
					c.Data(502, "application/json", []byte(`{"error":"PRIVATE"}`))
					return
				}
				c.Data(200, "application/json", []byte(pgmResponse))
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(pgmBody)))
			want := 503
			if kind == "deadline" {
				want = 504
			}
			if kind == "upstream_error" {
				want = 502
			}
			require.Equal(t, want, w.Code)
			require.NotContains(t, w.Body.String(), "PRIVATE")
			require.Zero(t, output.calls)
			require.Equal(t, 1, output.releases)
		})
	}
}
func TestPluginGatewayMiddlewareFallbackGroupRechecks(t *testing.T) {
	fallback := int64(43)
	input := pgmNewLease(contract.Input, 3, 43, service.PluginProtocolAnthropicMessages, func(context.Context, contract.HookRequest) (contract.HookResult, error) {
		return contract.HookResult{Decision: contract.Block, Rejection: &contract.Rejection{Code: "deny", Message: "PRIVATE"}}, nil
	})
	source := &pgmSource{leases: map[int64]map[contract.Stage][]service.PluginGatewayLease{42: {}, 43: {contract.Input: {input}}}}
	h := NewPluginGatewayHandlerWithService(service.NewPluginGatewayServiceWithSource(source), nil)
	r := pgmRouter(h, &service.Group{ID: 42, FallbackGroupIDOnInvalidRequest: &fallback}, "/v1/messages", func(c *gin.Context) {
		_, err := recheckPluginGatewayFallback(c, 43, []byte(pgmBody))
		require.ErrorIs(t, err, service.ErrPluginGatewayBlocked)
		status, code := pluginGatewayErrorStatus(err)
		pluginGatewayError(c, service.PluginProtocolAnthropicMessages, status, code)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", bytes.NewBufferString(pgmBody)))
	require.Equal(t, 403, w.Code)
	require.Equal(t, 1, input.calls)
	require.Equal(t, 1, input.releases)
}

type pgmProtocolFixture struct{ name, protocol, json, sse string }

func pgmProtocolFixtures() []pgmProtocolFixture {
	return []pgmProtocolFixture{
		{"chat", service.PluginProtocolOpenAIChat,
			`{"id":"c1","model":"m","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"secret","annotations":[{"text":"secret"}]},"finish_reason":"stop","logprobs":{"content":[{"token":"secret"}]}}],"usage":{"total_tokens":9}}`,
			": keepalive\n\ndata: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"sec\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ret\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"total_tokens\":9}}\n\ndata: [DONE]\n\n"},
		{"responses", service.PluginProtocolOpenAIResponses,
			`{"id":"r1","model":"m","object":"response","status":"completed","output":[{"id":"msg1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"sec","annotations":[{"text":"secret"}]},{"type":"output_text","text":"ret"}]}],"usage":{"total_tokens":9},"error":null}`,
			"event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\",\"model\":\"m\",\"status\":\"in_progress\",\"output\":[]}}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"sec\"}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"ret\"}\n\nevent: response.output_text.done\ndata: {\"type\":\"response.output_text.done\",\"output_index\":0,\"content_index\":0,\"text\":\"secret\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"model\":\"m\",\"status\":\"completed\",\"output\":[{\"id\":\"msg1\",\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"secret\"}]}],\"usage\":{\"total_tokens\":9}}}\n\n"},
		{"anthropic", service.PluginProtocolAnthropicMessages,
			`{"id":"a1","model":"m","type":"message","role":"assistant","content":[{"type":"text","text":"sec"},{"type":"text","text":"ret"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":4,"output_tokens":5}}`,
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"a1\",\"model\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":4,\"output_tokens\":0}}}\n\nevent: ping\ndata: {\"type\":\"ping\"}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"sec\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ret\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":5}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"},
		{"gemini", service.PluginProtocolGemini,
			`{"responseId":"g1","modelVersion":"m","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"sec"},{"text":"ret"}]},"finishReason":"STOP"}],"usageMetadata":{"totalTokenCount":9}}`,
			"data: {\"responseId\":\"g1\",\"modelVersion\":\"m\",\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"sec\"}]}}]}\n\ndata: {\"responseId\":\"g1\",\"modelVersion\":\"m\",\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ret\"}]},\"finishReason\":\"STOP\"}]}\n\ndata: {\"usageMetadata\":{\"totalTokenCount\":9}}\n\n"},
	}
}

func TestPluginGatewayMiddlewareFourProtocolsJSONSSE(t *testing.T) {
	paths := map[string]string{service.PluginProtocolOpenAIChat: "/v1/chat/completions", service.PluginProtocolOpenAIResponses: "/v1/responses", service.PluginProtocolAnthropicMessages: "/v1/messages", service.PluginProtocolGemini: "/v1beta/models/m:generateContent"}
	inputs := map[string]string{service.PluginProtocolOpenAIChat: pgmBody, service.PluginProtocolOpenAIResponses: `{"model":"m","input":"hello"}`, service.PluginProtocolAnthropicMessages: `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"hello"}]}`, service.PluginProtocolGemini: `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`}
	for _, f := range pgmProtocolFixtures() {
		for _, media := range []string{"application/json", "text/event-stream"} {
			for _, decision := range []contract.Decision{contract.Pass, contract.Replace, contract.Block} {
				t.Run(f.name+"/"+media+"/"+string(decision), func(t *testing.T) {
					output := pgmNewLease(contract.Output, 2, 42, f.protocol, func(_ context.Context, req contract.HookRequest) (contract.HookResult, error) {
						require.Equal(t, "text/plain; charset=utf-8", req.Content.MediaType)
						require.Equal(t, "secret", string(req.Content.Data))
						switch decision {
						case contract.Block:
							return contract.HookResult{Decision: decision, Rejection: &contract.Rejection{Code: "deny", Message: "PRIVATE"}}, nil
						case contract.Replace:
							return contract.HookResult{Decision: decision, Content: &contract.Content{MediaType: "text/plain", Data: []byte("approved")}}, nil
						default:
							return contract.HookResult{Decision: decision}, nil
						}
					})
					source := &pgmSource{leases: map[int64]map[contract.Stage][]service.PluginGatewayLease{42: {contract.Output: {output}}}}
					h := NewPluginGatewayHandlerWithService(service.NewPluginGatewayServiceWithSource(source), nil)
					body := f.json
					if media == "text/event-stream" {
						body = f.sse
					}
					path := paths[f.protocol]
					if f.protocol == service.PluginProtocolGemini && media == "text/event-stream" {
						path = "/v1beta/models/m:streamGenerateContent"
					}
					r := pgmRouter(h, &service.Group{ID: 42}, path, func(c *gin.Context) {
						c.Header("X-Private", "PRIVATE")
						c.Header("ETag", "old-content")
						c.Header("Content-Encoding", "gzip")
						c.Data(200, media, []byte(body))
						c.Writer.Flush()
					})
					w := httptest.NewRecorder()
					r.ServeHTTP(w, httptest.NewRequest("POST", path, bytes.NewBufferString(inputs[f.protocol])))
					if decision == contract.Block {
						require.Equal(t, 403, w.Code)
						require.NotContains(t, w.Body.String(), "secret")
						require.Empty(t, w.Header().Get("X-Private"))
					} else {
						require.Equal(t, 200, w.Code)
						if decision == contract.Pass {
							require.Equal(t, body, w.Body.String())
						} else {
							require.NotContains(t, w.Body.String(), "secret")
							require.Empty(t, w.Header().Get("X-Private"))
							require.Empty(t, w.Header().Get("ETag"))
							require.Empty(t, w.Header().Get("Content-Encoding"))
							content, err := service.PluginGatewayOutputContent(f.protocol, media, w.Body.Bytes())
							require.NoError(t, err)
							require.Equal(t, "approved", string(content.Data))
						}
					}
					require.Equal(t, 1, output.calls)
					require.Equal(t, 1, output.releases)
				})
			}
		}
	}
}

type pgmCountingBody struct {
	io.Reader
	n int
}

func (b *pgmCountingBody) Read(p []byte) (int, error) {
	n, e := b.Reader.Read(p)
	b.n += n
	return n, e
}
func (b *pgmCountingBody) Close() error { return nil }
func TestPluginGatewayMiddlewareInputReadBound(t *testing.T) {
	lease := pgmNewLease(contract.Input, 1, 42, service.PluginProtocolOpenAIChat, func(context.Context, contract.HookRequest) (contract.HookResult, error) {
		return contract.HookResult{Decision: contract.Pass}, nil
	})
	source := &pgmSource{leases: map[int64]map[contract.Stage][]service.PluginGatewayLease{42: {contract.Input: {lease}}}}
	cfg := &config.Config{}
	cfg.Gateway.TextMaxBodySize = 128
	h := NewPluginGatewayHandlerWithService(service.NewPluginGatewayServiceWithSource(source), cfg)
	upstream := 0
	r := pgmRouter(h, &service.Group{ID: 42}, "/v1/chat/completions", func(c *gin.Context) { upstream++; c.String(200, "unexpected") })
	body := &pgmCountingBody{Reader: bytes.NewBuffer(make([]byte, 1<<20))}
	req := httptest.NewRequest("POST", "/v1/chat/completions", body)
	req.ContentLength = -1
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 413, w.Code)
	require.LessOrEqual(t, body.n, 129)
	require.Zero(t, upstream)
	require.Zero(t, lease.calls)
	require.Equal(t, 1, lease.releases)
}

func TestPluginGatewayMiddlewareLiveSidebandBoundRejects(t *testing.T) {
	for _, path := range []string{"/v1/live/call-1", "/backend-api/codex/call-1"} {
		t.Run(path, func(t *testing.T) {
			lease := pgmNewLease(contract.Output, 2, 42, "unsupported.gateway", func(context.Context, contract.HookRequest) (contract.HookResult, error) {
				return contract.HookResult{Decision: contract.Pass}, nil
			})
			source := &pgmSource{leases: map[int64]map[contract.Stage][]service.PluginGatewayLease{42: {contract.Output: {lease}}}}
			h := NewPluginGatewayHandlerWithService(service.NewPluginGatewayServiceWithSource(source), nil)
			r := gin.New()
			gid := int64(42)
			r.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{GroupID: &gid, Group: &service.Group{ID: gid}})
			})
			calls := 0
			route := path
			if path == "/v1/live/call-1" {
				route = "/v1/live/:call_id"
			}
			if path == "/backend-api/codex/call-1" {
				route = "/backend-api/codex/:call_id"
			}
			r.GET(route, h.Middleware, func(c *gin.Context) { calls++; c.Status(101) })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			require.Equal(t, 422, w.Code)
			require.Zero(t, calls)
		})
	}
}
func TestPluginGatewayMiddlewareBodyReadDeadlineRealHTTP(t *testing.T) {
	lease := pgmNewLease(contract.Output, 2, 42, service.PluginProtocolOpenAIChat, func(context.Context, contract.HookRequest) (contract.HookResult, error) {
		return contract.HookResult{Decision: contract.Pass}, nil
	})
	source := &pgmSource{leases: map[int64]map[contract.Stage][]service.PluginGatewayLease{42: {contract.Output: {lease}}}}
	h := NewPluginGatewayHandlerWithService(service.NewPluginGatewayServiceWithSource(source), nil)
	h.hookTimeout = 50 * time.Millisecond
	h.outputTimeout = 100 * time.Millisecond
	r := pgmRouter(h, &service.Group{ID: 42}, "/v1/chat/completions", func(c *gin.Context) { c.Data(200, "application/json", []byte(pgmResponse)) })
	server := httptest.NewServer(r)
	defer server.Close()
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, err = fmt.Fprint(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: example\r\nContent-Length: 1024\r\n\r\n{")
	require.NoError(t, err)
	response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "POST"})
	require.NoError(t, err, "transport body read must actually terminate")
	defer response.Body.Close()
	require.Equal(t, 504, response.StatusCode)
	h.budget.mu.Lock()
	active := h.budget.active
	h.budget.mu.Unlock()
	require.Zero(t, active)
}

func TestPluginGatewayMiddlewareClaudeCodeFallbackBinding(t *testing.T) {
	for _, stage := range []contract.Stage{contract.Input, contract.Output} {
		t.Run(string(stage), func(t *testing.T) {
			fallback := int64(43)
			lease := pgmNewLease(stage, 2, 43, service.PluginProtocolAnthropicMessages, func(context.Context, contract.HookRequest) (contract.HookResult, error) {
				return contract.HookResult{Decision: contract.Block, Rejection: &contract.Rejection{Code: "deny", Message: "PRIVATE"}}, nil
			})
			source := &pgmSource{leases: map[int64]map[contract.Stage][]service.PluginGatewayLease{42: {}, 43: {stage: {lease}}}}
			h := NewPluginGatewayHandlerWithService(service.NewPluginGatewayServiceWithSource(source), nil)
			h.resolveGroup = func(context.Context, int64) (*service.Group, error) { return &service.Group{ID: 43}, nil }
			upstream := 0
			r := pgmRouter(h, &service.Group{ID: 42, ClaudeCodeOnly: true, FallbackGroupID: &fallback}, "/v1/messages", func(c *gin.Context) {
				// The real handler resolves the non-Claude-Code fallback before scheduling.
				key, _ := middleware.GetAPIKeyFromContext(c)
				result, auditKey, err := recheckPluginGatewayClaudeGroup(c, key, []byte(`{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"hello"}]}`))
				if err == nil {
					require.EqualValues(t, 43, *auditKey.GroupID)
				}
				if err != nil {
					status, code := pluginGatewayErrorStatus(err)
					pluginGatewayError(c, service.PluginProtocolAnthropicMessages, status, code)
					return
				}
				upstream++
				c.Data(200, "application/json", []byte(`{"id":"a1","model":"m","type":"message","role":"assistant","content":[{"type":"text","text":"PRIVATE"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
				_ = result
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", bytes.NewBufferString(`{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"hello"}]}`)))
			require.Equal(t, 403, w.Code)
			require.NotContains(t, w.Body.String(), "PRIVATE")
			if stage == contract.Input {
				require.Zero(t, upstream)
			} else {
				require.Equal(t, 1, upstream)
			}
			require.Equal(t, 1, lease.calls)
			require.Equal(t, 1, lease.releases)
		})
	}
}
func TestPluginGatewayMiddlewareFallbackInputBlockAfterHeartbeat(t *testing.T) {
	fallback := int64(43)
	lease := pgmNewLease(contract.Input, 2, 43, service.PluginProtocolAnthropicMessages, func(context.Context, contract.HookRequest) (contract.HookResult, error) {
		return contract.HookResult{Decision: contract.Block, Rejection: &contract.Rejection{Code: "deny", Message: "PRIVATE"}}, nil
	})
	source := &pgmSource{leases: map[int64]map[contract.Stage][]service.PluginGatewayLease{42: {}, 43: {contract.Input: {lease}}}}
	h := NewPluginGatewayHandlerWithService(service.NewPluginGatewayServiceWithSource(source), nil)
	r := pgmRouter(h, &service.Group{ID: 42, FallbackGroupIDOnInvalidRequest: &fallback}, "/v1/messages", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.Write([]byte(": keepalive\n\n"))
		c.Writer.Flush()
		_, err := recheckPluginGatewayFallback(c, 43, []byte(`{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"hello"}]}`))
		status, code := pluginGatewayErrorStatus(err)
		pluginGatewayError(c, service.PluginProtocolAnthropicMessages, status, code)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", bytes.NewBufferString(pgmBody)))
	require.Equal(t, 403, w.Code)
	require.NotContains(t, w.Body.String(), "keepalive")
	require.NotContains(t, w.Body.String(), "PRIVATE")
}

func TestPluginGatewayMiddlewareModelDetailsSkip(t *testing.T) {
	for _, tc := range []struct{ route, path string }{{"/v1/models/:model", "/v1/models/responses"}, {"/models/:model", "/models/responses"}, {"/v1beta/models/:model", "/v1beta/models/realtime-model"}, {"/backend-api/codex/models", "/backend-api/codex/models"}} {
		t.Run(tc.path, func(t *testing.T) {
			lease := pgmNewLease(contract.Output, 2, 42, "unsupported.gateway", func(context.Context, contract.HookRequest) (contract.HookResult, error) {
				return contract.HookResult{Decision: contract.Pass}, nil
			})
			source := &pgmSource{leases: map[int64]map[contract.Stage][]service.PluginGatewayLease{42: {contract.Output: {lease}}}}
			h := NewPluginGatewayHandlerWithService(service.NewPluginGatewayServiceWithSource(source), nil)
			r := gin.New()
			gid := int64(42)
			r.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{GroupID: &gid, Group: &service.Group{ID: gid}})
			})
			r.GET(tc.route, h.Middleware, func(c *gin.Context) { c.String(200, "model detail") })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
			require.Equal(t, 200, w.Code)
			require.Equal(t, "model detail", w.Body.String())
			require.Zero(t, lease.calls)
			require.Zero(t, lease.releases)
		})
	}
}
func TestPluginGatewayMiddlewareReplacementPreservesHostHeaders(t *testing.T) {
	lease := pgmNewLease(contract.Output, 2, 42, service.PluginProtocolOpenAIChat, func(context.Context, contract.HookRequest) (contract.HookResult, error) {
		return contract.HookResult{Decision: contract.Replace, Content: &contract.Content{MediaType: "text/plain", Data: []byte("approved")}}, nil
	})
	source := &pgmSource{leases: map[int64]map[contract.Stage][]service.PluginGatewayLease{42: {contract.Output: {lease}}}}
	h := NewPluginGatewayHandlerWithService(service.NewPluginGatewayServiceWithSource(source), nil)
	r := gin.New()
	r.Use(middleware.CORS(config.CORSConfig{AllowedOrigins: []string{"https://client.example"}, AllowCredentials: true}), middleware.SecurityHeaders(config.CSPConfig{}, nil))
	gid := int64(42)
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{GroupID: &gid, Group: &service.Group{ID: gid}})
	})
	r.POST("/v1/chat/completions", h.Middleware, func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "https://PRIVATE.example")
		c.Header("X-Content-Type-Options", "PRIVATE")
		c.Header("X-Private", "PRIVATE")
		c.Header("ETag", "old")
		c.Header("Content-Encoding", "gzip")
		c.Data(200, "application/json", []byte(pgmResponse))
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(pgmBody))
	req.Header.Set("Origin", "https://client.example")
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
	require.Equal(t, "https://client.example", w.Header().Get("Access-Control-Allow-Origin"))
	require.Equal(t, "true", w.Header().Get("Access-Control-Allow-Credentials"))
	require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
	require.Contains(t, w.Header().Get("Vary"), "Origin")
	require.Empty(t, w.Header().Get("X-Private"))
	require.Empty(t, w.Header().Get("ETag"))
	require.Empty(t, w.Header().Get("Content-Encoding"))
	require.NotContains(t, w.Body.String(), "PRIVATE")
}

func TestPluginGatewayMiddlewareFallbackAuditBlockAfterHeartbeat(t *testing.T) {
	fallback := int64(43)
	original := `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"hello"}]}`
	replacement := `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"blocked content"}]}`
	lease := pgmNewLease(contract.Input, 2, 43, service.PluginProtocolAnthropicMessages, func(context.Context, contract.HookRequest) (contract.HookResult, error) {
		return contract.HookResult{Decision: contract.Replace, Content: &contract.Content{MediaType: "application/json", Data: []byte(replacement)}}, nil
	})
	source := &pgmSource{leases: map[int64]map[contract.Stage][]service.PluginGatewayLease{42: {}, 43: {contract.Input: {lease}}}}
	h := NewPluginGatewayHandlerWithService(service.NewPluginGatewayServiceWithSource(source), nil)
	engine := &turnCountingEngine{mode: securityaudit.ModeBlocking, decisions: []*securityaudit.PromptDecision{{Kind: securityaudit.DecisionAllow, AllowNextStage: true}, {Kind: securityaudit.DecisionBlock, AllowNextStage: false}}}
	coordinator := securityaudit.NewCoordinator(nil, engine)
	fallbackUpstream := 0
	r := pgmRouter(h, &service.Group{ID: 42, FallbackGroupIDOnInvalidRequest: &fallback}, "/v1/messages", func(c *gin.Context) {
		key, _ := middleware.GetAPIKeyFromContext(c)
		first := runSecurityAudit(c, nil, coordinator, nil, key, middleware.AuthSubject{UserID: 7}, "anthropic_messages", "m", []byte(original), "http")
		require.True(t, first.AllowNextStage)
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.Write([]byte(": keepalive\n\n"))
		c.Writer.Flush()
		body, err := recheckPluginGatewayFallback(c, 43, []byte(original))
		require.NoError(t, err)
		key = cloneAPIKeyWithGroup(key, &service.Group{ID: 43})
		decision := runSecurityAudit(c, nil, coordinator, nil, key, middleware.AuthSubject{UserID: 7}, "anthropic_messages", "m", body, "http")
		require.False(t, decision.AllowNextStage)
		if !decision.AllowNextStage {
			markPluginGatewayFailure(c, decision.HTTPStatus, "security_audit_blocked")
			(&GatewayHandler{}).anthropicSecurityAuditError(c, decision)
			return
		}
		fallbackUpstream++
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/v1/messages", bytes.NewBufferString(original)))
	require.Equal(t, 403, w.Code)
	require.NotContains(t, w.Body.String(), "keepalive")
	require.NotContains(t, w.Body.String(), "blocked content")
	require.Contains(t, w.Body.String(), "security_audit_blocked")
	require.EqualValues(t, 2, engine.evaluates.Load())
	require.Zero(t, fallbackUpstream)
	require.Equal(t, 1, lease.releases)
}
