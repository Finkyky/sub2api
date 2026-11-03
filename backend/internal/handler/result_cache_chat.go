package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"time"
)

// A bounded non-streaming writer preserves failover Size/Written semantics.
// It never captures SSE/WebSocket traffic. On overflow it becomes passthrough.
type resultCacheWriter struct {
	gin.ResponseWriter
	buf                          bytes.Buffer
	limit, status                int
	written, overflow, committed bool
	size                         int
}

func newResultCacheWriter(w gin.ResponseWriter, limit int) *resultCacheWriter {
	return &resultCacheWriter{ResponseWriter: w, limit: limit, status: http.StatusOK, size: -1}
}
func (w *resultCacheWriter) WriteHeader(status int) {
	if !w.written && status > 0 {
		w.status = status
	}
}
func (w *resultCacheWriter) WriteHeaderNow() {
	w.written = true
	if w.size < 0 {
		w.size = 0
	}
}
func (w *resultCacheWriter) Status() int   { return w.status }
func (w *resultCacheWriter) Size() int     { return w.size }
func (w *resultCacheWriter) Written() bool { return w.written }
func (w *resultCacheWriter) Write(p []byte) (int, error) {
	w.WriteHeaderNow()
	w.size += len(p)
	if w.committed {
		return w.ResponseWriter.Write(p)
	}
	if w.buf.Len()+len(p) > w.limit {
		w.overflow = true
		w.Header().Set("X-Gateway-Cache-Status", "UNCACHEABLE")
		w.commit()
		return w.ResponseWriter.Write(p)
	}
	return w.buf.Write(p)
}
func (w *resultCacheWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *resultCacheWriter) Flush() {
	w.overflow = true
	w.Header().Set("X-Gateway-Cache-Status", "UNCACHEABLE")
	w.commit()
	w.ResponseWriter.Flush()
}
func (w *resultCacheWriter) commit() {
	if w.committed {
		return
	}
	w.committed = true
	w.ResponseWriter.WriteHeader(w.status)
	if w.buf.Len() > 0 {
		_, _ = w.ResponseWriter.Write(w.buf.Bytes())
		w.buf.Reset()
	} else {
		w.ResponseWriter.WriteHeaderNow()
	}
}

type chatResultCacheCapture struct {
	c        *gin.Context
	session  *service.ResultCacheSession
	writer   *resultCacheWriter
	original gin.ResponseWriter
}

func setChatCacheHeaders(c *gin.Context, q *service.ResultCacheSession) {
	c.Header("X-Gateway-Cache-Protocol", "result-cache")
	c.Header("X-Gateway-Cache-Status", q.Status)
	if q.Status == "HIT" && q.Entry != nil { // Age is based on original generation, never extended by a hit.
		c.Header("X-Gateway-Cache-Age", fmt.Sprint(resultCacheAge(q.Entry)))
		c.Header("X-Gateway-Cache-TTL", fmt.Sprint(int64(q.Entry.ExpiresAt.Sub(q.Entry.CreatedAt).Seconds())))
	}
}
func stripChatCacheHeaders(c *gin.Context) {
	for k := range c.Request.Header {
		if strings.HasPrefix(strings.ToLower(k), "x-gateway-cache-") {
			c.Request.Header.Del(k)
		}
	}
}
func beginChatResultCache(c *gin.Context, s *service.ResultCacheService, key *service.APIKey, intent service.ResultCacheIntent, body []byte, model string) (*chatResultCacheCapture, bool, error) {
	if !intent.Declared {
		return nil, false, nil
	}
	gid := int64(0)
	if key.GroupID != nil {
		gid = *key.GroupID
	}
	headers := map[string]string{}
	for _, name := range []string{"Anthropic-Version", "Anthropic-Beta", "OpenAI-Beta", "OpenAI-Organization", "OpenAI-Project", "Session_id", "X-Session-Id"} {
		if v := c.GetHeader(name); v != "" {
			headers[name] = v
		}
	}
	// The access row, response header and usage ledger share one identity.
	// Standalone handler callers without route middleware also need a stable
	// context identity before the asynchronous usage snapshot is captured.
	ctx := c.Request.Context()
	clientID, _ := ctx.Value(ctxkey.ClientRequestID).(string)
	localID, _ := ctx.Value(ctxkey.RequestID).(string)
	if strings.TrimSpace(clientID) == "" && strings.TrimSpace(localID) == "" {
		ctx = context.WithValue(ctx, ctxkey.ClientRequestID, uuid.NewString())
		c.Request = c.Request.WithContext(ctx)
	}
	requestID := service.ResolveUsageBillingRequestID(ctx, "")
	q, err := s.Begin(c.Request.Context(), intent, service.ResultCacheKeyMaterial{GroupID: gid, ScopeIdentity: fmt.Sprintf("%d/%d", key.UserID, key.ID), Endpoint: "/v1/chat/completions", EffectiveModel: model, EffectiveRequest: body, SemanticHeaders: headers}, service.ResultCacheAccess{RequestID: requestID, UserID: key.UserID, APIKeyID: key.ID, GroupID: gid})
	if err != nil {
		return nil, false, err
	}
	c.Header("X-Gateway-Request-Id", requestID)
	setChatCacheHeaders(c, q)
	if q.Status == "HIT" {
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(q.Entry.Body, &fields); err != nil {
			return nil, false, err
		}
		fields["id"], _ = json.Marshal("chatcmpl-cache-" + uuid.NewString())
		replay, err := json.Marshal(fields)
		if err != nil {
			return nil, false, err
		}
		c.Header("Content-Length", "")
		q.Complete(c.Request.Context(), http.StatusOK, nil)
		c.Data(http.StatusOK, "application/json", replay)
		return nil, true, nil
	}
	capture := &chatResultCacheCapture{c: c, session: q, original: c.Writer}
	if limit := q.CaptureLimit(); limit > 0 {
		capture.writer = newResultCacheWriter(c.Writer, limit)
		c.Writer = capture.writer
	}
	return capture, false, nil
}

// noteForward keeps upstream metadata distinct from the billable generation ID.
func (q *chatResultCacheCapture) noteForward(ctx context.Context, upstreamID, sentModel, responseModel string, responseModelConflict bool) {
	if q == nil {
		return
	}
	q.session.NoteUpstream(sentModel)
	q.session.NoteUpstream(responseModel)
	if responseModelConflict || strings.TrimSpace(sentModel) == "" || strings.TrimSpace(responseModel) == "" {
		q.session.Reject()
	}
	q.session.NoteSource(service.ResolveUsageBillingRequestID(ctx, upstreamID))
}

func (q *chatResultCacheCapture) finish() {
	if q == nil {
		return
	}
	if p := recover(); p != nil {
		q.c.Writer = q.original
		q.session.Complete(q.c.Request.Context(), 500, nil)
		panic(p)
	}
	var body []byte
	if q.writer != nil && !q.writer.overflow {
		body = q.writer.buf.Bytes()
	}
	q.session.Complete(q.c.Request.Context(), q.c.Writer.Status(), body)
	setChatCacheHeaders(q.c, q.session)
	if q.writer != nil {
		q.writer.commit()
		q.c.Writer = q.original
	}
}

func resultCacheAge(e *service.ResultCacheEntry) int64 {
	n := int64(time.Since(e.CreatedAt) / time.Second)
	if n < 0 {
		return 0
	}
	return n
}
