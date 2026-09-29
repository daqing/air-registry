package v2_api

import (
	"net/http"
	"testing"

	"github.com/daqing/air-registry/app/services/gc"
)

func TestGCKeepsReferencedBlobs(t *testing.T) {
	r, store := setupManifestServer(t)
	config := seedBlob(t, store, []byte("gc-config"))
	shared := seedBlob(t, store, []byte("gc-shared"))
	unique := seedBlob(t, store, []byte("gc-unique"))
	otherConfig := seedBlob(t, store, []byte("gc-other-config"))
	otherLayer := seedBlob(t, store, []byte("gc-other-layer"))

	pushTestManifest(t, r, "demo/app", "v1", config, shared, unique)
	pushTestManifest(t, r, "demo/app", "v2", config, shared)
	pushTestManifest(t, r, "demo/other", "v1", otherConfig, otherLayer)

	stats, err := gc.Run(store)
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if stats.DeletedBlobs != 0 {
		t.Fatalf("expected nothing deleted, got %+v", stats)
	}
	if stats.KeptBlobs != 5 {
		t.Fatalf("expected 5 kept blobs, got %+v", stats)
	}
}

func TestGCIdempotent(t *testing.T) {
	r, store := setupManifestServer(t)
	config := seedBlob(t, store, []byte("idem-config"))
	layer := seedBlob(t, store, []byte("idem-layer"))
	orphan := seedBlob(t, store, []byte("idem-orphan"))
	digest := pushTestManifest(t, r, "demo/app", "v1", config, layer)

	w := deleteManifest(t, r, "/v2/demo/app/manifests/"+digest)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (%s)", w.Code, w.Body.String())
	}

	// First explicit run: everything is already gone (DELETE auto-runs GC).
	stats, err := gc.Run(store)
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	if stats.DeletedBlobs != 0 {
		t.Fatalf("expected idempotent gc, got %+v", stats)
	}
	assertNoBlobRow(t, config)
	assertNoBlobRow(t, layer)
	assertNoBlobRow(t, orphan)
}
