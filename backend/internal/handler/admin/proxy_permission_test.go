package admin

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type proxyCredentialService struct {
	service.AdminService
	proxy      service.Proxy
	lastUpdate *service.UpdateProxyInput
}

func (s *proxyCredentialService) ListProxiesWithAccountCount(context.Context, int, int, string, string, string, string, string) ([]service.ProxyWithAccountCount, int64, error) {
	return []service.ProxyWithAccountCount{{Proxy: s.proxy, AccountCount: 3}}, 1, nil
}
func (s *proxyCredentialService) GetAllProxiesWithAccountCount(context.Context) ([]service.ProxyWithAccountCount, error) {
	return []service.ProxyWithAccountCount{{Proxy: s.proxy, AccountCount: 3}}, nil
}
func (s *proxyCredentialService) GetAllProxies(context.Context) ([]service.Proxy, error) {
	return []service.Proxy{s.proxy}, nil
}
func (s *proxyCredentialService) GetProxy(context.Context, int64) (*service.Proxy, error) {
	return &s.proxy, nil
}
func (s *proxyCredentialService) GetProxiesByIDs(context.Context, []int64) ([]service.Proxy, error) {
	return []service.Proxy{s.proxy}, nil
}
func (s *proxyCredentialService) CreateProxy(context.Context, *service.CreateProxyInput) (*service.Proxy, error) {
	return &s.proxy, nil
}
func (s *proxyCredentialService) UpdateProxy(_ context.Context, _ int64, input *service.UpdateProxyInput) (*service.Proxy, error) {
	s.lastUpdate = input
	return &s.proxy, nil
}

func TestProxyRBACNormalResponsesDoNotExposeStoredPassword(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/proxies", ""}, {"GET", "/proxies/all", ""}, {"GET", "/proxies/all?with_count=true", ""}, {"GET", "/proxies/1", ""},
		{"PUT", "/proxies/1", `{"name":"renamed"}`}, {"POST", "/proxies", `{"name":"new","protocol":"http","host":"localhost","port":8080}`},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			s := &proxyCredentialService{proxy: service.Proxy{ID: 1, Name: "proxy", Host: "localhost", Port: 8080, Username: "alice", Password: "stored-secret"}}
			h := NewProxyHandler(s)
			r := gin.New()
			r.GET("/proxies", h.List)
			r.GET("/proxies/all", h.GetAll)
			r.GET("/proxies/:id", h.GetByID)
			r.POST("/proxies", h.Create)
			r.PUT("/proxies/:id", h.Update)
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, 200, w.Code, w.Body.String())
			require.NotContains(t, w.Body.String(), "stored-secret")
			require.NotContains(t, w.Body.String(), `"password"`)
			require.Contains(t, w.Body.String(), "alice")
			require.Equal(t, "stored-secret", s.proxy.Password, "response mapping must not alter stored credentials")
			if tc.method == "PUT" {
				require.NotNil(t, s.lastUpdate)
				require.Nil(t, s.lastUpdate.Password, "omitting password preserves existing credentials")
			}
		})
	}
}

func TestProxyRBACCredentialExportStillIncludesPassword(t *testing.T) {
	s := &proxyCredentialService{proxy: service.Proxy{ID: 1, Name: "proxy", Protocol: "http", Host: "localhost", Port: 8080, Username: "alice", Password: "stored-secret"}}
	r := gin.New()
	r.GET("/proxies/data", NewProxyHandler(s).ExportData)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/proxies/data?ids=1", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "stored-secret")
}
