package v2_api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestIndexActionAnswersOCIVersionCheck(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	Routes(r)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	if got := w.Header().Get("Docker-Distribution-API-Version"); got != "registry/2.0" {
		t.Fatalf("expected API version header %q, got %q", "registry/2.0", got)
	}

	if body := w.Body.String(); body != "" {
		t.Fatalf("expected empty body, got %q", body)
	}
}
