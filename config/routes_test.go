package config

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRoutesRegistersCoreEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	Routes(r)

	registered := map[string]bool{}
	for _, route := range r.Routes() {
		registered[route.Method+" "+route.Path] = true
	}

	expected := []string{
		"GET /",
		"GET /repos",
		"GET /repos/*path",
		"GET /health",
		"GET /ws",
		"POST /ws/publish",
	}

	for _, route := range expected {
		if !registered[route] {
			t.Fatalf("expected route %s to be registered, got %#v", route, registered)
		}
	}
}

// TestRoutesAuthGate verifies the registry auth middleware is mounted on the
// /v2 routes: with the switch off the API answers, with it on every request
// is rejected until a credential backend lands.
func TestRoutesAuthGate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Setenv("REGISTRY_AUTH_ENABLED", "")
	r := gin.New()
	Routes(r)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v2/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200 with auth off, got %d", w.Code)
	}

	t.Setenv("REGISTRY_AUTH_ENABLED", "true")
	r = gin.New()
	Routes(r)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v2/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401 with auth on, got %d", w.Code)
	}
}
