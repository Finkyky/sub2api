package handler

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type pagePermissionChecker struct {
	allow bool
	calls []string
}

func (p *pagePermissionChecker) HasPermission(_ context.Context, _ int64, code string) (bool, error) {
	p.calls = append(p.calls, code)
	return p.allow, nil
}

func TestPageRBACListPermissionAndLiveRevocation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	checker := &pagePermissionChecker{allow: true}
	r := gin.New()
	auth := func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 8})
		c.Next()
	}
	RegisterPageRoutes(r.Group("/api/v1"), dir, auth, auth, nil, checker)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pages", "example.md"), []byte("# Example"), 0600))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/pages", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "example")
	checker.allow = false
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/pages", nil))
	require.Equal(t, 403, w.Code)
	require.Equal(t, []string{"pages.read", "pages.read"}, checker.calls)
}

func TestPageRBACDoesNotReplaceContentOrImageAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	checker := &pagePermissionChecker{}
	r := gin.New()
	jwt := func(c *gin.Context) { c.AbortWithStatus(418) }
	admin := func(c *gin.Context) { t.Fatal("admin auth must not run for content/images") }
	RegisterPageRoutes(r.Group("/api/v1"), t.TempDir(), jwt, admin, nil, checker)
	for _, tc := range []struct {
		path string
		want int
	}{{"/api/v1/pages/example", 418}, {"/api/v1/pages/invalid!/images/logo.png", 404}} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		require.Equal(t, tc.want, w.Code)
	}
	require.Empty(t, checker.calls)
}
