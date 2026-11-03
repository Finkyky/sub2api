package middleware

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

type permissionCheckerStub struct {
	allowed bool
	err     error
}

func (s permissionCheckerStub) HasPermission(context.Context, int64, string) (bool, error) {
	return s.allowed, s.err
}

func TestRequirePermissionRejectsMissingIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/admin", RequirePermission(permissionCheckerStub{allowed: true}, "users.read"), func(c *gin.Context) { c.Status(204) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/admin", nil))
	if w.Code != 401 {
		t.Fatalf("got status %d, want 401", w.Code)
	}
}

func TestRequirePermissionRejectsDeniedIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(string(ContextKeyUser), AuthSubject{UserID: 7}); c.Next() })
	r.GET("/admin", RequirePermission(permissionCheckerStub{allowed: false}, "users.read"), func(c *gin.Context) { c.Status(204) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/admin", nil))
	if w.Code != 403 {
		t.Fatalf("got status %d, want 403", w.Code)
	}
}

func TestRequirePermissionAllowsGrantedIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(string(ContextKeyUser), AuthSubject{UserID: 7}); c.Next() })
	r.GET("/admin", RequirePermission(permissionCheckerStub{allowed: true}, "users.read"), func(c *gin.Context) { c.Status(204) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/admin", nil))
	if w.Code != 204 {
		t.Fatalf("got status %d, want 204", w.Code)
	}
}

func TestRequirePermissionFailsClosedWithoutChecker(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(string(ContextKeyUser), AuthSubject{UserID: 7}); c.Next() })
	r.GET("/admin", RequirePermission(nil, "roles.read"), func(c *gin.Context) { c.Status(204) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/admin", nil))
	if w.Code != 503 {
		t.Fatalf("got status %d, want 503", w.Code)
	}
}
