package http

import (
	"net/http"
	"strings"

	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// CatalogueRoutes is the attribute-authoring surface the console's catalogue
// editor calls under /v1/admin/commerce/catalogue, one declared route per
// commerce token route. It used to be a key-authenticated catch-all proxy with
// an allowlist; explicit routes let publishing carry its own step-up, and
// anything not listed is a plain 404 that never reaches commerce.
//
// Commerce admits every one of them — reads included — only with
// commerce:catalogue.edit, so that is the permission here too (reads used to
// be open to commerce:products.moderate).
var CatalogueRoutes = []productRoute{
	{method: http.MethodGet, path: "/attribute-definitions", operation: "catalogue.get"},
	{method: http.MethodPost, path: "/attribute-definitions", operation: "catalogue.post"},
	{method: http.MethodGet, path: "/attribute-definitions/:defId", operation: "catalogue.get"},
	{method: http.MethodPatch, path: "/attribute-definitions/:defId", operation: "catalogue.patch"},
	{method: http.MethodGet, path: "/attribute-definitions/:defId/impact", operation: "catalogue.get"},
	{method: http.MethodGet, path: "/attribute-definitions/:defId/enum-values", operation: "catalogue.get"},
	{method: http.MethodPost, path: "/attribute-definitions/:defId/enum-values", operation: "catalogue.post"},
	{method: http.MethodPut, path: "/attribute-definitions/:defId/enum-values/order", operation: "catalogue.put"},
	{method: http.MethodPatch, path: "/attribute-definitions/:defId/enum-values/:valueId", operation: "catalogue.patch"},
	{method: http.MethodGet, path: "/categories/:categoryId/attributes", operation: "catalogue.get"},
	{method: http.MethodPut, path: "/categories/:categoryId/attributes", operation: "catalogue.put"},
	{method: http.MethodPost, path: "/categories", operation: "catalogue.post"},
	{method: http.MethodPatch, path: "/categories/:categoryId", operation: "catalogue.patch"},
	{method: http.MethodGet, path: "/attribute-schema", operation: "catalogue.get"},
	{method: http.MethodPost, path: "/attribute-schema/publish", operation: "catalogue.publish", stepUp: true},
}

const cataloguePrefix = "/catalogue"

// RegisterCatalogueRoutes adds the catalogue editor's routes. The audit target
// is the resource (the first path segment) and the path below the catalogue.
func (h *Handler) RegisterCatalogueRoutes(r *gin.Engine) {
	p := h.commerceProduct()
	routes := make([]productRoute, len(CatalogueRoutes))
	for i, rt := range CatalogueRoutes {
		rt.permission = permCatalogueEdit
		rt.path = cataloguePrefix + rt.path
		rt.upstream = strings.TrimPrefix(rt.path, cataloguePrefix)
		routes[i] = rt
	}
	// Operations repeat across catalogue routes, so every one shares the one
	// forwarding handler below instead of the per-operation map.
	forward := func(c *gin.Context) {
		rt, ok := catalogueRoute(c.Request.Method, strings.TrimPrefix(c.FullPath(), p.prefix+cataloguePrefix))
		if !ok {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Unknown catalogue resource", nil)
			return
		}
		path, _, err := productPath(c, rt.path)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, CodeInvalidPathParam, "Invalid path parameter", nil)
			return
		}
		rest := strings.TrimPrefix(path, "/")
		head := rest
		if i := strings.IndexByte(head, '/'); i >= 0 {
			head = head[:i]
		}
		info := auditFrom(c)
		info.targetType, info.targetID = head, rest
		pr := service.ProductRequest{Method: rt.method, Path: path, RawQuery: c.Request.URL.RawQuery}
		if rt.method != http.MethodGet {
			raw, _, err := jsonBody(c)
			if err != nil {
				api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, CodeInvalidBody, "The request body must be JSON", nil)
				return
			}
			pr.RawBody = raw
			info.set("method", rt.method)
			info.set("query", c.Request.URL.RawQuery)
		}
		h.productCall(c, p, pr, false)
	}
	for _, rt := range routes {
		h.gate.Handle(r, rt.method, p.prefix+rt.path, rt.requirement(), forward)
	}
}

func catalogueRoute(method, path string) (productRoute, bool) {
	for _, rt := range CatalogueRoutes {
		if rt.method == method && rt.path == path {
			return rt, true
		}
	}
	return productRoute{}, false
}
