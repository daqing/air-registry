// Package blobs keeps the blobs and repo_blobs tables in sync with the
// content that finished uploading to disk.
package blobs

import (
	"errors"
	"time"

	"github.com/daqing/airway/lib/repo"
	airwaysql "github.com/daqing/airway/lib/sql"

	"github.com/daqing/air-registry/app/models"
	"github.com/daqing/air-registry/app/services/blobstore"
)

// ErrBlobUnknown marks a blob that does not exist or is not linked to the
// repository in question.
var ErrBlobUnknown = errors.New("blob unknown")

// EnsureRepository finds or creates the repository row for name. Two
// uploads starting concurrently on a fresh repository race to create the
// row; the loser accepts the winner's row instead of failing.
func EnsureRepository(name string) (*models.Repository, error) {
	r, err := repo.FindOneBy[models.Repository](airwaysql.H{"name": name})
	if err != nil || r != nil {
		return r, err
	}

	now := time.Now()
	r, err = repo.CreateFrom[models.Repository](airwaysql.H{
		"name":       name,
		"created_at": now,
		"updated_at": now,
	})
	if err != nil {
		if existing, findErr := repo.FindOneBy[models.Repository](airwaysql.H{"name": name}); findErr == nil && existing != nil {
			return existing, nil
		}
		return nil, err
	}
	return r, nil
}

// RegisterUpload records a fully uploaded blob (digest already verified
// against on-disk content by the caller) and links it to the repository.
func RegisterUpload(repoName, digest string, size int64, path string) error {
	r, err := EnsureRepository(repoName)
	if err != nil {
		return err
	}

	blob, err := ensureBlob(digest, size, path)
	if err != nil {
		return err
	}

	return linkBlobToRepo(r, blob)
}

// Mount links digest into repoName without moving any bytes, provided the
// from repository already holds that blob. It reports false when the source
// cannot satisfy the mount (unknown repo or digest not linked there), in
// which case the caller falls back to a regular upload.
func Mount(repoName, from, digest string) (bool, error) {
	src, err := repo.FindOneBy[models.Repository](airwaysql.H{"name": from})
	if err != nil || src == nil {
		return false, err
	}

	blob, err := repo.FindOneBy[models.Blob](airwaysql.H{"digest": digest})
	if err != nil || blob == nil {
		return false, err
	}

	linked, err := repo.ExistsWhere[models.RepoBlob](airwaysql.H{"repo_id": src.ID, "blob_id": blob.ID})
	if err != nil || !linked {
		return false, err
	}

	r, err := EnsureRepository(repoName)
	if err != nil {
		return false, err
	}

	if err := linkBlobToRepo(r, blob); err != nil {
		return false, err
	}
	return true, nil
}

// Linked reports whether digest is associated with repoName in repo_blobs.
func Linked(repoName, digest string) (bool, error) {
	r, err := repo.FindOneBy[models.Repository](airwaysql.H{"name": repoName})
	if err != nil || r == nil {
		return false, err
	}

	blob, err := repo.FindOneBy[models.Blob](airwaysql.H{"digest": digest})
	if err != nil || blob == nil {
		return false, err
	}

	return repo.ExistsWhere[models.RepoBlob](airwaysql.H{"repo_id": r.ID, "blob_id": blob.ID})
}

// Delete unlinks digest from repoName and, once no other repository links
// it, removes the blob file and its blobs table row. Manifests that still
// reference the digest are left dangling; garbage collection (T13)
// reconciles them. Deleting a blob that is not linked to repoName yields
// ErrBlobUnknown.
func Delete(store *blobstore.Store, repoName, digest string) error {
	r, err := repo.FindOneBy[models.Repository](airwaysql.H{"name": repoName})
	if err != nil {
		return err
	}
	if r == nil {
		return ErrBlobUnknown
	}

	blob, err := repo.FindOneBy[models.Blob](airwaysql.H{"digest": digest})
	if err != nil {
		return err
	}
	if blob == nil {
		return ErrBlobUnknown
	}

	linked, err := repo.ExistsWhere[models.RepoBlob](airwaysql.H{"repo_id": r.ID, "blob_id": blob.ID})
	if err != nil {
		return err
	}
	if !linked {
		return ErrBlobUnknown
	}

	if err := repo.DeleteWhere[models.RepoBlob](airwaysql.H{"repo_id": r.ID, "blob_id": blob.ID}); err != nil {
		return err
	}

	stillLinked, err := repo.ExistsWhere[models.RepoBlob](airwaysql.H{"blob_id": blob.ID})
	if err != nil {
		return err
	}
	if stillLinked {
		return nil // another repository shares the file
	}

	if err := store.Delete(digest); err != nil {
		return err
	}
	return repo.DeleteByID[models.Blob](blob.ID)
}

// ensureBlob finds or creates the blobs row for digest, tolerating a create
// race with a concurrent upload of the same content.
func ensureBlob(digest string, size int64, path string) (*models.Blob, error) {
	blob, err := repo.FindOneBy[models.Blob](airwaysql.H{"digest": digest})
	if err != nil || blob != nil {
		return blob, err
	}

	now := time.Now()
	blob, err = repo.CreateFrom[models.Blob](airwaysql.H{
		"digest":     digest,
		"size":       size,
		"path":       path,
		"created_at": now,
		"updated_at": now,
	})
	if err != nil {
		if existing, findErr := repo.FindOneBy[models.Blob](airwaysql.H{"digest": digest}); findErr == nil && existing != nil {
			return existing, nil
		}
		return nil, err
	}
	return blob, nil
}

// linkBlobToRepo idempotently associates blob with r, tolerating a create
// race with a concurrent upload or mount.
func linkBlobToRepo(r *models.Repository, blob *models.Blob) error {
	linked, err := repo.ExistsWhere[models.RepoBlob](airwaysql.H{"repo_id": r.ID, "blob_id": blob.ID})
	if err != nil || linked {
		return err
	}

	now := time.Now()
	_, err = repo.CreateFrom[models.RepoBlob](airwaysql.H{
		"repo_id":    r.ID,
		"blob_id":    blob.ID,
		"created_at": now,
		"updated_at": now,
	})
	if err != nil {
		if linked, findErr := repo.ExistsWhere[models.RepoBlob](airwaysql.H{"repo_id": r.ID, "blob_id": blob.ID}); findErr == nil && linked {
			return nil
		}
		return err
	}
	return nil
}
