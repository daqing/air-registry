// Package middlewares hosts the registry's HTTP middlewares.
package middlewares

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/daqing/air-registry/app/services/registrycfg"
)

// authRealm names the protection space advertised to clients. Docker, crane
// and oras read the resulting `WWW-Authenticate: Basic realm="air-registry"`
// challenge and prompt the terminal user for a username and password before
// retrying the push or pull.
const authRealm = "air-registry"

// RegistryAuth guards the OCI registry API with HTTP basic auth. It is the
// gate mounted on the /v2 routes; the web UI is unaffected.
//
// When auth is disabled (the default) requests pass through untouched. When
// it is enabled every request must carry credentials matching
// REGISTRY_AUTH_USERNAME/REGISTRY_AUTH_PASSWORD. Missing or wrong credentials
// get a 401 with a Basic challenge, which is what makes docker prompt on the
// command line; if auth is enabled but no credentials are configured the
// gate fails closed rather than exposing the registry.
func RegistryAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !registrycfg.AuthEnabled() {
			c.Next()
			return
		}

		username, password, configured := registrycfg.AuthCredentials()
		if !configured {
			abortUnauthorized(c, "registry auth is enabled but no credentials are configured")
			return
		}

		givenUser, givenPass, ok := c.Request.BasicAuth()
		if !ok || !credentialsMatch(givenUser, givenPass, username, password) {
			abortUnauthorized(c, "authentication required")
			return
		}

		c.Next()
	}
}

// credentialsMatch compares both halves in constant time to keep the
// comparison from leaking the expected values through timing.
func credentialsMatch(gotUser, gotPass, wantUser, wantPass string) bool {
	userOK := subtle.ConstantTimeCompare([]byte(gotUser), []byte(wantUser)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(gotPass), []byte(wantPass)) == 1
	return userOK && passOK
}

// abortUnauthorized sets the Basic challenge (before the status, so it is
// flushed with the 401) and OCI-shaped error body clients expect.
func abortUnauthorized(c *gin.Context, message string) {
	c.Header("WWW-Authenticate", `Basic realm="`+authRealm+`"`)
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
		"errors": []gin.H{{
			"code":    "UNAUTHORIZED",
			"message": message,
		}},
	})
}
