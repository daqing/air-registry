package repos

import "encoding/json"

// islandScript wraps the marshaled props in their JSON script tag. The tag
// is built in Go because templ treats <script> bodies as raw text, so the
// props cannot be interpolated from inside the template.
func islandScript(props string) string {
	return `<script type="application/json" id="island-data-repos">` + props + `</script>`
}

// islandProps marshals the pagination island props embedded in the script
// tag above.
func islandProps(page, pageCount int, base string) string {
	data, err := json.Marshal(map[string]any{
		"page":      page,
		"pageCount": pageCount,
		"base":      base,
	})
	if err != nil {
		return "{}"
	}
	return string(data)
}
