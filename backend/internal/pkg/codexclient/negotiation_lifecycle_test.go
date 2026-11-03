package codexclient

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
)

// The capture helper must leave the connection open so the fixture can drain
// in-flight client frames before the client closes its idle connection.
func TestCaptureHTTP2ExchangeLeavesConnectionOpenForClientShutdown(t *testing.T) {
	server, client := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	require.NoError(t, client.SetDeadline(time.Now().Add(5*time.Second)))

	captured := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer server.Close()
		_, err := captureHTTP2Exchange(server)
		captured <- err
		if err == nil {
			_, _ = io.Copy(io.Discard, server)
		}
	}()

	_, err := io.WriteString(client, http2.ClientPreface)
	require.NoError(t, err)
	framer := http2.NewFramer(client, client)
	frame, err := framer.ReadFrame()
	require.NoError(t, err)
	require.IsType(t, &http2.SettingsFrame{}, frame)
	require.NoError(t, framer.WriteSettings())
	frame, err = framer.ReadFrame()
	require.NoError(t, err)
	require.True(t, frame.(*http2.SettingsFrame).IsAck())
	require.NoError(t, framer.WriteHeaders(http2.HeadersFrameParam{
		StreamID: 1, EndStream: true, EndHeaders: true,
	}))
	frame, err = framer.ReadFrame()
	require.NoError(t, err)
	require.IsType(t, &http2.HeadersFrame{}, frame)
	require.NoError(t, <-captured)

	// This frame is written after the response, matching the SETTINGS ACK race
	// on Windows. A fixture-owned early Close makes this fail deterministically.
	require.NoError(t, framer.WriteSettingsAck())
	require.NoError(t, client.Close())
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("fixture did not terminate after the client closed")
	}
}
