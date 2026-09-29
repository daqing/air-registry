package v2_api

import (
	"net/http"
	"strings"
	"testing"
)

func TestUploadSizeLimit(t *testing.T) {
	r, _ := setupUploadServer(t)
	t.Setenv("MAX_UPLOAD_SIZE", "16")

	// Monolithic POST over the cap → 413, nothing published.
	data := randomBytes(t, 64)
	w := uploadRequest(t, r, http.MethodPost, "/v2/limited/app/blobs/uploads/?digest="+digestOf(data), data)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413, got %d (%s)", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "TOO_LARGE") {
		t.Fatalf("expected a TOO_LARGE error code, got %q", body)
	}

	// PATCH over the cap → 413 and the session is dropped.
	location, _ := startSession(t, r, "limited/app")
	w = uploadRequest(t, r, http.MethodPatch, location, randomBytes(t, 64))
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413, got %d (%s)", w.Code, w.Body.String())
	}
	w = uploadRequest(t, r, http.MethodPut, location+"?digest="+digestOf([]byte("x")), []byte("x"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 for the dropped session, got %d", w.Code)
	}

	// A body within the cap still uploads.
	data = randomBytes(t, 8)
	w = uploadRequest(t, r, http.MethodPost, "/v2/limited/app/blobs/uploads/?digest="+digestOf(data), data)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201 under the cap, got %d (%s)", w.Code, w.Body.String())
	}
}

func TestUploadSizeLimitUnlimitedByDefault(t *testing.T) {
	r, _ := setupUploadServer(t)
	t.Setenv("MAX_UPLOAD_SIZE", "")

	data := randomBytes(t, 64 * 1024)
	w := uploadRequest(t, r, http.MethodPost, "/v2/unlimited/app/blobs/uploads/?digest="+digestOf(data), data)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d (%s)", w.Code, w.Body.String())
	}
}
