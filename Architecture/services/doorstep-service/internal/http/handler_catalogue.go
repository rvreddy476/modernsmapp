package http

import (
	"net/http"

	"github.com/atpost/doorstep-service/internal/model"
	"github.com/gin-gonic/gin"
)

// GET /v1/doorstep/catalogue?city=HYD
func (h *Handler) getCatalogue(c *gin.Context) {
	v, err := h.svc.Catalogue(c.Request.Context(), c.Query("city"))
	respond(c, http.StatusOK, v, err)
}

// GET /v1/doorstep/categories/:slug?city=HYD
func (h *Handler) getCategory(c *gin.Context) {
	v, err := h.svc.Category(c.Request.Context(), c.Query("city"), c.Param("slug"))
	respond(c, http.StatusOK, v, err)
}

// GET /v1/doorstep/services/:id?city=HYD
func (h *Handler) getService(c *gin.Context) {
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	v, err := h.svc.ServiceDetail(c.Request.Context(), c.Query("city"), id)
	respond(c, http.StatusOK, v, err)
}

// POST /v1/doorstep/serviceability {lat, lng}
func (h *Handler) postServiceability(c *gin.Context) {
	var req model.ServiceabilityRequest
	if !bindJSON(c, &req) {
		return
	}
	v, err := h.svc.Serviceability(c.Request.Context(), req)
	respond(c, http.StatusOK, v, err)
}

// POST /v1/doorstep/quotes
func (h *Handler) postQuote(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	var req model.QuoteRequest
	if !bindJSON(c, &req) {
		return
	}
	v, err := h.svc.CreateQuote(c.Request.Context(), uid, req)
	respond(c, http.StatusCreated, v, err)
}

// GET /v1/doorstep/quotes/:id
func (h *Handler) getQuote(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	v, err := h.svc.Quote(c.Request.Context(), uid, id)
	respond(c, http.StatusOK, v, err)
}
