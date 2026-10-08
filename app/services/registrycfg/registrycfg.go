// Package registrycfg collects the registry's tunable knobs in one place:
// the storage root, the per-upload size cap, and the auth gate. Each knob
// reads its env var on call, matching how the rest of the app resolves
// configuration, so changing a value requires a restart to take effect
// everywhere.
package registrycfg

import (
	"os"
	"strconv"
	"strings"
)

const defaultDataDir = "./data/storage"

// DataDir returns the blob storage root. DATA_DIR wins; STORAGE_ROOT is
// honored for backward compatibility; the default is ./data/storage.
func DataDir() string {
	if dir := strings.TrimSpace(os.Getenv("DATA_DIR")); dir != "" {
		return dir
	}
	if dir := strings.TrimSpace(os.Getenv("STORAGE_ROOT")); dir != "" {
		return dir
	}
	return defaultDataDir
}

// MaxUploadSize returns the per-request blob upload cap in bytes. Zero or a
// negative value (the default, including unparsable input) means unlimited.
func MaxUploadSize() int64 {
	size, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("MAX_UPLOAD_SIZE")), 10, 64)
	if err != nil || size < 0 {
		return 0
	}
	return size
}

// AuthUsername returns the configured basic-auth username
// (REGISTRY_AUTH_USERNAME), or "" when unset.
func AuthUsername() string {
	return strings.TrimSpace(os.Getenv("REGISTRY_AUTH_USERNAME"))
}

// AuthPassword returns the configured basic-auth password
// (REGISTRY_AUTH_PASSWORD), or "" when unset. It is returned verbatim so
// passwords may contain spaces or other significant characters.
func AuthPassword() string {
	return os.Getenv("REGISTRY_AUTH_PASSWORD")
}

// AuthCredentials returns the configured basic-auth pair. ok is false unless
// both parts are present: a lone username or password cannot authenticate
// anyone, so it never counts as configured.
func AuthCredentials() (username, password string, ok bool) {
	username = AuthUsername()
	password = AuthPassword()
	return username, password, username != "" && password != ""
}

// AuthEnabled reports whether basic auth is required on the /v2/ API. An
// explicit REGISTRY_AUTH_ENABLED boolean wins; when the variable is unset (or
// unparsable) auth turns on as soon as a username/password pair is configured,
// so dropping credentials into .env is enough to protect the registry.
func AuthEnabled() bool {
	if enabled, err := strconv.ParseBool(strings.TrimSpace(os.Getenv("REGISTRY_AUTH_ENABLED"))); err == nil {
		return enabled
	}
	_, _, ok := AuthCredentials()
	return ok
}
