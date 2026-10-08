package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseProjectDir(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    []string
		dir     string
		wantErr bool
	}{
		{name: "none", args: nil, want: []string{}},
		{name: "leading flag", args: []string{"-d", "/srv/reg", "server"}, want: []string{"server"}, dir: "/srv/reg"},
		{name: "trailing flag", args: []string{"server", "-d", "/srv/reg"}, want: []string{"server"}, dir: "/srv/reg"},
		{name: "long flag", args: []string{"--dir", "/srv/reg", "gc"}, want: []string{"gc"}, dir: "/srv/reg"},
		{name: "equals short", args: []string{"-d=/srv/reg", "db:migrate"}, want: []string{"db:migrate"}, dir: "/srv/reg"},
		{name: "equals long", args: []string{"--dir=/srv/reg"}, want: []string{}, dir: "/srv/reg"},
		{name: "missing value", args: []string{"server", "-d"}, wantErr: true},
		{name: "duplicate", args: []string{"-d", "/a", "-d", "/b"}, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, dir, err := parseProjectDir(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got dir=%q rest=%v", dir, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if dir != tc.dir {
				t.Errorf("dir = %q, want %q", dir, tc.dir)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("rest = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEnterProjectDir(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(t.TempDir()) // start somewhere else so the chdir is observable

	abs, err := enterProjectDir(dir)
	if err != nil {
		t.Fatalf("enterProjectDir: %v", err)
	}

	// t.TempDir() may return a symlinked path (macOS /var -> /private/var),
	// so compare after resolving symlinks.
	want := resolved(t, dir)
	if resolved(t, abs) != want {
		t.Errorf("abs = %q, want %q", abs, want)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if resolved(t, wd) != want {
		t.Errorf("cwd = %q, want %q", wd, want)
	}
}

func resolved(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("Abs(%q): %v", path, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", abs, err)
	}
	return resolved
}

func TestEnterProjectDirRejectsMissingAndFiles(t *testing.T) {
	t.Chdir(t.TempDir())

	if _, err := enterProjectDir(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected error for missing directory")
	}

	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := enterProjectDir(file); err == nil {
		t.Error("expected error for regular file")
	}
}

func TestRunEmbeddedMigrationCommandDefersToOnDisk(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join(dir, "db", "migrate"), 0o755); err != nil {
		t.Fatal(err)
	}

	handled, err := runEmbeddedMigrationCommand([]string{"db:migrate"})
	if handled || err != nil {
		t.Fatalf("handled=%v err=%v, want false/nil when db/migrate exists", handled, err)
	}
}

func TestRunEmbeddedMigrationCommandIgnoresOtherCommands(t *testing.T) {
	t.Chdir(t.TempDir())

	handled, err := runEmbeddedMigrationCommand([]string{"server"})
	if handled || err != nil {
		t.Fatalf("handled=%v err=%v, want false/nil for a non-migration command", handled, err)
	}
}

func TestRunEmbeddedMigrationCommandNeedsDSN(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("AIRWAY_DSN", "")
	t.Setenv("DSN", "")

	handled, err := runEmbeddedMigrationCommand([]string{"db:status"})
	if !handled {
		t.Fatal("handled = false, want true for db:status in a bare project dir")
	}
	if err == nil {
		t.Fatal("expected an error when no DSN is configured")
	}
}

func TestApplyProjectDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AIRWAY_DSN", "")
	t.Setenv("DSN", "")
	t.Setenv("DATA_DIR", "")
	t.Setenv("STORAGE_ROOT", "")

	applyProjectDefaults(dir)

	if got, want := os.Getenv("AIRWAY_DSN"), "sqlite://"+filepath.Join(dir, "registry.db"); got != want {
		t.Errorf("AIRWAY_DSN = %q, want %q", got, want)
	}
	if got, want := os.Getenv("DATA_DIR"), filepath.Join(dir, "data", "storage"); got != want {
		t.Errorf("DATA_DIR = %q, want %q", got, want)
	}
}

func TestApplyProjectDefaultsKeepsExplicitValues(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DSN", "sqlite://./custom.db")
	t.Setenv("DATA_DIR", "./custom-storage")
	t.Setenv("AIRWAY_DSN", "")
	t.Setenv("STORAGE_ROOT", "")

	applyProjectDefaults(dir)

	if got := os.Getenv("AIRWAY_DSN"); got != "" {
		t.Errorf("AIRWAY_DSN = %q, want empty (DSN is set)", got)
	}
	if got := os.Getenv("DATA_DIR"); got != "./custom-storage" {
		t.Errorf("DATA_DIR = %q, want ./custom-storage", got)
	}
}
