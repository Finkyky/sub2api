package handler

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	httputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// One production adapter owns general plugin execution. Legacy securityaudit
// remains in the existing handlers and sees the validated replacement input.
type PluginGatewayHandler struct {
	resolveGroup                    func(context.Context, int64) (*service.Group, error)
	service                         *service.PluginGatewayService
	budget                          *strictPluginBufferBudget
	hookTimeout, outputTimeout      time.Duration
	requestMaxBytes, outputMaxBytes int64
}

func NewPluginGatewayHandler(instances *service.PluginInstanceService, gateway *service.GatewayService, cfg *config.Config) *PluginGatewayHandler {
	h := NewPluginGatewayHandlerWithService(service.NewPluginGatewayService(instances), cfg)
	if gateway != nil {
		h.resolveGroup = gateway.ResolveGroupByID
	}
	return h
}
func NewPluginGatewayHandlerWithService(s *service.PluginGatewayService, cfg *config.Config) *PluginGatewayHandler {
	call, total := 10, 120
	maxResponse, totalBytes, requestMax := int64(8<<20), int64(128<<20), int64(16<<20)
	concurrency := 16
	if cfg != nil {
		p := cfg.Plugins
		if p.GatewayHookTimeoutSeconds > 0 {
			call = p.GatewayHookTimeoutSeconds
		}
		if p.StrictOutputTimeoutSeconds > 0 {
			total = p.StrictOutputTimeoutSeconds
		}
		if p.StrictOutputMaxBytes > 0 {
			maxResponse = p.StrictOutputMaxBytes
		}
		if p.StrictOutputTotalBytes > 0 {
			totalBytes = p.StrictOutputTotalBytes
		}
		if p.StrictOutputMaxConcurrency > 0 {
			concurrency = p.StrictOutputMaxConcurrency
		}
		if cfg.Gateway.TextMaxBodySize > 0 {
			requestMax = cfg.Gateway.TextMaxBodySize
		} else if cfg.Gateway.MaxBodySize > 0 {
			requestMax = cfg.Gateway.MaxBodySize
		}
	}
	return &PluginGatewayHandler{service: s, budget: newStrictPluginBufferBudget(concurrency, totalBytes), hookTimeout: time.Duration(call) * time.Second, outputTimeout: time.Duration(total) * time.Second, requestMaxBytes: requestMax, outputMaxBytes: maxResponse}
}

const pluginGatewayStateKey = "sub2api.plugin_gateway.request"

type pluginGatewayState struct {
	failureStatus       int
	failureCode         string
	plans               map[int64]*service.PluginGatewayRequest
	groups              map[int64]*service.Group
	activeGroupID       int64
	active              *service.PluginGatewayRequest
	protocol, requestID string
	hookTimeout         time.Duration
	originalBody        []byte
	maxInputBytes       int64
}

func pluginGatewayProtocol(c *gin.Context) (string, bool) {
	path := c.Request.URL.Path
	if c.Request.Method == http.MethodGet {
		// Route templates, never model/call-id strings, determine WebSocket paths.
		switch c.FullPath() {
		case "/v1/responses", "/responses", "/backend-api/codex/responses", "/v1/realtime", "/v1/live/:call_id", "/backend-api/codex/:call_id":
			return "unsupported.gateway", true
		default:
			return "", false
		}
	}
	if c.Request.Method != http.MethodPost && c.Request.Method != http.MethodGet {
		return "", false
	}
	if strings.HasSuffix(path, "/count_tokens") {
		return "", false
	}
	if c.Request.Method == http.MethodPost {
		switch {
		case strings.HasSuffix(path, "/chat/completions"):
			return service.PluginProtocolOpenAIChat, true
		case strings.HasSuffix(path, "/messages"):
			return service.PluginProtocolAnthropicMessages, true
		case strings.HasSuffix(path, "/responses"):
			return service.PluginProtocolOpenAIResponses, true
		case strings.HasSuffix(path, ":generateContent") || strings.HasSuffix(path, ":streamGenerateContent"):
			return service.PluginProtocolGemini, true
		}
	}
	return "unsupported.gateway", true
}
func pluginGatewayError(c *gin.Context, protocol string, status int, code string) {
	markPluginGatewayFailure(c, status, code)
	// Never expose plugin-owned rejection text/errors or a buffered upstream body.
	message := "Gateway plugin check did not allow this request"
	switch protocol {
	case service.PluginProtocolAnthropicMessages:
		c.AbortWithStatusJSON(status, gin.H{"type": "error", "error": gin.H{"type": code, "message": message}})
	case service.PluginProtocolGemini:
		c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": status, "status": "FAILED_PRECONDITION", "message": message}})
	default:
		c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"type": code, "code": code, "message": message}})
	}
}
func pluginGatewayErrorStatus(err error) (int, string) {
	switch {
	case errors.Is(err, service.ErrPluginGatewayBlocked):
		return http.StatusForbidden, "plugin_blocked"
	case errors.Is(err, service.ErrPluginGatewayProtocol):
		return http.StatusUnprocessableEntity, "plugin_protocol_unsupported"
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "plugin_timeout"
	default:
		return http.StatusServiceUnavailable, "plugin_check_failed"
	}
}
func (h *PluginGatewayHandler) Middleware(c *gin.Context) {
	if h == nil || h.service == nil || c.Request == nil {
		c.Next()
		return
	}
	protocol, eligible := pluginGatewayProtocol(c)
	if !eligible {
		c.Next()
		return
	}
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil || key.GroupID == nil || *key.GroupID <= 0 {
		c.Next()
		return
	}
	requestID, _ := c.Request.Context().Value(ctxkey.RequestID).(string)
	if requestID == "" {
		requestID = uuid.NewString()
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.RequestID, requestID))
	}
	state := &pluginGatewayState{plans: make(map[int64]*service.PluginGatewayRequest), protocol: protocol, requestID: requestID, hookTimeout: h.hookTimeout, maxInputBytes: h.requestMaxBytes}
	defer func() {
		for _, p := range state.plans {
			p.Close()
		}
	}()
	groupIDs := []int64{*key.GroupID}
	state.groups = map[int64]*service.Group{*key.GroupID: key.Group}
	// Freeze every configured Claude-Code fallback before the handler can emit
	// keepalives. The real client classification selects the active group later.
	force, _ := c.Request.Context().Value(ctxkey.ForcePlatform).(string)
	if force == "" && protocol == service.PluginProtocolAnthropicMessages {
		seen := map[int64]bool{*key.GroupID: true}
		for group := key.Group; group != nil && group.ClaudeCodeOnly && group.FallbackGroupID != nil; {
			gid := *group.FallbackGroupID
			if gid <= 0 || seen[gid] || len(seen) >= 32 || h.resolveGroup == nil {
				pluginGatewayError(c, protocol, 503, "plugin_check_failed")
				return
			}
			seen[gid] = true
			ctx, cancel := context.WithTimeout(c.Request.Context(), h.hookTimeout)
			resolved, err := h.resolveGroup(ctx, gid)
			cancel()
			if err != nil || resolved == nil || resolved.ID != gid {
				pluginGatewayError(c, protocol, 503, "plugin_check_failed")
				return
			}
			state.groups[gid] = resolved
			groupIDs = append(groupIDs, gid)
			group = resolved
		}
	}

	// Buffer from the start if a configured Anthropic fallback may require output
	// checks; otherwise early keepalive bytes could escape before that group wins.
	if protocol == service.PluginProtocolAnthropicMessages {
		for _, group := range state.groups {
			if group == nil || group.FallbackGroupIDOnInvalidRequest == nil || *group.FallbackGroupIDOnInvalidRequest <= 0 {
				continue
			}
			gid := *group.FallbackGroupIDOnInvalidRequest
			present := false
			for _, existing := range groupIDs {
				present = present || existing == gid
			}
			if !present {
				groupIDs = append(groupIDs, gid)
			}
		}
	}
	bound, strict := false, false
	for _, gid := range groupIDs {
		ctx, cancel := context.WithTimeout(c.Request.Context(), h.hookTimeout)
		p, err := h.service.Prepare(ctx, gid, protocol)
		cancel()
		if err != nil {
			status, code := pluginGatewayErrorStatus(err)
			pluginGatewayError(c, protocol, status, code)
			return
		}
		state.plans[gid] = p
		bound = bound || p.Bound()
		strict = strict || p.HasOutput() || (gid != *key.GroupID && p.Bound())
	}
	if !bound {
		c.Next()
		return
	}
	if !service.PluginGatewayProtocolSupported(protocol) {
		pluginGatewayError(c, protocol, 422, "plugin_protocol_unsupported")
		return
	}
	state.active = state.plans[*key.GroupID]
	state.activeGroupID = *key.GroupID
	c.Set(pluginGatewayStateKey, state)
	c.Request = c.Request.WithContext(service.WithPluginGatewayEffectiveGroup(service.WithPluginGatewayCacheBypass(c.Request.Context()), *key.GroupID))
	var buffer *strictPluginResponseWriter
	base := c.Writer
	if strict {
		ctx, cancel := context.WithTimeout(c.Request.Context(), h.outputTimeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		reservation, err := h.budget.acquire(ctx)
		if err != nil {
			pluginGatewayError(c, protocol, 503, "plugin_buffer_capacity")
			return
		}
		defer reservation.release()
		buffer = newStrictPluginResponseWriter(base, reservation, h.outputMaxBytes, ctx, cancel)
		c.Writer = buffer
		defer func() { c.Writer = base }()
	}
	// Do not consume an unbounded chunked/unknown-length body before checking
	// the text-input budget. Composite routing may already have preread it.
	if _, preread := c.Request.Body.(*httputil.PrereadBody); !preread && c.Request.Body != nil {
		c.Request.Body = http.MaxBytesReader(base, c.Request.Body, h.requestMaxBytes)
	}
	readDeadline := time.Now().Add(h.hookTimeout)
	if deadline, ok := c.Request.Context().Deadline(); ok && deadline.Before(readDeadline) {
		readDeadline = deadline
	}
	c.Request = c.Request.WithContext(httputil.WithRequestBodyReadLimit(c.Request.Context(), h.requestMaxBytes))
	controller := http.NewResponseController(base)
	deadlineErr := controller.SetReadDeadline(readDeadline)
	body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
	if deadlineErr == nil && err == nil {
		_ = controller.SetReadDeadline(time.Time{})
	}
	if err != nil {
		var timeout interface{ Timeout() bool }
		if errors.As(err, &timeout) && timeout.Timeout() {
			c.Writer = base
			pluginGatewayError(c, protocol, 504, "plugin_timeout")
			return
		}
	}

	if err != nil || int64(len(body)) > h.requestMaxBytes {
		c.Writer = base
		pluginGatewayError(c, protocol, 413, "plugin_input_too_large")
		return
	}
	state.originalBody = append([]byte(nil), body...)
	ctx, cancel := context.WithTimeout(c.Request.Context(), h.hookTimeout)
	replacement, err := state.active.CheckInput(ctx, requestID, body)
	cancel()
	if err != nil {
		c.Writer = base
		status, code := pluginGatewayErrorStatus(err)
		pluginGatewayError(c, protocol, status, code)
		return
	}
	if int64(len(replacement)) > h.requestMaxBytes {
		c.Writer = base
		pluginGatewayError(c, protocol, 413, "plugin_input_too_large")
		return
	}
	c.Request.Body = httputil.NewPrereadBody(replacement)
	c.Request.ContentLength = int64(len(replacement))
	c.Request.Header.Del("Content-Encoding")
	c.Request.Header.Set("Content-Length", strconv.Itoa(len(replacement)))
	c.Next()
	if state.failureStatus != 0 && buffer != nil {
		c.Writer = base
		pluginGatewayError(c, protocol, state.failureStatus, state.failureCode)
		return
	}
	if buffer == nil {
		return
	}
	c.Writer = base
	if err := buffer.Error(); err != nil {
		status, code := pluginGatewayErrorStatus(err)
		pluginGatewayError(c, protocol, status, code)
		return
	}
	if err := c.Request.Context().Err(); err != nil {
		status, code := pluginGatewayErrorStatus(err)
		pluginGatewayError(c, protocol, status, code)
		return
	}
	// Non-success bodies can echo inspected inputs or foreign errors. Preserve
	// the HTTP status, not the unapproved body/header payload.
	if buffer.Status() < 200 || buffer.Status() >= 300 {
		pluginGatewayError(c, protocol, buffer.Status(), "plugin_upstream_failed")
		return
	}
	original := buffer.Bytes()
	result := original
	replaced := false
	if state.active.HasOutput() {
		content, err := service.PluginGatewayOutputContent(protocol, buffer.Header().Get("Content-Type"), original)
		if err != nil {
			pluginGatewayError(c, protocol, 502, "plugin_response_invalid")
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), h.hookTimeout)
		checked, err := state.active.CheckOutput(ctx, requestID, content)
		cancel()
		if err != nil {
			status, code := pluginGatewayErrorStatus(err)
			pluginGatewayError(c, protocol, status, code)
			return
		}
		if checked.MediaType != content.MediaType || !bytes.Equal(checked.Data, content.Data) {
			replaced = true
			result, err = service.ApplyPluginGatewayOutputReplacement(protocol, buffer.Header().Get("Content-Type"), original, checked)
			if err != nil {
				pluginGatewayError(c, protocol, 502, "plugin_response_invalid")
				return
			}
		}
	}
	if int64(len(result)) > h.outputMaxBytes {
		pluginGatewayError(c, protocol, 502, "plugin_response_too_large")
		return
	}
	var commitErr error
	if replaced {
		commitErr = buffer.CommitReplacement(result, buffer.Header().Get("Content-Type"))
	} else {
		commitErr = buffer.Commit(result)
	}
	if err := commitErr; err != nil {
		if !base.Written() {
			pluginGatewayError(c, protocol, 503, "plugin_commit_failed")
		}
	}
}

// Called at the actual group-switch point, before retrying an upstream request.
// Input is reevaluated once under the fallback group's own pinned publication.
func recheckPluginGatewayFallback(c *gin.Context, groupID int64, body []byte) ([]byte, error) {
	raw, ok := c.Get(pluginGatewayStateKey)
	if !ok {
		return body, nil
	}
	state, ok := raw.(*pluginGatewayState)
	if !ok {
		return nil, service.ErrPluginGatewayCheck
	}
	plan := state.plans[groupID]
	if plan == nil {
		return nil, service.ErrPluginGatewayCheck
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), state.hookTimeout)
	defer cancel()
	result, err := plan.CheckInput(ctx, state.requestID, body)
	if err != nil {
		return nil, err
	}
	if int64(len(result)) > state.maxInputBytes {
		return nil, service.ErrPluginGatewayCheck
	}
	state.active = plan
	state.activeGroupID = groupID
	c.Request = c.Request.WithContext(service.WithPluginGatewayEffectiveGroup(c.Request.Context(), groupID))
	return result, nil
}

// Client classification happens in the real handler; selecting from prepared
// groups here cannot discover an unbuffered output chain after an early flush.
func recheckPluginGatewayClaudeGroup(c *gin.Context, key *service.APIKey, body []byte) ([]byte, *service.APIKey, error) {
	raw, exists := c.Get(pluginGatewayStateKey)
	if !exists {
		return body, key, nil
	}
	state, ok := raw.(*pluginGatewayState)
	if !ok || key == nil || key.GroupID == nil {
		return nil, nil, service.ErrPluginGatewayCheck
	}
	force, _ := c.Request.Context().Value(ctxkey.ForcePlatform).(string)
	if force != "" || service.IsClaudeCodeClient(c.Request.Context()) {
		return body, key, nil
	}
	group := key.Group
	for group != nil && group.ClaudeCodeOnly {
		if group.FallbackGroupID == nil {
			return nil, nil, service.ErrPluginGatewayCheck
		}
		group = state.groups[*group.FallbackGroupID]
		if group == nil {
			return nil, nil, service.ErrPluginGatewayCheck
		}
	}
	if group == nil || group.ID == *key.GroupID {
		return body, key, nil
	}
	if group.ID == state.activeGroupID {
		return body, cloneAPIKeyWithGroup(key, group), nil
	}
	result, err := recheckPluginGatewayFallback(c, group.ID, body)
	return result, cloneAPIKeyWithGroup(key, group), err
}

func markPluginGatewayFailure(c *gin.Context, status int, code string) {
	if raw, ok := c.Get(pluginGatewayStateKey); ok {
		if state, ok := raw.(*pluginGatewayState); ok {
			state.failureStatus = status
			state.failureCode = code
		}
	}
}
