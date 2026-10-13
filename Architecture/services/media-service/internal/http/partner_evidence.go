package http

import (
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"net/http"
	"strings"
)

const PartnerEvidenceOperation = "media:partner-evidence.read"

// Unlike legacy image reads this route NEVER accepts the internal key alone.
func (h *Handler) RegisterPartnerEvidence(r *gin.Engine, kid, pub string) error {
	verifier := servicetoken.NewVerifier("media")
	if pub != "" {
		if err := verifier.RegisterBase64("admin-service", kid, pub, []string{PartnerEvidenceOperation}, nil); err != nil {
			return err
		}
	}
	r.GET("/v1/media/admin/evidence/:mediaId", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store, private")
		c.Header("X-Content-Type-Options", "nosniff")
		token := strings.TrimPrefix(c.GetHeader("X-Service-Authorization"), "Bearer ")
		v, err := verifier.Verify(token, PartnerEvidenceOperation, "")
		if err != nil || v.Issuer != "admin-service" {
			c.Status(http.StatusForbidden)
			return
		}
		actor, err := uuid.Parse(v.Actor)
		if err != nil || actor == uuid.Nil {
			c.Status(http.StatusForbidden)
			return
		}
		id, e1 := uuid.Parse(c.Param("mediaId"))
		owner, e2 := uuid.Parse(c.Query("owner_id"))
		if e1 != nil || e2 != nil || id == uuid.Nil || owner == uuid.Nil {
			c.Status(http.StatusNotFound)
			return
		}
		out, err := h.svc.ImageBytesReader().PartnerEvidence(c.Request.Context(), id, owner, c.Query("kind"))
		if err != nil || out == nil {
			c.Status(http.StatusNotFound)
			return
		}
		c.Data(http.StatusOK, out.ContentType, out.Bytes)
	})
	return nil
}
