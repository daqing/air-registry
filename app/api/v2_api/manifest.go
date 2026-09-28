package v2_api

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/daqing/air-registry/app/services/manifests"
)

// maxManifestSize bounds PUT bodies; large indexes fit comfortably.
const maxManifestSize = 32 << 20

// putManifest answers PUT /v2/<name>/manifests/<reference>.
func (h *Handler) putManifest(c *gin.Context, name, reference string) {
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxManifestSize))
	if err != nil {
		ociError(c, http.StatusBadRequest, "MANIFEST_INVALID", "manifest unreadable or too large")
		return
	}

	digest, err := manifests.Store(name, reference, body, c.GetHeader("Content-Type"))
	if err != nil {
		switch {
		case errors.Is(err, manifests.ErrInvalidManifest):
			ociError(c, http.StatusBadRequest, "MANIFEST_INVALID", err.Error())
		case errors.Is(err, manifests.ErrInvalidTag):
			ociError(c, http.StatusBadRequest, "TAG_INVALID", err.Error())
		case errors.Is(err, manifests.ErrMissingBlob):
			ociError(c, http.StatusBadRequest, "MANIFEST_BLOB_UNKNOWN", err.Error())
		default:
			ociError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		}
		return
	}

	c.Header("Docker-Content-Digest", digest)
	c.Header("Location", "/v2/"+name+"/manifests/"+digest)
	c.Status(http.StatusCreated)
}

// getManifest answers GET and HEAD /v2/<name>/manifests/<reference>, serving
// the stored bytes with their original media type.
func (h *Handler) getManifest(c *gin.Context, name, reference string) {
	m, err := manifests.Find(name, reference)
	if err != nil {
		ociError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}
	if m == nil {
		ociError(c, http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest unknown")
		return
	}

	c.Header("Content-Type", m.MediaType)
	c.Header("Docker-Content-Digest", m.Digest)
	c.Header("Content-Length", strconv.Itoa(len(m.Content)))
	c.Status(http.StatusOK)

	if c.Request.Method == http.MethodGet {
		_, _ = io.WriteString(c.Writer, m.Content)
	}
}

// listTags answers GET /v2/<name>/tags/list. The n/last pagination
// parameters are accepted but not enforced yet.
func (h *Handler) listTags(c *gin.Context, name string) {
	tags, err := manifests.ListTags(name)
	if err != nil {
		ociError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}
	if tags == nil {
		ociError(c, http.StatusNotFound, "NAME_UNKNOWN", "repository name not known to registry")
		return
	}

	c.JSON(http.StatusOK, gin.H{"name": name, "tags": tags})
}
