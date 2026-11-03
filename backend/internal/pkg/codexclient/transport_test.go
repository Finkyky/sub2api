package codexclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/cryptobyte"
)

type capturedHello struct {
	LegacyVersion       uint16             `json:"legacy_version"`
	RecordVersion       uint16             `json:"record_version"`
	SessionIDSize       int                `json:"session_id_size"`
	CipherSuites        []uint16           `json:"cipher_suites"`
	Extensions          []uint16           `json:"extensions"`
	PointFormats        []byte             `json:"point_formats"`
	SupportedGroups     []uint16           `json:"supported_groups"`
	SignatureAlgorithms []uint16           `json:"signature_algorithms"`
	SupportedVersions   []uint16           `json:"supported_versions"`
	PSKModes            []byte             `json:"psk_modes"`
	KeyShares           []capturedKeyShare `json:"key_shares"`
	ALPN                []string           `json:"alpn"`
}

type capturedKeyShare struct {
	Group uint16 `json:"group"`
	Size  int    `json:"size"`
}

func parseHello(t *testing.T, record []byte) capturedHello {
	t.Helper()
	result := capturedHello{RecordVersion: binary.BigEndian.Uint16(record[1:3])}
	input := cryptobyte.String(record[9:])
	require.True(t, input.ReadUint16(&result.LegacyVersion))
	require.True(t, input.Skip(32))
	var session, values, extensions cryptobyte.String
	require.True(t, input.ReadUint8LengthPrefixed(&session))
	result.SessionIDSize = len(session)
	require.True(t, input.ReadUint16LengthPrefixed(&values))
	result.CipherSuites = parseUint16s(t, values)
	require.True(t, input.ReadUint8LengthPrefixed(&values))
	require.True(t, input.ReadUint16LengthPrefixed(&extensions))
	for !extensions.Empty() {
		var identifier uint16
		require.True(t, extensions.ReadUint16(&identifier))
		require.True(t, extensions.ReadUint16LengthPrefixed(&values))
		result.Extensions = append(result.Extensions, identifier)
		var vector cryptobyte.String
		switch identifier {
		case 10, 13, 51, 16:
			require.True(t, values.ReadUint16LengthPrefixed(&vector))
		case 11, 43, 45:
			require.True(t, values.ReadUint8LengthPrefixed(&vector))
		}
		switch identifier {
		case 10:
			result.SupportedGroups = parseUint16s(t, vector)
		case 13:
			result.SignatureAlgorithms = parseUint16s(t, vector)
		case 43:
			result.SupportedVersions = parseUint16s(t, vector)
		case 11:
			result.PointFormats = append([]byte(nil), vector...)
		case 45:
			result.PSKModes = append([]byte(nil), vector...)
		case 51:
			for !vector.Empty() {
				var group uint16
				var key cryptobyte.String
				require.True(t, vector.ReadUint16(&group))
				require.True(t, vector.ReadUint16LengthPrefixed(&key))
				result.KeyShares = append(result.KeyShares, capturedKeyShare{Group: group, Size: len(key)})
			}
		case 16:
			for !vector.Empty() {
				var protocol cryptobyte.String
				require.True(t, vector.ReadUint8LengthPrefixed(&protocol))
				result.ALPN = append(result.ALPN, string(protocol))
			}
		}
	}
	return result
}

func parseUint16s(t *testing.T, values cryptobyte.String) []uint16 {
	t.Helper()
	var result []uint16
	for !values.Empty() {
		var value uint16
		require.True(t, values.ReadUint16(&value))
		result = append(result, value)
	}
	return result
}

func TestClientHelloMatchesOfficialLinuxCapture(t *testing.T) {
	for _, websocket := range []bool{false, true} {
		name := "http"
		if websocket {
			name = "websocket"
		}
		t.Run(name, func(t *testing.T) {
			fixtureBytes, err := os.ReadFile("testdata/linux-0.153.4-" + name + ".json")
			require.NoError(t, err)
			var fixture struct {
				Hello capturedHello `json:"tls_client_hello"`
			}
			require.NoError(t, json.Unmarshal(fixtureBytes, &fixture))
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			defer listener.Close()
			records := make(chan []byte, 1)
			go func() {
				connection, acceptErr := listener.Accept()
				if acceptErr != nil {
					return
				}
				defer connection.Close()
				_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
				header := make([]byte, 5)
				if _, readErr := io.ReadFull(connection, header); readErr != nil {
					return
				}
				body := make([]byte, binary.BigEndian.Uint16(header[3:5]))
				if _, readErr := io.ReadFull(connection, body); readErr != nil {
					return
				}
				records <- append(header, body...)
			}()
			transport := &http.Transport{TLSHandshakeTimeout: time.Second}
			require.NoError(t, ConfigureTransport(transport, nil, websocket))
			defer transport.CloseIdleConnections()
			_, port, err := net.SplitHostPort(listener.Addr().String())
			require.NoError(t, err)
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			_, err = client.Get("https://localhost:" + port)
			require.Error(t, err)
			select {
			case record := <-records:
				actual := parseHello(t, record)
				require.ElementsMatch(t, fixture.Hello.Extensions, actual.Extensions)
				actual.Extensions = fixture.Hello.Extensions
				require.Equal(t, fixture.Hello, actual)
			case <-time.After(3 * time.Second):
				t.Fatal("no ClientHello captured")
			}
		})
	}
}

func TestTransportVerifiesCertificatesAndPreservesHTTPPolicy(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Empty(t, request.Header.Get("Accept-Encoding"))
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	for _, websocket := range []bool{false, true} {
		transport := &http.Transport{DisableKeepAlives: true, TLSHandshakeTimeout: time.Second}
		require.NoError(t, ConfigureTransport(transport, nil, websocket))
		client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
		_, err := client.Get(server.URL)
		require.Error(t, err)
		transport.CloseIdleConnections()
		roots := x509.NewCertPool()
		roots.AddCert(server.Certificate())
		trusted := &http.Transport{DisableKeepAlives: true, TLSClientConfig: &tls.Config{RootCAs: roots}}
		require.NoError(t, ConfigureTransport(trusted, nil, websocket))
		require.True(t, trusted.DisableKeepAlives)
		require.False(t, trusted.ForceAttemptHTTP2)
		client.Transport = trusted
		response, err := client.Get(server.URL)
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, response.StatusCode)
		require.NoError(t, response.Body.Close())
		trusted.CloseIdleConnections()
	}
}

func TestTransportHonorsCancelledContext(t *testing.T) {
	transport := &http.Transport{}
	require.NoError(t, ConfigureTransport(transport, nil, false))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := transport.DialTLSContext(ctx, "tcp", "127.0.0.1:1")
	require.ErrorIs(t, err, context.Canceled)
}
