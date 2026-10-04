package http

import (
	"net/http"

	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Dispatch, presence and realtime (A4): the professional's duty, location,
// offers and jobs, the realtime token routes, and the admin redispatch.
// Every /pro route acts on the caller's own professional.

func (h *Handler) registerDispatchRoutes(user *gin.RouterGroup) {
	user.POST("/realtime/token", h.customerRealtimeToken)

	user.GET("/pro/me/duty", h.proDuty)
	user.POST("/pro/duty/on", h.proDutyOn)
	user.POST("/pro/duty/off", h.proDutyOff)
	user.POST("/pro/location", h.proLocation)
	user.GET("/pro/zones", h.proZones)
	user.GET("/pro/me/skills", h.proMySkills)
	user.GET("/pro/me/area", h.proMyArea)
	user.GET("/pro/me/bank", h.proMyBank)
	user.GET("/pro/me/documents", h.proMyDocuments)

	user.GET("/pro/offers", h.proOffers)
	user.GET("/pro/offers/:id", h.proOffer)
	user.POST("/pro/offers/:id/accept", h.proAcceptOffer)
	user.POST("/pro/offers/:id/decline", h.proDeclineOffer)
	user.GET("/pro/jobs", h.proJobs)
	user.GET("/pro/jobs/:id", h.proJob)
	user.POST("/pro/jobs/:id/cancel", h.proCancelJob)
	user.POST("/pro/realtime/token", h.proRealtimeToken)
}

// dispatchAdminRoutes: ops re-run dispatch (never pick a professional).
func (h *Handler) dispatchAdminRoutes() []adminRoute {
	return []adminRoute{
		{http.MethodPost, "/bookings/:id/redispatch", PermBookingsRedispatch, h.adminRedispatch},
	}
}

// proGet runs fn with the caller.
func proGet[Out any](c *gin.Context, fn func(uid uuid.UUID) (Out, error)) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	v, err := fn(uid)
	respond(c, http.StatusOK, v, err)
}

// proWithID runs fn with the caller and the :id path parameter.
func proWithID[Out any](c *gin.Context, status int, fn func(uid, id uuid.UUID) (Out, error)) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	v, err := fn(uid, id)
	respond(c, status, v, err)
}

func (h *Handler) customerRealtimeToken(c *gin.Context) {
	proBody(c, http.StatusOK, func(uid uuid.UUID, in model.RealtimeTokenRequest) (*model.RealtimeToken, error) {
		return h.svc.CustomerRealtimeToken(c.Request.Context(), uid, in)
	})
}

func (h *Handler) proRealtimeToken(c *gin.Context) {
	proGet(c, func(uid uuid.UUID) (*model.RealtimeToken, error) {
		return h.svc.ProRealtimeToken(c.Request.Context(), uid)
	})
}

func (h *Handler) proDuty(c *gin.Context) {
	proGet(c, func(uid uuid.UUID) (*model.DutyState, error) { return h.svc.ProDutyState(c.Request.Context(), uid) })
}

// proDutyOn takes an optional LocationInput body (an empty body is no fix).
func (h *Handler) proDutyOn(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	var in *model.LocationInput
	if c.Request.ContentLength != 0 {
		in = &model.LocationInput{}
		if !bindJSON(c, in) {
			return
		}
	}
	v, err := h.svc.ProDutyOn(c.Request.Context(), uid, in)
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) proDutyOff(c *gin.Context) {
	proGet(c, func(uid uuid.UUID) (*model.DutyState, error) { return h.svc.ProDutyOff(c.Request.Context(), uid) })
}

func (h *Handler) proLocation(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	var in model.LocationInput
	if !bindJSON(c, &in) {
		return
	}
	if err := h.svc.ProLocation(c.Request.Context(), uid, in); err != nil {
		writeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) proZones(c *gin.Context) {
	proGet(c, func(uid uuid.UUID) (model.List[model.ProZone], error) {
		return list(h.svc.ProZones(c.Request.Context(), uid))
	})
}

func (h *Handler) proMySkills(c *gin.Context) {
	proGet(c, func(uid uuid.UUID) (model.List[model.ProSkill], error) {
		return list(h.svc.ProMySkills(c.Request.Context(), uid))
	})
}

func (h *Handler) proMyArea(c *gin.Context) {
	proGet(c, func(uid uuid.UUID) (*model.ProArea, error) { return h.svc.ProMyArea(c.Request.Context(), uid) })
}

func (h *Handler) proMyBank(c *gin.Context) {
	proGet(c, func(uid uuid.UUID) (*model.PayoutAccount, error) { return h.svc.ProMyBank(c.Request.Context(), uid) })
}

func (h *Handler) proMyDocuments(c *gin.Context) {
	proGet(c, func(uid uuid.UUID) (model.List[model.ProDocument], error) {
		return list(h.svc.ProMyDocuments(c.Request.Context(), uid))
	})
}

func (h *Handler) proOffers(c *gin.Context) {
	proGet(c, func(uid uuid.UUID) (model.List[model.Offer], error) {
		return list(h.svc.ProOffers(c.Request.Context(), uid))
	})
}

func (h *Handler) proOffer(c *gin.Context) {
	proWithID(c, http.StatusOK, func(uid, id uuid.UUID) (*model.Offer, error) {
		return h.svc.ProOffer(c.Request.Context(), uid, id)
	})
}

func (h *Handler) proAcceptOffer(c *gin.Context) {
	proWithID(c, http.StatusOK, func(uid, id uuid.UUID) (*model.ProJob, error) {
		return h.svc.AcceptOffer(c.Request.Context(), uid, id)
	})
}

// proDeclineOffer takes an optional {"reason": ...} body.
func (h *Handler) proDeclineOffer(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	var in model.DeclineInput
	if c.Request.ContentLength != 0 && !bindJSON(c, &in) {
		return
	}
	if err := h.svc.DeclineOffer(c.Request.Context(), uid, id, in); err != nil {
		writeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) proJobs(c *gin.Context) {
	limit, aerr := service.ParseLimit(c.Query("limit"))
	if aerr != nil {
		writeErr(c, aerr)
		return
	}
	proGet(c, func(uid uuid.UUID) (*model.ProJobPage, error) {
		return h.svc.ProJobs(c.Request.Context(), uid, c.Query("status"), c.Query("date"), c.Query("cursor"), limit)
	})
}

func (h *Handler) proJob(c *gin.Context) {
	proWithID(c, http.StatusOK, func(uid, id uuid.UUID) (*model.ProJob, error) {
		return h.svc.ProJob(c.Request.Context(), uid, id)
	})
}

func (h *Handler) proCancelJob(c *gin.Context) {
	uid, ok := userID(c)
	if !ok {
		return
	}
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	var in model.ProCancelInput
	if !bindJSON(c, &in) {
		return
	}
	if err := h.svc.ProCancelJob(c.Request.Context(), uid, id, in); err != nil {
		writeErr(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) adminRedispatch(c *gin.Context) {
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	var in model.AdminRedispatchInput
	if !bindJSON(c, &in) {
		return
	}
	v, err := h.svc.AdminRedispatch(c.Request.Context(), adminActor(c), id, in)
	respond(c, http.StatusOK, v, err)
}
