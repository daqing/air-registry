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

// AuthEnabled reports whether the registry auth gate is enabled
// (REGISTRY_AUTH_ENABLED). It defaults to off; any value that is not a
// recognizable boolean falls back to off as well.
func AuthEnabled() bool {
	enabled, err := strconv.ParseBool(strings.TrimSpace(os.Getenv("REGISTRY_AUTH_ENABLED")))
	return err == nil && enabled
}
