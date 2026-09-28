package v2_api

import (
	"errors"
	"io"
	"net/http"

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
