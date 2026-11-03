package service

import (
	"bytes"
	"testing"
)

func TestResultCacheFingerprint(t *testing.T) {
	a, err := CanonicalResultCacheJSON([]byte(`{"z":9007199254740993,"a":[" x ",1]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalResultCacheJSON([]byte(`{ "a":[" x ",1],"z":9007199254740993 }`))
	if err != nil || !bytes.Equal(a, b) {
		t.Fatalf("canonical %s %s %v", a, b, err)
	}
	for _, bad := range []string{`{"a":1,"a":2}`, `{"a":{"b":1,"b":2}}`, `{} {}`, `[1,]`} {
		if _, e := CanonicalResultCacheJSON([]byte(bad)); e == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	m := ResultCacheKeyMaterial{GroupID: 1, ScopeIdentity: "1/2", EffectiveModel: "m", EffectiveRequest: []byte(`{"messages":[{"content":"q"}]}`)}
	k, e := BuildResultCacheKey(m)
	if e != nil {
		t.Fatal(e)
	}
	m.EffectiveRequest = []byte(`{"messages":[{"content":"q "}]}`)
	other, _ := BuildResultCacheKey(m)
	if k == other {
		t.Fatal("trimmed meaningful content")
	}
	m.EffectiveRequest = []byte(`{"messages":[{"content":"q"}]}`)
	m.ScopeIdentity = "1/3"
	other, _ = BuildResultCacheKey(m)
	if k == other {
		t.Fatal("identity collision")
	}
}
