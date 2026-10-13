package http

import (
	"errors"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"net/http"
	"strings"
)

// Registered before internal-key middleware: only a scoped, signed admin-service
// token is authority. Edge/internal-key/user headers alone cannot reach it.
func (h *Handler) RegisterPartnerNotificationAdmin(r *gin.Engine, v *servicetoken.Verifier) {
	gate := func(permission string) gin.HandlerFunc {
		return func(c *gin.Context) {
			raw := strings.TrimSpace(strings.TrimPrefix(c.GetHeader("X-Service-Authorization"), "Bearer "))
			if v == nil || raw == "" {
				c.AbortWithStatus(http.StatusUnauthorized)
				return
			}
			token, err := v.Verify(raw, permission, "")
			if err != nil || token.Issuer != "admin-service" {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			actor, err := uuid.Parse(token.Actor)
			if err != nil || actor == uuid.Nil {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			c.Set("notification_admin_actor", actor)
			c.Next()
		}
	}
	g := r.Group("/v1/notifications/internal/admin")
	g.GET("/partner-jobs", gate("platform:audit.read"), func(c *gin.Context) {
		rows, err := h.svc.PartnerNotificationFailures(c.Request.Context())
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, 503, "NOTIFICATION_QUEUE_UNAVAILABLE", "Notification delivery status is temporarily unavailable", nil)
			return
		}
		api.JSON(c.Writer, 200, rows, nil)
	})
	g.POST("/partner-jobs/:jobId/retry", gate("platform:roles.manage"), func(c *gin.Context) {
		id, err := uuid.Parse(c.Param("jobId"))
		var b struct {
			Reason string `json:"reason"`
		}
		if err != nil || c.ShouldBindJSON(&b) != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, 400, "INVALID_RETRY", "Enter a valid notification and reason", nil)
			return
		}
		actor := c.MustGet("notification_admin_actor").(uuid.UUID)
		err = h.svc.RetryPartnerNotification(c.Request.Context(), id, actor, b.Reason)
		if errors.Is(err, postgres.ErrNotificationRetryConflict) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, 409, "NOTIFICATION_RETRY_CONFLICT", "Only failed account/job updates can be retried. Offers cannot be replayed.", nil)
			return
		}
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, 503, "NOTIFICATION_QUEUE_UNAVAILABLE", "Retry could not be saved", nil)
			return
		}
		api.JSON(c.Writer, 200, gin.H{"status": "pending"}, nil)
	})
}
