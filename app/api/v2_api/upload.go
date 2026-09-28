package v2_api

import (
	"errors"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/daqing/air-registry/app/services/blobs"
	"github.com/daqing/air-registry/app/services/blobstore"
	"github.com/daqing/air-registry/app/services/uploads"
)

// startUpload answers POST /v2/<name>/blobs/uploads/ — with ?digest= and a
// body it completes in one shot (monolithic), otherwise it opens a session.
func (h *Handler) startUpload(c *gin.Context, name string) {
	if !validRepoName(name) {
		ociError(c, http.StatusBadRequest, "NAME_INVALID", "invalid repository name")
		return
	}

	sess, err := h.Uploads.Start(name)
	if err != nil {
		ociError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}

	if digest := c.Query("digest"); digest != "" {
		if _, err := h.Uploads.Append(sess.UUID, c.Request.Body); err != nil {
			h.Uploads.Remove(sess.UUID)
			ociError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
			return
		}
		h.finishUpload(c, name, sess, digest)
		return
	}

	c.Header("Location", uploadLocation(name, sess.UUID))
	c.Header("Range", "0-0")
	c.Header("Docker-Upload-UUID", sess.UUID)
	c.Status(http.StatusAccepted)
}

// appendUpload answers PATCH (continue) and PUT (finalize) on
// /v2/<name>/blobs/uploads/<uuid>.
func (h *Handler) appendUpload(c *gin.Context, name, uuid string) {
	sess, ok := h.Uploads.Get(uuid)
	if !ok || sess.Repo != name {
		ociError(c, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "upload session unknown")
		return
	}

	if _, err := h.Uploads.Append(uuid, c.Request.Body); err != nil {
		ociError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}

	if c.Request.Method == http.MethodPatch {
		c.Header("Location", uploadLocation(name, uuid))
		c.Header("Range", "0-"+strconv.FormatInt(maxInt64(sess.Offset-1, 0), 10))
		c.Header("Docker-Upload-UUID", uuid)
		c.Status(http.StatusAccepted)
		return
	}

	digest := c.Query("digest")
	if digest == "" {
		ociError(c, http.StatusBadRequest, "DIGEST_INVALID", "missing digest query parameter")
		return
	}
	h.finishUpload(c, name, sess, digest)
}

// finishUpload publishes the session's temp file into the blob store
// (verifying its digest) and records it in the database.
func (h *Handler) finishUpload(c *gin.Context, name string, sess *uploads.Session, digest string) {
	f, err := os.Open(sess.Path)
	if err != nil {
		ociError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}
	defer f.Close()

	size, err := h.Blobs.Put(f, digest)
	if err != nil {
		switch {
		case errors.Is(err, blobstore.ErrInvalidDigest), errors.Is(err, blobstore.ErrDigestMismatch):
			// Keep the session alive so the client can retry.
			ociError(c, http.StatusBadRequest, "DIGEST_INVALID", err.Error())
		default:
			ociError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		}
		return
	}

	relPath, err := blobstore.RelativePath(digest)
	if err != nil {
		ociError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}
	if err := blobs.RegisterUpload(name, digest, size, relPath); err != nil {
		ociError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}

	h.Uploads.Remove(sess.UUID)

	c.Header("Docker-Content-Digest", digest)
	c.Header("Location", "/v2/"+name+"/blobs/"+digest)
	c.Status(http.StatusCreated)
}

// abortUpload answers DELETE /v2/<name>/blobs/uploads/<uuid>.
func (h *Handler) abortUpload(c *gin.Context, name, uuid string) {
	sess, ok := h.Uploads.Get(uuid)
	if !ok || sess.Repo != name {
		ociError(c, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "upload session unknown")
		return
	}
	h.Uploads.Remove(uuid)
	c.Status(http.StatusNoContent)
}

func uploadLocation(name, uuid string) string {
	return "/v2/" + name + "/blobs/uploads/" + uuid
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
