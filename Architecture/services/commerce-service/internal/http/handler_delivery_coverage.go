package http

import (
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *Handler) GetDeliveryCoverage(c *gin.Context) {
	rows, err := h.svc.DeliveryAreas(c.Request.Context(), true)
	if err != nil {
		deliveryError(c, err)
		return
	}
	api.JSON(c.Writer, 200, rows, nil)
}
func (h *Handler) AdminDeliveryAreas(c *gin.Context) {
	rows, err := h.svc.DeliveryAreas(c.Request.Context(), false)
	if err != nil {
		deliveryError(c, err)
		return
	}
	api.JSON(c.Writer, 200, rows, nil)
}
func (h *Handler) AdminSaveDeliveryArea(c *gin.Context) {
	actor, ok := tokenActor(c)
	if !ok {
		return
	}
	var area postgres.DeliveryArea
	if c.ShouldBindJSON(&area) != nil {
		deliveryError(c, postgres.ErrDeliveryConfigInvalid)
		return
	}
	// URL identity is authoritative; POST is creation only.
	if c.Param("areaId") != "" {
		id, ok := parseUUID(c, "areaId")
		if !ok {
			return
		}
		area.ID = id
	} else {
		area.ID = uuid.Nil
	}
	result, err := h.svc.SaveDeliveryArea(c.Request.Context(), actor, area)
	if err != nil {
		deliveryError(c, err)
		return
	}
	api.JSON(c.Writer, 200, result, nil)
}
func (h *Handler) AdminDeliveryAreaSellers(c *gin.Context) {
	id, ok := parseUUID(c, "areaId")
	if !ok {
		return
	}
	rows, err := h.svc.DeliveryAreaSellers(c.Request.Context(), id)
	if err != nil {
		deliveryError(c, err)
		return
	}
	api.JSON(c.Writer, 200, rows, nil)
}
func (h *Handler) AdminSaveDeliveryAreaSeller(c *gin.Context) {
	actor, ok := tokenActor(c)
	if !ok {
		return
	}
	id, ok := parseUUID(c, "areaId")
	if !ok {
		return
	}
	seller, ok := parseUUID(c, "sellerId")
	if !ok {
		return
	}
	var area postgres.DeliveryAreaSeller
	if c.ShouldBindJSON(&area) != nil {
		deliveryError(c, postgres.ErrDeliveryConfigInvalid)
		return
	}
	area.AreaID, area.SellerID = id, seller
	if err := h.svc.SaveDeliveryAreaSeller(c.Request.Context(), actor, area); err != nil {
		deliveryError(c, err)
		return
	}
	h.AdminDeliveryAreaSellers(c)
}
