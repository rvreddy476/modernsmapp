package http

import (
	"errors"
	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Translate store sentinels at this seam, never reveal a driver's message.
func careRespond(c *gin.Context, status int, v any, e error) {
	if errors.Is(e, store.ErrNotFound) {
		e = apperr.New(404, apperr.CodeNotFound, "this record is not available")
	}
	if errors.Is(e, store.ErrConflict) {
		e = apperr.New(409, apperr.CodeConflict, "this action was already recorded or its limit has been reached")
	}
	if errors.Is(e, store.ErrStale) {
		e = apperr.New(409, apperr.CodeInvalidTransition, "the visit has changed; reload before continuing")
	}
	respond(c, status, v, e)
}
func careID(c *gin.Context, fn func(uuid.UUID, uuid.UUID) (any, error), status int) {
	u, ok := userID(c)
	if !ok {
		return
	}
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	v, e := fn(u, id)
	careRespond(c, status, v, e)
}
func careBody[In any](c *gin.Context, fn func(uuid.UUID, uuid.UUID, In) (any, error), status int) {
	u, ok := userID(c)
	if !ok {
		return
	}
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	var in In
	if !bindJSON(c, &in) {
		return
	}
	v, e := fn(u, id, in)
	careRespond(c, status, v, e)
}
func (h *Handler) registerAftercareRoutes(user *gin.RouterGroup) {
	user.DELETE("/bookings/:id/share", func(c *gin.Context) {
		careID(c, func(u, id uuid.UUID) (any, error) { return nil, h.svc.RevokeVisitShare(c.Request.Context(), u, id) }, 204)
	})
	user.GET("/bookings/:id/rework", func(c *gin.Context) {
		careID(c, func(u, id uuid.UUID) (any, error) {
			items, e := h.svc.VisitReworks(c.Request.Context(), u, id)
			if items == nil {
				items = []model.ReworkRequest{}
			}
			return model.List[model.ReworkRequest]{Items: items}, e
		}, 200)
	})
	user.POST("/bookings/:id/rework", func(c *gin.Context) {
		careBody(c, func(u, id uuid.UUID, in model.ReworkInput) (any, error) {
			return h.svc.RequestVisitRework(c.Request.Context(), u, id, in)
		}, 201)
	})
	for _, prefix := range []string{"/bookings/:id", "/pro/jobs/:id"} {
		kind := "customer"
		if prefix == "/pro/jobs/:id" {
			kind = "pro"
		}
		user.POST(prefix+"/rating", func(c *gin.Context) {
			careBody(c, func(u, id uuid.UUID, in model.RatingInput) (any, error) {
				return h.svc.RateVisit(c.Request.Context(), u, id, kind, in)
			}, 201)
		})
		user.GET(prefix+"/messages", func(c *gin.Context) {
			careID(c, func(u, id uuid.UUID) (any, error) {
				return h.svc.VisitMessages(c.Request.Context(), u, id, kind, c.Query("cursor"))
			}, 200)
		})
		user.POST(prefix+"/messages", func(c *gin.Context) {
			careBody(c, func(u, id uuid.UUID, in model.MessageInput) (any, error) {
				return h.svc.SendVisitMessage(c.Request.Context(), u, id, kind, in)
			}, 201)
		})
		user.POST(prefix+"/messages/:msgId/read", func(c *gin.Context) {
			careID(c, func(u, id uuid.UUID) (any, error) {
				msg, ok := uuidParam(c, "msgId")
				if !ok {
					return nil, apperr.Invalid("msgId", "invalid message id")
				}
				return nil, h.svc.ReadVisitMessage(c.Request.Context(), u, id, msg)
			}, 200)
		})
		user.POST(prefix+"/sos", func(c *gin.Context) {
			careBody(c, func(u, id uuid.UUID, in model.SOSInput) (any, error) {
				return h.svc.VisitSOS(c.Request.Context(), u, id, kind, false, in)
			}, 201)
		})
	}
	user.POST("/pro/jobs/:id/unsafe-exit", func(c *gin.Context) {
		careBody(c, func(u, id uuid.UUID, in model.SOSInput) (any, error) {
			return h.svc.VisitSOS(c.Request.Context(), u, id, "pro", true, in)
		}, 201)
	})
	user.POST("/bookings/:id/share", func(c *gin.Context) {
		careID(c, func(u, id uuid.UUID) (any, error) { return h.svc.ShareVisit(c.Request.Context(), u, id) }, 201)
	})
	user.GET("/trusted-contact", func(c *gin.Context) {
		u, ok := userID(c)
		if !ok {
			return
		}
		v, e := h.svc.TrustedContact(c.Request.Context(), u)
		careRespond(c, 200, v, e)
	})
	user.PUT("/trusted-contact", func(c *gin.Context) {
		u, ok := userID(c)
		if !ok {
			return
		}
		var in model.TrustedContactInput
		if !bindJSON(c, &in) {
			return
		}
		v, e := h.svc.SaveTrustedContact(c.Request.Context(), u, in)
		careRespond(c, 200, v, e)
	})
	user.GET("/tickets", func(c *gin.Context) {
		u, ok := userID(c)
		if !ok {
			return
		}
		v, e := list(h.svc.VisitTickets(c.Request.Context(), u))
		careRespond(c, 200, v, e)
	})
	user.GET("/tickets/:id", func(c *gin.Context) {
		careID(c, func(u, id uuid.UUID) (any, error) { return h.svc.VisitTicket(c.Request.Context(), u, id) }, 200)
	})
	user.POST("/tickets", func(c *gin.Context) {
		u, ok := userID(c)
		if !ok {
			return
		}
		var in model.TicketInput
		if !bindJSON(c, &in) {
			return
		}
		v, e := h.svc.OpenVisitTicket(c.Request.Context(), u, in)
		careRespond(c, 201, v, e)
	})
	user.GET("/pro/earnings", func(c *gin.Context) {
		u, ok := userID(c)
		if !ok {
			return
		}
		v, e := h.svc.VisitEarnings(c.Request.Context(), u, c.Query("from"), c.Query("to"))
		careRespond(c, 200, v, e)
	})
}
