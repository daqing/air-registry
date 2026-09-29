// Package middlewares hosts the registry's HTTP middlewares.
package middlewares

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/daqing/air-registry/app/services/registrycfg"
)

// RegistryAuth is the auth gate for the OCI registry API and the extension
// point where credential checking will plug in (basic auth / htpasswd — see
// the roadmap). Auth is off by default; while no credential backend exists,
// an enabled gate denies every request with 401 rather than silently
// allowing pushes and pulls.
func RegistryAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !registrycfg.AuthEnabled() {
			c.Next()
			return
		}

		c.Header("WWW-Authenticate", `Basic realm="air-registry"`)
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"errors": []gin.H{{
				"code":    "UNAUTHORIZED",
				"message": "registry auth is enabled but no credential backend is configured yet",
			}},
		})
	}
}
