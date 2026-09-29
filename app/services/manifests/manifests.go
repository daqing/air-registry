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
	"sort"
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
	ErrManifestUnknown = errors.New("manifest unknown")
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
	Platform  *struct {
		Architecture string `json:"architecture"`
		OS           string `json:"os"`
		Variant      string `json:"variant"`
	} `json:"platform"`
}

func (d descriptor) platformLabel() string {
	if d.Platform == nil {
		return ""
	}
	label := d.Platform.OS + "/" + d.Platform.Architecture
	if d.Platform.Variant != "" {
		label += "/" + d.Platform.Variant
	}
	return label
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

	// Annotations feeds the Referrers API descriptors.
	Annotations map[string]string

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

	meta := &Meta{MediaType: mediaType, Annotations: doc.Annotations}
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

// Find resolves a manifest by tag or digest reference within a repository.
// Returns (nil, nil) when nothing matches.
func Find(repoName, reference string) (*models.Manifest, error) {
	r, err := repo.FindOneBy[models.Repository](airwaysql.H{"name": repoName})
	if err != nil || r == nil {
		return nil, err
	}

	digest := reference
	if !strings.Contains(reference, ":") {
		tag, err := repo.FindOneBy[models.Tag](airwaysql.H{"repo_id": r.ID, "name": reference})
		if err != nil {
			return nil, err
		}
		if tag == nil {
			return nil, nil
		}
		digest = tag.ManifestDigest
	}

	return repo.FindOneBy[models.Manifest](airwaysql.H{"repo_id": r.ID, "digest": digest})
}

// ListTags returns the sorted tag names of a repository, or (nil, nil) when
// the repository does not exist.
func ListTags(repoName string) ([]string, error) {
	r, err := repo.FindOneBy[models.Repository](airwaysql.H{"name": repoName})
	if err != nil || r == nil {
		return nil, err
	}

	tags, err := repo.FindBy[models.Tag](airwaysql.H{"repo_id": r.ID})
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(tags))
	for _, t := range tags {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	return names, nil
}

// Referrer is one descriptor of the referrers index response.
type Referrer struct {
	MediaType    string
	Digest       string
	Size         int64
	ArtifactType *string
	Annotations  map[string]string
}

// Referrers lists the manifests of repoName that declare digest as their
// subject, optionally filtered by artifact type. Unknown repositories and
// subjects yield an empty list (the endpoint answers an empty index, not
// 404).
func Referrers(repoName, digest, artifactType string) ([]Referrer, error) {
	r, err := repo.FindOneBy[models.Repository](airwaysql.H{"name": repoName})
	if err != nil || r == nil {
		return nil, err
	}

	rows, err := repo.FindBy[models.Manifest](airwaysql.H{"repo_id": r.ID, "subject_digest": digest})
	if err != nil {
		return nil, err
	}

	referrers := make([]Referrer, 0, len(rows))
	for _, m := range rows {
		var doc manifestDoc
		effectiveType := m.ArtifactType
		if effectiveType == nil && json.Unmarshal([]byte(m.Content), &doc) == nil &&
			doc.Config != nil && doc.Config.MediaType != "" {
			// OCI 1.1: a manifest without an artifactType field falls back
			// to its config descriptor's mediaType.
			v := doc.Config.MediaType
			effectiveType = &v
		}
		if artifactType != "" && (effectiveType == nil || *effectiveType != artifactType) {
			continue
		}
		ref := Referrer{
			MediaType:    m.MediaType,
			Digest:       m.Digest,
			Size:         m.Size,
			ArtifactType: effectiveType,
		}
		if meta, err := Parse([]byte(m.Content), m.MediaType); err == nil {
			ref.Annotations = meta.Annotations
		}
		referrers = append(referrers, ref)
	}
	return referrers, nil
}

// Entry is one row of an expanded manifest: the config, a layer/blob, or
// (for indexes) a child manifest.
type Entry struct {
	Digest    string
	MediaType string
	Size      int64
	// Platform is set for index children, e.g. "linux/amd64".
	Platform string
}

// Expanded is a manifest fully expanded for the web detail page.
type Expanded struct {
	Digest       string
	MediaType    string
	ArtifactType *string
	Annotations  map[string]string
	// Subject is set when this manifest is a referrer (OCI 1.1).
	Subject *string
	IsIndex bool

	// Config and Layers describe image/artifact manifests; Layers also
	// holds artifact blobs. Children lists the child manifests of an index.
	Config   *Entry
	Layers   []Entry
	Children []Entry

	// TotalSize is config+layers for image manifests, and — recursively —
	// child manifest sizes plus their content for indexes.
	TotalSize int64
}

// Expand parses m's content into display form. Index children are looked up
// under repoID to add their own content to TotalSize; children missing from
// the repository contribute only the size declared by the index descriptor.
func Expand(repoID airwaysql.IdType, m *models.Manifest) (*Expanded, error) {
	return expand(repoID, m, map[string]bool{})
}

func expand(repoID airwaysql.IdType, m *models.Manifest, seen map[string]bool) (*Expanded, error) {
	var doc manifestDoc
	if err := json.Unmarshal([]byte(m.Content), &doc); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidManifest, err)
	}

	x := &Expanded{
		Digest:       m.Digest,
		MediaType:    m.MediaType,
		ArtifactType: m.ArtifactType,
		Annotations:  doc.Annotations,
		IsIndex:      m.MediaType == MediaTypeOCIIndex || m.MediaType == MediaTypeDockerManifestList,
	}
	if doc.Subject != nil && doc.Subject.Digest != "" {
		d := doc.Subject.Digest
		x.Subject = &d
	}

	if x.IsIndex {
		for _, c := range doc.Manifests {
			x.Children = append(x.Children, Entry{
				Digest:    c.Digest,
				MediaType: c.MediaType,
				Size:      c.Size,
				Platform:  c.platformLabel(),
			})
			x.TotalSize += c.Size
			if seen[c.Digest] {
				continue
			}
			seen[c.Digest] = true
			child, err := repo.FindOneBy[models.Manifest](airwaysql.H{
				"repo_id": repoID,
				"digest":  c.Digest,
			})
			if err != nil {
				return nil, err
			}
			if child == nil {
				continue // deleted or never pushed; descriptor size already counted
			}
			expanded, err := expand(repoID, child, seen)
			if err != nil {
				return nil, err
			}
			x.TotalSize += expanded.TotalSize
		}
		return x, nil
	}

	if doc.Config != nil {
		x.Config = &Entry{
			Digest:    doc.Config.Digest,
			MediaType: doc.Config.MediaType,
			Size:      doc.Config.Size,
		}
		x.TotalSize += doc.Config.Size
	}
	for _, d := range append(doc.Layers, doc.Blobs...) {
		x.Layers = append(x.Layers, Entry{
			Digest:    d.Digest,
			MediaType: d.MediaType,
			Size:      d.Size,
		})
		x.TotalSize += d.Size
	}
	return x, nil
}

// Delete removes the manifest referenced by tag or digest, along with every
// tag pointing at it. Blobs referenced only by this manifest are unlinked
// from the repository; blobs still referenced by other manifests in the
// repository stay linked, and blob files / rows are left for GC (T13) to
// reclaim. Referrer manifests whose subject was deleted are kept as-is —
// deletion does not cascade.
func Delete(repoName, reference string) error {
	r, err := repo.FindOneBy[models.Repository](airwaysql.H{"name": repoName})
	if err != nil {
		return err
	}
	if r == nil {
		return ErrManifestUnknown
	}

	m, err := Find(repoName, reference)
	if err != nil {
		return err
	}
	if m == nil {
		return ErrManifestUnknown
	}

	meta, err := Parse([]byte(m.Content), m.MediaType)
	if err != nil {
		return err
	}

	// Digests still referenced by other manifests of this repository must
	// keep their repo_blobs link so those manifests stay pullable.
	others, err := repo.FindBy[models.Manifest](airwaysql.H{"repo_id": r.ID})
	if err != nil {
		return err
	}
	stillReferenced := map[string]bool{}
	for _, o := range others {
		if o.ID == m.ID {
			continue
		}
		om, err := Parse([]byte(o.Content), o.MediaType)
		if err != nil {
			continue // leave links; GC reconciles
		}
		for _, d := range om.BlobDigests {
			stillReferenced[d] = true
		}
	}

	for _, d := range meta.BlobDigests {
		if stillReferenced[d] {
			continue
		}
		blob, err := repo.FindOneBy[models.Blob](airwaysql.H{"digest": d})
		if err != nil {
			return err
		}
		if blob == nil {
			continue
		}
		if err := repo.DeleteWhere[models.RepoBlob](airwaysql.H{"repo_id": r.ID, "blob_id": blob.ID}); err != nil {
			return err
		}
	}

	if err := repo.DeleteWhere[models.Tag](airwaysql.H{"repo_id": r.ID, "manifest_digest": m.Digest}); err != nil {
		return err
	}
	return repo.DeleteByID[models.Manifest](m.ID)
}

// Store persists content and its metadata, upserts the repository, links
// the referenced blobs to it, and points the tag at the manifest digest
// when reference is a tag. A digest reference stores the manifest without
// creating a tag. Returns the manifest digest and its parsed metadata (the
// subject feeds the OCI-Subject response header).
func Store(repoName, reference string, content []byte, contentType string) (string, *Meta, error) {
	meta, err := Parse(content, contentType)
	if err != nil {
		return "", nil, err
	}
	digest := Digest(content)

	isDigestRef := strings.Contains(reference, ":")
	if isDigestRef {
		if err := blobstore.ValidateDigest(reference); err != nil {
			return "", nil, fmt.Errorf("%w: bad digest reference: %v", ErrInvalidManifest, err)
		}
		if reference != digest {
			return "", nil, fmt.Errorf("%w: digest reference does not match content", ErrInvalidManifest)
		}
	} else if !tagPattern.MatchString(reference) {
		return "", nil, fmt.Errorf("%w %q", ErrInvalidTag, reference)
	}

	// Referenced content must already be uploaded, like docker
	// distribution enforces on manifest push.
	if meta.IsIndex {
		for _, child := range meta.ChildDigests {
			found, err := repo.ExistsWhere[models.Manifest](airwaysql.H{"digest": child})
			if err != nil {
				return "", nil, err
			}
			if !found {
				return "", nil, fmt.Errorf("%w: %s", ErrMissingBlob, child)
			}
		}
	} else {
		for _, d := range meta.BlobDigests {
			found, err := repo.ExistsWhere[models.Blob](airwaysql.H{"digest": d})
			if err != nil {
				return "", nil, err
			}
			if !found {
				return "", nil, fmt.Errorf("%w: %s", ErrMissingBlob, d)
			}
		}
	}

	now := time.Now()

	r, err := repo.FindOneBy[models.Repository](airwaysql.H{"name": repoName})
	if err != nil {
		return "", nil, err
	}
	if r == nil {
		r, err = repo.CreateFrom[models.Repository](airwaysql.H{
			"name":       repoName,
			"created_at": now,
			"updated_at": now,
		})
		if err != nil {
			return "", nil, err
		}
	}

	m, err := repo.FindOneBy[models.Manifest](airwaysql.H{"repo_id": r.ID, "digest": digest})
	if err != nil {
		return "", nil, err
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
			return "", nil, err
		}
	}

	for _, d := range meta.BlobDigests {
		blob, err := repo.FindOneBy[models.Blob](airwaysql.H{"digest": d})
		if err != nil {
			return "", nil, err
		}
		if blob == nil {
			continue // raced with a delete; GC will reconcile
		}
		linked, err := repo.ExistsWhere[models.RepoBlob](airwaysql.H{"repo_id": r.ID, "blob_id": blob.ID})
		if err != nil {
			return "", nil, err
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
			return "", nil, err
		}
	}

	if isDigestRef {
		return digest, meta, nil
	}

	tag, err := repo.FindOneBy[models.Tag](airwaysql.H{"repo_id": r.ID, "name": reference})
	if err != nil {
		return "", nil, err
	}
	if tag == nil {
		_, err = repo.CreateFrom[models.Tag](airwaysql.H{
			"repo_id":         r.ID,
			"name":            reference,
			"manifest_digest": digest,
			"created_at":      now,
			"updated_at":      now,
		})
		if err != nil {
			return "", nil, err
		}
		return digest, meta, nil
	}
	if tag.ManifestDigest != digest {
		if err := repo.UpdateByID[models.Tag](tag.ID, airwaysql.H{
			"manifest_digest": digest,
			"updated_at":      now,
		}); err != nil {
			return "", nil, err
		}
	}
	return digest, meta, nil
}
