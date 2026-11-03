package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type rcHandlerPolicy struct{}

func (rcHandlerPolicy) Current(context.Context, int64) (service.ResultCachePolicy, error) {
	return service.ResultCachePolicy{Active: true, Version: "v1", Config: service.GroupResponseResultCacheConfig{Enabled: true, TTLSeconds: 30, MaxResponseBytes: 2 << 20, Revision: 1}}, nil
}
func TestResultCacheChatMissHit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	s := service.NewResultCacheService(repository.NewResultCacheStore(rdb), rcHandlerPolicy{}, nil, nil)
	defer s.Stop()
	gid := int64(7)
	key := &service.APIKey{ID: 3, GroupID: &gid, UserID: 2}
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"question"}],"stream":false}`)
	intent := service.ResultCacheIntent{Declared: true, Mode: "read_write", Scope: "user"}
	calls := 0
	execute := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		capture, hit, e := beginChatResultCache(c, s, key, intent, body, "m")
		if e != nil {
			t.Fatal(e)
		}
		if hit {
			return rec
		}
		defer capture.finish()
		calls++
		capture.session.NoteUpstream("m")
		c.JSON(http.StatusOK, gin.H{"id": "chatcmpl-upstream", "model": "m", "choices": []any{gin.H{"message": gin.H{"role": "assistant", "content": "same answer"}, "finish_reason": "stop"}}})
		return rec
	}
	a := execute()
	b := execute()
	if calls != 1 || a.Header().Get("X-Gateway-Cache-Status") != "MISS" || b.Header().Get("X-Gateway-Cache-Status") != "HIT" {
		t.Fatalf("calls=%d headers %v %v", calls, a.Header(), b.Header())
	}
	var aa, bb map[string]any
	_ = json.Unmarshal(a.Body.Bytes(), &aa)
	_ = json.Unmarshal(b.Body.Bytes(), &bb)
	if aa["id"] == bb["id"] {
		t.Fatal("reused completion id")
	}
}
func TestResultCacheChatBoundedCapture(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/", nil)
	w := newResultCacheWriter(c.Writer, 4)
	_, _ = w.Write([]byte("12345"))
	w.commit()
	if rec.Body.String() != "12345" || !w.overflow {
		t.Fatalf("overflow %s %v", rec.Body.String(), w.overflow)
	}
}

type rcChatMutablePolicy struct{ policy service.ResultCachePolicy }

func (p *rcChatMutablePolicy) Current(context.Context, int64) (service.ResultCachePolicy, error) {
	return p.policy, nil
}

type rcChatAccessRecorder struct{ records []service.ResultCacheAccess }

func (a *rcChatAccessRecorder) Append(_ context.Context, r service.ResultCacheAccess) error {
	a.records = append(a.records, r)
	return nil
}
func (a *rcChatAccessRecorder) Cleanup(context.Context, time.Time) error { return nil }

type rcChatFixture struct {
	service *service.ResultCacheService
	redis   *miniredis.Miniredis
	policy  *rcChatMutablePolicy
	access  *rcChatAccessRecorder
	calls   int
}

func newRCChatFixture(t *testing.T) *rcChatFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	p := &rcChatMutablePolicy{policy: service.ResultCachePolicy{Active: true, Version: "v1", Config: service.GroupResponseResultCacheConfig{Enabled: true, TTLSeconds: 30, MaxResponseBytes: 2 << 20, Revision: 1}}}
	access := &rcChatAccessRecorder{}
	s := service.NewResultCacheService(repository.NewResultCacheStore(rdb), p, access, nil)
	t.Cleanup(s.Stop)
	return &rcChatFixture{service: s, redis: mr, policy: p, access: access}
}
func rcChatKey(user, key, group int64) *service.APIKey {
	return &service.APIKey{ID: key, UserID: user, GroupID: &group}
}
func (f *rcChatFixture) execute(t *testing.T, key *service.APIKey, mode, scope, finish string, beforeFinish func()) *httptest.ResponseRecorder {
	t.Helper()
	fields := map[string]any{"model": "m", "messages": []any{map[string]any{"role": "user", "content": "same question"}}, "stream": false, "prompt_cache_key": "upstream-key", "cache_control": map[string]any{"type": "ephemeral"}}
	if mode != "" {
		fields["gateway_cache"] = map[string]any{"mode": mode, "scope": scope}
	}
	body, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(body)))
	if mode != "" {
		c.Request.Header.Set("X-Gateway-Cache-Protocol", "result-cache")
		c.Request.Header.Set("X-Gateway-Cache-Untrusted", "not-forwarded")
	}
	intent, forward, err := service.ParseResultCacheIntent(c.Request.Header, body)
	if err != nil {
		t.Fatal(err)
	}
	stripChatCacheHeaders(c)
	capture, hit, err := beginChatResultCache(c, f.service, key, intent, forward, "m")
	if err != nil {
		c.String(http.StatusForbidden, "%s", err)
		return rec
	}
	if hit {
		return rec
	}
	if capture != nil {
		defer capture.finish()
	}
	f.calls++
	if strings.Contains(string(forward), "gateway_cache") || c.GetHeader("X-Gateway-Cache-Protocol") != "" || c.GetHeader("X-Gateway-Cache-Untrusted") != "" {
		t.Fatal("gateway controls reached upstream")
	}
	var upstream map[string]json.RawMessage
	_ = json.Unmarshal(forward, &upstream)
	if string(upstream["prompt_cache_key"]) != `"upstream-key"` || string(upstream["cache_control"]) != `{"type":"ephemeral"}` {
		t.Fatal("upstream cache fields were stripped")
	}
	if capture != nil {
		capture.session.NoteUpstream("m")
	}
	c.JSON(http.StatusOK, gin.H{"id": fmt.Sprintf("chatcmpl-%d", f.calls), "model": "m", "created": 12345, "choices": []any{gin.H{"message": gin.H{"role": "assistant", "content": fmt.Sprintf("answer-%d", f.calls)}, "finish_reason": finish}}, "usage": gin.H{"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18}})
	if beforeFinish != nil {
		beforeFinish()
	}
	return rec
}
func rcChatStatus(rec *httptest.ResponseRecorder) string {
	return rec.Result().Header.Get("X-Gateway-Cache-Status")
}
func TestResultCacheChatModeMatrix(t *testing.T) {
	t.Run("no declaration", func(t *testing.T) {
		f := newRCChatFixture(t)
		key := rcChatKey(1, 2, 3)
		a := f.execute(t, key, "", "", "stop", nil)
		b := f.execute(t, key, "", "", "stop", nil)
		if f.calls != 2 || rcChatStatus(a) != "" || rcChatStatus(b) != "" || len(f.access.records) != 0 {
			t.Fatal("undeclared request used cache")
		}
		t.Log("NO_DECLARATION upstream_calls=2 access_records=0")
	})
	t.Run("read only miss never fills", func(t *testing.T) {
		f := newRCChatFixture(t)
		key := rcChatKey(1, 2, 3)
		a := f.execute(t, key, "read_only", "user", "stop", nil)
		b := f.execute(t, key, "read_only", "user", "stop", nil)
		if f.calls != 2 || rcChatStatus(a) != "MISS" || rcChatStatus(b) != "MISS" {
			t.Fatal("read-only miss filled cache")
		}
	})
	t.Run("read write hit write only refresh bypass", func(t *testing.T) {
		f := newRCChatFixture(t)
		key := rcChatKey(1, 2, 3)
		a := f.execute(t, key, "read_write", "user", "stop", nil)
		b := f.execute(t, key, "read_only", "user", "stop", nil)
		c := f.execute(t, key, "write_only", "user", "stop", nil)
		d := f.execute(t, key, "read_write", "user", "stop", nil)
		e := f.execute(t, key, "bypass", "user", "stop", nil)
		if f.calls != 3 || rcChatStatus(a) != "MISS" || rcChatStatus(b) != "HIT" || rcChatStatus(c) != "UPDATED" || rcChatStatus(d) != "HIT" || rcChatStatus(e) != "BYPASS" {
			t.Fatalf("calls=%d statuses=%s/%s/%s/%s/%s", f.calls, rcChatStatus(a), rcChatStatus(b), rcChatStatus(c), rcChatStatus(d), rcChatStatus(e))
		}
		if !strings.Contains(d.Body.String(), "answer-2") {
			t.Fatal("refresh did not replace entry")
		}
		var original, replay map[string]json.RawMessage
		_ = json.Unmarshal(a.Body.Bytes(), &original)
		_ = json.Unmarshal(b.Body.Bytes(), &replay)
		for _, name := range []string{"created", "choices", "usage", "model"} {
			if string(original[name]) != string(replay[name]) {
				t.Fatal("replay changed", name)
			}
		}
		if string(original["id"]) == string(replay["id"]) || a.Header().Get("X-Gateway-Request-Id") == b.Header().Get("X-Gateway-Request-Id") {
			t.Fatal("hit reused request identity")
		}
		if b.Header().Get("X-Gateway-Cache-TTL") != "30" || b.Header().Get("X-Gateway-Cache-Age") == "" {
			t.Fatal("hit metadata missing")
		}
		hit := f.access.records[1]
		if hit.UpstreamCalled || hit.Status != "HIT" || hit.SourceRequestID == "" || hit.RequestID == hit.SourceRequestID {
			t.Fatal("hit access metadata incorrect", hit)
		}
		t.Log("MODE_MATRIX MISS/HIT/UPDATED/HIT/BYPASS upstream_calls=3 HIT_upstream_called=false")
	})
}
func TestResultCacheChatIsolationAndSharing(t *testing.T) {
	t.Run("user api key and group isolation", func(t *testing.T) {
		f := newRCChatFixture(t)
		for _, key := range []*service.APIKey{rcChatKey(1, 2, 3), rcChatKey(4, 2, 3), rcChatKey(1, 5, 3), rcChatKey(1, 2, 6)} {
			rec := f.execute(t, key, "read_write", "user", "stop", nil)
			if rcChatStatus(rec) != "MISS" {
				t.Fatal("cross-identity hit", key)
			}
		}
		hit := f.execute(t, rcChatKey(1, 2, 3), "read_write", "user", "stop", nil)
		if f.calls != 4 || rcChatStatus(hit) != "HIT" {
			t.Fatal("own entry not reusable")
		}
	})
	t.Run("group sharing requires both approvals", func(t *testing.T) {
		f := newRCChatFixture(t)
		denied := f.execute(t, rcChatKey(1, 2, 3), "read_write", "group", "stop", nil)
		if denied.Code != http.StatusForbidden || f.calls != 0 {
			t.Fatal("unapproved sharing called upstream")
		}
		f.policy.policy.Config.AllowGroupSharing = true
		a := f.execute(t, rcChatKey(1, 2, 3), "read_write", "group", "stop", nil)
		b := f.execute(t, rcChatKey(4, 5, 3), "read_write", "group", "stop", nil)
		c := f.execute(t, rcChatKey(4, 5, 6), "read_write", "group", "stop", nil)
		if f.calls != 2 || rcChatStatus(a) != "MISS" || rcChatStatus(b) != "HIT" || rcChatStatus(c) != "MISS" {
			t.Fatal("group scope boundary failed")
		}
	})
}
func TestResultCacheChatDisableAndTTL(t *testing.T) {
	f := newRCChatFixture(t)
	key := rcChatKey(1, 2, 3)
	_ = f.execute(t, key, "read_write", "user", "stop", nil)
	f.policy.policy.Config.Enabled = false
	f.policy.policy.Version = "disabled"
	disabled := f.execute(t, key, "read_write", "user", "stop", nil)
	if rcChatStatus(disabled) != "BYPASS" || f.calls != 2 {
		t.Fatal("disabled group read old entry")
	}
	f.policy.policy.Config.Enabled = true
	f.policy.policy.Version = "v2"
	inflight := f.execute(t, key, "read_write", "user", "stop", func() { f.policy.policy.Config.Enabled = false; f.policy.policy.Version = "disabled-again" })
	if rcChatStatus(inflight) != "BYPASS" {
		t.Fatal("disabled in-flight producer published")
	}
	f.policy.policy.Config.Enabled = true
	f.policy.policy.Version = "v3"
	first := f.execute(t, key, "read_write", "user", "stop", nil)
	hit := f.execute(t, key, "read_write", "user", "stop", nil)
	f.redis.FastForward(31 * time.Second)
	expired := f.execute(t, key, "read_write", "user", "stop", nil)
	if rcChatStatus(first) != "MISS" || rcChatStatus(hit) != "HIT" || rcChatStatus(expired) != "MISS" || f.calls != 5 {
		t.Fatalf("disable/TTL calls=%d statuses=%s/%s/%s", f.calls, rcChatStatus(first), rcChatStatus(hit), rcChatStatus(expired))
	}
	t.Log("DISABLE inflight=BYPASS REENABLE=MISS HIT=HIT EXPIRED=MISS upstream_calls=5")
}
func TestResultCacheChatIncompleteNeverFills(t *testing.T) {
	for _, finish := range []string{"length", "content_filter", "tool_calls"} {
		t.Run(finish, func(t *testing.T) {
			f := newRCChatFixture(t)
			key := rcChatKey(1, 2, 3)
			a := f.execute(t, key, "read_write", "user", finish, nil)
			b := f.execute(t, key, "read_write", "user", finish, nil)
			if a.Code != 200 || b.Code != 200 || rcChatStatus(a) != "UNCACHEABLE" || rcChatStatus(b) != "UNCACHEABLE" || f.calls != 2 {
				t.Fatal("incomplete result was cached")
			}
		})
	}
}
