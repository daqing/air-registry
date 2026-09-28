package v2_api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetManifestByTag(t *testing.T) {
	r, store := setupManifestServer(t)
	config := seedBlob(t, store, []byte("config"))
	body := imageManifestBody(t, config, seedBlob(t, store, []byte("l1")))

	w := putManifest(t, r, "/v2/library/demo/manifests/v1", "application/vnd.oci.image.manifest.v1+json", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("setup put failed: %d", w.Code)
	}

	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v2/library/demo/manifests/v1", nil)
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/vnd.oci.image.manifest.v1+json" {
		t.Fatalf("expected stored media type, got %q", got)
	}
	if got := w.Header().Get("Docker-Content-Digest"); got != digestOf(body) {
		t.Fatalf("expected digest header %q, got %q", digestOf(body), got)
	}
	if !bytes.Equal(w.Body.Bytes(), body) {
		t.Fatalf("GET bytes differ from PUT bytes:\n got %q\nwant %q", w.Body.String(), string(body))
	}
}

func TestGetManifestByDigest(t *testing.T) {
	r, store := setupManifestServer(t)
	config := seedBlob(t, store, []byte("config"))
	body := imageManifestBody(t, config, seedBlob(t, store, []byte("l1")))
	digest := digestOf(body)

	putManifest(t, r, "/v2/demo/app/manifests/v1", "application/vnd.oci.image.manifest.v1+json", body)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v2/demo/app/manifests/"+digest, nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), body) {
		t.Fatalf("body mismatch")
	}
}

func TestHeadManifest(t *testing.T) {
	r, store := setupManifestServer(t)
	config := seedBlob(t, store, []byte("config"))
	body := imageManifestBody(t, config, seedBlob(t, store, []byte("l1")))

	putManifest(t, r, "/v2/demo/app/manifests/v1", "application/vnd.oci.image.manifest.v1+json", body)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodHead, "/v2/demo/app/manifests/v1", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got := w.Header().Get("Docker-Content-Digest"); got != digestOf(body) {
		t.Fatalf("expected digest header, got %q", got)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("HEAD must not return a body, got %q", w.Body.String())
	}
}

func TestGetManifestUnknownReturns404(t *testing.T) {
	r, _ := setupManifestServer(t)

	cases := []string{
		"/v2/demo/app/manifests/nope",                     // unknown tag
		"/v2/demo/app/manifests/" + digestOf([]byte("x")), // unknown digest
		"/v2/ghost/repo/manifests/v1",                     // unknown repository
	}
	for _, url := range cases {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, url, nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("%s: expected 404, got %d", url, w.Code)
		}
		if !bytes.Contains(w.Body.Bytes(), []byte("MANIFEST_UNKNOWN")) {
			t.Fatalf("%s: expected MANIFEST_UNKNOWN, got %q", url, w.Body.String())
		}
	}
}

func TestListTags(t *testing.T) {
	r, store := setupManifestServer(t)
	config := seedBlob(t, store, []byte("config"))
	body := imageManifestBody(t, config, seedBlob(t, store, []byte("l1")))

	putManifest(t, r, "/v2/demo/app/manifests/v2", "application/vnd.oci.image.manifest.v1+json", body)
	putManifest(t, r, "/v2/demo/app/manifests/v1", "application/vnd.oci.image.manifest.v1+json", body)
	putManifest(t, r, "/v2/demo/app/manifests/latest", "application/vnd.oci.image.manifest.v1+json", body)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v2/demo/app/tags/list", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}

	var resp struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Name != "demo/app" {
		t.Fatalf("unexpected name %q", resp.Name)
	}
	want := []string{"latest", "v1", "v2"}
	if len(resp.Tags) != len(want) {
		t.Fatalf("expected tags %v, got %v", want, resp.Tags)
	}
	for i := range want {
		if resp.Tags[i] != want[i] {
			t.Fatalf("expected sorted tags %v, got %v", want, resp.Tags)
		}
	}
}

func TestListTagsAcceptsPaginationParams(t *testing.T) {
	r, store := setupManifestServer(t)
	config := seedBlob(t, store, []byte("config"))
	body := imageManifestBody(t, config, seedBlob(t, store, []byte("l1")))
	putManifest(t, r, "/v2/demo/app/manifests/v1", "application/vnd.oci.image.manifest.v1+json", body)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v2/demo/app/tags/list?n=1&last=v0", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("pagination params must be tolerated, got %d (%s)", w.Code, w.Body.String())
	}
}

func TestListTagsUnknownRepoReturns404(t *testing.T) {
	r, _ := setupManifestServer(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v2/ghost/repo/tags/list", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("NAME_UNKNOWN")) {
		t.Fatalf("expected NAME_UNKNOWN, got %q", w.Body.String())
	}
}
