package v2_api

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/daqing/air-registry/app/services/blobstore"
	"github.com/daqing/air-registry/app/services/uploads"
)

// Handler serves the OCI /v2/ API, dispatching catch-all paths like
// "library/nginx/blobs/sha256:..." to the right endpoint.
type Handler struct {
	Blobs   *blobstore.Store
	Uploads *uploads.Manager
}

func (h *Handler) Dispatch(c *gin.Context) {
	c.Header(apiVersionHeader, apiVersion)

	path := strings.Trim(c.Param("path"), "/")
	if path == "" {
		IndexAction(c)
		return
	}

	// _catalog is reserved by the spec and can never be a repository name.
	if path == "_catalog" {
		if c.Request.Method == http.MethodGet {
			h.listCatalog(c)
		} else {
			ociError(c, http.StatusNotFound, "UNSUPPORTED", "unsupported API endpoint")
		}
		return
	}

	segs := strings.Split(path, "/")
	if name, uuid, ok := parseUploadSessionRef(segs); ok {
		if !validRepoName(name) {
			ociError(c, http.StatusBadRequest, "NAME_INVALID", "invalid repository name")
			return
		}
		switch c.Request.Method {
		case http.MethodPut, http.MethodPatch:
			h.appendUpload(c, name, uuid)
		case http.MethodDelete:
			h.abortUpload(c, name, uuid)
		case http.MethodGet:
			h.getUploadStatus(c, name, uuid)
		default:
			ociError(c, http.StatusNotFound, "UNSUPPORTED", "unsupported API endpoint")
		}
		return
	}
	if name, ok := parseUploadStartRef(segs); ok {
		if !validRepoName(name) {
			ociError(c, http.StatusBadRequest, "NAME_INVALID", "invalid repository name")
			return
		}
		if c.Request.Method == http.MethodPost {
			h.startUpload(c, name)
		} else {
			ociError(c, http.StatusNotFound, "UNSUPPORTED", "unsupported API endpoint")
		}
		return
	}
	if name, ok := parseTagsListRef(segs); ok {
		if !validRepoName(name) {
			ociError(c, http.StatusBadRequest, "NAME_INVALID", "invalid repository name")
			return
		}
		if c.Request.Method == http.MethodGet {
			h.listTags(c, name)
		} else {
			ociError(c, http.StatusNotFound, "UNSUPPORTED", "unsupported API endpoint")
		}
		return
	}
	if name, digest, ok := parseReferrersRef(segs); ok {
		if !validRepoName(name) {
			ociError(c, http.StatusBadRequest, "NAME_INVALID", "invalid repository name")
			return
		}
		if c.Request.Method == http.MethodGet {
			h.listReferrers(c, name, digest)
		} else {
			ociError(c, http.StatusNotFound, "UNSUPPORTED", "unsupported API endpoint")
		}
		return
	}
	if name, ref, ok := parseManifestRef(segs); ok {
		if !validRepoName(name) {
			ociError(c, http.StatusBadRequest, "NAME_INVALID", "invalid repository name")
			return
		}
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead:
			h.getManifest(c, name, ref)
		case http.MethodPut:
			h.putManifest(c, name, ref)
		case http.MethodDelete:
			h.deleteManifest(c, name, ref)
		default:
			ociError(c, http.StatusNotFound, "UNSUPPORTED", "unsupported API endpoint")
		}
		return
	}
	if name, digest, ok := parseBlobRef(segs); ok {
		if !validRepoName(name) {
			ociError(c, http.StatusBadRequest, "NAME_INVALID", "invalid repository name")
			return
		}
		switch c.Request.Method {
		case http.MethodHead:
			h.checkBlob(c, name, digest)
		case http.MethodDelete:
			h.deleteBlob(c, name, digest)
		default:
			h.serveBlob(c, name, digest)
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

// parseUploadStartRef matches "<name>/blobs/uploads" where <name> may span
// multiple path segments.
func parseUploadStartRef(segs []string) (name string, ok bool) {
	if len(segs) < 4 || segs[len(segs)-2] != "blobs" || segs[len(segs)-1] != "uploads" {
		return "", false
	}
	name = strings.Join(segs[:len(segs)-2], "/")
	return name, name != ""
}

// parseUploadSessionRef matches "<name>/blobs/uploads/<uuid>".
func parseUploadSessionRef(segs []string) (name, uuid string, ok bool) {
	if len(segs) < 5 || segs[len(segs)-3] != "blobs" || segs[len(segs)-2] != "uploads" {
		return "", "", false
	}
	name = strings.Join(segs[:len(segs)-3], "/")
	uuid = segs[len(segs)-1]
	return name, uuid, name != "" && uuid != ""
}

// parseTagsListRef matches "<name>/tags/list" where <name> may span multiple
// path segments.
func parseTagsListRef(segs []string) (name string, ok bool) {
	if len(segs) < 3 || segs[len(segs)-2] != "tags" || segs[len(segs)-1] != "list" {
		return "", false
	}
	name = strings.Join(segs[:len(segs)-2], "/")
	return name, name != ""
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

// parseReferrersRef matches "<name>/referrers/<digest>" where <name> may
// span multiple path segments.
func parseReferrersRef(segs []string) (name, digest string, ok bool) {
	if len(segs) < 3 || segs[len(segs)-2] != "referrers" {
		return "", "", false
	}
	name = strings.Join(segs[:len(segs)-2], "/")
	digest = segs[len(segs)-1]
	return name, digest, name != "" && digest != ""
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
