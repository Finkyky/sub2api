package codexclient

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
	"golang.org/x/net/proxy"
)

func ConfigureTransport(transport *http.Transport, proxyURL *url.URL, websocket bool) error {
	if transport == nil {
		return errors.New("Codex transport is nil")
	}
	if proxyURL != nil {
		if proxyURL.Hostname() == "" {
			return errors.New("Codex proxy host is empty")
		}
		copyURL := *proxyURL
		copyURL.Scheme = strings.ToLower(copyURL.Scheme)
		proxyURL = &copyURL
		switch proxyURL.Scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return errors.New("unsupported Codex proxy scheme")
		}
	}
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	baseDial := transport.DialContext
	if baseDial == nil {
		baseDial = dialer.DialContext
	}
	var tlsConfig *tls.Config
	if transport.TLSClientConfig != nil {
		tlsConfig = transport.TLSClientConfig.Clone()
		if tlsConfig.InsecureSkipVerify {
			return errors.New("Codex alignment requires TLS certificate verification")
		}
		if tlsConfig.VerifyConnection != nil || tlsConfig.GetClientCertificate != nil || len(tlsConfig.Certificates) > 0 || len(tlsConfig.CipherSuites) > 0 || tlsConfig.MinVersion > tls.VersionTLS12 || (tlsConfig.MaxVersion != 0 && tlsConfig.MaxVersion < tls.VersionTLS13) {
			return errors.New("custom TLS policy conflicts with the fixed Codex profile")
		}
	}
	timeout := transport.TLSHandshakeTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	transport.ForceAttemptHTTP2 = false
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP1(true)
	transport.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
	http1Only := websocket || transport.DisableKeepAlives
	var sessions utls.ClientSessionCache
	if !http1Only && (tlsConfig == nil || !tlsConfig.SessionTicketsDisabled) {
		sessions = newClientSessionCache()
	}
	if !http1Only {
		transport.HTTP2 = &http.HTTP2Config{
			MaxReceiveBufferPerConnection: 5177345,
			MaxReceiveBufferPerStream:     2097152,
			MaxReadFrameSize:              16384,
			SendPingTimeout:               10 * time.Second,
			PingTimeout:                   5 * time.Second,
		}
		http2Transport, err := http2.ConfigureTransports(transport)
		if err != nil {
			return err
		}
		http2Transport.MaxHeaderListSize = 16384
		http2Transport.ReadIdleTimeout = transport.HTTP2.SendPingTimeout
		http2Transport.PingTimeout = transport.HTTP2.PingTimeout
	}
	transport.DisableCompression = true
	transport.Proxy = func(request *http.Request) (*url.URL, error) {
		if request.URL.Scheme == "https" {
			return nil, nil
		}
		return proxyURL, nil
	}
	transport.DialContext = baseDial
	transport.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		handshakeContext, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		connection, err := dialTarget(handshakeContext, baseDial, proxyURL, network, address, tlsConfig)
		if err != nil {
			return nil, err
		}
		stopCancellation := context.AfterFunc(handshakeContext, func() { _ = connection.Close() })
		defer stopCancellation()
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			_ = connection.Close()
			return nil, err
		}
		settings := &utls.Config{ServerName: host, MinVersion: utls.VersionTLS12, ClientSessionCache: sessions, OmitEmptyPsk: true}
		if tlsConfig != nil {
			settings.RootCAs = tlsConfig.RootCAs
			settings.SessionTicketsDisabled = tlsConfig.SessionTicketsDisabled
			settings.VerifyPeerCertificate = tlsConfig.VerifyPeerCertificate
			if tlsConfig.ServerName != "" {
				settings.ServerName = tlsConfig.ServerName
			}
		}
		secured := utls.UClient(connection, settings, utls.HelloCustom)
		if err := secured.ApplyPreset(clientHello(websocket, http1Only)); err != nil {
			_ = connection.Close()
			return nil, fmt.Errorf("build Codex ClientHello: %w", err)
		}
		if err := secured.HandshakeContext(handshakeContext); err != nil {
			_ = connection.Close()
			return nil, err
		}
		return &tlsConnection{UConn: secured}, nil
	}
	return nil
}

type tlsConnection struct {
	*utls.UConn
}

func (connection *tlsConnection) ConnectionState() tls.ConnectionState {
	state := connection.UConn.ConnectionState()
	return tls.ConnectionState{
		Version: state.Version, HandshakeComplete: state.HandshakeComplete,
		DidResume: state.DidResume, CipherSuite: state.CipherSuite,
		NegotiatedProtocol:         state.NegotiatedProtocol,
		NegotiatedProtocolIsMutual: state.NegotiatedProtocolIsMutual,
		ServerName:                 state.ServerName, PeerCertificates: state.PeerCertificates,
		VerifiedChains:              state.VerifiedChains,
		SignedCertificateTimestamps: state.SignedCertificateTimestamps,
		OCSPResponse:                state.OCSPResponse, TLSUnique: state.TLSUnique,
		ECHAccepted: state.ECHAccepted,
	}
}

type contextDialer func(context.Context, string, string) (net.Conn, error)

func (dial contextDialer) Dial(network, address string) (net.Conn, error) {
	return dial(context.Background(), network, address)
}

func (dial contextDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return dial(ctx, network, address)
}

func dialTarget(ctx context.Context, dial contextDialer, proxyURL *url.URL, network, address string, tlsConfig *tls.Config) (net.Conn, error) {
	if proxyURL == nil {
		return dial(ctx, network, address)
	}
	if proxyURL.Scheme == "socks5" || proxyURL.Scheme == "socks5h" {
		socksDialer, err := proxy.FromURL(proxyURL, dial)
		if err != nil {
			return nil, errors.New("invalid Codex SOCKS proxy")
		}
		contextual, ok := socksDialer.(proxy.ContextDialer)
		if !ok {
			return nil, errors.New("Codex SOCKS proxy must support cancellation")
		}
		return contextual.DialContext(ctx, network, address)
	}
	port := proxyURL.Port()
	if port == "" {
		port = "80"
		if proxyURL.Scheme == "https" {
			port = "443"
		}
	}
	connection, err := dial(ctx, network, net.JoinHostPort(proxyURL.Hostname(), port))
	if err != nil {
		return nil, err
	}
	rawConnection := connection
	stopCancellation := context.AfterFunc(ctx, func() { _ = rawConnection.Close() })
	defer stopCancellation()
	success := false
	defer func() {
		if !success {
			_ = connection.Close()
		}
	}()
	if proxyURL.Scheme == "https" {
		settings := &tls.Config{ServerName: proxyURL.Hostname(), MinVersion: tls.VersionTLS12}
		if tlsConfig != nil {
			settings.RootCAs = tlsConfig.RootCAs
		}
		secured := tls.Client(connection, settings)
		if err := secured.HandshakeContext(ctx); err != nil {
			return nil, err
		}
		connection = secured
	}
	request := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: address}, Host: address, Header: make(http.Header)}
	if proxyURL.User != nil {
		password, _ := proxyURL.User.Password()
		credentials := proxyURL.User.Username() + ":" + password
		request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(credentials)))
	}
	if err := request.Write(connection); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("invalid Codex proxy CONNECT response")
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Codex proxy CONNECT returned HTTP %d", response.StatusCode)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	success = true
	return &bufferedConnection{Conn: connection, reader: reader}, nil
}

type bufferedConnection struct {
	net.Conn
	reader *bufio.Reader
}

func (connection *bufferedConnection) Read(buffer []byte) (int, error) {
	return connection.reader.Read(buffer)
}
