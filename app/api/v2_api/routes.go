package v2_api

import "github.com/gin-gonic/gin"

// Routes mounts the OCI /v2/ API. Gin wildcards only match at the end of a
// route while repository names contain slashes, so a single catch-all
// serves the whole API and Dispatch parses the path.
func (h *Handler) Routes(r *gin.Engine) {
	r.GET("/v2/*path", h.Dispatch)
	r.HEAD("/v2/*path", h.Dispatch)
	r.POST("/v2/*path", h.Dispatch)
	r.PUT("/v2/*path", h.Dispatch)
	r.PATCH("/v2/*path", h.Dispatch)
	r.DELETE("/v2/*path", h.Dispatch)
}
