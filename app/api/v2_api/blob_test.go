package v2_api

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/daqing/air-registry/app/services/blobstore"
)

func setupBlobServer(t *testing.T, data []byte) (*gin.Engine, string) {
	t.Helper()

	store := blobstore.New(t.TempDir())
	sum := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(sum[:])

	if _, err := store.Put(bytes.NewReader(data), digest); err != nil {
		t.Fatalf("seed blob: %v", err)
	}

	h := &Handler{Blobs: store}
	r := gin.New()
	h.Routes(r)
	return r, digest
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	data := make([]byte, n)
	if _, err := rand.Read(data); err != nil {
		t.Fatalf("generate random data: %v", err)
	}
	return data
}

func TestServeBlobRoundTrip(t *testing.T) {
	data := randomBytes(t, 512*1024)
	r, digest := setupBlobServer(t, data)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v2/library/nginx/blobs/"+digest, nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (%s)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("expected octet-stream content type, got %q", got)
	}
	if got := w.Header().Get("Docker-Content-Digest"); got != digest {
		t.Fatalf("expected digest header %q, got %q", digest, got)
	}
	if !bytes.Equal(w.Body.Bytes(), data) {
		t.Fatalf("downloaded %d bytes differ from the %d uploaded", w.Body.Len(), len(data))
	}
}

func TestServeBlobSingleSegmentName(t *testing.T) {
	data := []byte("tiny")
	r, digest := setupBlobServer(t, data)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v2/app/blobs/"+digest, nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (%s)", w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), data) {
		t.Fatalf("body mismatch: %q", w.Body.String())
	}
}

func TestServeBlobUnknownDigestReturns404(t *testing.T) {
	r, _ := setupBlobServer(t, []byte("x"))

	missing := "sha256:" + hex.EncodeToString(make([]byte, 32))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v2/app/blobs/"+missing, nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", w.Code)
	}
	if body := w.Body.String(); !bytes.Contains([]byte(body), []byte("BLOB_UNKNOWN")) {
		t.Fatalf("expected BLOB_UNKNOWN error body, got %q", body)
	}
}

func TestServeBlobMalformedDigestReturns400(t *testing.T) {
	r, _ := setupBlobServer(t, []byte("x"))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v2/app/blobs/not-a-digest", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
	if body := w.Body.String(); !bytes.Contains([]byte(body), []byte("DIGEST_INVALID")) {
		t.Fatalf("expected DIGEST_INVALID error body, got %q", body)
	}
}

func TestServeBlobInvalidRepoNameReturns400(t *testing.T) {
	data := []byte("x")
	r, digest := setupBlobServer(t, data)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v2/Bad_Name/blobs/"+digest, nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
	if body := w.Body.String(); !bytes.Contains([]byte(body), []byte("NAME_INVALID")) {
		t.Fatalf("expected NAME_INVALID error body, got %q", body)
	}
}

func TestCheckBlob(t *testing.T) {
	data := randomBytes(t, 2048)
	r, digest := setupBlobServer(t, data)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodHead, "/v2/library/nginx/blobs/"+digest, nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (%s)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Docker-Content-Digest"); got != digest {
		t.Fatalf("expected digest header %q, got %q", digest, got)
	}
	if got := w.Header().Get("Content-Length"); got != "2048" {
		t.Fatalf("expected content length 2048, got %q", got)
	}
}

func TestCheckBlobMissingReturns404(t *testing.T) {
	r, _ := setupBlobServer(t, []byte("x"))

	missing := "sha256:" + hex.EncodeToString(bytes.Repeat([]byte{0xff}, 32))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodHead, "/v2/app/blobs/"+missing, nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", w.Code)
	}
}

func TestDispatchUnknownEndpointReturns404(t *testing.T) {
	r, digest := setupBlobServer(t, []byte("x"))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v2/app/whatever/"+digest, nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", w.Code)
	}
}

func TestServeBlobStreamsContent(t *testing.T) {
	// Regression guard: the handler must stream through io.Copy, so a blob
	// larger than any buffer still arrives intact.
	data := randomBytes(t, 4*1024*1024)
	r, digest := setupBlobServer(t, data)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v2/big/blob/blobs/"+digest, nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	got, err := io.ReadAll(w.Result().Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("streamed blob corrupted")
	}
}
