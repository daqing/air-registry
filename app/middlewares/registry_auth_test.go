package middlewares

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func setupAuthServer() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v2/", RegistryAuth(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	return r
}

func TestRegistryAuthDisabledByDefault(t *testing.T) {
	t.Setenv("REGISTRY_AUTH_ENABLED", "")
	r := setupAuthServer()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v2/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
}

func TestRegistryAuthEnabledRejectsRequests(t *testing.T) {
	t.Setenv("REGISTRY_AUTH_ENABLED", "true")
	r := setupAuthServer()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v2/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", w.Code)
	}
	if got := w.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Basic") {
		t.Fatalf("expected a Basic challenge, got %q", got)
	}
	if body := w.Body.String(); !strings.Contains(body, "UNAUTHORIZED") {
		t.Fatalf("expected an OCI error body, got %q", body)
	}
}
