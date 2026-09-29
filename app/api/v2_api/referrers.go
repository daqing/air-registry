package v2_api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/daqing/air-registry/app/services/manifests"
)

// listReferrers answers GET /v2/<name>/referrers/<digest> (OCI 1.1): an
// index of the manifests that declare digest as their subject, optionally
// filtered by ?artifactType=. Subjects without referrers get an empty
// index with 200, never 404.
func (h *Handler) listReferrers(c *gin.Context, name, digest string) {
	if !h.validBlobDigest(c, digest) {
		return
	}

	refs, err := manifests.Referrers(name, digest, c.Query("artifactType"))
	if err != nil {
		ociError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}
	if filter := c.Query("artifactType"); filter != "" {
		c.Header("OCI-Filters-Applied", "artifactType")
	}

	descriptors := make([]gin.H, 0, len(refs))
	for _, ref := range refs {
		d := gin.H{
			"mediaType": ref.MediaType,
			"digest":    ref.Digest,
			"size":      ref.Size,
		}
		if ref.ArtifactType != nil {
			d["artifactType"] = *ref.ArtifactType
		}
		if len(ref.Annotations) > 0 {
			d["annotations"] = ref.Annotations
		}
		descriptors = append(descriptors, d)
	}

	c.Header("Content-Type", manifests.MediaTypeOCIIndex)
	c.JSON(http.StatusOK, gin.H{
		"schemaVersion": 2,
		"mediaType":     manifests.MediaTypeOCIIndex,
		"manifests":     descriptors,
	})
}
