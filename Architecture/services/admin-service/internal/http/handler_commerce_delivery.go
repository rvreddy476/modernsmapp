package http

import (
	"net/http"

	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Commerce validates document ownership; this proxy additionally records the
// admin view before sending bytes, with the same limits as other KYC views.
func (h *Handler) viewCommerceDeliveryDocument(p product) gin.HandlerFunc {
	return func(c *gin.Context) {
		profile, err := uuid.Parse(c.Param("profileId"))
		document, docErr := uuid.Parse(c.Param("documentId"))
		if err != nil || docErr != nil || profile == uuid.Nil || document == uuid.Nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, 400, CodeInvalidPathParam, "Invalid document or profile", nil)
			return
		}
		permission, ok := kycPermission(c)
		if !ok {
			return
		}
		info := auditFrom(c)
		info.targetType, info.targetID = "commerce_delivery_partner", profile.String()
		info.set("document_id", document.String())
		h.relayDocumentImage(c, p, info, service.ProductRequest{
			Method:     http.MethodGet,
			Path:       "/delivery/profiles/" + profile.String() + "/documents/" + document.String() + "/image",
			Permission: permission, Actor: actorFrom(c),
		}, kycImageTypes, func() {
			api.ErrorWithContext(c.Request.Context(), c.Writer, 404, CodeDocumentNotFound, "Document not found", nil)
		}, "commerce delivery document stream ended early", "profile_id", profile, "document_id", document)
	}
}
