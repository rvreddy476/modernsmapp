package http

import (
	"context"
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
// permission split matches what the actions do: reading the taxonomy is
// moderation work (commerce:products.moderate), changing it needs
// commerce:catalogue.edit.
func (h *Handler) RegisterCatalogueRoutes(r *gin.Engine, cc *service.CommerceClient) {
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

		upstreamPath := "/v1/commerce/internal/" + rest
		if c.Request.Method != http.MethodGet {
			h.forwardCommerceWrite(c, commerceWrite{
				targetType: head,
				targetID:   rest,
				payload: map[string]any{
					"method": c.Request.Method,
					"path":   upstreamPath,
					"query":  c.Request.URL.RawQuery,
				},
				call: func(ctx context.Context, actorID string) ([]byte, int, error) {
					return cc.RawProxy(ctx, c.Request.Method, upstreamPath,
						c.Request.URL.RawQuery, actorID, c.Request.Body)
				},
			})
			return
		}

		info := auditFrom(c)
		info.targetType, info.targetID = head, rest
		data, status, err := cc.RawProxy(c.Request.Context(), c.Request.Method,
			upstreamPath, c.Request.URL.RawQuery, "", c.Request.Body)
		writeUpstream(c, info, data, status, err)
	}

	const path = "/v1/admin/commerce/catalogue/*rest"
	h.gate.Handle(r, http.MethodGet, path,
		Requirement{Operation: "catalogue.get", Permission: permProductsModerate}, proxy)
	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodPut} {
		h.gate.Handle(r, method, path,
			Requirement{Operation: "catalogue." + strings.ToLower(method), Permission: permCatalogueEdit}, proxy)
	}
}
