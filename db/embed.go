// Package db embeds the SQL migrations so a `go install`ed binary can create
// and upgrade its schema with nothing on disk but a .env and a project
// directory (`air-registry -d <dir> server`). Source checkouts keep using the
// on-disk db/migrate directory via the Airway CLI, so schema snapshots and
// any Go DSL migrations there still work.
package db

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed migrate/*.sql
var migrations embed.FS

// Migrations returns the embedded migrations rooted at the directory that
// holds the <version>_<name>.up.sql / .down.sql pairs, which is the layout
// migrate.Options expects.
func Migrations() fs.FS {
	sub, err := fs.Sub(migrations, "migrate")
	if err != nil {
		// migrate/ is embedded at build time, so this can only fail if the
		// go:embed directive above is removed.
		panic(err)
	}
	return sub
}

// OnDisk reports whether the current working directory has a db/migrate
// directory. When it does, the Airway CLI owns migrations (with its schema
// snapshot); when it does not, the embedded migrations are used instead.
func OnDisk() bool {
	info, err := os.Stat(filepath.Join("db", "migrate"))
	return err == nil && info.IsDir()
}
