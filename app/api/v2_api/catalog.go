package v2_api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/daqing/air-registry/app/services/catalog"
)

// listCatalog answers GET /v2/_catalog: repository names in lexicographic
// order, paginated by n (page size) and last (exclusive lower bound from
// the previous page, carried in the Link header).
func (h *Handler) listCatalog(c *gin.Context) {
	n := 0
	if v := c.Query("n"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed < 1 {
			ociError(c, http.StatusBadRequest, "PAGINATION_INVALID", "invalid n query parameter")
			return
		}
		n = parsed
	}

	names, hasMore, err := catalog.List(c.Query("last"), n)
	if err != nil {
		ociError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}

	if hasMore && len(names) > 0 {
		c.Header("Link", `</v2/_catalog?n=`+strconv.Itoa(n)+`&last=`+names[len(names)-1]+`>; rel="next"`)
	}
	c.JSON(http.StatusOK, gin.H{"repositories": names})
}
