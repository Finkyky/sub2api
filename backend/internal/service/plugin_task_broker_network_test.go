//go:build unit

package service

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

type capabilityRoundTripper func(*http.Request) (*http.Response, error)

func (f capabilityRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func networkBroker(ips ...string) *PluginTaskCapabilityBroker {
	b := NewPluginTaskCapabilityBroker(nil, nil)
	b.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		var out []net.IPAddr
		for _, ip := range ips {
			out = append(out, net.IPAddr{IP: net.ParseIP(ip)})
		}
		return out, nil
	}
	return b
}
func TestPluginTaskCapabilityNetworkPolicy(t *testing.T) {
	for _, u := range []string{"http://example.com", "https://user:pass@example.com", "https://localhost", "https://127.0.0.1", "https://[::1]", "https://example.com:8443", "https://example.com/#x"} {
		t.Run(u, func(t *testing.T) {
			_, err := networkBroker("8.8.8.8").readNetwork(context.Background(), u)
			require.Error(t, err)
		})
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.169.254", "::1", "fc00::1", "fe80::1", "100.64.0.1", "0.0.0.0", "224.0.0.1", "192.0.2.1", "2001:db8::1", "::ffff:127.0.0.1"} {
		t.Run(ip, func(t *testing.T) {
			b := networkBroker("8.8.8.8", ip)
			b.testRoundTripper = capabilityRoundTripper(func(*http.Request) (*http.Response, error) {
				t.Fatal("forbidden DNS reached transport")
				return nil, nil
			})
			_, err := b.readNetwork(context.Background(), "https://example.com/prices")
			require.Error(t, err)
		})
	}
}
func TestPluginTaskCapabilityNetworkGETBoundsAndRedirect(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		status     int
		ok         bool
	}{{"json", `{"USD":1}`, 200, true}, {"redirect", `{}`, 302, false}, {"nonjson", "text", 200, false}, {"large", strings.Repeat(" ", PluginTaskNetworkBodyLimit+1), 200, false}, {"error", `{}`, 500, false}} {
		t.Run(tt.name, func(t *testing.T) {
			b := networkBroker("8.8.8.8")
			calls := 0
			b.testRoundTripper = capabilityRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "GET", r.Method)
				require.Empty(t, r.Header.Get("Authorization"))
				require.Empty(t, r.Header.Get("Cookie"))
				require.Nil(t, r.Body)
				return &http.Response{StatusCode: tt.status, Header: http.Header{"Location": []string{"https://127.0.0.1"}}, Body: io.NopCloser(strings.NewReader(tt.body)), Request: r}, nil
			})
			body, err := b.readNetwork(context.Background(), "https://example.com/prices")
			if tt.ok {
				require.NoError(t, err)
				require.JSONEq(t, tt.body, string(body))
			} else {
				require.Error(t, err)
			}
			require.Equal(t, 1, calls)
		})
	}
	b := networkBroker("8.8.8.8")
	b.testRoundTripper = capabilityRoundTripper(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := b.readNetwork(ctx, "https://example.com")
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrPluginTaskCapability))
}

func TestPluginTaskCapabilityNetworkSingleResolution(t *testing.T) {
	b := networkBroker()
	calls := 0
	b.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		calls++
		if calls == 1 {
			return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	b.testRoundTripper = capabilityRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}, Request: r}, nil
	})
	_, err := b.readNetwork(context.Background(), "https://example.com")
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}
