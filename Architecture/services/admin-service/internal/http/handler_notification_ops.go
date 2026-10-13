package http

import (
	"github.com/atpost/admin-service/internal/service"
	"github.com/gin-gonic/gin"
	"net/http"
)

func (h *Handler) WithNotifications(c *service.ProductClient) *Handler { h.notifications = c; return h }
func (h *Handler) RegisterNotificationOpsRoutes(r *gin.Engine) {
	h.registerProduct(r, product{app: "platform", label: "Partner notifications", prefix: "/v1/admin/notifications", client: h.notifications}, []productRoute{
		{method: http.MethodGet, path: "/partner-jobs", operation: "notifications.partner_jobs", permission: "platform:audit.read"},
		{method: http.MethodPost, path: "/partner-jobs/:jobId/retry", operation: "notifications.partner_retry", permission: "platform:roles.manage", stepUp: true, targetType: "partner_notification_job"},
	}, nil)
}
