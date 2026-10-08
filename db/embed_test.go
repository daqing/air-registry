package db

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationsContainSQLFiles(t *testing.T) {
	entries, err := fs.ReadDir(Migrations(), ".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	var up, down int
	for _, entry := range entries {
		switch {
		case strings.HasSuffix(entry.Name(), ".up.sql"):
			up++
		case strings.HasSuffix(entry.Name(), ".down.sql"):
			down++
		}
	}

	if up == 0 {
		t.Fatal("no .up.sql migrations embedded")
	}
	if up != down {
		t.Fatalf("up=%d down=%d, want the same number of up/down files", up, down)
	}
}

func TestOnDisk(t *testing.T) {
	t.Chdir(t.TempDir())

	if OnDisk() {
		t.Error("OnDisk() = true in an empty directory, want false")
	}

	if err := os.MkdirAll(filepath.Join("db", "migrate"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if !OnDisk() {
		t.Error("OnDisk() = false after creating db/migrate, want true")
	}
}
