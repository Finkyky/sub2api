package codexclient

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
	"github.com/stretchr/testify/require"
)

func TestClientSessionCacheSingleUseAndCapacity(t *testing.T) {
	cache := newClientSessionCache()
	state := &utls.ClientSessionState{}
	state.SetVers(utls.VersionTLS13)
	cache.Put("one-use", state)
	var found atomic.Int32
	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() {
			if _, ok := cache.Get("one-use"); ok {
				found.Add(1)
			}
		})
	}
	workers.Wait()
	require.Equal(t, int32(1), found.Load())
	legacy := &utls.ClientSessionState{}
	legacy.SetVers(utls.VersionTLS12)
	cache.Put("legacy", legacy)
	for range 2 {
		actual, ok := cache.Get("legacy")
		require.True(t, ok)
		require.Same(t, legacy, actual)
	}
	cache.Put("legacy", nil)
	_, ok := cache.Get("legacy")
	require.False(t, ok)
	for index := 0; index < 257; index++ {
		cache.Put(fmt.Sprint(index), state)
	}
	_, ok = cache.Get("0")
	require.False(t, ok)
	_, ok = cache.Get("256")
	require.True(t, ok)
}

func TestClientSessionCacheDiscardMissingOrConsumedState(t *testing.T) {
	cache := newClientSessionCache()
	cache.Put("missing", nil)
	require.NotPanics(t, func() {
		state, found := cache.Get("missing")
		require.Nil(t, state)
		require.False(t, found)
	})
	state := &utls.ClientSessionState{}
	state.SetVers(utls.VersionTLS13)
	cache.Put("consumed", state)
	_, found := cache.Get("consumed")
	require.True(t, found)
	cache.Put("consumed", nil)
	require.NotPanics(t, func() {
		actual, found := cache.Get("consumed")
		require.Nil(t, actual)
		require.False(t, found)
	})
	cache.cache.Put("invalid", nil)
	require.NotPanics(t, func() {
		actual, found := cache.Get("invalid")
		require.Nil(t, actual)
		require.False(t, found)
	})
}

func TestTransportTLS12SessionResumption(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Connection", "close")
		_, _ = io.WriteString(writer, "reply")
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}
	require.NoError(t, ConfigureTransport(transport, nil, false))
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	for attempt := 0; attempt < 3; attempt++ {
		response, err := client.Get(server.URL)
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, response.Body)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		require.Equal(t, uint16(tls.VersionTLS12), response.TLS.Version)
		require.Equal(t, attempt > 0, response.TLS.DidResume)
	}
}
