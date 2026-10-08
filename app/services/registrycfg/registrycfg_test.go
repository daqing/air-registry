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
		name     string
		env      string
		user     string
		password string
		want     bool
	}{
		{"unset", "", "", "", false},
		{"off", "false", "", "", false},
		{"on", "true", "", "", true},
		{"one", "1", "", "", true},
		{"garbage is off", "yes", "", "", false},
		{"credentials alone enable auth", "", "admin", "secret", true},
		{"credentials with unparsable switch", "yes", "admin", "secret", true},
		{"explicit off wins over credentials", "false", "admin", "secret", false},
		{"explicit on with credentials", "true", "admin", "secret", true},
		{"lone username is not enough", "", "admin", "", false},
		{"lone password is not enough", "", "", "secret", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("REGISTRY_AUTH_ENABLED", tc.env)
			t.Setenv("REGISTRY_AUTH_USERNAME", tc.user)
			t.Setenv("REGISTRY_AUTH_PASSWORD", tc.password)
			if got := AuthEnabled(); got != tc.want {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

func TestAuthCredentials(t *testing.T) {
	cases := []struct {
		name         string
		user         string
		password     string
		wantUser     string
		wantPassword string
		wantOK       bool
	}{
		{"both set", "admin", "secret", "admin", "secret", true},
		{"username trimmed", "  admin  ", "secret", "admin", "secret", true},
		{"password kept verbatim", "admin", " s3 cret ", "admin", " s3 cret ", true},
		{"empty", "", "", "", "", false},
		{"blank username", "   ", "secret", "", "secret", false},
		{"password missing", "admin", "", "admin", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("REGISTRY_AUTH_USERNAME", tc.user)
			t.Setenv("REGISTRY_AUTH_PASSWORD", tc.password)
			user, password, ok := AuthCredentials()
			if user != tc.wantUser || password != tc.wantPassword || ok != tc.wantOK {
				t.Fatalf("expected (%q, %q, %v), got (%q, %q, %v)",
					tc.wantUser, tc.wantPassword, tc.wantOK, user, password, ok)
			}
		})
	}
}
