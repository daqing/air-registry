package registrycfg

import "testing"

func TestDataDir(t *testing.T) {
	cases := []struct {
		name    string
		dataDir string
		storage string
		want    string
	}{
		{"default", "", "", "./data/storage"},
		{"legacy storage root", "", "./legacy", "./legacy"},
		{"data dir wins", "./data-dir", "./legacy", "./data-dir"},
		{"blank data dir falls back", "  ", "./legacy", "./legacy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DATA_DIR", tc.dataDir)
			t.Setenv("STORAGE_ROOT", tc.storage)
			if got := DataDir(); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestMaxUploadSize(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want int64
	}{
		{"unset", "", 0},
		{"unlimited", "0", 0},
		{"cap", "1048576", 1048576},
		{"negative means unlimited", "-5", 0},
		{"garbage means unlimited", "lots", 0},
		{"blank means unlimited", "  ", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MAX_UPLOAD_SIZE", tc.env)
			if got := MaxUploadSize(); got != tc.want {
				t.Fatalf("expected %d, got %d", tc.want, got)
			}
		})
	}
}

func TestAuthEnabled(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want bool
	}{
		{"unset", "", false},
		{"off", "false", false},
		{"on", "true", true},
		{"one", "1", true},
		{"garbage is off", "yes", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("REGISTRY_AUTH_ENABLED", tc.env)
			if got := AuthEnabled(); got != tc.want {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
		})
	}
}
