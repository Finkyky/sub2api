package service

import (
	"bytes"
	"compress/flate"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codexclient"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

type codexCapturedWSFrame struct {
	Final      bool `json:"fin"`
	Compressed bool `json:"compressed"`
	Opcode     byte `json:"opcode"`
	Masked     bool `json:"masked"`
}

type codexCapturedWSMessage struct {
	Frames    []codexCapturedWSFrame
	Payload   []byte
	Extension string
	Err       error
}

func readCodexWSMessage(reader io.Reader) ([]codexCapturedWSFrame, []byte, error) {
	var frames []codexCapturedWSFrame
	var message bytes.Buffer
	for {
		var header [2]byte
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			return nil, nil, err
		}
		length := uint64(header[1] & 127)
		if length == 126 {
			var extended [2]byte
			if _, err := io.ReadFull(reader, extended[:]); err != nil {
				return nil, nil, err
			}
			length = uint64(binary.BigEndian.Uint16(extended[:]))
		} else if length == 127 {
			var extended [8]byte
			if _, err := io.ReadFull(reader, extended[:]); err != nil {
				return nil, nil, err
			}
			length = binary.BigEndian.Uint64(extended[:])
		}
		if length > 1<<20 {
			return nil, nil, fmt.Errorf("oversized test frame")
		}
		frame := codexCapturedWSFrame{Final: header[0]&128 != 0, Compressed: header[0]&64 != 0, Opcode: header[0] & 15, Masked: header[1]&128 != 0}
		var mask [4]byte
		if frame.Masked {
			if _, err := io.ReadFull(reader, mask[:]); err != nil {
				return nil, nil, err
			}
		}
		payload := make([]byte, int(length))
		if _, err := io.ReadFull(reader, payload); err != nil {
			return nil, nil, err
		}
		if frame.Masked {
			for index := range payload {
				payload[index] ^= mask[index%4]
			}
		}
		frames = append(frames, frame)
		message.Write(payload)
		if frame.Final {
			break
		}
	}
	payload := message.Bytes()
	if frames[0].Compressed {
		compressed := append(append([]byte(nil), payload...), 0, 0, 255, 255, 1, 0, 0, 255, 255)
		inflater := flate.NewReader(bytes.NewReader(compressed))
		defer inflater.Close()
		decoded, err := io.ReadAll(inflater)
		return frames, decoded, err
	}
	return frames, payload, nil
}

func TestCodexLinuxWebSocketMessageMatchesOfficialCapture(t *testing.T) {
	SetCodexClientProfile(codexclient.Profile)
	t.Cleanup(func() { SetCodexClientProfile("") })
	fixtureBytes, err := os.ReadFile("../pkg/codexclient/testdata/linux-0.153.4-websocket-message.json")
	require.NoError(t, err)
	var fixture struct {
		Headers map[string]string `json:"headers"`
		Message struct {
			Extension       string                 `json:"server_extension"`
			Frames          []codexCapturedWSFrame `json:"frames"`
			TrailingNewline bool                   `json:"trailing_newline"`
		} `json:"websocket_message"`
	}
	require.NoError(t, json.Unmarshal(fixtureBytes, &fixture))
	for _, extension := range []string{"permessage-deflate", fixture.Message.Extension} {
		t.Run(extension, func(t *testing.T) {
			captured := make(chan codexCapturedWSMessage, 2)
			proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				connection, buffered, hijackErr := writer.(http.Hijacker).Hijack()
				if hijackErr != nil {
					captured <- codexCapturedWSMessage{Err: hijackErr}
					return
				}
				defer connection.Close()
				_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
				digest := sha1.Sum([]byte(request.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
				_, _ = fmt.Fprintf(buffered, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: %s\r\nSec-WebSocket-Extensions: %s\r\n\r\n", base64.StdEncoding.EncodeToString(digest[:]), extension)
				_ = buffered.Flush()
				for range 2 {
					frames, payload, readErr := readCodexWSMessage(buffered)
					captured <- codexCapturedWSMessage{Frames: frames, Payload: payload, Extension: request.Header.Get("Sec-WebSocket-Extensions"), Err: readErr}
					if readErr != nil {
						return
					}
				}
			}))
			defer proxy.Close()
			dialer := newDefaultOpenAIWSClientDialer()
			connection, _, _, err := dialer.Dial(t.Context(), "ws://chatgpt.com/backend-api/codex/responses", nil, proxy.URL)
			require.NoError(t, err)
			t.Cleanup(func() {
				if closer, ok := connection.(interface{ CloseNow() error }); ok {
					_ = closer.CloseNow()
				} else {
					_ = connection.Close()
				}
			})
			value := map[string]any{"type": "response.create", "input": "ping"}
			require.NoError(t, connection.WriteJSON(t.Context(), value))
			select {
			case actual := <-captured:
				require.NoError(t, actual.Err)
				require.Equal(t, fixture.Headers["sec-websocket-extensions"], actual.Extension)
				require.Equal(t, fixture.Message.Frames, actual.Frames)
				require.Equal(t, fixture.Message.TrailingNewline, bytes.HasSuffix(actual.Payload, []byte("\n")))
				expected, err := json.Marshal(value)
				require.NoError(t, err)
				require.JSONEq(t, string(expected), string(actual.Payload))
			case <-time.After(6 * time.Second):
				t.Fatal("no WebSocket message captured")
			}
			framed, ok := connection.(*codexAlignedWSClientConn)
			require.True(t, ok)
			require.NoError(t, framed.WriteFrame(t.Context(), coderws.MessageText, nil))
			select {
			case empty := <-captured:
				require.NoError(t, empty.Err)
				require.Equal(t, fixture.Message.Frames, empty.Frames)
				require.Empty(t, empty.Payload)
			case <-time.After(6 * time.Second):
				t.Fatal("no empty WebSocket frame captured")
			}
		})
	}
}
