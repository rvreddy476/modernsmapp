package http

import (
	"net/http"
	"strings"

	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// allowedCataloguePrefixes is the whole of what the authoring console may
// reach through this proxy.
//
// An unrestricted passthrough to /v1/commerce/internal/* would quietly open a
// second door onto seller approval and payout routes, gated by whatever this
// group happens to require rather than by the gating those routes were given.
// The allowlist keeps the door exactly as wide as the catalogue.
var allowedCataloguePrefixes = map[string]struct{}{
	"attribute-definitions": {},
	"attribute-schema":      {},
	"categories":            {},
}

// RegisterCatalogueRoutes proxies the attribute-authoring surface, which lives
// behind commerce-service's internal-service key.
//
// A browser cannot hold that key, so without this the console the founder
// authors the taxonomy in could not call a single one of those routes. The
// scope split matches what the actions do: reading the taxonomy is moderator
// work, changing it is not.
func (h *Handler) RegisterCatalogueRoutes(r *gin.Engine, cc *service.CommerceClient) {
	g := r.Group("/v1/admin/commerce/catalogue")

	proxy := func(c *gin.Context) {
		rest := strings.TrimPrefix(c.Param("rest"), "/")
		if rest == "" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound,
				"NOT_FOUND", "No catalogue path given", nil)
			return
		}
		// Defence in depth against a traversal that would climb out of the
		// internal namespace and hit an unrelated route with the key attached.
		if strings.Contains(rest, "..") {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
				"INVALID_PATH", "Path traversal is not allowed", nil)
			return
		}
		head := rest
		if i := strings.IndexByte(head, '/'); i >= 0 {
			head = head[:i]
		}
		if _, ok := allowedCataloguePrefixes[head]; !ok {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound,
				"NOT_FOUND", "Unknown catalogue resource", nil)
			return
		}

		data, status, err := cc.RawProxy(c.Request.Context(), c.Request.Method,
			"/v1/commerce/internal/"+rest, c.Request.URL.RawQuery, c.Request.Body)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadGateway,
				"UPSTREAM_ERROR", err.Error(), nil)
			return
		}
		if len(data) == 0 {
			c.Status(status)
			return
		}
		c.Data(status, "application/json", data)
	}

	g.GET("/*rest", requireScopeFn("moderator", "admin", "superadmin"), proxy)
	for _, register := range []func(string, ...gin.HandlerFunc) gin.IRoutes{
		g.POST, g.PATCH, g.PUT,
	} {
		register("/*rest", requireScopeFn("admin", "superadmin"), proxy)
	}
}
