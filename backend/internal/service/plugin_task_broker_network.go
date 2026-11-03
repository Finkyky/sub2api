package service

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const (
	PluginTaskNetworkURLLimit  = 8
	PluginTaskNetworkBodyLimit = 256 * 1024
	PluginTaskNetworkTimeout   = 5 * time.Second
)

var pluginTaskDeniedNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("172.16.0.0/12"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("224.0.0.0/4"), netip.MustParsePrefix("240.0.0.0/4"),
	// Permit ordinary global IPv6 only, excluding translation/tunneling ranges.
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
}

func pluginTaskPublicIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return false
	}
	if addr.Is6() && !netip.MustParsePrefix("2000::/3").Contains(addr) {
		return false
	}
	for _, p := range pluginTaskDeniedNetworks {
		if p.Contains(addr) {
			return false
		}
	}
	return true
}
func validatePluginTaskNetworkURL(raw string) (*url.URL, error) {
	if len(raw) > 2048 {
		return nil, ErrPluginTaskCapability
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || (u.Port() != "" && u.Port() != "443") {
		return nil, ErrPluginTaskCapability
	}
	host := u.Hostname()
	lower := strings.ToLower(strings.TrimSuffix(host, "."))
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") || strings.Contains(host, "%") || strings.ContainsAny(host, "\\\x00\r\n") {
		return nil, ErrPluginTaskCapability
	}
	if literal := net.ParseIP(host); literal != nil && !pluginTaskPublicIP(literal) {
		return nil, ErrPluginTaskCapability
	}
	return u, nil
}
func (b *PluginTaskCapabilityBroker) readNetwork(ctx context.Context, raw string) (json.RawMessage, error) {
	u, err := validatePluginTaskNetworkURL(raw)
	if err != nil {
		return nil, err
	}
	host := u.Hostname()
	ctx, cancel := context.WithTimeout(ctx, PluginTaskNetworkTimeout)
	defer cancel()
	var ips []net.IPAddr
	if literal := net.ParseIP(host); literal != nil {
		ips = []net.IPAddr{{IP: literal}}
	} else {
		ips, err = b.lookupIP(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("%w: DNS lookup", ErrPluginTaskCapability)
		}
	}
	if len(ips) == 0 || len(ips) > 16 {
		return nil, ErrPluginTaskCapability
	}
	for _, ip := range ips {
		if ip.Zone != "" || !pluginTaskPublicIP(ip.IP) {
			return nil, ErrPluginTaskCapability
		}
	}
	// Resolve once, validate ALL answers, and dial only pinned public IPs. No
	// default DNS dial, redirect, proxy, credentials, cookie jar, or caller headers.
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}, TLSHandshakeTimeout: 2 * time.Second, ResponseHeaderTimeout: 3 * time.Second, MaxResponseHeaderBytes: 16 * 1024, DisableKeepAlives: true, DisableCompression: true}
	transport.DialContext = func(dctx context.Context, network, address string) (net.Conn, error) {
		h, p, e := net.SplitHostPort(address)
		if e != nil || h != host || p != "443" {
			return nil, ErrPluginTaskCapability
		}
		var last error
		for _, ip := range ips {
			conn, e := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(dctx, "tcp", net.JoinHostPort(ip.IP.String(), "443"))
			if e == nil {
				return conn, nil
			}
			last = e
		}
		return nil, last
	}
	defer transport.CloseIdleConnections()
	var rt http.RoundTripper = transport
	if b.testRoundTripper != nil {
		rt = b.testRoundTripper
	}
	client := &http.Client{Transport: rt, Timeout: PluginTaskNetworkTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, ErrPluginTaskCapability
	}
	// Never inherit proxy settings, default transport or application auth headers.
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: HTTPS read", ErrPluginTaskCapability)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 || response.ContentLength > PluginTaskNetworkBodyLimit {
		return nil, ErrPluginTaskCapability
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, PluginTaskNetworkBodyLimit+1))
	if err != nil || len(body) > PluginTaskNetworkBodyLimit || !json.Valid(body) {
		return nil, ErrPluginTaskInvalid
	}
	return json.RawMessage(body), nil
}
