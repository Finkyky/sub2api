package service

import (
	"net/http"
	"strings"
	"testing"
)

func TestResultCacheProtocol(t *testing.T) {
	h := http.Header{"X-Gateway-Cache-Protocol": []string{"result-cache"}}
	in := []byte(`{"model":"m","messages":[{"role":"user","content":"q"}],"prompt_cache_key":"keep","gateway_cache":{"mode":"read_only","scope":"user"}}`)
	intent, body, err := ParseResultCacheIntent(h, in)
	if err != nil || !intent.Declared || intent.Mode != "read_only" || intent.Scope != "user" || strings.Contains(string(body), "gateway_cache") || !strings.Contains(string(body), "prompt_cache_key") {
		t.Fatalf("parse: %+v %s %v", intent, body, err)
	}
	for _, bad := range []string{`{"gateway_cache":{"mode":"bad"}}`, `{"gateway_cache":{"scope":"tenant"}}`, `{"gateway_cache":{"key":"inject"}}`, `{"gateway_cache":true}`, `{"gateway_cache":{"mode":"read_only","mode":"bypass"}}`} {
		if _, _, e := ParseResultCacheIntent(h, []byte(bad)); e == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	if _, _, e := ParseResultCacheIntent(http.Header{}, in); e == nil {
		t.Fatal("control without opt-in accepted")
	}
	intent, body, err = ParseResultCacheIntent(http.Header{}, []byte(`{"prompt_cache_key":"keep"}`))
	if err != nil || intent.Declared || string(body) != `{"prompt_cache_key":"keep"}` {
		t.Fatal("upstream hint opted in")
	}
}
func TestResultCacheEligibility(t *testing.T) {
	for _, body := range []string{`{"stream":true}`, `{"tools":[{}]}`, `{"functions":[{}]}`, `{"previous_response_id":"x"}`, `{"messages":[{"content":[{"type":"image_url","image_url":{}}]}]}`} {
		if ResultCacheRequestEligible([]byte(body)) {
			t.Fatalf("eligible %s", body)
		}
	}
	if !ResultCacheRequestEligible([]byte(`{"model":"m","messages":[{"role":"user","content":"question"}],"stream":false}`)) {
		t.Fatal("text not eligible")
	}
}
