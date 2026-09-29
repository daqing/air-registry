package v2_api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/daqing/airway/lib/repo"
	airwaysql "github.com/daqing/airway/lib/sql"

	"github.com/daqing/air-registry/app/models"
	"github.com/daqing/air-registry/app/services/manifests"
)

func deleteManifest(t *testing.T, r *gin.Engine, url string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, url, nil)
	r.ServeHTTP(w, req)
	return w
}

func assertBlobLinkedToRepo(t *testing.T, repoName, digest string, want bool) {
	t.Helper()

	r, err := repo.FindOneBy[models.Repository](airwaysql.H{"name": repoName})
	if err != nil || r == nil {
		t.Fatalf("expected repository %q, got %+v (%v)", repoName, r, err)
	}
	blob, err := repo.FindOneBy[models.Blob](airwaysql.H{"digest": digest})
	if err != nil || blob == nil {
		t.Fatalf("expected blobs row for %s, got %+v (%v)", digest, blob, err)
	}
	linked, err := repo.ExistsWhere[models.RepoBlob](airwaysql.H{"repo_id": r.ID, "blob_id": blob.ID})
	if err != nil {
		t.Fatalf("check repo_blobs: %v", err)
	}
	if linked != want {
		t.Fatalf("expected repo_blobs link=%v for %s in %q", want, digest, repoName)
	}
}

func assertTagsEmpty(t *testing.T, r *gin.Engine, repoName string) {
	t.Helper()
	w := uploadRequest(t, r, http.MethodGet, "/v2/"+repoName+"/tags/list", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("tags/list: expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"tags":[]`)) && !bytes.Contains(w.Body.Bytes(), []byte(`"tags": []`)) {
		t.Fatalf("expected empty tags, got %q", w.Body.String())
	}
}

func pushTestManifest(t *testing.T, r *gin.Engine, repoName, tag, config string, layers ...string) (digest string) {
	t.Helper()
	body := imageManifestBody(t, config, layers...)
	w := putManifest(t, r, "/v2/"+repoName+"/manifests/"+tag, manifests.MediaTypeOCIManifest, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("push manifest %s:%s: expected 201, got %d (%s)", repoName, tag, w.Code, w.Body.String())
	}
	return manifests.Digest(body)
}

func TestDeleteManifestByDigest(t *testing.T) {
	r, store := setupManifestServer(t)
	config := seedBlob(t, store, []byte("delete-config"))
	layer := seedBlob(t, store, []byte("delete-layer"))
	digest := pushTestManifest(t, r, "demo/app", "v1", config, layer)

	w := deleteManifest(t, r, "/v2/demo/app/manifests/"+digest)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (%s)", w.Code, w.Body.String())
	}

	// Manifest and tag are gone.
	w = uploadRequest(t, r, http.MethodGet, "/v2/demo/app/manifests/"+digest, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", w.Code)
	}
	w = uploadRequest(t, r, http.MethodGet, "/v2/demo/app/manifests/v1", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for tag after delete, got %d", w.Code)
	}
	assertTagsEmpty(t, r, "demo/app")

	// repo_blobs links are removed; blob files stay on disk for GC.
	assertBlobLinkedToRepo(t, "demo/app", config, false)
	assertBlobLinkedToRepo(t, "demo/app", layer, false)
	if ok, err := store.Has(config); err != nil || !ok {
		t.Fatalf("blob file must remain on disk until GC, has=%v (%v)", ok, err)
	}
}

func TestDeleteManifestByTag(t *testing.T) {
	r, store := setupManifestServer(t)
	config := seedBlob(t, store, []byte("tag-config"))
	layer := seedBlob(t, store, []byte("tag-layer"))
	digest := pushTestManifest(t, r, "demo/app", "v1", config, layer)

	w := deleteManifest(t, r, "/v2/demo/app/manifests/v1")
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (%s)", w.Code, w.Body.String())
	}

	w = uploadRequest(t, r, http.MethodGet, "/v2/demo/app/manifests/"+digest, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete by tag, got %d", w.Code)
	}
	assertTagsEmpty(t, r, "demo/app")
}

func TestDeleteManifestRemovesAllTagsOfDigest(t *testing.T) {
	r, store := setupManifestServer(t)
	config := seedBlob(t, store, []byte("multi-config"))
	layer := seedBlob(t, store, []byte("multi-layer"))
	digest := pushTestManifest(t, r, "demo/app", "v1", config, layer)

	// Point a second tag at the same manifest.
	body := imageManifestBody(t, config, layer)
	w := putManifest(t, r, "/v2/demo/app/manifests/latest", manifests.MediaTypeOCIManifest, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("push second tag: expected 201, got %d (%s)", w.Code, w.Body.String())
	}

	w = deleteManifest(t, r, "/v2/demo/app/manifests/"+digest)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (%s)", w.Code, w.Body.String())
	}

	for _, ref := range []string{"v1", "latest"} {
		w = uploadRequest(t, r, http.MethodGet, "/v2/demo/app/manifests/"+ref, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for %s, got %d", ref, w.Code)
		}
	}
}

func TestDeleteManifestKeepsBlobsSharedWithSiblingManifest(t *testing.T) {
	r, store := setupManifestServer(t)
	config := seedBlob(t, store, []byte("shared-config"))
	shared := seedBlob(t, store, []byte("shared-layer"))
	unique := seedBlob(t, store, []byte("unique-layer"))

	pushTestManifest(t, r, "demo/app", "v1", config, shared, unique)
	pushTestManifest(t, r, "demo/app", "v2", config, shared)

	w := deleteManifest(t, r, "/v2/demo/app/manifests/v1")
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (%s)", w.Code, w.Body.String())
	}

	// v2 must stay pullable with all its blobs.
	w = uploadRequest(t, r, http.MethodGet, "/v2/demo/app/manifests/v2", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected v2 to survive, got %d", w.Code)
	}
	assertBlobLinkedToRepo(t, "demo/app", config, true)
	assertBlobLinkedToRepo(t, "demo/app", shared, true)

	// The layer only v1 used is unlinked from the repository.
	assertBlobLinkedToRepo(t, "demo/app", unique, false)
}

func TestDeleteManifestKeepsOtherRepoLinks(t *testing.T) {
	r, store := setupManifestServer(t)
	config := seedBlob(t, store, []byte("mount-config"))
	layer := seedBlob(t, store, []byte("mount-layer"))
	digest := pushTestManifest(t, r, "demo/base", "v1", config, layer)

	// Mount both blobs into demo/app, then delete demo/base's manifest.
	for _, d := range []string{config, layer} {
		w := uploadRequest(t, r, http.MethodPost, "/v2/demo/app/blobs/uploads/?mount="+d+"&from=demo/base", nil)
		if w.Code != http.StatusCreated {
			t.Fatalf("mount %s: expected 201, got %d (%s)", d, w.Code, w.Body.String())
		}
	}

	w := deleteManifest(t, r, "/v2/demo/base/manifests/"+digest)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (%s)", w.Code, w.Body.String())
	}

	// demo/base's links are gone, demo/app's survive.
	assertBlobLinkedToRepo(t, "demo/base", config, false)
	assertBlobLinkedToRepo(t, "demo/base", layer, false)
	assertBlobLinkedToRepo(t, "demo/app", config, true)
	assertBlobLinkedToRepo(t, "demo/app", layer, true)
}

func TestDeleteManifestUnknown(t *testing.T) {
	r, store := setupManifestServer(t)
	config := seedBlob(t, store, []byte("known-config"))
	layer := seedBlob(t, store, []byte("known-layer"))
	pushTestManifest(t, r, "demo/app", "v1", config, layer)

	unknownDigest := digestOf([]byte("never pushed"))
	for _, url := range []string{
		"/v2/demo/app/manifests/" + unknownDigest,
		"/v2/demo/app/manifests/nope",
		"/v2/demo/missing/manifests/" + unknownDigest,
	} {
		w := deleteManifest(t, r, url)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s: expected 404, got %d (%s)", url, w.Code, w.Body.String())
		}
		if !bytes.Contains(w.Body.Bytes(), []byte("MANIFEST_UNKNOWN")) {
			t.Fatalf("%s: expected MANIFEST_UNKNOWN, got %q", url, w.Body.String())
		}
	}
}
