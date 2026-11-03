package codexclient

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

type capturedHTTP2 struct {
	Settings      [][2]uint32 `json:"http2_settings"`
	WindowUpdates [][2]uint32 `json:"http2_window_updates"`
}

func TestTransportPreservesHTTP2HealthChecks(t *testing.T) {
	transport := &http.Transport{}
	require.NoError(t, ConfigureTransport(transport, nil, false))
	defer transport.CloseIdleConnections()
	require.NotNil(t, transport.HTTP2)
	require.Equal(t, 10*time.Second, transport.HTTP2.SendPingTimeout)
	require.Equal(t, 5*time.Second, transport.HTTP2.PingTimeout)
}

func captureHTTP2Exchange(connection net.Conn) (capturedHTTP2, error) {
	result := capturedHTTP2{}
	if err := connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return result, err
	}
	preface := make([]byte, len(http2.ClientPreface))
	if _, err := io.ReadFull(connection, preface); err != nil {
		return result, err
	}
	if string(preface) != http2.ClientPreface {
		return result, fmt.Errorf("unexpected HTTP/2 preface: %q", preface)
	}
	framer := http2.NewFramer(connection, connection)
	if err := framer.WriteSettings(); err != nil {
		return result, err
	}
	for {
		frame, err := framer.ReadFrame()
		if err != nil {
			return result, err
		}
		switch frame := frame.(type) {
		case *http2.SettingsFrame:
			if !frame.IsAck() {
				if err := frame.ForeachSetting(func(setting http2.Setting) error {
					result.Settings = append(result.Settings, [2]uint32{uint32(setting.ID), setting.Val})
					return nil
				}); err != nil {
					return result, err
				}
				if err := framer.WriteSettingsAck(); err != nil {
					return result, err
				}
			}
		case *http2.WindowUpdateFrame:
			result.WindowUpdates = append(result.WindowUpdates, [2]uint32{frame.StreamID, frame.Increment})
		case *http2.HeadersFrame:
			var block bytes.Buffer
			if err := hpack.NewEncoder(&block).WriteField(hpack.HeaderField{Name: ":status", Value: "204"}); err != nil {
				return result, err
			}
			err := framer.WriteHeaders(http2.HeadersFrameParam{StreamID: frame.StreamID, BlockFragment: block.Bytes(), EndStream: true, EndHeaders: true})
			return result, err
		}
	}
}

func TestTransportNegotiatesOfficialHTTP2Settings(t *testing.T) {
	fixtureBytes, err := os.ReadFile("testdata/linux-0.153.4-http.json")
	require.NoError(t, err)
	var fixture capturedHTTP2
	require.NoError(t, json.Unmarshal(fixtureBytes, &fixture))
	certificateServer := httptest.NewTLSServer(http.NotFoundHandler())
	certificateServer.Close()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: certificateServer.TLS.Certificates, NextProtos: []string{"h2"},
	})
	require.NoError(t, err)
	defer listener.Close()
	results := make(chan capturedHTTP2, 1)
	errors := make(chan error, 1)
	done := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("HTTP/2 capture fixture did not terminate")
		}
	})
	go func() {
		defer close(done)
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			errors <- acceptErr
			return
		}
		defer connection.Close()
		result, captureErr := captureHTTP2Exchange(connection)
		results <- result
		errors <- captureErr
		if captureErr == nil {
			// Drain late SETTINGS ACKs until the client closes its idle connection.
			// Closing immediately after HEADERS can reset the socket on Windows
			// before the client has read the response.
			_, _ = io.Copy(io.Discard, connection)
		}
	}()
	roots := x509.NewCertPool()
	roots.AddCert(certificateServer.Certificate())
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}
	require.NoError(t, ConfigureTransport(transport, nil, false))
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	response, err := client.Get("https://" + listener.Addr().String())
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, 2, response.ProtoMajor)
	require.Equal(t, "h2", response.TLS.NegotiatedProtocol)
	require.Equal(t, http.StatusNoContent, response.StatusCode)
	require.NoError(t, <-errors)
	require.Equal(t, fixture, <-results)
}

func TestTransportNegotiatesHTTP1FallbackAndHarvest(t *testing.T) {
	for _, test := range []struct {
		name           string
		http2, harvest bool
	}{
		{"fallback", false, false},
		{"harvest", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.WriteHeader(http.StatusNoContent)
			}))
			server.EnableHTTP2 = test.http2
			server.StartTLS()
			defer server.Close()
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			transport := &http.Transport{DisableKeepAlives: test.harvest, TLSClientConfig: &tls.Config{RootCAs: roots}}
			require.NoError(t, ConfigureTransport(transport, nil, false))
			defer transport.CloseIdleConnections()
			response, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Get(server.URL)
			require.NoError(t, err)
			defer response.Body.Close()
			require.Equal(t, 1, response.ProtoMajor)
			require.Equal(t, http.StatusNoContent, response.StatusCode)
		})
	}
}

func TestTransportNegotiatesEveryAdvertisedTLS12Cipher(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	require.NoError(t, err)
	ecdsaCertificate := tls.Certificate{Certificate: [][]byte{certificateDER}, PrivateKey: privateKey}
	for _, suite := range []uint16{
		tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
		tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
	} {
		t.Run(tls.CipherSuiteName(suite), func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.WriteHeader(http.StatusNoContent)
			}))
			server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12, CipherSuites: []uint16{suite}}
			if suite == tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384 || suite == tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256 || suite == tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256 {
				server.TLS.Certificates = []tls.Certificate{ecdsaCertificate}
			}
			server.StartTLS()
			defer server.Close()
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}
			require.NoError(t, ConfigureTransport(transport, nil, false))
			defer transport.CloseIdleConnections()
			response, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Get(server.URL)
			require.NoError(t, err)
			defer response.Body.Close()
			require.Equal(t, uint16(tls.VersionTLS12), response.TLS.Version)
			require.Equal(t, suite, response.TLS.CipherSuite)
		})
	}
}
