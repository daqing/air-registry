package v2_api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/daqing/air-registry/app/services/blobstore"
	"github.com/daqing/air-registry/app/services/manifests"
)

type referrersResponse struct {
	SchemaVersion int    `json:"schemaVersion"`
	MediaType     string `json:"mediaType"`
	Manifests     []struct {
		MediaType    string            `json:"mediaType"`
		Digest       string            `json:"digest"`
		Size         int64             `json:"size"`
		ArtifactType string            `json:"artifactType"`
		Annotations  map[string]string `json:"annotations"`
	} `json:"manifests"`
}

// pushSubjectAndReferrers stores a subject manifest tagged v1 plus the
// given referrer manifests, returning the subject digest.
func pushSubjectAndReferrers(t *testing.T, r *gin.Engine, store *blobstore.Store, repoName string, referrers ...[]byte) (subject string) {
	t.Helper()

	config := seedBlob(t, store, []byte("subject-config"))
	layer := seedBlob(t, store, []byte("subject-layer"))
	subjectBody := imageManifestBody(t, config, layer)
	w := putManifest(t, r, "/v2/"+repoName+"/manifests/v1", manifests.MediaTypeOCIManifest, subjectBody)
	if w.Code != http.StatusCreated {
		t.Fatalf("push subject: expected 201, got %d (%s)", w.Code, w.Body.String())
	}
	subject = manifests.Digest(subjectBody)

	for i, body := range referrers {
		w := putManifest(t, r, "/v2/"+repoName+"/manifests/"+itoa(i), manifests.MediaTypeOCIManifest, body)
		if w.Code != http.StatusCreated {
			t.Fatalf("push referrer %d: expected 201, got %d (%s)", i, w.Code, w.Body.String())
		}
	}
	return subject
}

func referrerBody(t *testing.T, artifactType, subject string, annotations map[string]string) []byte {
	t.Helper()
	m := testManifest{
		SchemaVersion: 2,
		MediaType:     manifests.MediaTypeOCIManifest,
		ArtifactType:  artifactType,
		Subject:       &testDescriptor{MediaType: manifests.MediaTypeOCIManifest, Digest: subject},
		Annotations:   annotations,
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal referrer: %v", err)
	}
	return data
}

func getReferrers(t *testing.T, r *gin.Engine, url string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	r.ServeHTTP(w, req)
	return w
}

func decodeReferrers(t *testing.T, w *httptest.ResponseRecorder) referrersResponse {
	t.Helper()
	if ct := w.Header().Get("Content-Type"); ct != manifests.MediaTypeOCIIndex {
		t.Fatalf("expected index content type, got %q", ct)
	}
	var resp referrersResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode referrers response: %v (%s)", err, w.Body.String())
	}
	if resp.Manifests == nil {
		t.Fatalf("manifests must be [] not null (%s)", w.Body.String())
	}
	return resp
}

func TestReferrersEmptyForUnknownSubject(t *testing.T) {
	r, store := setupManifestServer(t)
	subject := pushSubjectAndReferrers(t, r, store, "demo/app")

	w := getReferrers(t, r, "/v2/demo/app/referrers/"+digestOf([]byte("unreferenced")))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	resp := decodeReferrers(t, w)
	if len(resp.Manifests) != 0 {
		t.Fatalf("expected empty index, got %v", resp.Manifests)
	}

	// Unknown repository: still an empty index, not 404.
	w = getReferrers(t, r, "/v2/demo/missing/referrers/"+subject)
	if w.Code != http.StatusOK || len(decodeReferrers(t, w).Manifests) != 0 {
		t.Fatalf("expected 200 empty index for unknown repo, got %d (%s)", w.Code, w.Body.String())
	}
}

func TestReferrersListsReferrers(t *testing.T) {
	r, store := setupManifestServer(t)

	config := seedBlob(t, store, []byte("subject-config"))
	layer := seedBlob(t, store, []byte("subject-layer"))
	subjectBody := imageManifestBody(t, config, layer)
	w := putManifest(t, r, "/v2/demo/app/manifests/v1", manifests.MediaTypeOCIManifest, subjectBody)
	if w.Code != http.StatusCreated {
		t.Fatalf("push subject: expected 201, got %d (%s)", w.Code, w.Body.String())
	}
	subject := manifests.Digest(subjectBody)

	sbomBody := referrerBody(t, "application/vnd.example.sbom", subject, map[string]string{"org.example.version": "1.0"})
	sigBody := referrerBody(t, "application/vnd.example.signature", subject, nil)

	sbomDigest := manifests.Digest(sbomBody)
	sigDigest := manifests.Digest(sigBody)

	for tag, body := range map[string][]byte{"ref-sbom": sbomBody, "ref-sig": sigBody} {
		w := putManifest(t, r, "/v2/demo/app/manifests/"+tag, manifests.MediaTypeOCIManifest, body)
		if w.Code != http.StatusCreated {
			t.Fatalf("push referrer %s: expected 201, got %d (%s)", tag, w.Code, w.Body.String())
		}
	}

	w = getReferrers(t, r, "/v2/demo/app/referrers/"+subject)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	resp := decodeReferrers(t, w)
	if resp.SchemaVersion != 2 || resp.MediaType != manifests.MediaTypeOCIIndex {
		t.Fatalf("unexpected envelope %+v", resp)
	}
	if len(resp.Manifests) != 2 {
		t.Fatalf("expected 2 referrers, got %v", resp.Manifests)
	}

	byDigest := map[string]struct {
		MediaType    string
		Size         int64
		ArtifactType string
		Annotations  map[string]string
	}{}
	for _, d := range resp.Manifests {
		byDigest[d.Digest] = struct {
			MediaType    string
			Size         int64
			ArtifactType string
			Annotations  map[string]string
		}{d.MediaType, d.Size, d.ArtifactType, d.Annotations}
	}

	sbom, ok := byDigest[sbomDigest]
	if !ok {
		t.Fatalf("sbom referrer missing from %v", resp.Manifests)
	}
	if sbom.MediaType != manifests.MediaTypeOCIManifest {
		t.Fatalf("unexpected sbom media type %q", sbom.MediaType)
	}
	if sbom.Size != int64(len(sbomBody)) {
		t.Fatalf("expected sbom size %d, got %d", len(sbomBody), sbom.Size)
	}
	if sbom.ArtifactType != "application/vnd.example.sbom" {
		t.Fatalf("unexpected sbom artifact type %q", sbom.ArtifactType)
	}
	if sbom.Annotations["org.example.version"] != "1.0" {
		t.Fatalf("missing sbom annotations, got %v", sbom.Annotations)
	}

	sig, ok := byDigest[sigDigest]
	if !ok {
		t.Fatalf("signature referrer missing from %v", resp.Manifests)
	}
	if sig.ArtifactType != "application/vnd.example.signature" {
		t.Fatalf("unexpected sig artifact type %q", sig.ArtifactType)
	}
	if len(sig.Annotations) != 0 {
		t.Fatalf("expected no annotations, got %v", sig.Annotations)
	}
}

func TestReferrersFilterByArtifactType(t *testing.T) {
	r, store := setupManifestServer(t)

	config := seedBlob(t, store, []byte("f-config"))
	layer := seedBlob(t, store, []byte("f-layer"))
	subjectBody := imageManifestBody(t, config, layer)
	w := putManifest(t, r, "/v2/demo/app/manifests/v1", manifests.MediaTypeOCIManifest, subjectBody)
	if w.Code != http.StatusCreated {
		t.Fatalf("push subject: expected 201, got %d (%s)", w.Code, w.Body.String())
	}
	subject := manifests.Digest(subjectBody)

	sbom := referrerBody(t, "application/vnd.example.sbom", subject, nil)
	sig := referrerBody(t, "application/vnd.example.signature", subject, nil)
	for i, body := range [][]byte{sbom, sig} {
		w := putManifest(t, r, "/v2/demo/app/manifests/ref-"+itoa(i), manifests.MediaTypeOCIManifest, body)
		if w.Code != http.StatusCreated {
			t.Fatalf("push referrer %d: expected 201, got %d (%s)", i, w.Code, w.Body.String())
		}
	}

	w = getReferrers(t, r, "/v2/demo/app/referrers/"+subject+"?artifactType=application/vnd.example.sbom")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	resp := decodeReferrers(t, w)
	if len(resp.Manifests) != 1 || resp.Manifests[0].ArtifactType != "application/vnd.example.sbom" {
		t.Fatalf("expected only the sbom referrer, got %v", resp.Manifests)
	}

	w = getReferrers(t, r, "/v2/demo/app/referrers/"+subject+"?artifactType=application/vnd.example.nope")
	resp = decodeReferrers(t, w)
	if len(resp.Manifests) != 0 {
		t.Fatalf("expected empty filtered index, got %v", resp.Manifests)
	}
}

func TestReferrersInvalidDigest(t *testing.T) {
	r, _ := setupManifestServer(t)

	w := getReferrers(t, r, "/v2/demo/app/referrers/not-a-digest")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (%s)", w.Code, w.Body.String())
	}
}

func TestReferrersRejectsNonGet(t *testing.T) {
	r, _ := setupManifestServer(t)

	w := uploadRequest(t, r, http.MethodPost, "/v2/demo/app/referrers/"+digestOf([]byte("x")), nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
