// Package blobs keeps the blobs and repo_blobs tables in sync with the
// content that finished uploading to disk.
package blobs

import (
	"time"

	"github.com/daqing/airway/lib/repo"
	airwaysql "github.com/daqing/airway/lib/sql"

	"github.com/daqing/air-registry/app/models"
)

// EnsureRepository finds or creates the repository row for name.
func EnsureRepository(name string) (*models.Repository, error) {
	r, err := repo.FindOneBy[models.Repository](airwaysql.H{"name": name})
	if err != nil || r != nil {
		return r, err
	}

	now := time.Now()
	return repo.CreateFrom[models.Repository](airwaysql.H{
		"name":       name,
		"created_at": now,
		"updated_at": now,
	})
}

// RegisterUpload records a fully uploaded blob (digest already verified
// against on-disk content by the caller) and links it to the repository.
func RegisterUpload(repoName, digest string, size int64, path string) error {
	r, err := EnsureRepository(repoName)
	if err != nil {
		return err
	}

	now := time.Now()

	blob, err := repo.FindOneBy[models.Blob](airwaysql.H{"digest": digest})
	if err != nil {
		return err
	}
	if blob == nil {
		blob, err = repo.CreateFrom[models.Blob](airwaysql.H{
			"digest":     digest,
			"size":       size,
			"path":       path,
			"created_at": now,
			"updated_at": now,
		})
		if err != nil {
			return err
		}
	}

	linked, err := repo.ExistsWhere[models.RepoBlob](airwaysql.H{"repo_id": r.ID, "blob_id": blob.ID})
	if err != nil {
		return err
	}
	if linked {
		return nil
	}

	_, err = repo.CreateFrom[models.RepoBlob](airwaysql.H{
		"repo_id":    r.ID,
		"blob_id":    blob.ID,
		"created_at": now,
		"updated_at": now,
	})
	return err
}
