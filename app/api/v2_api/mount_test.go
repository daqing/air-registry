package v2_api

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/daqing/airway/lib/repo"
	airwaysql "github.com/daqing/airway/lib/sql"

	"github.com/daqing/air-registry/app/models"
)

// seedBlob uploads data into repoName through the monolithic endpoint so the
// blob exists on disk, in blobs and in repo_blobs.
func pushBlob(t *testing.T, r *gin.Engine, repoName string, data []byte) string {
	t.Helper()
	digest := digestOf(data)
	w := uploadRequest(t, r, http.MethodPost, "/v2/"+repoName+"/blobs/uploads/?digest="+digest, data)
	if w.Code != http.StatusCreated {
		t.Fatalf("seed %s: expected 201, got %d (%s)", repoName, w.Code, w.Body.String())
	}
	return digest
}

func TestMountFromSourceRepo(t *testing.T) {
	r, store := setupUploadServer(t)
	data := randomBytes(t, 128*1024)
	digest := pushBlob(t, r, "demo/base", data)

	w := uploadRequest(t, r, http.MethodPost, "/v2/demo/app/blobs/uploads/?mount="+digest+"&from=demo/base", nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (%s)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Docker-Content-Digest"); got != digest {
		t.Fatalf("expected digest header %q, got %q", digest, got)
	}
	if got := w.Header().Get("Location"); got != "/v2/demo/app/blobs/"+digest {
		t.Fatalf("unexpected location %q", got)
	}
	if got := w.Header().Get("Docker-Upload-UUID"); got != "" {
		t.Fatalf("mount must not open an upload session, got uuid %q", got)
	}

	// The mounted blob is served from the target repo.
	w = uploadRequest(t, r, http.MethodGet, "/v2/demo/app/blobs/"+digest, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), data) {
		t.Fatalf("mounted blob bytes differ from source")
	}

	// One blob row shared by both repos; no upload temp files left behind.
	assertBlobRegistered(t, store, "demo/base", digest, data)
	assertBlobRegistered(t, store, "demo/app", digest, data)
}

func TestMountUnknownDigestFallsBackToUpload(t *testing.T) {
	r, store := setupUploadServer(t)
	data := randomBytes(t, 64*1024)
	digest := digestOf(data)

	// Source repo exists but does not hold the requested digest.
	pushBlob(t, r, "demo/base", randomBytes(t, 32*1024))

	w := uploadRequest(t, r, http.MethodPost, "/v2/demo/app/blobs/uploads/?mount="+digest+"&from=demo/base", nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202 fallback, got %d (%s)", w.Code, w.Body.String())
	}
	location := w.Header().Get("Location")
	if location == "" {
		t.Fatalf("missing Location header")
	}
	if got := w.Header().Get("Docker-Upload-UUID"); !uuidPattern.MatchString(got) {
		t.Fatalf("expected a Docker-Upload-UUID, got %q", got)
	}

	// The fallback session completes the upload end to end.
	w = uploadRequest(t, r, http.MethodPut, location+"?digest="+digest, data)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (%s)", w.Code, w.Body.String())
	}
	assertBlobRegistered(t, store, "demo/app", digest, data)
}

func TestMountFromUnknownRepoFallsBackToUpload(t *testing.T) {
	r, _ := setupUploadServer(t)
	digest := digestOf(randomBytes(t, 1024))

	w := uploadRequest(t, r, http.MethodPost, "/v2/demo/app/blobs/uploads/?mount="+digest+"&from=demo/missing", nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202 fallback, got %d (%s)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Docker-Upload-UUID"); !uuidPattern.MatchString(got) {
		t.Fatalf("expected a Docker-Upload-UUID, got %q", got)
	}
}

func TestMountInvalidDigest(t *testing.T) {
	r, _ := setupUploadServer(t)

	w := uploadRequest(t, r, http.MethodPost, "/v2/demo/app/blobs/uploads/?mount=not-a-digest&from=demo/base", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (%s)", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("DIGEST_INVALID")) {
		t.Fatalf("expected DIGEST_INVALID, got %q", w.Body.String())
	}
}

func TestMountIsIdempotent(t *testing.T) {
	r, store := setupUploadServer(t)
	data := randomBytes(t, 8*1024)
	digest := pushBlob(t, r, "demo/base", data)

	for i := 0; i < 2; i++ {
		w := uploadRequest(t, r, http.MethodPost, "/v2/demo/app/blobs/uploads/?mount="+digest+"&from=demo/base", nil)
		if w.Code != http.StatusCreated {
			t.Fatalf("mount %d: expected 201, got %d (%s)", i, w.Code, w.Body.String())
		}
	}
	assertBlobRegistered(t, store, "demo/app", digest, data)

	repoApp, err := repo.FindOneBy[models.Repository](airwaysql.H{"name": "demo/app"})
	if err != nil || repoApp == nil {
		t.Fatalf("expected repository demo/app, got %+v (%v)", repoApp, err)
	}
	blob, err := repo.FindOneBy[models.Blob](airwaysql.H{"digest": digest})
	if err != nil || blob == nil {
		t.Fatalf("expected blobs row, got %+v (%v)", blob, err)
	}
	n, err := repo.CountWhere[models.RepoBlob](airwaysql.H{"repo_id": repoApp.ID, "blob_id": blob.ID})
	if err != nil || n != 1 {
		t.Fatalf("expected exactly one repo_blobs link, got %d (%v)", n, err)
	}
}
