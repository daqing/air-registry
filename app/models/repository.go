package models

import (
	"time"

	airwaysql "github.com/daqing/airway/lib/sql"
)

type Repository struct {
	ID        airwaysql.IdType `db:"id" json:"id"`
	Name      string           `db:"name" json:"name"`
	CreatedAt time.Time        `db:"created_at" json:"created_at"`
	UpdatedAt time.Time        `db:"updated_at" json:"updated_at"`
}

func (Repository) TableName() string {
	return "repositories"
}

func init() {
	registerREPLModel("Repository", Repository{})
}
