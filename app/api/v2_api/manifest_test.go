package v2_api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/daqing/airway/lib/repo"
	airwaysql "github.com/daqing/airway/lib/sql"

	"github.com/daqing/air-registry/app/models"
	"github.com/daqing/air-registry/app/services/blobstore"
	"github.com/daqing/air-registry/app/services/manifests"
)

type testDescriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int    `json:"size"`
}

type testManifest struct {
	SchemaVersion int              `json:"schemaVersion"`
	MediaType     string           `json:"mediaType"`
	Config        *testDescriptor  `json:"config,omitempty"`
	Layers        []testDescriptor `json:"layers,omitempty"`
	Manifests     []testDescriptor `json:"manifests,omitempty"`
	Subject       *testDescriptor  `json:"subject,omitempty"`
	ArtifactType  string           `json:"artifactType,omitempty"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

func setupManifestServer(t *testing.T) (*gin.Engine, *blobstore.Store) {
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
	h := &Handler{Blobs: store}
	r := gin.New()
	h.Routes(r)
	return r, store
}

// seedBlob stores data in the blob store and registers the blobs table row
// that manifest pushes require.
func seedBlob(t *testing.T, store *blobstore.Store, data []byte) string {
	t.Helper()
	digest := digestOf(data)
	if _, err := store.Put(bytes.NewReader(data), digest); err != nil {
		t.Fatalf("seed blob file: %v", err)
	}
	path, err := store.Path(digest)
	if err != nil {
		t.Fatalf("blob path: %v", err)
	}
	now := time.Now()
	if _, err := repo.CreateFrom[models.Blob](airwaysql.H{
		"digest":     digest,
		"size":       int64(len(data)),
		"path":       path,
		"created_at": now,
		"updated_at": now,
	}); err != nil {
		t.Fatalf("seed blob row: %v", err)
	}
	return digest
}

func putManifest(t *testing.T, r *gin.Engine, url, contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	r.ServeHTTP(w, req)
	return w
}

func imageManifestBody(t *testing.T, config string, layers ...string) []byte {
	t.Helper()
	m := testManifest{
		SchemaVersion: 2,
		MediaType:     manifests.MediaTypeOCIManifest,
		Config:        &testDescriptor{MediaType: "application/vnd.oci.image.config.v1+json", Digest: config, Size: 1},
	}
	for _, l := range layers {
		m.Layers = append(m.Layers, testDescriptor{MediaType: "application/vnd.oci.image.layer.v1.tar+gzip", Digest: l, Size: 2})
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func TestPutManifestByTag(t *testing.T) {
	r, store := setupManifestServer(t)
	config := seedBlob(t, store, []byte("config"))
	layer1 := seedBlob(t, store, []byte("layer-1"))
	layer2 := seedBlob(t, store, []byte("layer-2"))
	body := imageManifestBody(t, config, layer1, layer2)

	w := putManifest(t, r, "/v2/library/demo/manifests/v1", manifests.MediaTypeOCIManifest, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (%s)", w.Code, w.Body.String())
	}

	digest := digestOf(body)
	if got := w.Header().Get("Docker-Content-Digest"); got != digest {
		t.Fatalf("expected digest header %q, got %q", digest, got)
	}
	if got := w.Header().Get("Location"); got != "/v2/library/demo/manifests/"+digest {
		t.Fatalf("unexpected location %q", got)
	}

	repoRow, err := repo.FindOneBy[models.Repository](airwaysql.H{"name": "library/demo"})
	if err != nil || repoRow == nil {
		t.Fatalf("expected repository row, got %+v (%v)", repoRow, err)
	}

	m, err := repo.FindOneBy[models.Manifest](airwaysql.H{"repo_id": repoRow.ID, "digest": digest})
	if err != nil || m == nil {
		t.Fatalf("expected manifest row, got %+v (%v)", m, err)
	}
	if m.MediaType != manifests.MediaTypeOCIManifest || m.Content != string(body) || m.Size != int64(len(body)) {
		t.Fatalf("manifest row mismatch: %+v", m)
	}
	if m.ArtifactType != nil || m.SubjectDigest != nil {
		t.Fatalf("unexpected optional fields %+v %+v", m.ArtifactType, m.SubjectDigest)
	}

	tag, err := repo.FindOneBy[models.Tag](airwaysql.H{"repo_id": repoRow.ID, "name": "v1"})
	if err != nil || tag == nil || tag.ManifestDigest != digest {
		t.Fatalf("expected tag v1 → %s, got %+v (%v)", digest, tag, err)
	}

	links, err := repo.CountWhere[models.RepoBlob](airwaysql.H{"repo_id": repoRow.ID})
	if err != nil || links != 3 {
		t.Fatalf("expected 3 repo_blob links, got %d (%v)", links, err)
	}
}

func TestPutManifestIsIdempotent(t *testing.T) {
	r, store := setupManifestServer(t)
	config := seedBlob(t, store, []byte("config"))
	body := imageManifestBody(t, config, seedBlob(t, store, []byte("l1")))

	for i := 0; i < 2; i++ {
		w := putManifest(t, r, "/v2/demo/app/manifests/v1", manifests.MediaTypeOCIManifest, body)
		if w.Code != http.StatusCreated {
			t.Fatalf("put %d: expected 201, got %d (%s)", i, w.Code, w.Body.String())
		}
	}

	repoRow, _ := repo.FindOneBy[models.Repository](airwaysql.H{"name": "demo/app"})
	tags, _ := repo.CountWhere[models.Tag](airwaysql.H{"repo_id": repoRow.ID})
	manifestsCount, _ := repo.CountWhere[models.Manifest](airwaysql.H{"repo_id": repoRow.ID})
	links, _ := repo.CountWhere[models.RepoBlob](airwaysql.H{"repo_id": repoRow.ID})
	if tags != 1 || manifestsCount != 1 || links != 2 {
		t.Fatalf("expected 1 tag / 1 manifest / 2 links, got %d / %d / %d", tags, manifestsCount, links)
	}
}

func TestPutManifestByDigestReference(t *testing.T) {
	r, store := setupManifestServer(t)
	config := seedBlob(t, store, []byte("config"))
	body := imageManifestBody(t, config, seedBlob(t, store, []byte("l1")))
	digest := digestOf(body)

	w := putManifest(t, r, "/v2/demo/app/manifests/"+digest, manifests.MediaTypeOCIManifest, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (%s)", w.Code, w.Body.String())
	}

	repoRow, _ := repo.FindOneBy[models.Repository](airwaysql.H{"name": "demo/app"})
	tags, _ := repo.CountWhere[models.Tag](airwaysql.H{"repo_id": repoRow.ID})
	if tags != 0 {
		t.Fatalf("digest reference must not create a tag, found %d", tags)
	}

	// A reference that does not match the body is rejected.
	w = putManifest(t, r, "/v2/demo/app/manifests/"+digestOf([]byte("other")), manifests.MediaTypeOCIManifest, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for mismatched digest ref, got %d", w.Code)
	}
}

func TestPutManifestWithSubject(t *testing.T) {
	r, store := setupManifestServer(t)
	config := seedBlob(t, store, []byte("config"))
	body := imageManifestBody(t, config, seedBlob(t, store, []byte("sbom-data")))

	m := testManifest{}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	m.ArtifactType = "application/vnd.example.sbom"
	m.Subject = &testDescriptor{MediaType: manifests.MediaTypeOCIManifest, Digest: digestOf([]byte("subject-image"))}
	body, _ = json.Marshal(m)

	w := putManifest(t, r, "/v2/demo/app/manifests/sbom-v1", manifests.MediaTypeOCIManifest, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (%s)", w.Code, w.Body.String())
	}

	repoRow, _ := repo.FindOneBy[models.Repository](airwaysql.H{"name": "demo/app"})
	stored, err := repo.FindOneBy[models.Manifest](airwaysql.H{"repo_id": repoRow.ID, "digest": digestOf(body)})
	if err != nil || stored == nil {
		t.Fatalf("manifest not found: %v", err)
	}
	if stored.SubjectDigest == nil || *stored.SubjectDigest != digestOf([]byte("subject-image")) {
		t.Fatalf("subject_digest not stored: %+v", stored.SubjectDigest)
	}
	if stored.ArtifactType == nil || *stored.ArtifactType != "application/vnd.example.sbom" {
		t.Fatalf("artifact_type not stored: %+v", stored.ArtifactType)
	}
}

func TestPutIndex(t *testing.T) {
	r, store := setupManifestServer(t)

	childBody1 := imageManifestBody(t, seedBlob(t, store, []byte("c1")), seedBlob(t, store, []byte("l1")))
	childBody2 := imageManifestBody(t, seedBlob(t, store, []byte("c2")), seedBlob(t, store, []byte("l2")))
	putManifest(t, r, "/v2/demo/app/manifests/amd64", manifests.MediaTypeOCIManifest, childBody1)
	putManifest(t, r, "/v2/demo/app/manifests/arm64", manifests.MediaTypeOCIManifest, childBody2)

	index := testManifest{
		SchemaVersion: 2,
		MediaType:     manifests.MediaTypeOCIIndex,
		Manifests: []testDescriptor{
			{MediaType: manifests.MediaTypeOCIManifest, Digest: digestOf(childBody1), Size: len(childBody1)},
			{MediaType: manifests.MediaTypeOCIManifest, Digest: digestOf(childBody2), Size: len(childBody2)},
		},
	}
	indexBody, _ := json.Marshal(index)

	w := putManifest(t, r, "/v2/demo/app/manifests/latest", manifests.MediaTypeOCIIndex, indexBody)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d (%s)", w.Code, w.Body.String())
	}

	repoRow, _ := repo.FindOneBy[models.Repository](airwaysql.H{"name": "demo/app"})
	stored, err := repo.FindOneBy[models.Manifest](airwaysql.H{"repo_id": repoRow.ID, "digest": digestOf(indexBody)})
	if err != nil || stored == nil {
		t.Fatalf("index not stored: %v", err)
	}
	if stored.MediaType != manifests.MediaTypeOCIIndex {
		t.Fatalf("unexpected stored index media type: %+v", stored)
	}
}

func TestPutIndexUnknownChild(t *testing.T) {
	r, _ := setupManifestServer(t)

	index := testManifest{
		SchemaVersion: 2,
		MediaType:     manifests.MediaTypeOCIIndex,
		Manifests:     []testDescriptor{{MediaType: manifests.MediaTypeOCIManifest, Digest: digestOf([]byte("ghost"))}},
	}
	body, _ := json.Marshal(index)

	w := putManifest(t, r, "/v2/demo/app/manifests/latest", manifests.MediaTypeOCIIndex, body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("MANIFEST_BLOB_UNKNOWN")) {
		t.Fatalf("expected MANIFEST_BLOB_UNKNOWN, got %q", w.Body.String())
	}
}

func TestPutManifestRejectsBadInput(t *testing.T) {
	r, store := setupManifestServer(t)
	config := seedBlob(t, store, []byte("config"))
	body := imageManifestBody(t, config, seedBlob(t, store, []byte("l1")))

	missingLayer := imageManifestBody(t, config, digestOf([]byte("never-uploaded")))

	cases := []struct {
		name        string
		url         string
		contentType string
		body        []byte
		code        string
	}{
		{"broken json", "/v2/demo/app/manifests/v1", manifests.MediaTypeOCIManifest, []byte(`{oops`), "MANIFEST_INVALID"},
		{"schemaVersion 1", "/v2/demo/app/manifests/v1", manifests.MediaTypeOCIManifest, []byte(`{"schemaVersion":1,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`), "MANIFEST_INVALID"},
		{"no media type", "/v2/demo/app/manifests/v1", "", []byte(`{"schemaVersion":2}`), "MANIFEST_INVALID"},
		{"missing layer blob", "/v2/demo/app/manifests/v1", manifests.MediaTypeOCIManifest, missingLayer, "MANIFEST_BLOB_UNKNOWN"},
		{"bad tag", "/v2/demo/app/manifests/-bad..tag", manifests.MediaTypeOCIManifest, body, "TAG_INVALID"},
		{"bad digest ref", "/v2/demo/app/manifests/sha256:zzz", manifests.MediaTypeOCIManifest, body, "MANIFEST_INVALID"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := putManifest(t, r, tc.url, tc.contentType, tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d (%s)", w.Code, w.Body.String())
			}
			if !bytes.Contains(w.Body.Bytes(), []byte(tc.code)) {
				t.Fatalf("expected error code %q, got %q", tc.code, w.Body.String())
			}
		})
	}
}
