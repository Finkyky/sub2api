package codexclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func relayConnections(client, upstream net.Conn) {
	defer client.Close()
	defer upstream.Close()
	finished := make(chan struct{})
	go func() {
		_, _ = io.Copy(upstream, client)
		_ = upstream.Close()
		close(finished)
	}()
	_, _ = io.Copy(client, upstream)
	_ = client.Close()
	<-finished
}

func TestTransportRoutesThroughHTTPAndHTTPSConnect(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Empty(t, request.Header.Get("Proxy-Authorization"))
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	for _, secureProxy := range []bool{false, true} {
		var connections atomic.Int64
		proxyServer := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			require.Equal(t, http.MethodConnect, request.Method)
			require.Equal(t, "Basic dXNlcjpwYXNz", request.Header.Get("Proxy-Authorization"))
			upstream, err := net.DialTimeout("tcp", request.Host, time.Second)
			if err != nil {
				writer.WriteHeader(502)
				return
			}
			client, buffered, err := writer.(http.Hijacker).Hijack()
			if err != nil {
				_ = upstream.Close()
				return
			}
			connections.Add(1)
			_, _ = buffered.WriteString("HTTP/1.1 200 Connection established\r\n\r\n")
			_ = buffered.Flush()
			relayConnections(client, upstream)
		}))
		if secureProxy {
			proxyServer.StartTLS()
		} else {
			proxyServer.Start()
		}
		proxyURL, err := url.Parse(proxyServer.URL)
		require.NoError(t, err)
		proxyURL.User = url.UserPassword("user", "pass")
		roots := x509.NewCertPool()
		roots.AddCert(target.Certificate())
		if secureProxy {
			roots.AddCert(proxyServer.Certificate())
		}
		for _, websocket := range []bool{false, true} {
			transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}
			require.NoError(t, ConfigureTransport(transport, proxyURL, websocket))
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			response, err := client.Get(target.URL)
			require.NoError(t, err)
			require.Equal(t, http.StatusNoContent, response.StatusCode)
			require.NoError(t, response.Body.Close())
			transport.CloseIdleConnections()
		}
		require.EqualValues(t, 2, connections.Load())
		proxyServer.Close()
	}
}

func TestTransportCancelsStalledProxyHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
		_, _ = io.Copy(io.Discard, connection)
	}()
	proxyURL, err := url.Parse("http://" + listener.Addr().String())
	require.NoError(t, err)
	transport := &http.Transport{}
	require.NoError(t, ConfigureTransport(transport, proxyURL, false))
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err = transport.DialTLSContext(ctx, "tcp", "example.invalid:443")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("proxy connection leaked after cancellation")
	}
}

func TestTransportRoutesThroughSOCKS(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(204) }))
	defer target.Close()
	for _, scheme := range []string{"socks5", "socks5h"} {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		finished := make(chan error, 1)
		go func() {
			connection, err := listener.Accept()
			if err != nil {
				finished <- err
				return
			}
			defer connection.Close()
			_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
			prefix := make([]byte, 2)
			if _, err = io.ReadFull(connection, prefix); err != nil {
				finished <- err
				return
			}
			methods := make([]byte, prefix[1])
			_, _ = io.ReadFull(connection, methods)
			_, _ = connection.Write([]byte{5, 0})
			header := make([]byte, 4)
			_, _ = io.ReadFull(connection, header)
			var host string
			switch header[3] {
			case 1:
				address := make([]byte, 4)
				_, _ = io.ReadFull(connection, address)
				host = net.IP(address).String()
			case 3:
				length := make([]byte, 1)
				_, _ = io.ReadFull(connection, length)
				address := make([]byte, length[0])
				_, _ = io.ReadFull(connection, address)
				host = string(address)
			}
			portBytes := make([]byte, 2)
			_, _ = io.ReadFull(connection, portBytes)
			address := &net.TCPAddr{IP: net.ParseIP(host), Port: int(binary.BigEndian.Uint16(portBytes))}
			upstream, err := net.DialTimeout("tcp", address.String(), time.Second)
			if err != nil {
				finished <- err
				return
			}
			_, _ = connection.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
			relayConnections(connection, upstream)
			finished <- nil
		}()
		proxyURL, err := url.Parse(scheme + "://" + listener.Addr().String())
		require.NoError(t, err)
		roots := x509.NewCertPool()
		roots.AddCert(target.Certificate())
		transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}
		require.NoError(t, ConfigureTransport(transport, proxyURL, false))
		response, err := (&http.Client{Transport: transport, Timeout: 3 * time.Second}).Get(target.URL)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		transport.CloseIdleConnections()
		require.NoError(t, <-finished)
		_ = listener.Close()
	}
}

func TestTransportRejectsWeakenedTLSAndRedactsProxyErrors(t *testing.T) {
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	require.Error(t, ConfigureTransport(transport, nil, false))
	proxyURL, err := url.Parse("ftp://user:secret@localhost:1234")
	require.NoError(t, err)
	err = ConfigureTransport(&http.Transport{}, proxyURL, false)
	require.Error(t, err)
	require.False(t, strings.Contains(err.Error(), "secret"))
}
