package v2_api

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/daqing/air-registry/app/services/blobstore"
)

// serveBlob answers GET /v2/<name>/blobs/<digest>.
func (h *Handler) serveBlob(c *gin.Context, digest string) {
	if !h.validBlobDigest(c, digest) {
		return
	}

	size, err := h.Blobs.Size(digest)
	if errors.Is(err, fs.ErrNotExist) {
		ociError(c, http.StatusNotFound, "BLOB_UNKNOWN", "blob unknown to registry")
		return
	}
	if err != nil {
		ociError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}

	rc, err := h.Blobs.Get(digest)
	if err != nil {
		ociError(c, http.StatusNotFound, "BLOB_UNKNOWN", "blob unknown to registry")
		return
	}
	defer rc.Close()

	c.Header("Content-Type", "application/octet-stream")
	c.Header("Docker-Content-Digest", digest)
	c.Header("Content-Length", strconv.FormatInt(size, 10))
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, rc)
}

// checkBlob answers HEAD /v2/<name>/blobs/<digest>.
func (h *Handler) checkBlob(c *gin.Context, digest string) {
	if !h.validBlobDigest(c, digest) {
		return
	}

	size, err := h.Blobs.Size(digest)
	if errors.Is(err, fs.ErrNotExist) {
		ociError(c, http.StatusNotFound, "BLOB_UNKNOWN", "blob unknown to registry")
		return
	}
	if err != nil {
		ociError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}

	c.Header("Content-Type", "application/octet-stream")
	c.Header("Docker-Content-Digest", digest)
	c.Header("Content-Length", strconv.FormatInt(size, 10))
	c.Status(http.StatusOK)
}

// validBlobDigest answers the request with 400 on malformed digests and
// reports whether the digest is usable.
func (h *Handler) validBlobDigest(c *gin.Context, digest string) bool {
	if _, err := h.Blobs.Path(digest); err != nil {
		if errors.Is(err, blobstore.ErrInvalidDigest) {
			ociError(c, http.StatusBadRequest, "DIGEST_INVALID", err.Error())
			return false
		}
		ociError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return false
	}
	return true
}
