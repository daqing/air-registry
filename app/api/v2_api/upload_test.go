package v2_api

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/daqing/airway/lib/repo"
	airwaysql "github.com/daqing/airway/lib/sql"

	"github.com/daqing/air-registry/app/models"
	"github.com/daqing/air-registry/app/services/blobstore"
	"github.com/daqing/air-registry/app/services/uploads"
)

func setupUploadServer(t *testing.T) (*gin.Engine, *blobstore.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dsn := "sqlite://" + filepath.ToSlash(filepath.Join(t.TempDir(), "registry-test.db"))
	if err := migrateRun(t, dsn); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	if _, err := repo.SetupDB(dsn); err != nil {
		t.Fatalf("setup db: %v", err)
	}

	store := blobstore.New(t.TempDir())
	h := &Handler{
		Blobs:   store,
		Uploads: uploads.NewManager(filepath.Join(store.Root, "uploads")),
	}
	r := gin.New()
	h.Routes(r)
	return r, store
}

func uploadRequest(t *testing.T, r *gin.Engine, method, url string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, url, bytes.NewReader(body))
	r.ServeHTTP(w, req)
	return w
}

var uuidPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func startSession(t *testing.T, r *gin.Engine, name string) (location, uuid string) {
	t.Helper()
	w := uploadRequest(t, r, http.MethodPost, "/v2/"+name+"/blobs/uploads/", nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (%s)", w.Code, w.Body.String())
	}
	location = w.Header().Get("Location")
	if location == "" {
		t.Fatalf("missing Location header")
	}
	if got := w.Header().Get("Range"); got != "0-0" {
		t.Fatalf("expected Range 0-0, got %q", got)
	}
	uuid = w.Header().Get("Docker-Upload-UUID")
	if !uuidPattern.MatchString(uuid) {
		t.Fatalf("expected a Docker-Upload-UUID, got %q", uuid)
	}
	return location, uuid
}

func TestMonolithicPostWithDigest(t *testing.T) {
	r, store := setupUploadServer(t)
	data := randomBytes(t, 100*1024)
	digest := digestOf(data)

	w := uploadRequest(t, r, http.MethodPost, "/v2/demo/app/blobs/uploads/?digest="+digest, data)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (%s)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Docker-Content-Digest"); got != digest {
		t.Fatalf("expected digest header %q, got %q", digest, got)
	}

	assertBlobRegistered(t, store, "demo/app", digest, data)
}

func TestPostThenPutCompletes(t *testing.T) {
	r, store := setupUploadServer(t)
	data := randomBytes(t, 50*1024)
	digest := digestOf(data)

	location, _ := startSession(t, r, "demo/app")

	w := uploadRequest(t, r, http.MethodPut, location+"?digest="+digest, data)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (%s)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Location"); got != "/v2/demo/app/blobs/"+digest {
		t.Fatalf("unexpected location %q", got)
	}

	assertBlobRegistered(t, store, "demo/app", digest, data)
}

func TestPatchThenPutCompletes(t *testing.T) {
	r, store := setupUploadServer(t)
	data := randomBytes(t, 300*1024)
	digest := digestOf(data)

	location, _ := startSession(t, r, "demo/app")

	cut := 100 * 1024
	w := uploadRequest(t, r, http.MethodPatch, location, data[:cut])
	if w.Code != http.StatusAccepted {
		t.Fatalf("patch: expected 202, got %d (%s)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Range"); got == "" || got == "0-0" {
		t.Fatalf("expected progressed Range header, got %q", got)
	}

	w = uploadRequest(t, r, http.MethodPut, location+"?digest="+digest, data[cut:])
	if w.Code != http.StatusCreated {
		t.Fatalf("put: expected 201, got %d (%s)", w.Code, w.Body.String())
	}

	assertBlobRegistered(t, store, "demo/app", digest, data)
}

func TestUploadDigestMismatch(t *testing.T) {
	r, _ := setupUploadServer(t)
	data := randomBytes(t, 1024)
	wrong := digestOf([]byte("something else"))

	location, uuid := startSession(t, r, "demo/app")

	w := uploadRequest(t, r, http.MethodPut, location+"?digest="+wrong, data)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (%s)", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("DIGEST_INVALID")) {
		t.Fatalf("expected DIGEST_INVALID, got %q", w.Body.String())
	}

	// The session survives a mismatch so the client can retry.
	w = uploadRequest(t, r, http.MethodDelete, "/v2/demo/app/blobs/uploads/"+uuid, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected session to survive (DELETE 204), got %d", w.Code)
	}
}

func TestUploadUnknownSession(t *testing.T) {
	r, _ := setupUploadServer(t)

	w := uploadRequest(t, r, http.MethodPut, "/v2/demo/app/blobs/uploads/deadbeefdeadbeefdeadbeefdeadbeef00?digest="+digestOf([]byte("x")), []byte("x"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("BLOB_UPLOAD_UNKNOWN")) {
		t.Fatalf("expected BLOB_UPLOAD_UNKNOWN, got %q", w.Body.String())
	}
}

func TestAbortUpload(t *testing.T) {
	r, _ := setupUploadServer(t)

	location, uuid := startSession(t, r, "demo/app")
	_ = location

	w := uploadRequest(t, r, http.MethodDelete, "/v2/demo/app/blobs/uploads/"+uuid, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}

	w = uploadRequest(t, r, http.MethodPut, "/v2/demo/app/blobs/uploads/"+uuid+"?digest="+digestOf([]byte("x")), []byte("x"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after abort, got %d", w.Code)
	}
}

func TestUploadInvalidRepoName(t *testing.T) {
	r, _ := setupUploadServer(t)

	w := uploadRequest(t, r, http.MethodPost, "/v2/Bad/Name/blobs/uploads/", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestMultiplePatchesThenPut(t *testing.T) {
	r, store := setupUploadServer(t)
	data := randomBytes(t, 500*1024)
	digest := digestOf(data)

	location, _ := startSession(t, r, "demo/app")

	// Three chunks of uneven size, no Content-Range (plain append).
	cuts := []int{100 * 1024, 230 * 1024, 500 * 1024}
	prev := 0
	for _, cut := range cuts {
		w := uploadRequest(t, r, http.MethodPatch, location, data[prev:cut])
		if w.Code != http.StatusAccepted {
			t.Fatalf("patch %d: expected 202, got %d (%s)", cut, w.Code, w.Body.String())
		}
		wantRange := "0-" + itoa(cut-1)
		if got := w.Header().Get("Range"); got != wantRange {
			t.Fatalf("expected Range %q, got %q", wantRange, got)
		}
		prev = cut
	}

	w := uploadRequest(t, r, http.MethodPut, location+"?digest="+digest, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("put: expected 201, got %d (%s)", w.Code, w.Body.String())
	}
	assertBlobRegistered(t, store, "demo/app", digest, data)
}

func TestPatchWithContentRange(t *testing.T) {
	r, store := setupUploadServer(t)
	data := randomBytes(t, 100*1024)
	digest := digestOf(data)

	location, _ := startSession(t, r, "demo/app")

	patch := func(rangeHeader string, chunk []byte) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPatch, "http://127.0.0.1"+location, bytes.NewReader(chunk))
		if rangeHeader != "" {
			req.Header.Set("Content-Range", rangeHeader)
		}
		r.ServeHTTP(w, req)
		return w
	}

	w := patch("0-65535", data[:64*1024])
	if w.Code != http.StatusAccepted {
		t.Fatalf("first ranged patch: expected 202, got %d (%s)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Range"); got != "0-65535" {
		t.Fatalf("expected Range 0-65535, got %q", got)
	}

	w = patch("bytes=65536-102399", data[64*1024:])
	if w.Code != http.StatusAccepted {
		t.Fatalf("bytes= prefixed range: expected 202, got %d (%s)", w.Code, w.Body.String())
	}

	w = uploadRequest(t, r, http.MethodPut, location+"?digest="+digest, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("put: expected 201, got %d (%s)", w.Code, w.Body.String())
	}
	assertBlobRegistered(t, store, "demo/app", digest, data)
}

func TestPatchContentRangeMismatch(t *testing.T) {
	r, _ := setupUploadServer(t)
	data := randomBytes(t, 50*1024)

	location, _ := startSession(t, r, "demo/app")

	patch := func(rangeHeader string, chunk []byte) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPatch, "http://127.0.0.1"+location, bytes.NewReader(chunk))
		req.Header.Set("Content-Range", rangeHeader)
		r.ServeHTTP(w, req)
		return w
	}

	// Gap: claims start at 10 but session is empty.
	w := patch("10-19", data[:10])
	if w.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("expected 416, got %d (%s)", w.Code, w.Body.String())
	}

	// Valid write of 10 bytes.
	w = patch("0-9", data[:10])
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", w.Code)
	}

	// Overlap: claims start at 5, session is at 10.
	w = patch("5-19", data[10:20])
	if w.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("expected 416 for overlap, got %d", w.Code)
	}

	// Malformed values.
	for _, bad := range []string{"abc", "1-0", "-5", "5-"} {
		w = patch(bad, data[:1])
		if w.Code != http.StatusRequestedRangeNotSatisfiable {
			t.Fatalf("range %q: expected 416, got %d", bad, w.Code)
		}
	}
}

func TestPutWithoutDigest(t *testing.T) {
	r, _ := setupUploadServer(t)

	location, _ := startSession(t, r, "demo/app")

	w := uploadRequest(t, r, http.MethodPut, location, []byte("data"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (%s)", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("DIGEST_INVALID")) {
		t.Fatalf("expected DIGEST_INVALID, got %q", w.Body.String())
	}
}

// Regression guard: docker pushes layers concurrently, and every concurrent
// upload to a fresh repository used to lose the repositories-name race with
// a 500 UNIQUE constraint error.
func TestConcurrentMonolithicUploadsToNewRepo(t *testing.T) {
	r, _ := setupUploadServer(t)

	const n = 8
	type result struct {
		code int
		body string
	}
	results := make(chan result, n)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			data := bytes.Repeat([]byte{byte(i)}, 16*1024+i)
			digest := digestOf(data)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v2/race/test/blobs/uploads/?digest="+digest, bytes.NewReader(data))
			r.ServeHTTP(w, req)
			results <- result{w.Code, w.Body.String()}
		}(i)
	}
	wg.Wait()
	close(results)

	for res := range results {
		if res.code != http.StatusCreated {
			t.Fatalf("expected 201, got %d (%s)", res.code, res.body)
		}
	}

	repoRow, err := repo.FindOneBy[models.Repository](airwaysql.H{"name": "race/test"})
	if err != nil || repoRow == nil {
		t.Fatalf("expected one repository row, got %+v (%v)", repoRow, err)
	}
	links, err := repo.CountWhere[models.RepoBlob](airwaysql.H{"repo_id": repoRow.ID})
	if err != nil || links != n {
		t.Fatalf("expected %d repo_blobs links, got %d (%v)", n, links, err)
	}
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

func assertBlobRegistered(t *testing.T, store *blobstore.Store, repoName, digest string, data []byte) {
	t.Helper()

	rc, err := store.Get(digest)
	if err != nil {
		t.Fatalf("blob not on disk: %v", err)
	}
	defer rc.Close()
	onDisk, _ := io.ReadAll(rc)
	if !bytes.Equal(onDisk, data) {
		t.Fatalf("on-disk bytes differ from uploaded data")
	}

	r, err := repo.FindOneBy[models.Repository](airwaysql.H{"name": repoName})
	if err != nil || r == nil {
		t.Fatalf("expected repository %q, got %+v (%v)", repoName, r, err)
	}

	blob, err := repo.FindOneBy[models.Blob](airwaysql.H{"digest": digest})
	if err != nil || blob == nil {
		t.Fatalf("expected blobs row, got %+v (%v)", blob, err)
	}
	if blob.Size != int64(len(data)) {
		t.Fatalf("expected size %d, got %d", len(data), blob.Size)
	}

	linked, err := repo.ExistsWhere[models.RepoBlob](airwaysql.H{"repo_id": r.ID, "blob_id": blob.ID})
	if err != nil || !linked {
		t.Fatalf("expected repo_blobs link, got %v (%v)", linked, err)
	}

	uploadsDir := filepath.Join(store.Root, "uploads")
	entries, _ := os.ReadDir(uploadsDir)
	for _, e := range entries {
		t.Fatalf("leftover upload temp file: %s", e.Name())
	}
}
