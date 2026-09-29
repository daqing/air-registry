package models

import (
	"time"

	airwaysql "github.com/daqing/airway/lib/sql"
)

type Blob struct {
	ID        airwaysql.IdType `db:"id" json:"id"`
	Digest    string           `db:"digest" json:"digest"`
	Size      int64            `db:"size" json:"size"`
	Path      string           `db:"path" json:"path"`
	CreatedAt time.Time        `db:"created_at" json:"created_at"`
	UpdatedAt time.Time        `db:"updated_at" json:"updated_at"`
}

func (Blob) TableName() string {
	return "blobs"
}

func init() {
	registerREPLModel("Blob", Blob{})
}
