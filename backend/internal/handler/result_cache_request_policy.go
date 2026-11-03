package handler

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// BindChatResultCacheRequestPolicy runs after authentication but before group
// allowlists and composite routing. The gateway handlers share the same injected
// ResultCacheService. Their later binding remains idempotent for direct callers.
func (h *GatewayHandler) BindChatResultCacheRequestPolicy(c *gin.Context) {
	if h == nil || c.Request.Method != http.MethodPost || (c.FullPath() != "/v1/chat/completions" && c.FullPath() != "/chat/completions") || c.GetHeader("X-Gateway-Cache-Protocol") == "" {
		c.Next()
		return
	}
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key == nil {
		c.Next()
		return
	}
	body, err := readLenientJSONRequestBodyWithPrealloc(c.Request, h.cfg)
	if err != nil {
		status := http.StatusBadRequest
		message := "Failed to read request body"
		if maxErr, ok := extractMaxBytesError(err); ok {
			status = http.StatusRequestEntityTooLarge
			message = buildBodyTooLargeMessage(maxErr.Limit)
		}
		h.chatCompletionsErrorResponse(c, status, "invalid_request_error", message)
		c.Abort()
		return
	}
	// Keep the original cache control payload for the handler's validation and
	// stripping. Only the context changes; preread bytes remain reusable.
	requestmodel.ResetRequestBody(c.Request, body)
	intent, _, err := service.ParseResultCacheIntent(c.Request.Header, body)
	if err == nil {
		c.Request = c.Request.WithContext(h.resultCacheService.BindRequestPolicy(c.Request.Context(), intent, key.GroupID))
	}
	c.Next()
}
