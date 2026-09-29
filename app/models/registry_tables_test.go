package models

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/daqing/airway/lib/migrate"
	"github.com/daqing/airway/lib/repo"
	airwaysql "github.com/daqing/airway/lib/sql"
)

func setupRegistryTestDB(t *testing.T) {
	t.Helper()

	dsn := "sqlite://" + filepath.ToSlash(filepath.Join(t.TempDir(), "registry-test.db"))

	err := migrate.Run(migrate.Options{
		DSN:          dsn,
		Migrations:   os.DirFS(filepath.Join("..", "..", "db", "migrate")),
		SnapshotPath: "",
		Out:          io.Discard,
	})
	if err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	if _, err := repo.SetupDB(dsn); err != nil {
		t.Fatalf("setup db: %v", err)
	}
}

func nowStamp() time.Time {
	return time.Now()
}

func TestRepositoryCRUD(t *testing.T) {
	setupRegistryTestDB(t)

	repo_, err := repo.CreateFrom[Repository](airwaysql.H{
		"name":       "library/demo",
		"created_at": nowStamp(),
		"updated_at": nowStamp(),
	})
	if err != nil {
		t.Fatalf("create repository: %v", err)
	}
	if repo_.ID <= 0 {
		t.Fatalf("expected generated id, got %d", repo_.ID)
	}
	if repo_.Name != "library/demo" {
		t.Fatalf("unexpected name %q", repo_.Name)
	}
	if repo_.CreatedAt.IsZero() || repo_.UpdatedAt.IsZero() {
		t.Fatalf("expected timestamps to round-trip, got %+v", repo_)
	}

	byName, err := repo.FindOneBy[Repository](airwaysql.H{"name": "library/demo"})
	if err != nil {
		t.Fatalf("find by name: %v", err)
	}
	if byName == nil || byName.ID != repo_.ID {
		t.Fatalf("expected to find repository %d, got %+v", repo_.ID, byName)
	}

	_, err = repo.CreateFrom[Repository](airwaysql.H{
		"name":       "library/demo",
		"created_at": nowStamp(),
		"updated_at": nowStamp(),
	})
	if err == nil {
		t.Fatalf("expected unique violation on duplicate repository name")
	}

	if err := repo.DeleteByID[Repository](repo_.ID); err != nil {
		t.Fatalf("delete repository: %v", err)
	}
	gone, err := repo.FindByID[Repository](repo_.ID)
	if err != nil {
		t.Fatalf("find deleted repository: %v", err)
	}
	if gone != nil {
		t.Fatalf("expected repository to be deleted, got %+v", gone)
	}
}

func TestBlobCRUD(t *testing.T) {
	setupRegistryTestDB(t)

	blob, err := repo.CreateFrom[Blob](airwaysql.H{
		"digest":     "sha256:aaaabbbbccccddddeeeeffff0000111122223333444455556666777788889999",
		"size":       42,
		"path":       "blobs/sha256/aa/aaaabbbb",
		"created_at": nowStamp(),
		"updated_at": nowStamp(),
	})
	if err != nil {
		t.Fatalf("create blob: %v", err)
	}
	if blob.ID <= 0 {
		t.Fatalf("expected generated id, got %d", blob.ID)
	}

	found, err := repo.FindOneBy[Blob](airwaysql.H{"digest": blob.Digest})
	if err != nil {
		t.Fatalf("find blob by digest: %v", err)
	}
	if found == nil || found.ID != blob.ID || found.Size != 42 || found.Path != blob.Path {
		t.Fatalf("unexpected blob %+v", found)
	}

	_, err = repo.CreateFrom[Blob](airwaysql.H{
		"digest":     blob.Digest,
		"size":       1,
		"path":       "elsewhere",
		"created_at": nowStamp(),
		"updated_at": nowStamp(),
	})
	if err == nil {
		t.Fatalf("expected unique violation on duplicate blob digest")
	}

	if err := repo.DeleteByID[Blob](blob.ID); err != nil {
		t.Fatalf("delete blob: %v", err)
	}
}

func TestRepoBlobCRUD(t *testing.T) {
	setupRegistryTestDB(t)

	r, err := repo.CreateFrom[Repository](airwaysql.H{
		"name":       "library/demo",
		"created_at": nowStamp(),
		"updated_at": nowStamp(),
	})
	if err != nil {
		t.Fatalf("create repository: %v", err)
	}
	b, err := repo.CreateFrom[Blob](airwaysql.H{
		"digest":     "sha256:aaaabbbbccccddddeeeeffff0000111122223333444455556666777788889999",
		"size":       10,
		"path":       "blobs/sha256/aa/aaaabbbb",
		"created_at": nowStamp(),
		"updated_at": nowStamp(),
	})
	if err != nil {
		t.Fatalf("create blob: %v", err)
	}

	link, err := repo.CreateFrom[RepoBlob](airwaysql.H{
		"repo_id":    r.ID,
		"blob_id":    b.ID,
		"created_at": nowStamp(),
		"updated_at": nowStamp(),
	})
	if err != nil {
		t.Fatalf("create repo_blob: %v", err)
	}

	found, err := repo.FindOneBy[RepoBlob](airwaysql.H{"repo_id": r.ID, "blob_id": b.ID})
	if err != nil {
		t.Fatalf("find repo_blob: %v", err)
	}
	if found == nil || found.ID != link.ID {
		t.Fatalf("expected repo_blob %d, got %+v", link.ID, found)
	}

	_, err = repo.CreateFrom[RepoBlob](airwaysql.H{
		"repo_id":    r.ID,
		"blob_id":    b.ID,
		"created_at": nowStamp(),
		"updated_at": nowStamp(),
	})
	if err == nil {
		t.Fatalf("expected unique violation on duplicate (repo_id, blob_id)")
	}

	if err := repo.DeleteByID[RepoBlob](link.ID); err != nil {
		t.Fatalf("delete repo_blob: %v", err)
	}
}

func TestManifestCRUD(t *testing.T) {
	setupRegistryTestDB(t)

	r, err := repo.CreateFrom[Repository](airwaysql.H{
		"name":       "library/demo",
		"created_at": nowStamp(),
		"updated_at": nowStamp(),
	})
	if err != nil {
		t.Fatalf("create repository: %v", err)
	}

	content := `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"," layers":[]}`
	manifest, err := repo.CreateFrom[Manifest](airwaysql.H{
		"repo_id":        r.ID,
		"digest":         "sha256:1111222233334444555566667777888899990000aaaabbbbccccddddeeeeffff",
		"media_type":     "application/vnd.oci.image.manifest.v1+json",
		"artifact_type":  nil,
		"subject_digest": nil,
		"size":           int64(len(content)),
		"content":        content,
		"created_at":     nowStamp(),
		"updated_at":     nowStamp(),
	})
	if err != nil {
		t.Fatalf("create manifest: %v", err)
	}
	if manifest.ArtifactType != nil || manifest.SubjectDigest != nil {
		t.Fatalf("expected nil optional fields, got %+v", manifest)
	}
	if manifest.Content != content {
		t.Fatalf("content round-trip mismatch:\n got %q\nwant %q", manifest.Content, content)
	}

	found, err := repo.FindOneBy[Manifest](airwaysql.H{"repo_id": r.ID, "digest": manifest.Digest})
	if err != nil {
		t.Fatalf("find manifest: %v", err)
	}
	if found == nil || found.ID != manifest.ID {
		t.Fatalf("expected manifest %d, got %+v", manifest.ID, found)
	}

	_, err = repo.CreateFrom[Manifest](airwaysql.H{
		"repo_id":    r.ID,
		"digest":     manifest.Digest,
		"media_type": manifest.MediaType,
		"size":       1,
		"content":    "{}",
		"created_at": nowStamp(),
		"updated_at": nowStamp(),
	})
	if err == nil {
		t.Fatalf("expected unique violation on duplicate (repo_id, digest)")
	}

	artifactType := "application/vnd.example.sbom"
	subject := manifest.Digest
	referrer, err := repo.CreateFrom[Manifest](airwaysql.H{
		"repo_id":        r.ID,
		"digest":         "sha256:ffffeeeeddddccccbbbbaaaa9999888877776666555544443333222211110000",
		"media_type":     "application/vnd.oci.image.manifest.v1+json",
		"artifact_type":  &artifactType,
		"subject_digest": &subject,
		"size":           2,
		"content":        "{}",
		"created_at":     nowStamp(),
		"updated_at":     nowStamp(),
	})
	if err != nil {
		t.Fatalf("create referrer manifest: %v", err)
	}
	if referrer.ArtifactType == nil || *referrer.ArtifactType != artifactType {
		t.Fatalf("artifact_type did not round-trip: %+v", referrer.ArtifactType)
	}
	if referrer.SubjectDigest == nil || *referrer.SubjectDigest != subject {
		t.Fatalf("subject_digest did not round-trip: %+v", referrer.SubjectDigest)
	}

	referrers, err := repo.FindBy[Manifest](airwaysql.H{"subject_digest": subject})
	if err != nil {
		t.Fatalf("find referrers: %v", err)
	}
	if len(referrers) != 1 || referrers[0].ID != referrer.ID {
		t.Fatalf("expected one referrer, got %+v", referrers)
	}

	if err := repo.DeleteByID[Manifest](manifest.ID); err != nil {
		t.Fatalf("delete manifest: %v", err)
	}
}

func TestTagCRUD(t *testing.T) {
	setupRegistryTestDB(t)

	r, err := repo.CreateFrom[Repository](airwaysql.H{
		"name":       "library/demo",
		"created_at": nowStamp(),
		"updated_at": nowStamp(),
	})
	if err != nil {
		t.Fatalf("create repository: %v", err)
	}

	digestV1 := "sha256:1111222233334444555566667777888899990000aaaabbbbccccddddeeeeffff"
	tag, err := repo.CreateFrom[Tag](airwaysql.H{
		"repo_id":         r.ID,
		"name":            "v1",
		"manifest_digest": digestV1,
		"created_at":      nowStamp(),
		"updated_at":      nowStamp(),
	})
	if err != nil {
		t.Fatalf("create tag: %v", err)
	}

	found, err := repo.FindOneBy[Tag](airwaysql.H{"repo_id": r.ID, "name": "v1"})
	if err != nil {
		t.Fatalf("find tag: %v", err)
	}
	if found == nil || found.ID != tag.ID || found.ManifestDigest != digestV1 {
		t.Fatalf("unexpected tag %+v", found)
	}

	_, err = repo.CreateFrom[Tag](airwaysql.H{
		"repo_id":         r.ID,
		"name":            "v1",
		"manifest_digest": digestV1,
		"created_at":      nowStamp(),
		"updated_at":      nowStamp(),
	})
	if err == nil {
		t.Fatalf("expected unique violation on duplicate (repo_id, name)")
	}

	digestV2 := "sha256:0000111122223333444455556666777788889999aaaabbbbccccddddeeeeffff"
	if err := repo.UpdateByID[Tag](tag.ID, airwaysql.H{
		"manifest_digest": digestV2,
		"updated_at":      nowStamp(),
	}); err != nil {
		t.Fatalf("retag: %v", err)
	}
	retagged, err := repo.FindByID[Tag](tag.ID)
	if err != nil {
		t.Fatalf("find retagged: %v", err)
	}
	if retagged.ManifestDigest != digestV2 {
		t.Fatalf("expected retag to %q, got %q", digestV2, retagged.ManifestDigest)
	}

	if err := repo.DeleteByID[Tag](tag.ID); err != nil {
		t.Fatalf("delete tag: %v", err)
	}
}
