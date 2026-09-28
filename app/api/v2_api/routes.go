package v2_api

import "github.com/gin-gonic/gin"

func Routes(r *gin.Engine) {
	r.GET("/v2/", IndexAction)
}
