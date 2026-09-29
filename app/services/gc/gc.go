// Package gc reclaims blobs that no manifest references anymore: blob
// files, blobs rows and dangling repo_blobs links go away together.
package gc

import (
	"github.com/daqing/airway/lib/repo"
	airwaysql "github.com/daqing/airway/lib/sql"

	"github.com/daqing/air-registry/app/models"
	"github.com/daqing/air-registry/app/services/blobstore"
	"github.com/daqing/air-registry/app/services/manifests"
)

type Stats struct {
	KeptBlobs    int
	DeletedBlobs int
}

// Run scans every manifest, collects the digests content still needs
// (each manifest's own content digest, its config/layers, and the child
// manifests of indexes) and deletes every other blob. Blobs linked to a
// repository but referenced by no manifest are garbage too. Re-running on
// an already clean store deletes nothing.
func Run(store *blobstore.Store) (*Stats, error) {
	keep := map[string]bool{}

	rows, err := repo.FindAll[models.Manifest]()
	if err != nil {
		return nil, err
	}
	for _, m := range rows {
		keep[m.Digest] = true
		// Stored docs may omit the mediaType field (the row carries the
		// type negotiated at PUT time), so pass it as the fallback.
		meta, err := manifests.Parse([]byte(m.Content), m.MediaType)
		if err != nil {
			continue // unparseable: keep the manifest row itself
		}
		for _, d := range meta.BlobDigests {
			keep[d] = true
		}
		for _, d := range meta.ChildDigests {
			keep[d] = true
		}
	}

	blobs, err := repo.FindAll[models.Blob]()
	if err != nil {
		return nil, err
	}

	stats := &Stats{}
	for _, b := range blobs {
		if keep[b.Digest] {
			stats.KeptBlobs++
			continue
		}
		if err := store.Delete(b.Digest); err != nil {
			return stats, err
		}
		if err := repo.DeleteWhere[models.RepoBlob](airwaysql.H{"blob_id": b.ID}); err != nil {
			return stats, err
		}
		if err := repo.DeleteByID[models.Blob](b.ID); err != nil {
			return stats, err
		}
		stats.DeletedBlobs++
	}
	return stats, nil
}
