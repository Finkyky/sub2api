package codexclient

import (
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
)

type helloRecordingListener struct {
	net.Listener
	records chan []byte
}
type helloRecordingConn struct {
	net.Conn
	records  chan []byte
	hello    []byte
	recorded bool
}

func (listener helloRecordingListener) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &helloRecordingConn{Conn: connection, records: listener.records}, nil
}

func (connection *helloRecordingConn) Read(buffer []byte) (int, error) {
	count, err := connection.Conn.Read(buffer)
	if !connection.recorded {
		connection.hello = append(connection.hello, buffer[:count]...)
		if len(connection.hello) >= 5 {
			length := 5 + int(binary.BigEndian.Uint16(connection.hello[3:5]))
			if len(connection.hello) >= length {
				connection.records <- append([]byte(nil), connection.hello[:length]...)
				connection.recorded = true
				connection.hello = nil
			}
		}
	}
	return count, err
}

func TestTransportTLS13ReconnectMatchesOfficialCapture(t *testing.T) {
	fixtureBytes, err := os.ReadFile("testdata/linux-0.153.4-resumption.json")
	require.NoError(t, err)
	var fixture struct {
		Connections []struct {
			Hello  capturedHello `json:"tls_client_hello"`
			Reused bool          `json:"session_reused"`
		} `json:"connections"`
	}
	require.NoError(t, json.Unmarshal(fixtureBytes, &fixture))
	records := make(chan []byte, 4)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Connection", "close")
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(writer, "offline retry")
	}))
	server.Listener = helloRecordingListener{Listener: server.Listener, records: records}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: server.Certificate().DNSNames[0]}}
	require.NoError(t, ConfigureTransport(transport, nil, false))
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	for _, expected := range fixture.Connections {
		response, err := client.Get(server.URL)
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, response.Body)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		require.Equal(t, expected.Reused, response.TLS.DidResume)
		actual := parseHello(t, <-records)
		require.ElementsMatch(t, expected.Hello.Extensions, actual.Extensions)
		if expected.Reused {
			require.Equal(t, uint16(41), actual.Extensions[len(actual.Extensions)-1])
		}
		actual.Extensions = expected.Hello.Extensions
		require.Equal(t, expected.Hello, actual)
	}
}

func TestTransportResumptionIsIsolatedAndHarvestDisabled(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Connection", "close")
		_, _ = io.WriteString(writer, "reply")
	}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	for _, test := range []struct {
		name                         string
		websocket, harvest, disabled bool
		wantResume                   bool
	}{
		{"first_pool", false, false, false, true}, {"isolated_second_pool", false, false, false, true},
		{"harvest", false, true, false, false}, {"websocket", true, false, false, false}, {"disabled", false, false, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := &http.Transport{DisableKeepAlives: test.harvest, TLSClientConfig: &tls.Config{RootCAs: roots, SessionTicketsDisabled: test.disabled}}
			require.NoError(t, ConfigureTransport(transport, nil, test.websocket))
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
			for attempt := 0; attempt < 2; attempt++ {
				response, err := client.Get(server.URL)
				require.NoError(t, err)
				_, err = io.Copy(io.Discard, response.Body)
				require.NoError(t, err)
				require.NoError(t, response.Body.Close())
				require.Equal(t, attempt == 1 && test.wantResume, response.TLS.DidResume)
			}
		})
	}
}
