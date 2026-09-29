package models

import (
	"time"

	airwaysql "github.com/daqing/airway/lib/sql"
)

type RepoBlob struct {
	ID        airwaysql.IdType `db:"id" json:"id"`
	RepoID    airwaysql.IdType `db:"repo_id" json:"repo_id"`
	BlobID    airwaysql.IdType `db:"blob_id" json:"blob_id"`
	CreatedAt time.Time        `db:"created_at" json:"created_at"`
	UpdatedAt time.Time        `db:"updated_at" json:"updated_at"`
}

func (RepoBlob) TableName() string {
	return "repo_blobs"
}

func init() {
	registerREPLModel("RepoBlob", RepoBlob{})
}
