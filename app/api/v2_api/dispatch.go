package v2_api

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/daqing/air-registry/app/services/blobstore"
)

// Handler serves the OCI /v2/ API, dispatching catch-all paths like
// "library/nginx/blobs/sha256:..." to the right endpoint.
type Handler struct {
	Blobs *blobstore.Store
}

func (h *Handler) Dispatch(c *gin.Context) {
	c.Header(apiVersionHeader, apiVersion)

	path := strings.Trim(c.Param("path"), "/")
	if path == "" {
		IndexAction(c)
		return
	}

	segs := strings.Split(path, "/")
	if name, ref, ok := parseManifestRef(segs); ok {
		if !validRepoName(name) {
			ociError(c, http.StatusBadRequest, "NAME_INVALID", "invalid repository name")
			return
		}
		if c.Request.Method == http.MethodPut {
			h.putManifest(c, name, ref)
		} else {
			ociError(c, http.StatusNotFound, "UNSUPPORTED", "unsupported API endpoint")
		}
		return
	}
	if name, digest, ok := parseBlobRef(segs); ok {
		if !validRepoName(name) {
			ociError(c, http.StatusBadRequest, "NAME_INVALID", "invalid repository name")
			return
		}
		if c.Request.Method == http.MethodHead {
			h.checkBlob(c, digest)
		} else {
			h.serveBlob(c, digest)
		}
		return
	}

	ociError(c, http.StatusNotFound, "UNSUPPORTED", "unsupported API endpoint")
}

// parseBlobRef matches "<name>/blobs/<digest>" where <name> may span
// multiple path segments.
func parseBlobRef(segs []string) (name, digest string, ok bool) {
	if len(segs) < 3 || segs[len(segs)-2] != "blobs" {
		return "", "", false
	}
	name = strings.Join(segs[:len(segs)-2], "/")
	digest = segs[len(segs)-1]
	return name, digest, name != "" && digest != ""
}

// parseManifestRef matches "<name>/manifests/<reference>" where <name> may
// span multiple path segments.
func parseManifestRef(segs []string) (name, reference string, ok bool) {
	if len(segs) < 3 || segs[len(segs)-2] != "manifests" {
		return "", "", false
	}
	name = strings.Join(segs[:len(segs)-2], "/")
	reference = segs[len(segs)-1]
	return name, reference, name != "" && reference != ""
}

var repoNameSegPattern = regexp.MustCompile(`^[a-z0-9]+((\.|_|__|-+)[a-z0-9]+)*$`)

func validRepoName(name string) bool {
	for _, seg := range strings.Split(name, "/") {
		if !repoNameSegPattern.MatchString(seg) {
			return false
		}
	}
	return true
}

func ociError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{
		"errors": []gin.H{{"code": code, "message": message}},
	})
}
