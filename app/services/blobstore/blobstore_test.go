package blobstore

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func randomBlob(t *testing.T, size int) []byte {
	t.Helper()
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		t.Fatalf("generate random data: %v", err)
	}
	return data
}

func TestPutGetRoundTrip(t *testing.T) {
	store := New(t.TempDir())
	data := randomBlob(t, 1<<20)
	digest := digestOf(data)

	written, err := store.Put(bytes.NewReader(data), digest)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if written != int64(len(data)) {
		t.Fatalf("expected %d bytes written, got %d", len(data), written)
	}

	ok, err := store.Has(digest)
	if err != nil {
		t.Fatalf("has: %v", err)
	}
	if !ok {
		t.Fatalf("expected blob to exist after put")
	}

	size, err := store.Size(digest)
	if err != nil {
		t.Fatalf("size: %v", err)
	}
	if size != int64(len(data)) {
		t.Fatalf("expected size %d, got %d", len(data), size)
	}

	rc, err := store.Get(digest)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("read back %d bytes, want %d identical bytes", len(got), len(data))
	}

	wantPath := filepath.Join(store.Root, "blobs", "sha256", strings.TrimPrefix(digest, "sha256:"))
	path, err := store.Path(digest)
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if path != wantPath {
		t.Fatalf("expected path %q, got %q", wantPath, path)
	}
}

func TestPutIsIdempotent(t *testing.T) {
	store := New(t.TempDir())
	data := randomBlob(t, 100)
	digest := digestOf(data)

	if _, err := store.Put(bytes.NewReader(data), digest); err != nil {
		t.Fatalf("first put: %v", err)
	}

	second := randomBlob(t, 100) // different bytes, same promised digest
	written, err := store.Put(bytes.NewReader(second), digest)
	if err != nil {
		t.Fatalf("second put: %v", err)
	}
	if written != 100 {
		t.Fatalf("expected existing size 100, got %d", written)
	}

	rc, err := store.Get(digest)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, data) {
		t.Fatalf("idempotent put overwrote the original content")
	}
}

func TestPutDigestMismatchLeavesNothing(t *testing.T) {
	store := New(t.TempDir())
	data := randomBlob(t, 500)
	wrong := digestOf(randomBlob(t, 500))

	written, err := store.Put(bytes.NewReader(data), wrong)
	if err == nil {
		t.Fatalf("expected digest mismatch error")
	}
	if !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
	if written != 500 {
		t.Fatalf("expected 500 streamed bytes, got %d", written)
	}

	ok, err := store.Has(wrong)
	if err != nil {
		t.Fatalf("has: %v", err)
	}
	if ok {
		t.Fatalf("mismatched blob must not be stored")
	}

	entries, err := os.ReadDir(filepath.Join(store.Root, "blobs", "sha256"))
	if err != nil {
		t.Fatalf("read blob dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no leftover files, found %d", len(entries))
	}
}

func TestGetMissingBlob(t *testing.T) {
	store := New(t.TempDir())
	digest := digestOf([]byte("ghost"))

	_, err := store.Get(digest)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected fs.ErrNotExist, got %v", err)
	}

	ok, err := store.Has(digest)
	if err != nil {
		t.Fatalf("has: %v", err)
	}
	if ok {
		t.Fatalf("ghost blob must not exist")
	}

	if _, err := store.Size(digest); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected fs.ErrNotExist from size, got %v", err)
	}
}

func TestDelete(t *testing.T) {
	store := New(t.TempDir())
	data := randomBlob(t, 64)
	digest := digestOf(data)

	if _, err := store.Put(bytes.NewReader(data), digest); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := store.Delete(digest); err != nil {
		t.Fatalf("delete: %v", err)
	}

	ok, err := store.Has(digest)
	if err != nil {
		t.Fatalf("has: %v", err)
	}
	if ok {
		t.Fatalf("blob must be gone after delete")
	}

	// Deleting again is a no-op, so GC can call it blindly.
	if err := store.Delete(digest); err != nil {
		t.Fatalf("re-delete: %v", err)
	}
}

func TestInvalidDigestsRejected(t *testing.T) {
	store := New(t.TempDir())
	good := digestOf([]byte("x"))
	hexPart := strings.TrimPrefix(good, "sha256:")

	cases := []string{
		"",
		"no-colon",
		":deadbeef",
		"sha256:",
		"sha256:zzzz",                        // non-hex
		"sha256:" + strings.ToUpper(hexPart), // OCI digests are lowercase
		"sha256:" + hexPart[:8],              // too short
		"sha256:" + hexPart[:20] + "/../" + hexPart[:20], // path traversal
	}

	for _, digest := range cases {
		if _, err := store.Put(bytes.NewReader([]byte("x")), digest); err == nil {
			t.Fatalf("expected put error for digest %q", digest)
		}
		if _, err := store.Path(digest); err == nil {
			t.Fatalf("expected path error for digest %q", digest)
		}
	}
}

func TestPutUnsupportedAlgorithm(t *testing.T) {
	store := New(t.TempDir())
	data := []byte("whatever")

	if _, err := store.Put(bytes.NewReader(data), "md5:"+hex.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))); err == nil {
		t.Fatalf("expected error for non-sha256 algorithm")
	}
}
