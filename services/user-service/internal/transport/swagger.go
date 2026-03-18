package transport

import (
	_ "embed"
	"fmt"
	"net/http"
)

//go:embed docs/swagger.json
var swaggerJSON []byte

// SwaggerHandler returns an http.Handler that serves:
//
//	GET /swagger/swagger.json  → raw OpenAPI spec
//	GET /swagger/              → Swagger UI (CDN)
func SwaggerHandler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/swagger/swagger.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(swaggerJSON)
	})

	mux.HandleFunc("/swagger/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, swaggerUI)
	})

	return mux
}

const swaggerUI = `<!DOCTYPE html>
<html>
<head>
  <title>Banka API – Swagger UI</title>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <link rel="stylesheet" type="text/css" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
<script>
  window.onload = function() {
    SwaggerUIBundle({
      url: "/swagger/swagger.json",
      dom_id: '#swagger-ui',
      presets: [SwaggerUIBundle.presets.apis, SwaggerUIBundle.SwaggerUIStandalonePreset],
      layout: "BaseLayout",
      deepLinking: true,
    })
  }
</script>
</body>
</html>`
