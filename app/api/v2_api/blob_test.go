package v2_api

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/daqing/airway/lib/migrate"

	"github.com/daqing/air-registry/app/services/blobs"
	"github.com/daqing/air-registry/app/services/blobstore"
)

func setupBlobServer(t *testing.T, repoName string, data []byte) (*gin.Engine, string) {
	t.Helper()

	r, store := setupUploadServer(t)
	digest := digestOf(data)

	if _, err := store.Put(bytes.NewReader(data), digest); err != nil {
		t.Fatalf("seed blob: %v", err)
	}
	relPath, err := blobstore.RelativePath(digest)
	if err != nil {
		t.Fatalf("blob path: %v", err)
	}
	if err := blobs.RegisterUpload(repoName, digest, int64(len(data)), relPath); err != nil {
		t.Fatalf("register blob: %v", err)
	}

	return r, digest
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// migrateRun applies the project's migrations to a fresh SQLite database
// at dsn and returns it ready for repo.SetupDB.
func migrateRun(t *testing.T, dsn string) error {
	t.Helper()
	return migrate.Run(migrate.Options{
		DSN:          dsn,
		Migrations:   os.DirFS(filepath.Join("..", "..", "..", "db", "migrate")),
		SnapshotPath: "",
		Out:          io.Discard,
	})
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
	r, digest := setupBlobServer(t, "library/nginx", data)

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
	r, digest := setupBlobServer(t, "app", data)

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
	r, _ := setupBlobServer(t, "app", []byte("x"))

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
	r, _ := setupBlobServer(t, "app", []byte("x"))

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
	r, digest := setupBlobServer(t, "app", data)

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
	r, digest := setupBlobServer(t, "library/nginx", data)

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
	r, _ := setupBlobServer(t, "app", []byte("x"))

	missing := "sha256:" + hex.EncodeToString(bytes.Repeat([]byte{0xff}, 32))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodHead, "/v2/app/blobs/"+missing, nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", w.Code)
	}
}

func TestDispatchUnknownEndpointReturns404(t *testing.T) {
	r, digest := setupBlobServer(t, "app", []byte("x"))

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
	r, digest := setupBlobServer(t, "big/blob", data)

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

func TestServeBlobUnlinkedRepoReturns404(t *testing.T) {
	data := []byte("linked to another repo only")
	r, digest := setupBlobServer(t, "demo/base", data)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v2/demo/app/blobs/"+digest, nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d (%s)", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !bytes.Contains([]byte(body), []byte("BLOB_UNKNOWN")) {
		t.Fatalf("expected BLOB_UNKNOWN error body, got %q", body)
	}
}

func TestCheckBlobUnlinkedRepoReturns404(t *testing.T) {
	data := []byte("linked to another repo only")
	r, digest := setupBlobServer(t, "demo/base", data)

	// Docker probes blobs with HEAD before deciding to upload or mount;
	// an unlinked blob must look unknown so pushes (and mounts) trigger.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodHead, "/v2/demo/app/blobs/"+digest, nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", w.Code)
	}
}

func TestDeleteBlob(t *testing.T) {
	data := randomBytes(t, 100)
	r, digest := setupBlobServer(t, "delete/app", data)

	w := uploadRequest(t, r, http.MethodDelete, "/v2/delete/app/blobs/"+digest, nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d (%s)", w.Code, w.Body.String())
	}

	// Gone: GET and HEAD both 404, and a second delete 404s too.
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodDelete} {
		w = uploadRequest(t, r, method, "/v2/delete/app/blobs/"+digest, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected status 404 after delete, got %d for %s", w.Code, method)
		}
	}

	// Another repository's blob is untouched.
	w = uploadRequest(t, r, http.MethodDelete, "/v2/delete/other/blobs/"+digest, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 for an unlinked repository, got %d", w.Code)
	}
}

func TestDeleteBlobSharedAcrossRepos(t *testing.T) {
	data := randomBytes(t, 100)
	r, digest := setupBlobServer(t, "share/base", data)

	// Mount into a second repository, then delete from the first: the
	// second keeps the blob (file + row survive while any repo links it).
	if mounted, err := blobs.Mount("share/app", "share/base", digest); err != nil || !mounted {
		t.Fatalf("mount blob: %v (mounted=%v)", err, mounted)
	}
	w := uploadRequest(t, r, http.MethodDelete, "/v2/share/base/blobs/"+digest, nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d (%s)", w.Code, w.Body.String())
	}

	w = uploadRequest(t, r, http.MethodHead, "/v2/share/app/blobs/"+digest, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200 for the mounting repository, got %d", w.Code)
	}

	// Deleting from the last linking repository reclaims the file.
	w = uploadRequest(t, r, http.MethodDelete, "/v2/share/app/blobs/"+digest, nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d (%s)", w.Code, w.Body.String())
	}
	w = uploadRequest(t, r, http.MethodHead, "/v2/share/app/blobs/"+digest, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 after the last delete, got %d", w.Code)
	}
}

func TestDeleteBlobInvalidDigest(t *testing.T) {
	r, _ := setupBlobServer(t, "delete/app", randomBytes(t, 10))

	w := uploadRequest(t, r, http.MethodDelete, "/v2/delete/app/blobs/not-a-digest", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d (%s)", w.Code, w.Body.String())
	}
}
