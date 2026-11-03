package handler

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResultCacheChatRequestIDMatchesBillingContext(t *testing.T) {
	for _, tc := range []struct{ name, client, local, want string }{
		{"client wins", " client-id ", "local-id", "client:client-id"},
		{"local fallback", "  ", " local-id ", "local:local-id"},
		{"generate and bind", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRCChatFixture(t)
			r := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(r)
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			ctx := context.WithValue(c.Request.Context(), ctxkey.ClientRequestID, tc.client)
			ctx = context.WithValue(ctx, ctxkey.RequestID, tc.local)
			c.Request = c.Request.WithContext(ctx)
			intent := service.ResultCacheIntent{Declared: true, Mode: "read_only", Scope: "user"}
			gid := int64(7)
			key := &service.APIKey{ID: 3, UserID: 2, GroupID: &gid}
			capture, hit, err := beginChatResultCache(c, f.service, key, intent, []byte(`{"model":"m","messages":[{"role":"user","content":"question"}]}`), "m")
			require.NoError(t, err)
			require.False(t, hit)
			require.NotNil(t, capture)
			id := c.Writer.Header().Get("X-Gateway-Request-Id")
			if tc.want != "" {
				require.Equal(t, tc.want, id)
			} else {
				bound, _ := c.Request.Context().Value(ctxkey.ClientRequestID).(string)
				require.NotEmpty(t, strings.TrimSpace(bound))
				require.Equal(t, "client:"+strings.TrimSpace(bound), id)
			}
			c.Status(200)
			capture.finish()
			require.Len(t, f.access.records, 1)
			require.Equal(t, id, f.access.records[0].RequestID)
		})
	}
}
