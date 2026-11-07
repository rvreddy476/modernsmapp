// API documentation routes: the OpenAPI spec and the Scalar docs page.
// Pure move out of handler.go; behaviour is unchanged.

package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Docs Routes
func (h *Handler) RegisterDocsRoutes(r *gin.Engine) {
	v1 := r.Group("/v1/auth")
	{
		v1.GET("/openapi.json", h.OpenAPISpec)
		v1.GET("/docs", h.ScalarDocs)
	}
}

func (h *Handler) OpenAPISpec(c *gin.Context) {
	c.File("./docs/openapi.json")
}

func (h *Handler) ScalarDocs(c *gin.Context) {
	html := `<!doctype html>
<html>
  <head>
    <title>Auth Service API</title>
    <meta charset="utf-8" />
    <meta
      name="viewport"
      content="width=device-width, initial-scale=1" />
  </head>
  <body>
    <script
      id="api-reference"
      data-url="./openapi.json"></script>
    <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
  </body>
</html>`
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}
