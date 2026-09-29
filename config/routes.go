package config

import (
	"path/filepath"

	"github.com/gin-gonic/gin"

	"github.com/daqing/air-registry/app/assets"
	"github.com/daqing/airway/lib/jsbuild"
	"github.com/daqing/airway/lib/utils"

	"github.com/daqing/air-registry/app/api/health_api"
	"github.com/daqing/air-registry/app/api/home_api"
	"github.com/daqing/air-registry/app/api/v2_api"
	"github.com/daqing/air-registry/app/middlewares"
	"github.com/daqing/air-registry/app/services/blobstore"
	"github.com/daqing/air-registry/app/services/registrycfg"
	"github.com/daqing/air-registry/app/services/uploads"
	"github.com/daqing/airway/app/websocket"
	"github.com/daqing/airway/lib/plugin"
)

// Routes registers every route — public and internal — at the root paths. This
// is the full router used when the app is served without a URL_PREFIX.
func Routes(r *gin.Engine) {
	PublicRoutes(r)
	HealthRoutes(r)
}

// PublicRoutes registers the user-facing routes: the home page, the WebSocket,
// and the API. When a URL_PREFIX is configured these answer only under the
// prefix; see App.Handler.
func PublicRoutes(r *gin.Engine) {
	r.GET("/", home_api.IndexAction)
	r.GET("/repos", home_api.ReposAction)
	r.GET("/repos/*path", home_api.RepoAction)

	registryStore := blobstore.New(registrycfg.DataDir())
	registryAPI := &v2_api.Handler{
		Blobs:   registryStore,
		Uploads: uploads.NewManager(filepath.Join(registryStore.Root, "uploads")),
	}
	// The empty-prefix group attaches the auth middleware to the /v2 routes
	// without changing their paths.
	registryAPI.Routes(r.Group("", middlewares.RegistryAuth()))

	assetRoutes(r)
	websocketRoutes(r)

	plugin.MountAll(r)
}

// HealthRoutes registers the internal health-check route. It stays reachable at
// the unprefixed root (for load-balancer probes) even when the public routes
// are served under a URL_PREFIX.
func HealthRoutes(r *gin.Engine) {
	health_api.Routes(r)
}

func websocketRoutes(r *gin.Engine) {
	r.GET("/ws", websocket.Conn)
	r.POST("/ws/publish", websocket.Publish)
}

// assetRoutes serves the frontend bundle. In local development an in-memory
// esbuild server (started from main when AIRWAY_ENV=local) serves rebuilt
// output directly; otherwise the embedded production bundle answers.
func assetRoutes(r *gin.Engine) {
	if utils.AppConfig().IsLocal {
		if dev := jsbuild.Default(); dev != nil {
			r.GET("/assets/*path", gin.WrapH(dev.Handler()))
			return
		}
	}
	r.GET("/assets/*path", gin.WrapH(assets.Handler()))
}
