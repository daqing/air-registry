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

func serveAuth(r *gin.Engine, user, password string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	if user != "" || password != "" {
		req.SetBasicAuth(user, password)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func expectChallenge(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
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

func TestRegistryAuthDisabledByDefault(t *testing.T) {
	t.Setenv("REGISTRY_AUTH_ENABLED", "")
	t.Setenv("REGISTRY_AUTH_USERNAME", "")
	t.Setenv("REGISTRY_AUTH_PASSWORD", "")
	r := setupAuthServer()

	if w := serveAuth(r, "", ""); w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
}

func TestRegistryAuthEnabledWithoutCredentialsFailsClosed(t *testing.T) {
	t.Setenv("REGISTRY_AUTH_ENABLED", "true")
	t.Setenv("REGISTRY_AUTH_USERNAME", "")
	t.Setenv("REGISTRY_AUTH_PASSWORD", "")
	r := setupAuthServer()

	expectChallenge(t, serveAuth(r, "", ""))
	expectChallenge(t, serveAuth(r, "admin", "secret"))
}

func TestRegistryAuthRequiresMatchingCredentials(t *testing.T) {
	t.Setenv("REGISTRY_AUTH_ENABLED", "true")
	t.Setenv("REGISTRY_AUTH_USERNAME", "admin")
	t.Setenv("REGISTRY_AUTH_PASSWORD", "s3cret")
	r := setupAuthServer()

	// No credentials: docker sees this 401 + Basic challenge and prompts.
	expectChallenge(t, serveAuth(r, "", ""))
	// Wrong password.
	expectChallenge(t, serveAuth(r, "admin", "wrong"))
	// Wrong username.
	expectChallenge(t, serveAuth(r, "root", "s3cret"))
	// Correct pair passes through.
	if w := serveAuth(r, "admin", "s3cret"); w.Code != http.StatusOK {
		t.Fatalf("expected status 200 with valid credentials, got %d", w.Code)
	}
}

func TestRegistryAuthEnabledByCredentialsAlone(t *testing.T) {
	t.Setenv("REGISTRY_AUTH_ENABLED", "")
	t.Setenv("REGISTRY_AUTH_USERNAME", "admin")
	t.Setenv("REGISTRY_AUTH_PASSWORD", "s3cret")
	r := setupAuthServer()

	expectChallenge(t, serveAuth(r, "", ""))
	if w := serveAuth(r, "admin", "s3cret"); w.Code != http.StatusOK {
		t.Fatalf("expected status 200 with valid credentials, got %d", w.Code)
	}
}

func TestRegistryAuthRejectsNonBasicScheme(t *testing.T) {
	t.Setenv("REGISTRY_AUTH_ENABLED", "")
	t.Setenv("REGISTRY_AUTH_USERNAME", "admin")
	t.Setenv("REGISTRY_AUTH_PASSWORD", "s3cret")
	r := setupAuthServer()

	req := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	expectChallenge(t, w)
}
