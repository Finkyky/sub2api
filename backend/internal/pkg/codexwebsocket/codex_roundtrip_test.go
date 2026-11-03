//go:build !js

package websocket_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	websocket "github.com/Wei-Shaw/sub2api/internal/pkg/codexwebsocket"
	"github.com/Wei-Shaw/sub2api/internal/pkg/codexwebsocket/internal/test/wstest"
	"github.com/stretchr/testify/require"
)

func TestCodexAlignedMessagesPreserveContextAcrossWriteAPIs(t *testing.T) {
	for _, mode := range []websocket.CompressionMode{websocket.CompressionContextTakeover, websocket.CompressionNoContextTakeover} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			client, server := wstest.Pipe(&websocket.DialOptions{
				CompressionMode: websocket.CompressionContextTakeover, CompressionClientMaxWindowBits: true,
				SingleFrameMessages: true, CompressionThreshold: 1,
			}, &websocket.AcceptOptions{CompressionMode: mode})
			require.NotNil(t, client)
			require.NotNil(t, server)
			defer client.CloseNow()
			defer server.CloseNow()
			client.SetReadLimit(128 << 10)
			server.SetReadLimit(128 << 10)
			finished := make(chan error, 1)
			go func() {
				for {
					typeOfMessage, payload, err := server.Read(ctx)
					if err != nil {
						if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
							err = nil
						}
						finished <- err
						return
					}
					if err := server.Write(ctx, typeOfMessage, payload); err != nil {
						finished <- err
						return
					}
				}
			}()
			for index := 0; index < 6; index++ {
				payload := bytes.Repeat([]byte("Codex compressed message <> & "), 2048)
				if index == 0 {
					payload = []byte("small")
				}
				if index%2 == 0 {
					require.NoError(t, client.Write(ctx, websocket.MessageText, payload))
				} else {
					writer, err := client.Writer(ctx, websocket.MessageText)
					require.NoError(t, err)
					_, err = writer.Write(payload)
					require.NoError(t, err)
					require.NoError(t, writer.Close())
				}
				typeOfMessage, actual, err := client.Read(ctx)
				require.NoError(t, err)
				require.Equal(t, websocket.MessageText, typeOfMessage)
				require.Equal(t, payload, actual)
			}
			require.NoError(t, client.Close(websocket.StatusNormalClosure, ""))
			require.NoError(t, <-finished)
		})
	}
}
