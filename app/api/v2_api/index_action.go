package v2_api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const (
	apiVersionHeader = "Docker-Distribution-API-Version"
	apiVersion       = "registry/2.0"
)

// IndexAction answers the OCI version check (GET /v2/) that every client
// issues before pushing or pulling. The body stays empty on purpose.
func IndexAction(c *gin.Context) {
	c.Header(apiVersionHeader, apiVersion)
	c.Status(http.StatusOK)
}
