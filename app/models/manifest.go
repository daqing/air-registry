package models

import (
	"time"

	airwaysql "github.com/daqing/airway/lib/sql"
)

// Manifest stores the manifest bytes exactly as pushed, plus the metadata
// parsed out of them. ArtifactType and SubjectDigest are nil unless the
// manifest carries them (OCI 1.1); SubjectDigest feeds the Referrers API.
type Manifest struct {
	ID            airwaysql.IdType `db:"id" json:"id"`
	RepoID        airwaysql.IdType `db:"repo_id" json:"repo_id"`
	Digest        string           `db:"digest" json:"digest"`
	MediaType     string           `db:"media_type" json:"media_type"`
	ArtifactType  *string          `db:"artifact_type" json:"artifact_type"`
	SubjectDigest *string          `db:"subject_digest" json:"subject_digest"`
	Size          int64            `db:"size" json:"size"`
	Content       string           `db:"content" json:"content"`
	CreatedAt     time.Time        `db:"created_at" json:"created_at"`
	UpdatedAt     time.Time        `db:"updated_at" json:"updated_at"`
}

func (Manifest) TableName() string {
	return "manifests"
}

func init() {
	registerREPLModel("Manifest", Manifest{})
}
