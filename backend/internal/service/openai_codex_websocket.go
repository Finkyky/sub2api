package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync/atomic"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codexclient"
	alignedws "github.com/Wei-Shaw/sub2api/internal/pkg/codexwebsocket"
	openaiwsv2 "github.com/Wei-Shaw/sub2api/internal/service/openai_ws_v2"
	coderws "github.com/coder/websocket"
)

func (dialer *coderOpenAIWSClientDialer) dialCodexAligned(ctx context.Context, targetURL string, headers http.Header, configuredProxy string) (openAIWSClientConn, int, http.Header, error) {
	target, err := url.Parse(targetURL)
	if err != nil {
		return nil, 0, nil, err
	}
	proxyURL, err := resolveCodexWebSocketProxy(target, configuredProxy)
	if err != nil {
		return nil, 0, nil, err
	}
	client, err := dialer.proxyHTTPClientForProfile(proxyURL, true)
	if err != nil {
		return nil, 0, nil, err
	}
	headers = cloneHeader(headers)
	if headers == nil {
		headers = make(http.Header)
	}
	headers.Set("User-Agent", codexclient.UserAgent)
	headers.Set("originator", codexclient.Originator)
	headers.Set("version", codexclient.Version)
	headers.Del("Accept-Encoding")
	wrapped := &codexAlignedWSClientConn{}
	connection, response, err := alignedws.Dial(ctx, targetURL, &alignedws.DialOptions{
		HTTPClient: client, HTTPHeader: headers,
		CompressionMode:      alignedws.CompressionContextTakeover,
		CompressionThreshold: -1, CompressionClientMaxWindowBits: true, SingleFrameMessages: true,
		OnPingReceived: func(context.Context, []byte) bool {
			wrapped.upstreamPings.Add(1)
			return true
		},
	})
	if err != nil {
		var status int
		var responseHeaders http.Header
		var body []byte
		if response != nil {
			status = response.StatusCode
			responseHeaders = cloneHeader(response.Header)
			if response.Body != nil {
				body, _ = io.ReadAll(io.LimitReader(response.Body, 8<<10))
				_ = response.Body.Close()
			}
		}
		return nil, status, responseHeaders, &openAIWSHandshakeError{Body: body, Err: err}
	}
	connection.SetReadLimit(openAIWSMessageReadLimitBytes)
	wrapped.conn = connection
	return wrapped, 0, cloneHeader(response.Header), nil
}

type codexAlignedWSClientConn struct {
	conn          *alignedws.Conn
	upstreamPings atomic.Int64
}

var _ openAIWSClientConn = (*codexAlignedWSClientConn)(nil)
var _ openaiwsv2.FrameConn = (*codexAlignedWSClientConn)(nil)

func codexWSContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func normalizeCodexWSError(err error) error {
	if errors.Is(err, alignedws.ErrMessageTooBig) {
		return fmt.Errorf("%w: %w", coderws.ErrMessageTooBig, err)
	}
	var closed alignedws.CloseError
	if errors.As(err, &closed) {
		return coderws.CloseError{Code: coderws.StatusCode(closed.Code), Reason: closed.Reason}
	}
	return err
}

func (connection *codexAlignedWSClientConn) WriteJSON(ctx context.Context, value any) error {
	if connection == nil || connection.conn == nil {
		return errOpenAIWSConnClosed
	}
	var payload bytes.Buffer
	encoder := json.NewEncoder(&payload)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}
	return normalizeCodexWSError(connection.conn.Write(codexWSContext(ctx), alignedws.MessageText, bytes.TrimSuffix(payload.Bytes(), []byte("\n"))))
}

func (connection *codexAlignedWSClientConn) ReadMessage(ctx context.Context) ([]byte, error) {
	_, payload, err := connection.ReadFrame(ctx)
	return payload, err
}

func (connection *codexAlignedWSClientConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	if connection == nil || connection.conn == nil {
		return coderws.MessageText, nil, errOpenAIWSConnClosed
	}
	typeOfMessage, payload, err := connection.conn.Read(codexWSContext(ctx))
	return coderws.MessageType(typeOfMessage), payload, normalizeCodexWSError(err)
}

func (connection *codexAlignedWSClientConn) WriteFrame(ctx context.Context, typeOfMessage coderws.MessageType, payload []byte) error {
	if connection == nil || connection.conn == nil {
		return errOpenAIWSConnClosed
	}
	return normalizeCodexWSError(connection.conn.Write(codexWSContext(ctx), alignedws.MessageType(typeOfMessage), payload))
}

func (connection *codexAlignedWSClientConn) Ping(ctx context.Context) error {
	if connection == nil || connection.conn == nil {
		return errOpenAIWSConnClosed
	}
	return normalizeCodexWSError(connection.conn.Ping(codexWSContext(ctx)))
}

func (connection *codexAlignedWSClientConn) UpstreamPingCount() int64 {
	if connection == nil {
		return 0
	}
	return connection.upstreamPings.Load()
}

func (*codexAlignedWSClientConn) SupportsIdlePingWithoutReader() bool { return false }
func (*codexAlignedWSClientConn) RequiresReaderLoop() bool            { return true }

func (connection *codexAlignedWSClientConn) Close() error {
	if connection != nil && connection.conn != nil {
		_ = connection.conn.Close(alignedws.StatusNormalClosure, "")
		_ = connection.conn.CloseNow()
	}
	return nil
}

func (connection *codexAlignedWSClientConn) CloseNow() error {
	if connection != nil && connection.conn != nil {
		_ = connection.conn.CloseNow()
	}
	return nil
}
