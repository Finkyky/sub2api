//go:build unit

package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	contract "github.com/Wei-Shaw/sub2api/pkg/pluginapi/contracts/v1"
)

type pgProtocolFixture struct{ name, protocol, json, sse string }

func pgProtocolFixtures() []pgProtocolFixture {
	return []pgProtocolFixture{
		{"chat", PluginProtocolOpenAIChat,
			`{"id":"c1","model":"m","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"secret","annotations":[{"text":"secret"}]},"finish_reason":"stop","logprobs":{"content":[{"token":"secret"}]}}],"usage":{"total_tokens":9}}`,
			": keepalive\n\ndata: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"sec\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ret\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"total_tokens\":9}}\n\ndata: [DONE]\n\n"},
		{"responses", PluginProtocolOpenAIResponses,
			`{"id":"r1","model":"m","object":"response","status":"completed","output":[{"id":"msg1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"sec","annotations":[{"text":"secret"}]},{"type":"output_text","text":"ret"}]}],"usage":{"total_tokens":9},"error":null}`,
			"event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\",\"model\":\"m\",\"status\":\"in_progress\",\"output\":[]}}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"sec\"}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"ret\"}\n\nevent: response.output_text.done\ndata: {\"type\":\"response.output_text.done\",\"output_index\":0,\"content_index\":0,\"text\":\"secret\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"model\":\"m\",\"status\":\"completed\",\"output\":[{\"id\":\"msg1\",\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"secret\"}]}],\"usage\":{\"total_tokens\":9}}}\n\n"},
		{"anthropic", PluginProtocolAnthropicMessages,
			`{"id":"a1","model":"m","type":"message","role":"assistant","content":[{"type":"text","text":"sec"},{"type":"text","text":"ret"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":4,"output_tokens":5}}`,
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"a1\",\"model\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":4,\"output_tokens\":0}}}\n\nevent: ping\ndata: {\"type\":\"ping\"}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"sec\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ret\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":5}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"},
		{"gemini", PluginProtocolGemini,
			`{"responseId":"g1","modelVersion":"m","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"sec"},{"text":"ret"}]},"finishReason":"STOP"}],"usageMetadata":{"totalTokenCount":9}}`,
			"data: {\"responseId\":\"g1\",\"modelVersion\":\"m\",\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"sec\"}]}}]}\n\ndata: {\"responseId\":\"g1\",\"modelVersion\":\"m\",\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ret\"}]},\"finishReason\":\"STOP\"}]}\n\ndata: {\"usageMetadata\":{\"totalTokenCount\":9}}\n\n"},
	}
}

func TestPluginGatewayProtocolSupported(t *testing.T) {
	for _, f := range pgProtocolFixtures() {
		if !PluginGatewayProtocolSupported(f.protocol) {
			t.Errorf("missing %s", f.protocol)
		}
	}
	if PluginGatewayProtocolSupported("unknown") {
		t.Fatal("unknown supported")
	}
}
func TestPluginGatewayProtocolOutputAndReplacement(t *testing.T) {
	for _, f := range pgProtocolFixtures() {
		for _, media := range []string{"application/json", "text/event-stream"} {
			t.Run(f.name+"/"+media, func(t *testing.T) {
				body := []byte(f.json)
				if media == "text/event-stream" {
					body = []byte(f.sse)
				}
				c, err := PluginGatewayOutputContent(f.protocol, media+"; charset=utf-8", body)
				if err != nil {
					t.Fatal(err)
				}
				if c.MediaType != "text/plain; charset=utf-8" || string(c.Data) != "secret" {
					t.Fatalf("unexpected content: %#v", c)
				}
				replaced, err := ApplyPluginGatewayOutputReplacement(f.protocol, media, body, contract.Content{MediaType: "text/plain; charset=utf-8", Data: []byte("已处理\nclean")})
				if err != nil {
					t.Fatal(err)
				}
				for _, sensitive := range []string{"secret", "\"sec\"", "\"ret\""} {
					if bytes.Contains(replaced, []byte(sensitive)) {
						t.Fatalf("original leaked: %s", replaced)
					}
				}
				c, err = PluginGatewayOutputContent(f.protocol, media, replaced)
				if err != nil || string(c.Data) != "已处理\nclean" {
					t.Fatalf("replacement invalid: %s, %v, %q", replaced, err, c.Data)
				}
				if !bytes.Contains(replaced, []byte(`"m"`)) {
					t.Fatal("model lost")
				}
				usage := "total_tokens"
				if f.name == "gemini" {
					usage = "totalTokenCount"
				}
				if f.name == "anthropic" {
					usage = "input_tokens"
				}
				if !bytes.Contains(replaced, []byte(usage)) {
					t.Fatal("usage lost")
				}
				id := map[string]string{"chat": "c1", "responses": "r1", "anthropic": "a1", "gemini": "g1"}[f.name]
				if !bytes.Contains(replaced, []byte(id)) {
					t.Fatal("id lost")
				}
				terminal := map[string]string{"chat": "stop", "responses": "completed", "anthropic": "end_turn", "gemini": "STOP"}[f.name]
				if !bytes.Contains(replaced, []byte(terminal)) {
					t.Fatal("finish lost")
				}
			})
		}
	}
}
func TestPluginGatewayProtocolRejectMalformed(t *testing.T) {
	for _, f := range pgProtocolFixtures() {
		t.Run(f.name, func(t *testing.T) {
			for _, body := range []string{"null", "[]", "{}", f.json + "{}", strings.TrimSuffix(f.json, "}"), strings.Replace(f.json, `"m"`, `"m","`+map[bool]string{true: "modelVersion", false: "model"}[f.name == "gemini"]+`":"evil"`, 1), `{"error":{"message":"secret"}}`} {
				if _, err := PluginGatewayOutputContent(f.protocol, "application/json", []byte(body)); err == nil {
					t.Errorf("accepted malformed JSON: %s", body)
				}
			}
			last := strings.LastIndex(strings.TrimSuffix(f.sse, "\n\n"), "\n\n")
			for _, body := range []string{f.sse[:last+2], strings.TrimSuffix(f.sse, "\n\n"), f.sse + "data: {bad}\n\n", f.sse + "event: error\ndata: {\"error\":{\"message\":\"secret\"}}\n\n"} {
				// Gemini's usage-only tail may be omitted; remove the finish event for truncation.
				if f.name == "gemini" && body == f.sse[:last+2] {
					body = f.sse[:strings.Index(f.sse, "\n\n")+2]
				}
				if _, err := PluginGatewayOutputContent(f.protocol, "text/event-stream", []byte(body)); err == nil {
					t.Errorf("accepted malformed SSE: %q", body)
				}
			}
		})
	}
}
func TestPluginGatewayProtocolInput(t *testing.T) {
	bodies := map[string]string{
		PluginProtocolOpenAIChat:        `{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"before","cache_control":{"type":"ephemeral"}}]}],"stream":true,"gateway_cache":{"mode":"bypass"},"prompt_cache_key":"k","cache_control":{"type":"ephemeral"}}`,
		PluginProtocolOpenAIResponses:   `{"model":"m","input":"before","stream":true,"gateway_cache":{"mode":"bypass"},"prompt_cache_key":"k","cache_control":{"type":"ephemeral"}}`,
		PluginProtocolAnthropicMessages: `{"model":"m","max_tokens":40,"messages":[{"role":"user","content":[{"type":"text","text":"before","cache_control":{"type":"ephemeral"}}]}],"stream":true,"gateway_cache":{"mode":"bypass"},"prompt_cache_key":"k","cache_control":{"type":"ephemeral"}}`,
		PluginProtocolGemini:            `{"contents":[{"role":"user","parts":[{"text":"before"}]}],"stream":true,"gateway_cache":{"mode":"bypass"},"prompt_cache_key":"k","cache_control":{"type":"ephemeral"},"cachedContent":"cache/1"}`,
	}
	for protocol, before := range bodies {
		t.Run(protocol, func(t *testing.T) {
			after := strings.ReplaceAll(before, "before", "after")
			if err := ValidatePluginGatewayInput(protocol, []byte(before), []byte(after)); err != nil {
				t.Fatal(err)
			}
			for _, pair := range [][2]string{{`"m"`, `"other"`}, {`true`, `false`}, {`"bypass"`, `"read_write"`}, {`"k"`, `"other"`}, {`"ephemeral"`, `"other"`}, {`"cache/1"`, `"cache/2"`}} {
				changed := strings.Replace(after, pair[0], pair[1], 1)
				if changed == after {
					continue
				}
				if err := ValidatePluginGatewayInput(protocol, []byte(before), []byte(changed)); err == nil {
					t.Errorf("accepted control change %s", pair[0])
				}
			}
			for _, invalid := range []string{"null", "[]", "{}", after + "{}", strings.Replace(after, `true`, `"yes"`, 1)} {
				if err := ValidatePluginGatewayInput(protocol, []byte(before), []byte(invalid)); err == nil {
					t.Errorf("accepted input %s", invalid)
				}
			}
			if err := ValidatePluginGatewayInput(protocol, nil, []byte(after)); err == nil {
				t.Error("accepted nil original")
			}
		})
	}
}
func TestPluginGatewayProtocolUnknownAndReplacementType(t *testing.T) {
	for _, f := range pgProtocolFixtures() {
		for _, media := range []string{"text/plain", "application/octet-stream", "application/json; charset=latin1", "invalid"} {
			if _, err := PluginGatewayOutputContent(f.protocol, media, []byte(f.json)); err == nil {
				t.Errorf("accepted media %s", media)
			}
		}
		for _, r := range []contract.Content{{MediaType: "application/json", Data: []byte(`"clean"`)}, {MediaType: "text/plain", Data: []byte{0xff}}, {MediaType: "text/plain; charset=latin1", Data: []byte("clean")}} {
			if _, err := ApplyPluginGatewayOutputReplacement(f.protocol, "application/json", []byte(f.json), r); err == nil {
				t.Errorf("accepted replacement %#v", r)
			}
		}
		for _, body := range [][]byte{nil, {}, {0xff}} {
			if _, err := PluginGatewayOutputContent(f.protocol, "application/json", body); err == nil {
				t.Error("accepted nil/invalid body")
			}
		}
	}
	if ValidatePluginGatewayInput("unknown", []byte(`{}`), []byte(`{}`)) == nil {
		t.Fatal("unknown input")
	}
	if _, err := PluginGatewayOutputContent("unknown", "application/json", []byte(`{}`)); err == nil {
		t.Fatal("unknown output")
	}
	if _, err := ApplyPluginGatewayOutputReplacement("unknown", "application/json", []byte(`{}`), contract.Content{MediaType: "text/plain"}); err == nil {
		t.Fatal("unknown replacement")
	}
}
func TestPluginGatewayProtocolNilUsageAndEmptyText(t *testing.T) {
	for _, f := range pgProtocolFixtures() {
		var obj map[string]any
		if err := json.Unmarshal([]byte(f.json), &obj); err != nil {
			t.Fatal(err)
		}
		key := "usage"
		if f.name == "gemini" {
			key = "usageMetadata"
		}
		obj[key] = nil
		body, _ := json.Marshal(obj)
		result, err := ApplyPluginGatewayOutputReplacement(f.protocol, "application/json", body, contract.Content{MediaType: "text/plain", Data: nil})
		if err != nil {
			t.Fatal(err)
		}
		c, err := PluginGatewayOutputContent(f.protocol, "application/json", result)
		if err != nil || len(c.Data) != 0 {
			t.Fatalf("empty replacement: %q %v", c.Data, err)
		}
		if !bytes.Contains(result, []byte(`"`+key+`":null`)) {
			t.Fatalf("nil usage lost: %s", result)
		}
	}
	body := []byte(`{"id":"c","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":null},"finish_reason":"stop"}],"usage":null}`)
	c, err := PluginGatewayOutputContent(PluginProtocolOpenAIChat, "application/json", body)
	if err != nil || len(c.Data) != 0 {
		t.Fatalf("nil content: %v", err)
	}
}
func TestPluginGatewayProtocolRejectNonTextAndMultiple(t *testing.T) {
	tests := []struct{ p, b string }{
		{PluginProtocolOpenAIChat, `{"choices":[{"index":0,"message":{"role":"assistant","content":"secret","tool_calls":[{"function":{"arguments":"secret"}}]},"finish_reason":"stop"}]}`},
		{PluginProtocolOpenAIChat, `{"choices":[{"index":0,"message":{"content":"one"},"finish_reason":"stop"},{"index":1,"message":{"content":"secret"},"finish_reason":"stop"}]}`},
		{PluginProtocolOpenAIResponses, `{"status":"completed","output":[{"type":"function_call","arguments":"secret"}]}`},
		{PluginProtocolAnthropicMessages, `{"content":[{"type":"tool_use","input":{"text":"secret"}}],"stop_reason":"end_turn"}`},
		{PluginProtocolGemini, `{"candidates":[{"content":{"parts":[{"functionCall":{"args":{"text":"secret"}}}]},"finishReason":"STOP"}]}`},
	}
	for _, tt := range tests {
		if _, err := ApplyPluginGatewayOutputReplacement(tt.p, "application/json", []byte(tt.b), contract.Content{MediaType: "text/plain", Data: []byte("clean")}); err == nil {
			t.Errorf("nontext/multiple replacement accepted: %s", tt.b)
		}
	}
}

func TestPluginGatewayProtocolFrameAndStateIntegrity(t *testing.T) {
	for _, f := range pgProtocolFixtures() {
		t.Run(f.name, func(t *testing.T) {
			bad := []string{strings.TrimSuffix(f.sse, "\n"), f.sse + "data: [DONE]\n\n"}
			if f.name == "responses" {
				bad = append(bad, strings.Replace(f.sse, `"text":"secret"`, `"text":"wrong"`, 1), strings.Replace(f.sse, `"status":"completed"`, `"status":"incomplete"`, 1), strings.Replace(f.sse, `"output_index":0`, `"output_index":1`, 1))
			}
			if f.name == "chat" {
				bad = append(bad, strings.Replace(f.sse, `"finish_reason":"stop"`, `"finish_reason":"length"`, 1), strings.Replace(f.sse, `"content":"ret"`, `"content":"ret","tool_calls":[{"function":{"arguments":"secret"}}]`, 1))
			}
			if f.name == "anthropic" {
				bad = append(bad, strings.Replace(f.sse, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n", "", 1), strings.Replace(f.sse, `"stop_reason":"end_turn"`, `"stop_reason":"max_tokens"`, 1))
			}
			if f.name == "gemini" {
				bad = append(bad, strings.Replace(f.sse, `"finishReason":"STOP"`, `"finishReason":"MAX_TOKENS"`, 1), strings.Replace(f.sse, `"parts":[{"text":"ret"}]`, `"parts":[{"text":"ret"},{"functionCall":{"args":{"text":"secret"}}}]`, 1))
			}
			for i, b := range bad {
				if _, err := PluginGatewayOutputContent(f.protocol, "text/event-stream", []byte(b)); err == nil {
					t.Errorf("bad frame/state accepted %d", i)
				}
			}
			for _, b := range []string{strings.ReplaceAll(f.sse, "\n", "\r\n"), f.sse + ": keepalive\n\n", ": ping\n\nevent: ping\ndata: {\"type\":\"ping\"}\n\n" + f.sse} {
				c, err := PluginGatewayOutputContent(f.protocol, "text/event-stream", []byte(b))
				if err != nil || string(c.Data) != "secret" {
					t.Fatalf("normal keepalive rejected: %v", err)
				}
			}
		})
	}
}
func TestPluginGatewayProtocolInputAbsentAndNestedControls(t *testing.T) {
	before := []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"secret","cache_control":{"type":"ephemeral"}}]}],"stream":false,"gateway_cache":null}`)
	for _, s := range []string{
		`{"model":"m","messages":[{"role":"user","content":"clean"}],"stream":false,"gateway_cache":null}`,
		`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"clean","cache_control":{"type":"ephemeral"}}]}],"stream":false}`,
		`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"clean","cache_control":{"type":"ephemeral"}}]}],"stream":false,"gateway_cache":null,"routing":{"group_id":2}}`,
	} {
		if err := ValidatePluginGatewayInput(PluginProtocolOpenAIChat, before, []byte(s)); err == nil {
			t.Fatal("added/removed control accepted")
		}
	}
	if err := ValidatePluginGatewayInput(PluginProtocolGemini, []byte(`{"contents":[{"parts":[{"text":"x"}]}]}`), []byte(`{"model":"evil","contents":[{"parts":[{"text":"y"}]}]}`)); err == nil {
		t.Fatal("Gemini body model accepted")
	}
}
func TestPluginGatewayProtocolMalformedSemanticJSON(t *testing.T) {
	for _, f := range pgProtocolFixtures() {
		t.Run(f.name, func(t *testing.T) {
			for _, b := range []string{strings.Replace(f.json, `"sec"`, `"\ud800"`, 1), strings.Replace(f.json, `"secret"`, `"\ud800"`, 1), strings.Replace(f.json, `"9"`, `"bad"`, 1)} {
				if b == f.json {
					continue
				}
				if _, err := PluginGatewayOutputContent(f.protocol, "application/json", []byte(b)); err == nil {
					t.Errorf("malformed semantic JSON accepted: %s", b)
				}
			}
		})
	}
}
func TestPluginGatewayProtocolMultiPartChatAndAnthropicStream(t *testing.T) {
	chat := []byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":[{"type":"text","text":"sec"},{"type":"text","text":"ret"}]},"finish_reason":"stop"}],"usage":null}`)
	c, err := PluginGatewayOutputContent(PluginProtocolOpenAIChat, "application/json", chat)
	if err != nil || string(c.Data) != "secret" {
		t.Fatalf("multi-part chat: %v %q", err, c.Data)
	}
	f := pgProtocolFixtures()[2]
	s := strings.Replace(f.sse, `"text":"ret"`, `"text":""`, 1)
	extra := "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"ret\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n"
	s = strings.Replace(s, "event: message_delta", extra+"event: message_delta", 1)
	c, err = PluginGatewayOutputContent(f.protocol, "text/event-stream", []byte(s))
	if err != nil || string(c.Data) != "secret" {
		t.Fatalf("multi-block anthropic: %v %q", err, c.Data)
	}
	result, err := ApplyPluginGatewayOutputReplacement(f.protocol, "text/event-stream", []byte(s), contract.Content{MediaType: "text/plain", Data: []byte("clean")})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(result, []byte("secret")) || bytes.Contains(result, []byte(`"ret"`)) {
		t.Fatal("second block leaked")
	}
}

func TestPluginGatewayProtocolMetadataShape(t *testing.T) {
	for _, f := range pgProtocolFixtures() {
		t.Run(f.name, func(t *testing.T) {
			for _, pair := range [][2]string{{`"m"`, `42`}, {`"usage":{"total_tokens":9}`, `"usage":{"total_tokens":"9"}`}, {`"usageMetadata":{"totalTokenCount":9}`, `"usageMetadata":{"totalTokenCount":-1}`}, {`"index":0`, `"index":"0"`}, {`"stop_sequence":null`, `"stop_sequence":{}`}} {
				bad := strings.Replace(f.json, pair[0], pair[1], 1)
				if bad == f.json {
					continue
				}
				if _, err := PluginGatewayOutputContent(f.protocol, "application/json", []byte(bad)); err == nil {
					t.Errorf("malformed metadata accepted %s", pair[0])
				}
			}
			if !json.Valid([]byte(f.json)) {
				t.Fatal("invalid fixture")
			}
		})
	}
}
func TestPluginGatewayProtocolUsageExactAndNoMutation(t *testing.T) {
	for _, f := range pgProtocolFixtures() {
		for _, media := range []string{"application/json", "text/event-stream"} {
			t.Run(f.name+"/"+media, func(t *testing.T) {
				body := []byte(f.json)
				if media == "text/event-stream" {
					body = []byte(f.sse)
				}
				saved := bytes.Clone(body)
				b, err := ApplyPluginGatewayOutputReplacement(f.protocol, media, body, contract.Content{MediaType: "text/plain", Data: []byte("clean")})
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(saved, body) {
					t.Fatal("original buffer mutated")
				}
				var original map[string]any
				json.Unmarshal([]byte(f.json), &original)
				key := "usage"
				if f.name == "gemini" {
					key = "usageMetadata"
				}
				expected, _ := json.Marshal(original[key])
				var actual any
				if media == "application/json" {
					var obj map[string]any
					if err := json.Unmarshal(b, &obj); err != nil {
						t.Fatal(err)
					}
					actual = obj[key]
				} else {
					for _, line := range strings.Split(string(b), "\n") {
						if !strings.HasPrefix(line, "data: {") {
							continue
						}
						var obj map[string]any
						if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &obj); err != nil {
							t.Fatal(err)
						}
						if obj["type"] == "response.completed" {
							actual = obj["response"].(map[string]any)[key]
						} else if u, ok := obj[key]; ok {
							actual = u
						}
					}
				}
				got, _ := json.Marshal(actual)
				if !bytes.Equal(expected, got) {
					t.Fatalf("usage changed: want %s got %s", expected, got)
				}
			})
		}
	}
}

// A control event is not an uninspected payload channel when pass commits the
// original buffer. Only an empty object or its matching singleton type is valid.
func TestPluginGatewayProtocolHeartbeatRejectPayload(t *testing.T) {
	for _, f := range pgProtocolFixtures() {
		t.Run(f.name, func(t *testing.T) {
			bad := []string{
				"event: ping\ndata: {\"text\":\"PRIVATE\"}\n\n",
				"event: keepalive\ndata: {\"text\":\"PRIVATE\"}\n\n",
				"event: ping\ndata: {\"other\":42}\n\n",
				"event: keepalive\ndata: {\"other\":42}\n\n",
				"event: ping\ndata: {\"type\":\"ping\",\"text\":\"PRIVATE\"}\n\n",
				"event: keepalive\ndata: {\"type\":\"keepalive\",\"text\":\"PRIVATE\"}\n\n",
				"event: ping\ndata: {\"type\":\"keepalive\"}\n\n",
				"event: keepalive\ndata: {\"type\":\"ping\"}\n\n",
				"event: ping\ndata: [DONE]\n\n",
				"event: keepalive\ndata: [DONE]\n\n",
				"event: ping\ndata: {\"type\":null}\n\n",
				"event: keepalive\ndata: {\"type\":42}\n\n",
				"event: ping\n\n",
				"event: keepalive\n\n",
				": PRIVATE\n\n",
				": keepalive PRIVATE\n\n",
			}
			for i, control := range bad {
				t.Run(fmt.Sprintf("case_%02d", i), func(t *testing.T) {
					// Inspect both pre-terminal and post-terminal control-event positions.
					for _, body := range []string{control + f.sse, f.sse + control} {
						content, err := PluginGatewayOutputContent(f.protocol, "text/event-stream", []byte(body))
						if err == nil {
							t.Errorf("uninspected control payload accepted; extracted %q", content.Data)
						}
						if err != nil && strings.Contains(err.Error(), "PRIVATE") {
							t.Fatal("error exposes inspected payload")
						}
						replaced, err := ApplyPluginGatewayOutputReplacement(f.protocol, "text/event-stream", []byte(body), contract.Content{MediaType: "text/plain", Data: []byte("approved")})
						if err == nil || replaced != nil {
							t.Error("invalid control event allowed replacement")
						}
					}
				})
			}
		})
	}
}
func TestPluginGatewayProtocolHeartbeatNormalControls(t *testing.T) {
	controls := []string{
		":\n\n", ": ping\n\n", ": keepalive\n\n",
		"event: ping\ndata: {}\n\n",
		"event: keepalive\ndata: {}\n\n",
		"event: ping\ndata: {\"type\":\"ping\"}\n\n",
		"event: keepalive\ndata: {\"type\":\"keepalive\"}\n\n",
		"data: {\"type\":\"ping\"}\n\n",
		"data: {\"type\":\"keepalive\"}\n\n",
	}
	for _, f := range pgProtocolFixtures() {
		t.Run(f.name, func(t *testing.T) {
			for i, control := range controls {
				t.Run(fmt.Sprintf("case_%02d", i), func(t *testing.T) {
					for _, body := range []string{control + f.sse, f.sse + control, strings.ReplaceAll(control+f.sse, "\n", "\r\n")} {
						original := []byte(body)
						saved := bytes.Clone(original)
						content, err := PluginGatewayOutputContent(f.protocol, "text/event-stream", original)
						if err != nil || string(content.Data) != "secret" {
							t.Fatalf("normal heartbeat affected text: %q %v", content.Data, err)
						}
						if !bytes.Equal(original, saved) {
							t.Fatal("normal controls mutate pass buffer")
						}
						replaced, err := ApplyPluginGatewayOutputReplacement(f.protocol, "text/event-stream", original, contract.Content{MediaType: "text/plain; charset=utf-8", Data: []byte("approved")})
						if err != nil {
							t.Fatal(err)
						}
						content, err = PluginGatewayOutputContent(f.protocol, "text/event-stream", replaced)
						if err != nil || string(content.Data) != "approved" {
							t.Fatalf("replacement roundtrip: %q %v", content.Data, err)
						}
						if bytes.Contains(replaced, []byte("secret")) {
							t.Fatal("replacement retains original")
						}
					}
				})
			}
		})
	}
}
