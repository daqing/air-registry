// Package manifests parses OCI image manifests / indexes and stores them
// with their metadata: exact bytes go into the manifests table while
// repository, tag and blob associations are maintained alongside.
package manifests

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"regexp"
	"strings"
	"time"

	"github.com/daqing/airway/lib/repo"
	airwaysql "github.com/daqing/airway/lib/sql"

	"github.com/daqing/air-registry/app/models"
	"github.com/daqing/air-registry/app/services/blobstore"
)

var (
	ErrInvalidManifest = errors.New("invalid manifest")
	ErrMissingBlob     = errors.New("manifest references unknown blob")
	ErrInvalidTag      = errors.New("invalid tag form")
)

const (
	MediaTypeOCIManifest        = "application/vnd.oci.image.manifest.v1+json"
	MediaTypeOCIIndex           = "application/vnd.oci.image.index.v1+json"
	MediaTypeDockerManifest     = "application/vnd.docker.distribution.manifest.v2+json"
	MediaTypeDockerManifestList = "application/vnd.docker.distribution.manifest.list.v2+json"
)

type descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

type manifestDoc struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	ArtifactType  string            `json:"artifactType"`
	Config        *descriptor       `json:"config"`
	Layers        []descriptor      `json:"layers"`
	Blobs         []descriptor      `json:"blobs"`
	Manifests     []descriptor      `json:"manifests"`
	Subject       *descriptor       `json:"subject"`
	Annotations   map[string]string `json:"annotations"`
}

// Meta is what Store persists besides the raw bytes.
type Meta struct {
	MediaType string

	// ArtifactType and SubjectDigest are set only when the manifest
	// carries them (OCI 1.1); SubjectDigest feeds the Referrers API.
	ArtifactType  *string
	SubjectDigest *string

	IsIndex bool

	// BlobDigests lists content blobs the manifest references: config +
	// layers for image manifests, blobs for artifact manifests. Empty for
	// indexes.
	BlobDigests []string

	// ChildDigests lists the manifest digests an index references.
	ChildDigests []string
}

// Parse validates content and extracts its metadata. contentType is the
// PUT request's Content-Type header; when empty the mediaType field inside
// the document is used instead.
func Parse(content []byte, contentType string) (*Meta, error) {
	var doc manifestDoc
	if err := json.Unmarshal(content, &doc); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}
	if doc.SchemaVersion != 2 {
		return nil, fmt.Errorf("%w: unsupported schemaVersion %d", ErrInvalidManifest, doc.SchemaVersion)
	}

	mediaType := doc.MediaType
	if contentType != "" {
		mediaType = contentType
	}
	if mediaType == "" {
		return nil, fmt.Errorf("%w: missing media type", ErrInvalidManifest)
	}
	if mt, _, err := mime.ParseMediaType(mediaType); err == nil {
		mediaType = mt
	}

	meta := &Meta{MediaType: mediaType}
	if doc.ArtifactType != "" {
		v := doc.ArtifactType
		meta.ArtifactType = &v
	}
	if doc.Subject != nil && doc.Subject.Digest != "" {
		if err := blobstore.ValidateDigest(doc.Subject.Digest); err != nil {
			return nil, fmt.Errorf("%w: bad subject digest: %v", ErrInvalidManifest, err)
		}
		v := doc.Subject.Digest
		meta.SubjectDigest = &v
	}

	appendBlob := func(d descriptor) error {
		if err := blobstore.ValidateDigest(d.Digest); err != nil {
			return fmt.Errorf("%w: bad blob digest %q: %v", ErrInvalidManifest, d.Digest, err)
		}
		meta.BlobDigests = append(meta.BlobDigests, d.Digest)
		return nil
	}

	switch mediaType {
	case MediaTypeOCIIndex, MediaTypeDockerManifestList:
		meta.IsIndex = true
		for _, d := range doc.Manifests {
			if err := blobstore.ValidateDigest(d.Digest); err != nil {
				return nil, fmt.Errorf("%w: bad child digest %q: %v", ErrInvalidManifest, d.Digest, err)
			}
			meta.ChildDigests = append(meta.ChildDigests, d.Digest)
		}
	default:
		if doc.Config != nil {
			if err := appendBlob(*doc.Config); err != nil {
				return nil, err
			}
		}
		for _, d := range doc.Layers {
			if err := appendBlob(d); err != nil {
				return nil, err
			}
		}
		for _, d := range doc.Blobs {
			if err := appendBlob(d); err != nil {
				return nil, err
			}
		}
	}

	return meta, nil
}

// Digest returns the canonical content digest of raw manifest bytes.
func Digest(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

var tagPattern = regexp.MustCompile(`^[\w][\w.-]{0,127}$`)

// Store persists content and its metadata, upserts the repository, links
// the referenced blobs to it, and points the tag at the manifest digest
// when reference is a tag. A digest reference stores the manifest without
// creating a tag. Returns the manifest digest.
func Store(repoName, reference string, content []byte, contentType string) (string, error) {
	meta, err := Parse(content, contentType)
	if err != nil {
		return "", err
	}
	digest := Digest(content)

	isDigestRef := strings.Contains(reference, ":")
	if isDigestRef {
		if err := blobstore.ValidateDigest(reference); err != nil {
			return "", fmt.Errorf("%w: bad digest reference: %v", ErrInvalidManifest, err)
		}
		if reference != digest {
			return "", fmt.Errorf("%w: digest reference does not match content", ErrInvalidManifest)
		}
	} else if !tagPattern.MatchString(reference) {
		return "", fmt.Errorf("%w %q", ErrInvalidTag, reference)
	}

	// Referenced content must already be uploaded, like docker
	// distribution enforces on manifest push.
	if meta.IsIndex {
		for _, child := range meta.ChildDigests {
			found, err := repo.ExistsWhere[models.Manifest](airwaysql.H{"digest": child})
			if err != nil {
				return "", err
			}
			if !found {
				return "", fmt.Errorf("%w: %s", ErrMissingBlob, child)
			}
		}
	} else {
		for _, d := range meta.BlobDigests {
			found, err := repo.ExistsWhere[models.Blob](airwaysql.H{"digest": d})
			if err != nil {
				return "", err
			}
			if !found {
				return "", fmt.Errorf("%w: %s", ErrMissingBlob, d)
			}
		}
	}

	now := time.Now()

	r, err := repo.FindOneBy[models.Repository](airwaysql.H{"name": repoName})
	if err != nil {
		return "", err
	}
	if r == nil {
		r, err = repo.CreateFrom[models.Repository](airwaysql.H{
			"name":       repoName,
			"created_at": now,
			"updated_at": now,
		})
		if err != nil {
			return "", err
		}
	}

	m, err := repo.FindOneBy[models.Manifest](airwaysql.H{"repo_id": r.ID, "digest": digest})
	if err != nil {
		return "", err
	}
	if m == nil {
		_, err = repo.CreateFrom[models.Manifest](airwaysql.H{
			"repo_id":        r.ID,
			"digest":         digest,
			"media_type":     meta.MediaType,
			"artifact_type":  meta.ArtifactType,
			"subject_digest": meta.SubjectDigest,
			"size":           int64(len(content)),
			"content":        string(content),
			"created_at":     now,
			"updated_at":     now,
		})
		if err != nil {
			return "", err
		}
	}

	for _, d := range meta.BlobDigests {
		blob, err := repo.FindOneBy[models.Blob](airwaysql.H{"digest": d})
		if err != nil {
			return "", err
		}
		if blob == nil {
			continue // raced with a delete; GC will reconcile
		}
		linked, err := repo.ExistsWhere[models.RepoBlob](airwaysql.H{"repo_id": r.ID, "blob_id": blob.ID})
		if err != nil {
			return "", err
		}
		if linked {
			continue
		}
		if _, err := repo.CreateFrom[models.RepoBlob](airwaysql.H{
			"repo_id":    r.ID,
			"blob_id":    blob.ID,
			"created_at": now,
			"updated_at": now,
		}); err != nil {
			return "", err
		}
	}

	if isDigestRef {
		return digest, nil
	}

	tag, err := repo.FindOneBy[models.Tag](airwaysql.H{"repo_id": r.ID, "name": reference})
	if err != nil {
		return "", err
	}
	if tag == nil {
		_, err = repo.CreateFrom[models.Tag](airwaysql.H{
			"repo_id":         r.ID,
			"name":            reference,
			"manifest_digest": digest,
			"created_at":      now,
			"updated_at":      now,
		})
		return digest, err
	}
	if tag.ManifestDigest != digest {
		return digest, repo.UpdateByID[models.Tag](tag.ID, airwaysql.H{
			"manifest_digest": digest,
			"updated_at":      now,
		})
	}
	return digest, nil
}
