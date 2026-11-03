package sample

import (
	"context"
	"encoding/json"
	general "github.com/Wei-Shaw/sub2api/pkg/pluginapi/general/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"strings"
	"testing"
	"time"
)

func configured(t *testing.T, name, config string) *Server {
	t.Helper()
	s, err := New(name)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Configure(context.Background(), &general.ConfigureRequest{InstanceId: 7, ConfigRevision: 2, ConfigVersion: 1, ConfigJson: []byte(config)})
	if err != nil || r == nil || !r.Accepted || r.InstanceId != 7 || r.ConfigRevision != 2 {
		t.Fatalf("configure: %v %v", r, err)
	}
	return s
}
func hook(entry, stage, protocol, media, text string) *general.HookRequest {
	return &general.HookRequest{InstanceId: 7, ConfigRevision: 2, Entry: entry, Stage: stage, Protocol: protocol, Content: &general.Content{MediaType: media, Data: []byte(text)}}
}
func TestCredentialFourProtocolTextAndRedaction(t *testing.T) {
	s := configured(t, "credential-input", `{}`)
	secret := "sk-abcdefghijklmnop123456"
	cases := map[string]string{
		"openai.chat_completions.v1": `{"model":"m","messages":[{"role":"user","content":"` + secret + `"}]}`,
		"openai.responses.v1":        `{"model":"m","input":[{"role":"user","content":[{"type":"input_text","text":"` + secret + `"}]}]}`,
		"anthropic.messages.v1":      `{"model":"m","max_tokens":10,"system":[{"type":"text","text":"` + secret + `"}],"messages":[{"role":"user","content":"hello"}]}`,
		"gemini.generate_content.v1": `{"contents":[{"role":"user","parts":[{"text":"` + secret + `"}]}]}`,
	}
	for proto, body := range cases {
		t.Run(proto, func(t *testing.T) {
			r, e := s.Check(context.Background(), hook("credential-input", "input", proto, "application/json", body))
			if e != nil || r.Decision != "block" || r.Rejection == nil || len(r.Findings) != 1 {
				t.Fatalf("%v %v", r, e)
			}
			b, _ := json.Marshal(r)
			if strings.Contains(string(b), secret) || r.Content != nil {
				t.Fatal("secret escaped")
			}
		})
	}
	r, e := s.Check(context.Background(), hook("credential-input", "input", "openai.responses.v1", "application/json", `{"model":"sk-abcdefghijklmnop123456","input":"hello"}`))
	if e != nil || r.Decision != "pass" {
		t.Fatalf("metadata inspected: %v %v", r, e)
	}
}
func TestCredentialCustomRuleAndEmptyRulesRejected(t *testing.T) {
	s := configured(t, "credential-input", `{"config_version":1,"rules":[{"id":"custom","version":"2","pattern":"SECRET[0-9]+"}]}`)
	r, e := s.Check(context.Background(), hook("credential-input", "input", "openai.responses.v1", "application/json", `{"input":"SECRET42"}`))
	if e != nil || r.Decision != "block" || r.Findings[0].RuleVersion != "2" {
		t.Fatalf("%v %v", r, e)
	}
	s, _ = New("credential-input")
	_, e = s.Configure(context.Background(), &general.ConfigureRequest{InstanceId: 7, ConfigRevision: 2, ConfigVersion: 1, ConfigJson: []byte(`{"rules":[]}`)})
	if status.Code(e) != codes.InvalidArgument {
		t.Fatalf("%v", e)
	}
}
func TestBusinessKeywordThresholdNormalizedOutput(t *testing.T) {
	s := configured(t, "business-output", `{"rules":[{"id":"sales","version":"3","keywords":["guaranteed","profit"],"weight":0.6}],"threshold":1}`)
	for _, proto := range []string{"openai.chat_completions.v1", "openai.responses.v1", "anthropic.messages.v1", "gemini.generate_content.v1"} {
		for _, tc := range []struct{ text, decision string }{{"a profit discussion", "pass"}, {"GUARANTEED profit", "block"}, {"neutral", "pass"}} {
			r, e := s.Check(context.Background(), hook("business-output", "output", proto, "text/plain; charset=utf-8", tc.text))
			if e != nil || r.Decision != tc.decision || r.Content != nil {
				t.Fatalf("%v %v", r, e)
			}
		}
	}
	_, e := s.Check(context.Background(), hook("business-output", "output", "openai.responses.v1", "text/event-stream", "data: secret\n\n"))
	if status.Code(e) != codes.InvalidArgument {
		t.Fatalf("raw SSE accepted: %v", e)
	}
}
func TestConfigureStrictImmutableBinding(t *testing.T) {
	for _, cfg := range []string{`{"unknown":"secret"}`, `{"config_version":2}`, `{"threshold":0}`, `{"threshold":1,"threshold":2}`, `{} {}`, `null`, `{"rules":[{"id":"secret value","keywords":["x"],"weight":1}]}`} {
		s, _ := New("business-output")
		_, e := s.Configure(context.Background(), &general.ConfigureRequest{InstanceId: 7, ConfigRevision: 2, ConfigVersion: 1, ConfigJson: []byte(cfg)})
		if status.Code(e) != codes.InvalidArgument || strings.Contains(e.Error(), "secret") {
			t.Fatalf("%s: %v", cfg, e)
		}
	}
	s := configured(t, "business-output", `{}`)
	_, e := s.Configure(context.Background(), &general.ConfigureRequest{InstanceId: 7, ConfigRevision: 3, ConfigVersion: 1, ConfigJson: []byte(`{}`)})
	if status.Code(e) != codes.FailedPrecondition {
		t.Fatal(e)
	}
	req := hook("business-output", "output", "openai.responses.v1", "text/plain", "hello")
	req.ConfigRevision = 3
	_, e = s.Check(context.Background(), req)
	if status.Code(e) != codes.FailedPrecondition {
		t.Fatal(e)
	}
}
func TestHookLimitsCancellationAndShape(t *testing.T) {
	s := configured(t, "credential-input", `{}`)
	for _, body := range []string{`null`, `{"input":"a","input":"b"}`, `{"input":42}`, `{"metadata":{"text":"hi"}}`, `{"input":"\ud800"}`} {
		_, e := s.Check(context.Background(), hook("credential-input", "input", "openai.responses.v1", "application/json", body))
		if status.Code(e) != codes.InvalidArgument {
			t.Fatalf("accepted %q %v", body, e)
		}
	}
	req := hook("credential-input", "input", "openai.responses.v1", "application/json", strings.Repeat("x", MaxContentBytes+1))
	_, e := s.Check(context.Background(), req)
	if status.Code(e) != codes.ResourceExhausted {
		t.Fatal(e)
	}
	req = hook("credential-input", "input", "openai.responses.v1", "application/json", `{"input":"hello"}`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e = s.Check(ctx, req)
	if status.Code(e) != codes.Canceled {
		t.Fatal(e)
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, e = s.Check(ctx, req)
	if status.Code(e) != codes.DeadlineExceeded {
		t.Fatal(e)
	}
	req.Stage = "output"
	_, e = s.Check(context.Background(), req)
	if status.Code(e) != codes.InvalidArgument {
		t.Fatal(e)
	}
}

const pricingConfigFixture = `{"source_url":"https://prices.example.test/v1","rules":[{"channel_id":10,"pricing_id":20,"source_key":"model-a","field":"input_price","multiplier":2,"max_price":10,"max_change_ratio":3}]}`
const envelope = `{"input":{},"host":{"kv":{},"network":[{"url":"https://prices.example.test/v1","body":{"prices":{"model-a":{"input_price":2}}}}],"pricing":[{"channel_id":10,"pricing_id":20,"fields":{"input_price":1,"output_price":null}}]}}`

func task(body string) *general.TaskRequest {
	return &general.TaskRequest{InstanceId: 7, ConfigRevision: 2, Entry: "pricing-sync", InputJson: []byte(body)}
}
func TestPricingAuthorizedProposalAndNoop(t *testing.T) {
	s := configured(t, "pricing-sync", pricingConfigFixture)
	r, e := s.RunTask(context.Background(), task(envelope))
	if e != nil || r.Status != "succeeded" || !r.Changed || r.ErrorCode != "" {
		t.Fatalf("%v %v", r, e)
	}
	var out struct {
		PriceUpdates []struct {
			ChannelID int64              `json:"channel_id"`
			PricingID int64              `json:"pricing_id"`
			Fields    map[string]float64 `json:"fields"`
		} `json:"price_updates"`
		KVUpdates []any `json:"kv_updates"`
	}
	if e = json.Unmarshal(r.OutputJson, &out); e != nil || len(out.PriceUpdates) != 1 || out.PriceUpdates[0].Fields["input_price"] != 4 || len(out.KVUpdates) != 0 {
		t.Fatalf("%s %v", r.OutputJson, e)
	}
	r, e = s.RunTask(context.Background(), task(strings.Replace(envelope, `"input_price":1`, `"input_price":4`, 1)))
	if e != nil || r.Status != "succeeded" || r.Changed {
		t.Fatalf("noop %v %v", r, e)
	}
}
func TestPricingMissingUnscopedInvalidNeverInventsPrice(t *testing.T) {
	s := configured(t, "pricing-sync", pricingConfigFixture)
	for _, body := range []string{`{"input":{},"host":{"kv":{},"network":[],"pricing":[]}}`, strings.Replace(envelope, `"prices":{"model-a":{"input_price":2}}`, `"prices":{}`, 1), strings.Replace(envelope, `"pricing_id":20`, `"pricing_id":21`, 1), strings.Replace(envelope, `"input_price":2`, `"input_price":100`, 1), strings.Replace(envelope, `"input_price":2`, `"input_price":null`, 1), strings.Replace(envelope, `"input_price":1`, `"input_price":0`, 1), strings.Replace(envelope, `"fields":{"input_price":1,`, `"fields":{`, 1)} {
		r, e := s.RunTask(context.Background(), task(body))
		if e != nil || r.Status != "failed" || r.ErrorCode == "" || r.Changed || len(r.OutputJson) != 0 {
			t.Fatalf("unsafe result: %v %v", r, e)
		}
	}
	for _, body := range []string{`{}`, `{"input":{},"host":{},"token":"secret"}`, strings.Replace(envelope, `"input_price":2`, `"input_price":2,"input_price":3`, 1)} {
		_, e := s.RunTask(context.Background(), task(body))
		if status.Code(e) != codes.InvalidArgument || strings.Contains(e.Error(), "secret") {
			t.Fatal(e)
		}
	}
	_, e := s.RunTask(context.Background(), task(strings.Repeat("x", MaxJSONBytes+1)))
	if status.Code(e) != codes.ResourceExhausted {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e = s.RunTask(ctx, task(envelope))
	if status.Code(e) != codes.Canceled {
		t.Fatal(e)
	}
}
func TestUnsupportedMethodsFailClosed(t *testing.T) {
	s := configured(t, "business-output", `{}`)
	_, e := s.RunTask(context.Background(), task(envelope))
	if status.Code(e) != codes.Unimplemented {
		t.Fatal(e)
	}
	s = configured(t, "pricing-sync", pricingConfigFixture)
	_, e = s.Check(context.Background(), hook("business-output", "output", "openai.responses.v1", "text/plain", "a"))
	if status.Code(e) != codes.Unimplemented {
		t.Fatal(e)
	}
}

func TestPricingStaticOfflineAndNetworkPriority(t *testing.T) {
	s := configured(t, "pricing-sync", pricingConfigFixture)
	offline := `{"input":{"prices":{"model-a":{"input_price":1.2}}},"host":{"kv":{},"network":[],"pricing":[{"channel_id":10,"pricing_id":20,"fields":{"input_price":1}}]}}`
	r, e := s.RunTask(context.Background(), task(offline))
	if e != nil || r.Status != "succeeded" || !r.Changed || !strings.Contains(string(r.OutputJson), `"input_price":2.4`) {
		t.Fatalf("static %v %v", r, e)
	}
	online := strings.Replace(envelope, `"input":{}`, `"input":{"prices":{"model-a":{"input_price":1.2}}}`, 1)
	r, e = s.RunTask(context.Background(), task(online))
	if e != nil || r.Status != "succeeded" || !strings.Contains(string(r.OutputJson), `"input_price":4`) {
		t.Fatalf("priority %v %v", r, e)
	}
	r, e = s.RunTask(context.Background(), task(strings.Replace(online, `"prices":{"model-a":{"input_price":2}}`, `"prices":{}`, 1)))
	if e != nil || r.Status != "failed" || r.ErrorCode != "pricing_data_missing" || len(r.OutputJson) != 0 {
		t.Fatalf("silent fallback %v %v", r, e)
	}
	for _, bad := range []string{`null`, `[]`, `{}`, `{"model-a":{"input_price":null}}`} {
		body := strings.Replace(offline, `{"model-a":{"input_price":1.2}}`, bad, 1)
		r, e = s.RunTask(context.Background(), task(body))
		if e != nil || r.Status != "failed" || r.ErrorCode == "" || len(r.OutputJson) != 0 {
			t.Fatalf("invalid offline %v %v", r, e)
		}
	}
}

func TestPricingConfigMatchesHostSupportedFields(t *testing.T) {
	for _, field := range []string{"cache_write_1h_price", "image_input_price", "image_output_price", "unknown"} {
		s, _ := New("pricing-sync")
		_, e := s.Configure(context.Background(), &general.ConfigureRequest{InstanceId: 7, ConfigRevision: 2, ConfigVersion: 1, ConfigJson: []byte(strings.Replace(pricingConfigFixture, "input_price", field, 1))})
		if status.Code(e) != codes.InvalidArgument {
			t.Fatalf("unsupported field %s accepted %v", field, e)
		}
	}
}
func TestConfigureNullAndIncompleteRulesRejected(t *testing.T) {
	for _, cfg := range []string{`{"config_version":null}`, `{"threshold":null}`, `{"rules":[{"id":"new","keywords":["x"],"weight":1}]}`} {
		s, _ := New("business-output")
		_, e := s.Configure(context.Background(), &general.ConfigureRequest{InstanceId: 7, ConfigRevision: 2, ConfigVersion: 1, ConfigJson: []byte(cfg)})
		if status.Code(e) != codes.InvalidArgument {
			t.Fatalf("accepted %s: %v", cfg, e)
		}
	}
}
