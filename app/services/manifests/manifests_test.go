package manifests

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func fakeDigest(b byte) string {
	return fmt.Sprintf("sha256:%064x", b)
}

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
	Blobs         []testDescriptor `json:"blobs,omitempty"`
	Subject       *testDescriptor  `json:"subject,omitempty"`
	ArtifactType  string           `json:"artifactType,omitempty"`
}

func mustJSON(t *testing.T, m testManifest) []byte {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func TestParseImageManifest(t *testing.T) {
	content := mustJSON(t, testManifest{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCIManifest,
		Config:        &testDescriptor{MediaType: "application/vnd.oci.image.config.v1+json", Digest: fakeDigest(1), Size: 10},
		Layers: []testDescriptor{
			{MediaType: "application/vnd.oci.image.layer.v1.tar+gzip", Digest: fakeDigest(2), Size: 20},
			{MediaType: "application/vnd.oci.image.layer.v1.tar+gzip", Digest: fakeDigest(3), Size: 30},
		},
	})

	meta, err := Parse(content, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if meta.MediaType != MediaTypeOCIManifest {
		t.Fatalf("unexpected media type %q", meta.MediaType)
	}
	if meta.IsIndex {
		t.Fatalf("image manifest misdetected as index")
	}
	if len(meta.BlobDigests) != 3 || meta.BlobDigests[0] != fakeDigest(1) || meta.BlobDigests[2] != fakeDigest(3) {
		t.Fatalf("unexpected blob digests %v", meta.BlobDigests)
	}
	if meta.ArtifactType != nil || meta.SubjectDigest != nil {
		t.Fatalf("unexpected optional fields %+v %+v", meta.ArtifactType, meta.SubjectDigest)
	}
}

func TestParseContentTypeOverridesDoc(t *testing.T) {
	content := mustJSON(t, testManifest{
		SchemaVersion: 2,
		MediaType:     MediaTypeDockerManifest,
		Config:        &testDescriptor{Digest: fakeDigest(1)},
	})

	meta, err := Parse(content, MediaTypeOCIManifest+"; charset=utf-8")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if meta.MediaType != MediaTypeOCIManifest {
		t.Fatalf("expected header media type to win, got %q", meta.MediaType)
	}
}

func TestParseIndex(t *testing.T) {
	content := mustJSON(t, testManifest{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCIIndex,
		Manifests: []testDescriptor{
			{MediaType: MediaTypeOCIManifest, Digest: fakeDigest(10), Size: 100},
			{MediaType: MediaTypeOCIManifest, Digest: fakeDigest(11), Size: 200},
		},
	})

	meta, err := Parse(content, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !meta.IsIndex {
		t.Fatalf("index not detected")
	}
	if len(meta.ChildDigests) != 2 || meta.ChildDigests[0] != fakeDigest(10) {
		t.Fatalf("unexpected children %v", meta.ChildDigests)
	}
	if len(meta.BlobDigests) != 0 {
		t.Fatalf("index must not list blob digests, got %v", meta.BlobDigests)
	}
}

func TestParseArtifactManifestWithSubject(t *testing.T) {
	content := mustJSON(t, testManifest{
		SchemaVersion: 2,
		MediaType:     MediaTypeOCIManifest,
		ArtifactType:  "application/vnd.example.sbom",
		Blobs:         []testDescriptor{{MediaType: "application/json", Digest: fakeDigest(20), Size: 5}},
		Subject:       &testDescriptor{MediaType: MediaTypeOCIManifest, Digest: fakeDigest(21)},
	})

	meta, err := Parse(content, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if meta.ArtifactType == nil || *meta.ArtifactType != "application/vnd.example.sbom" {
		t.Fatalf("artifactType not parsed: %+v", meta.ArtifactType)
	}
	if meta.SubjectDigest == nil || *meta.SubjectDigest != fakeDigest(21) {
		t.Fatalf("subject not parsed: %+v", meta.SubjectDigest)
	}
	if len(meta.BlobDigests) != 1 || meta.BlobDigests[0] != fakeDigest(20) {
		t.Fatalf("unexpected blobs %v", meta.BlobDigests)
	}
}

func TestParseRejectsBadInput(t *testing.T) {
	cases := []struct {
		name    string
		content []byte
	}{
		{"broken json", []byte(`{not json`)},
		{"schemaVersion 1", mustJSON(t, testManifest{SchemaVersion: 1, MediaType: MediaTypeOCIManifest})},
		{"no media type", mustJSON(t, testManifest{SchemaVersion: 2})},
		{"bad layer digest", mustJSON(t, testManifest{
			SchemaVersion: 2,
			MediaType:     MediaTypeOCIManifest,
			Layers:        []testDescriptor{{Digest: "sha256:xyz"}},
		})},
		{"bad subject digest", mustJSON(t, testManifest{
			SchemaVersion: 2,
			MediaType:     MediaTypeOCIManifest,
			Subject:       &testDescriptor{Digest: "not-a-digest"},
		})},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.content, ""); !errors.Is(err, ErrInvalidManifest) {
				t.Fatalf("expected ErrInvalidManifest, got %v", err)
			}
		})
	}
}
