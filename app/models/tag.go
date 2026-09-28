package models

import (
	"time"

	airwaysql "github.com/daqing/airway/lib/sql"
)

// Tag maps a repo-local name to a manifest digest; retagging updates
// ManifestDigest in place.
type Tag struct {
	ID             airwaysql.IdType `db:"id" json:"id"`
	RepoID         airwaysql.IdType `db:"repo_id" json:"repo_id"`
	Name           string           `db:"name" json:"name"`
	ManifestDigest string           `db:"manifest_digest" json:"manifest_digest"`
	CreatedAt      time.Time        `db:"created_at" json:"created_at"`
	UpdatedAt      time.Time        `db:"updated_at" json:"updated_at"`
}

func (Tag) TableName() string {
	return "tags"
}

func init() {
	registerREPLModel("Tag", Tag{})
}
