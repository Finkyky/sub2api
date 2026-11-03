package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"net"
	"strings"
	"testing"
	"time"
)

func TestResultCacheStore(t *testing.T) {
	m := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: m.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	s := NewResultCacheStore(rdb)
	t.Cleanup(func() { _ = s.(interface{ Close() error }).Close() })
	ctx := context.Background()
	key := "result-cache:v1:" + strings.Repeat("a", 64)
	now := time.Now().UTC()
	entry := &service.ResultCacheEntry{Version: 1, CreatedAt: now, ExpiresAt: now.Add(30 * time.Second), Body: []byte(`{"choices":[]}`), PolicyVersion: "v", Model: "m"}
	ok, e := s.Acquire(ctx, key, "owner", time.Second)
	if e != nil || !ok {
		t.Fatal(ok, e)
	}
	if ok, e = s.Publish(ctx, key, "wrong", entry); e != nil || ok {
		t.Fatal("wrong owner publish", ok, e)
	}
	if ok, e = s.Publish(ctx, key, "owner", entry); e != nil || !ok {
		t.Fatal("publish", ok, e)
	}
	got, e := s.Get(ctx, key)
	if e != nil || got == nil || string(got.Body) != string(entry.Body) {
		t.Fatal(got, e)
	}
	before := m.TTL(key)
	_, _ = s.Get(ctx, key)
	if m.TTL(key) != before {
		t.Fatal("hit renewed ttl")
	}
	m.FastForward(31 * time.Second)
	got, e = s.Get(ctx, key)
	if e != nil || got != nil {
		t.Fatal("expired", got, e)
	}
}
func TestResultCacheLeaseOwnership(t *testing.T) {
	m := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: m.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	s := NewResultCacheStore(rdb)
	t.Cleanup(func() { _ = s.(interface{ Close() error }).Close() })
	ctx := context.Background()
	key := "result-cache:v1:" + strings.Repeat("b", 64)
	ok, e := s.Acquire(ctx, key, "old", time.Second)
	if e != nil || !ok {
		t.Fatal(ok, e)
	}
	m.FastForward(2 * time.Second)
	ok, e = s.Acquire(ctx, key, "new", time.Second)
	if e != nil || !ok {
		t.Fatal(ok, e)
	}
	_ = s.Release(ctx, key, "old")
	ok, e = s.Renew(ctx, key, "new", time.Second)
	if e != nil || !ok {
		t.Fatal("old released new lease")
	}
	entry := &service.ResultCacheEntry{Version: 1, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), Body: []byte(`{}`)}
	ok, e = s.Publish(ctx, key, "old", entry)
	if e != nil || ok {
		t.Fatal("stale published", ok, e)
	}
	m.Set(key, "corrupt")
	got, e := s.Get(ctx, key)
	if e != nil || got != nil {
		t.Fatal("corrupt served", got, e)
	}
}

func TestResultCacheStoreIOBudget(t *testing.T) {
	// A TCP endpoint accepts the connection but never responds, including HELLO.
	// This reproduces a slow Redis socket rather than a mock context deadline.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	finished := make(chan struct{})
	accepted := make(chan net.Conn, 1)
	t.Cleanup(func() {
		select {
		case conn := <-accepted:
			_ = conn.Close()
		default:
		}
	})
	go func() {
		defer close(finished)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		accepted <- conn
		buffer := make([]byte, 1024)
		for {
			if _, err = conn.Read(buffer); err != nil {
				return
			}
		}
	}()
	rdb := redis.NewClient(&redis.Options{Addr: listener.Addr().String(), ReadTimeout: time.Second, WriteTimeout: time.Second, MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	store := NewResultCacheStore(rdb)
	if closer, ok := store.(interface{ Close() error }); ok {
		t.Cleanup(func() { _ = closer.Close() })
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = store.Get(ctx, "result-cache:v1:deadline")
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("unresponsive Redis returned success")
	}
	if elapsed > 250*time.Millisecond {
		t.Fatalf("cache I/O exceeded 25ms budget: %s", elapsed)
	}
	if rdb.Options().ContextTimeoutEnabled || rdb.Options().ReadTimeout != time.Second {
		t.Fatal("shared Redis configuration was modified")
	}
	if closer, ok := store.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
	select {
	case conn := <-accepted:
		_ = conn.Close()
	case <-time.After(time.Second):
		t.Fatal("slow endpoint did not accept the connection")
	}
	<-finished
}

func TestResultCacheStoreOwnedClient(t *testing.T) {
	m := miniredis.RunT(t)
	shared := redis.NewClient(&redis.Options{Addr: m.Addr()})
	t.Cleanup(func() { _ = shared.Close() })
	store := NewResultCacheStore(shared).(*resultCacheStore)
	if store.rdb == shared || !store.rdb.Options().ContextTimeoutEnabled || store.rdb.Options().MaxRetries != 0 {
		t.Fatal("cache client is not independently bounded")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := shared.Ping(context.Background()).Err(); err != nil {
		t.Fatal("closing cache also closed shared client", err)
	}
}
