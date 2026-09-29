package v2_api

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/daqing/air-registry/app/services/blobs"
	"github.com/daqing/air-registry/app/services/blobstore"
	"github.com/daqing/air-registry/app/services/registrycfg"
	"github.com/daqing/air-registry/app/services/uploads"
)

// startUpload answers POST /v2/<name>/blobs/uploads/ — with ?mount=<digest>&from=<repo>
// it attempts a cross-repo mount, with ?digest= and a body it completes in
// one shot (monolithic), otherwise it opens a session.
func (h *Handler) startUpload(c *gin.Context, name string) {
	if !validRepoName(name) {
		ociError(c, http.StatusBadRequest, "NAME_INVALID", "invalid repository name")
		return
	}

	if digest, from := c.Query("mount"), c.Query("from"); digest != "" && from != "" {
		h.mountBlob(c, name, from, digest)
		return
	}

	if digest := c.Query("digest"); digest != "" {
		sess, err := h.Uploads.Start(name)
		if err != nil {
			ociError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
			return
		}
		if _, err := h.Uploads.Append(sess.UUID, limitUploadBody(c)); err != nil {
			h.Uploads.Remove(sess.UUID)
			writeUploadBodyError(c, err)
			return
		}
		h.finishUpload(c, name, sess, digest)
		return
	}

	h.startUploadSession(c, name)
}

// startUploadSession opens a fresh upload session and answers 202 with the
// session's Location and UUID.
func (h *Handler) startUploadSession(c *gin.Context, name string) {
	sess, err := h.Uploads.Start(name)
	if err != nil {
		ociError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}

	c.Header("Location", uploadLocation(name, sess.UUID))
	c.Header("Range", "0-0")
	c.Header("Docker-Upload-UUID", sess.UUID)
	c.Status(http.StatusAccepted)
}

// mountBlob answers POST /v2/<name>/blobs/uploads/?mount=<digest>&from=<repo>.
// When from holds the digest, the blob is linked into name without moving
// bytes and 201 is returned; otherwise the request degrades to a normal
// upload session (202) so the client can push the content.
func (h *Handler) mountBlob(c *gin.Context, name, from, digest string) {
	if !h.validBlobDigest(c, digest) {
		return
	}

	mounted, err := blobs.Mount(name, from, digest)
	if err != nil {
		ociError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}
	if !mounted {
		h.startUploadSession(c, name)
		return
	}

	c.Header("Docker-Content-Digest", digest)
	c.Header("Location", "/v2/"+name+"/blobs/"+digest)
	c.Status(http.StatusCreated)
}

// appendUpload answers PATCH (continue) and PUT (finalize) on
// /v2/<name>/blobs/uploads/<uuid>.
func (h *Handler) appendUpload(c *gin.Context, name, uuid string) {
	sess, ok := h.Uploads.Get(uuid)
	if !ok || sess.Repo != name {
		ociError(c, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "upload session unknown")
		return
	}

	if cr := c.GetHeader("Content-Range"); cr != "" {
		start, ok := parseContentRange(cr)
		if !ok || start != sess.Offset {
			ociError(c, http.StatusRequestedRangeNotSatisfiable, "REQUESTED_RANGE_NOT_SATISFIABLE", "content range does not match upload offset")
			return
		}
	}

	if _, err := h.Uploads.Append(uuid, limitUploadBody(c)); err != nil {
		if isUploadTooLarge(err) {
			// The session holds a truncated body; drop it so the client
			// starts a fresh upload.
			h.Uploads.Remove(uuid)
		}
		writeUploadBodyError(c, err)
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

// limitUploadBody caps the request body at the configured upload limit so an
// oversized push fails fast instead of filling the disk; past the cap the
// returned reader fails with an *http.MaxBytesError. An unlimited
// configuration passes the body through untouched.
func limitUploadBody(c *gin.Context) io.Reader {
	if limit := registrycfg.MaxUploadSize(); limit > 0 {
		return http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	}
	return c.Request.Body
}

func isUploadTooLarge(err error) bool {
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr)
}

// writeUploadBodyError maps a body read failure: over the upload cap → 413,
// anything else → 500.
func writeUploadBodyError(c *gin.Context, err error) {
	if isUploadTooLarge(err) {
		ociError(c, http.StatusRequestEntityTooLarge, "TOO_LARGE", "upload exceeds the configured size limit")
		return
	}
	ociError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
}

// parseContentRange reads a PATCH Content-Range header: "<start>-<end>"
// (an optional "bytes=" prefix is tolerated). Only the start matters for
// validation; end is checked for shape when present.
func parseContentRange(value string) (int64, bool) {
	value = strings.TrimSpace(strings.TrimPrefix(value, "bytes="))
	startStr, endStr, hasEnd := strings.Cut(value, "-")
	if startStr == "" {
		return 0, false
	}
	start, err := strconv.ParseInt(strings.TrimSpace(startStr), 10, 64)
	if err != nil || start < 0 {
		return 0, false
	}
	if hasEnd && strings.TrimSpace(endStr) != "" {
		end, err := strconv.ParseInt(strings.TrimSpace(endStr), 10, 64)
		if err != nil || end < start {
			return 0, false
		}
	}
	return start, true
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
