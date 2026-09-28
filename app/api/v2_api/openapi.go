package v2_api

import "github.com/daqing/airway/lib/openapi"

func init() {
	openapi.Get("/v2/", func(o *openapi.Operation) {
		o.Summary("OCI version check").Tag("v2").OK()
	})
}
