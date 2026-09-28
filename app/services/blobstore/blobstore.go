// Package blobstore keeps blobs as content-addressed files on the local
// filesystem under <root>/blobs/<algo>/<hex>. Writes stream to a temp file
// in the same directory and are published with an atomic rename only after
// the streamed SHA-256 matches the digest the caller promised.
package blobstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const defaultRoot = "./data/storage"

var (
	algoPattern = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*$`)
	hexPattern  = regexp.MustCompile(`^[a-f0-9]{32,}$`)

	// ErrInvalidDigest marks malformed digests; distinguishable from
	// fs.ErrNotExist so callers can answer 400 instead of 404.
	ErrInvalidDigest = errors.New("invalid digest")
)

// Store keeps blobs under Root (blobs land in <Root>/blobs).
type Store struct {
	Root string
}

// New returns a Store rooted at root.
func New(root string) *Store {
	return &Store{Root: root}
}

// DefaultRoot resolves the storage root from STORAGE_ROOT, falling back to
// ./data/storage.
func DefaultRoot() string {
	if root := strings.TrimSpace(os.Getenv("STORAGE_ROOT")); root != "" {
		return root
	}
	return defaultRoot
}

func parseDigest(digest string) (algo, encoded string, err error) {
	algo, encoded, ok := strings.Cut(digest, ":")
	if !ok || !algoPattern.MatchString(algo) || !hexPattern.MatchString(encoded) {
		return "", "", fmt.Errorf("%w %q", ErrInvalidDigest, digest)
	}
	return algo, encoded, nil
}

// ValidateDigest reports whether digest is well-formed, without touching
// the filesystem.
func ValidateDigest(digest string) error {
	_, _, err := parseDigest(digest)
	return err
}

// Path returns the on-disk location of digest.
func (s *Store) Path(digest string) (string, error) {
	algo, encoded, err := parseDigest(digest)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.Root, "blobs", algo, encoded), nil
}

// Has reports whether digest is stored.
func (s *Store) Has(digest string) (bool, error) {
	path, err := s.Path(digest)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(path); err == nil {
		return true, nil
	} else if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else {
		return false, err
	}
}

// Size returns the stored byte size of digest.
func (s *Store) Size(digest string) (int64, error) {
	path, err := s.Path(digest)
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// Get opens digest for reading; the caller must close it.
func (s *Store) Get(digest string) (io.ReadCloser, error) {
	path, err := s.Path(digest)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

// Put streams r to disk, verifies its SHA-256 against the promised digest,
// and only then publishes the file. On mismatch the temp file is removed
// and nothing is left behind. Putting an already-stored digest succeeds
// without rewriting the file.
func (s *Store) Put(r io.Reader, digest string) (int64, error) {
	algo, _, err := parseDigest(digest)
	if err != nil {
		return 0, err
	}
	if algo != "sha256" {
		return 0, fmt.Errorf("unsupported digest algorithm %q", algo)
	}

	path, err := s.Path(digest)
	if err != nil {
		return 0, err
	}

	if ok, err := s.Has(digest); err != nil {
		return 0, err
	} else if ok {
		return s.Size(digest)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return 0, err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, hash), r)
	if err != nil {
		return written, fmt.Errorf("write blob: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return written, fmt.Errorf("close blob temp file: %w", err)
	}

	got := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	if got != digest {
		return written, fmt.Errorf("digest mismatch: got %s, want %s", got, digest)
	}

	if err := os.Rename(tmpName, path); err != nil {
		// A concurrent Put may have published first; the existing file is
		// content-addressed to the same digest, so accept it.
		if _, statErr := os.Stat(path); statErr == nil {
			_ = os.Remove(tmpName)
			committed = true
			return written, nil
		}
		return written, fmt.Errorf("publish blob: %w", err)
	}

	committed = true
	return written, nil
}

// Delete removes digest. Deleting a blob that is not stored is a no-op so
// garbage collection can run blindly.
func (s *Store) Delete(digest string) error {
	path, err := s.Path(digest)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
