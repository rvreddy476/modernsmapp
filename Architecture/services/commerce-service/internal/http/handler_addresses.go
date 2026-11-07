// Customer address route handlers: list, add, update, delete and set the
// default address. Moved out of handler.go unchanged.

package http

import (
	"net/http"
	"strings"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// ─── Address handlers ────────────────────────────────────────────

func (h *Handler) ListAddresses(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	addrs, err := h.svc.GetAddresses(c.Request.Context(), userID)
	if err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, addrs, nil)
}

type addAddressReq struct {
	AddressType string `json:"address_type"`

	// The contact's name, under either wire name.
	//
	// The column is `contact_name`, the read path returns `contact_name`, and
	// the Android client sends `contact_name`. Only this request struct said
	// `full_name`, and it was `binding:"required"` — so every address the app
	// tried to save was rejected with a 400 before it reached the service. A
	// new buyer could not add a delivery address, and therefore could not
	// check out at all.
	//
	// Both are accepted rather than renaming outright: an existing web caller
	// may still send `full_name`, and breaking it to fix the app would just
	// move the outage. `contactName()` resolves the pair, and neither field
	// carries `binding:"required"` because "required" on one of two aliases
	// rejects the request that supplies the other.
	ContactName string `json:"contact_name"`
	FullName    string `json:"full_name"`

	Phone        string  `json:"phone" binding:"required"`
	AddressLine1 string  `json:"address_line_1" binding:"required"`
	AddressLine2 *string `json:"address_line_2"`
	Landmark     *string `json:"landmark"`
	City         string  `json:"city" binding:"required"`
	State        string  `json:"state" binding:"required"`
	PostalCode   string  `json:"postal_code" binding:"required"`
	Country      string  `json:"country"`
	IsDefault    bool    `json:"is_default"`
}

// contactName resolves the two accepted wire names.
//
// A blank result is refused by the caller: the name is what a courier reads
// off the label, and an address without one cannot be delivered.
func (r addAddressReq) contactName() string {
	if n := strings.TrimSpace(r.ContactName); n != "" {
		return n
	}
	return strings.TrimSpace(r.FullName)
}

func (h *Handler) AddAddress(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req addAddressReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	name := req.contactName()
	if name == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY",
			"contact_name is required", nil)
		return
	}
	country := req.Country
	if country == "" {
		country = "IN"
	}
	addressType := req.AddressType
	if addressType == "" {
		addressType = "home"
	}
	addr := &postgres.CustomerAddress{
		UserID:       userID,
		AddressType:  addressType,
		ContactName:  name,
		Phone:        req.Phone,
		AddressLine1: req.AddressLine1,
		AddressLine2: req.AddressLine2,
		Landmark:     req.Landmark,
		City:         req.City,
		State:        req.State,
		PostalCode:   req.PostalCode,
		Country:      country,
		IsDefault:    req.IsDefault,
	}
	if err := h.svc.AddAddress(c.Request.Context(), addr); err != nil {
		handleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, addr, nil)
}

func (h *Handler) UpdateAddress(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	addrID, ok := parseUUID(c, "addressId")
	if !ok {
		return
	}
	var req addAddressReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	country := req.Country
	if country == "" {
		country = "IN"
	}
	addressType := req.AddressType
	if addressType == "" {
		addressType = "home"
	}
	addr := &postgres.CustomerAddress{
		AddressType: addressType, ContactName: req.contactName(), Phone: req.Phone,
		AddressLine1: req.AddressLine1, AddressLine2: req.AddressLine2,
		Landmark: req.Landmark, City: req.City, State: req.State,
		PostalCode: req.PostalCode, Country: country, IsDefault: req.IsDefault,
	}
	if err := h.svc.UpdateAddress(c.Request.Context(), addrID, userID, addr); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) DeleteAddress(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	addrID, ok := parseUUID(c, "addressId")
	if !ok {
		return
	}
	if err := h.svc.DeleteAddress(c.Request.Context(), addrID, userID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) SetDefaultAddress(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	addrID, ok := parseUUID(c, "addressId")
	if !ok {
		return
	}
	if err := h.svc.SetDefaultAddress(c.Request.Context(), addrID, userID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		return
	}
	c.Status(http.StatusNoContent)
}
