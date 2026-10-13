package http

import (
	"encoding/json"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/ownedmedia"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"net/http"
	"net/url"
	"strings"
)

func (h *Handler) WithEvidence(c *service.ProductClient) *Handler { h.evidence = c; return h }

// Resolve media through the owning module using the checked permission. A
// caller cannot supply a media id, owner, URL, or kind to this browser route.
func (h *Handler) viewPartnerEvidence(p product, parentSegment, parentField, docField string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store, private")
		id, err := uuid.Parse(c.Param(docField))
		if err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		path := "/documents/" + id.String() + "/evidence"
		if parentField != "" {
			parent, e := uuid.Parse(c.Param(parentField))
			if e != nil {
				c.Status(http.StatusBadRequest)
				return
			}
			path = "/" + parentSegment + "/" + parent.String() + path
		}
		perm, ok := kycPermission(c)
		if !ok {
			return
		}
		info := auditFrom(c)
		info.targetID = id.String()
		response, err := p.client.Do(productContext(c), service.ProductRequest{Method: http.MethodGet, Path: path, Permission: perm, Actor: actorFrom(c)})
		if err != nil {
			writeProduct(c, info, p.label, service.ProductResponse{}, err, false)
			return
		}
		if response.Status != http.StatusOK {
			writeProduct(c, info, p.label, response, nil, false)
			return
		}
		var env struct {
			Data ownedmedia.Reference `json:"data"`
		}
		if json.Unmarshal(response.Body, &env) != nil || env.Data.MediaID == uuid.Nil || env.Data.OwnerID == uuid.Nil || env.Data.Kind == "" {
			upstreamError(c, info, 0, "Evidence reference unavailable")
			return
		}
		ref := env.Data
		query := url.Values{"owner_id": {ref.OwnerID.String()}, "kind": {strings.ToLower(ref.Kind)}}
		types := map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true, "image/gif": true, "image/avif": true}
		if strings.EqualFold(ref.Kind, "introduction_video") {
			types = map[string]bool{"video/mp4": true, "video/webm": true, "video/quicktime": true}
		}
		h.relayDocumentImage(c, product{client: h.evidence, label: "Private evidence"}, info, service.ProductRequest{Method: http.MethodGet, Path: "/" + ref.MediaID.String(), Query: query, Permission: "media:partner-evidence.read", Actor: actorFrom(c)}, types, func() {
			api.ErrorWithContext(c.Request.Context(), c.Writer, 404, CodeDocumentNotFound, "Evidence is not available", nil)
		}, "private evidence stream ended early", "document_id", id.String())
	}
}
