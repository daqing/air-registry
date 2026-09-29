package config

import (
	"path/filepath"

	"github.com/gin-gonic/gin"

	"github.com/daqing/air-registry/app/assets"
	"github.com/daqing/airway/lib/jsbuild"
	"github.com/daqing/airway/lib/openapi"
	"github.com/daqing/airway/lib/utils"

	"github.com/daqing/air-registry/app/api/health_api"
	"github.com/daqing/air-registry/app/api/home_api"
	"github.com/daqing/air-registry/app/api/openapi_api"
	"github.com/daqing/air-registry/app/api/storage_api"
	"github.com/daqing/air-registry/app/api/v2_api"
	"github.com/daqing/air-registry/app/services/blobstore"
	"github.com/daqing/air-registry/app/services/uploads"
	"github.com/daqing/airway/app/websocket"
	"github.com/daqing/airway/lib/plugin"
)

// The openapi:generate command enumerates the routes of the binary it runs
// in; config owns that table, so it registers itself as the route source.
func init() {
	openapi.RegisterRouteSource(Routes)
}

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

	registryStore := blobstore.New(blobstore.DefaultRoot())
	registryAPI := &v2_api.Handler{
		Blobs:   registryStore,
		Uploads: uploads.NewManager(filepath.Join(registryStore.Root, "uploads")),
	}
	registryAPI.Routes(r)

	assetRoutes(r)
	websocketRoutes(r)
	apiGroupRoutes(r)
	openapiRoutes(r)

	plugin.MountAll(r)
}

// HealthRoutes registers the internal health-check route. It stays reachable at
// the unprefixed root (for load-balancer probes) even when the public routes
// are served under a URL_PREFIX.
func HealthRoutes(r *gin.Engine) {
	health_api.Routes(r)
}

func apiGroupRoutes(r *gin.Engine) {
	v1 := r.Group("/api/v1")
	{
		storage_api.Routes(v1)
	}
}

func websocketRoutes(r *gin.Engine) {
	r.GET("/ws", websocket.Conn)
	r.POST("/ws/publish", websocket.Publish)
}

// openapiRoutes serves the generated OpenAPI document at /openapi.json.
func openapiRoutes(r *gin.Engine) {
	openapi_api.Routes(r)
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
