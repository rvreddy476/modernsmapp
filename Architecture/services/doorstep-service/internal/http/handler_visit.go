package http

import (
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"net/http"
)

func (h *Handler) registerVisitRoutes(user *gin.RouterGroup) {
	user.DELETE("/pro/jobs/:id/extras/:extraId", func(c *gin.Context) {
		extra, ok := uuidParam(c, "extraId")
		if !ok {
			return
		}
		careID(c, func(u, id uuid.UUID) (any, error) {
			return nil, h.svc.ProWithdrawExtra(c.Request.Context(), u, id, extra)
		}, 204)
	})
	user.POST("/pro/jobs/:id/complete", func(c *gin.Context) {
		visitBody(c, 200, func(u, id uuid.UUID, in model.OTPInput) (*model.ProJob, error) {
			return h.svc.ProVisitComplete(c.Request.Context(), u, id, in)
		})
	})
	user.POST("/pro/jobs/:id/no-show", func(c *gin.Context) {
		proWithID(c, 200, func(u, id uuid.UUID) (*model.ProJob, error) { return h.svc.ProVisitNoShow(c.Request.Context(), u, id) })
	})
	user.GET("/pro/jobs/:id/extras/options", func(c *gin.Context) {
		proWithID(c, 200, func(u, id uuid.UUID) (model.List[model.ExtraOption], error) {
			return list(h.svc.ProExtraOptions(c.Request.Context(), u, id))
		})
	})
	user.GET("/pro/jobs/:id/extras", func(c *gin.Context) {
		proWithID(c, 200, func(u, id uuid.UUID) (model.List[model.Extra], error) {
			return list(h.svc.VisitExtras(c.Request.Context(), u, id, false))
		})
	})
	user.POST("/pro/jobs/:id/extras", func(c *gin.Context) {
		visitBody(c, 201, func(u, id uuid.UUID, in model.ExtraInput) (*model.Extra, error) {
			return h.svc.ProProposeExtra(c.Request.Context(), u, id, in)
		})
	})
	user.GET("/bookings/:id/extras", func(c *gin.Context) {
		proWithID(c, 200, func(u, id uuid.UUID) (model.List[model.Extra], error) {
			return list(h.svc.VisitExtras(c.Request.Context(), u, id, true))
		})
	})
	for _, choice := range []string{"approve", "decline"} {
		choice := choice
		user.POST("/bookings/:id/extras/:extraId/"+choice, func(c *gin.Context) {
			u, ok := userID(c)
			if !ok {
				return
			}
			id, ok := uuidParam(c, "id")
			if !ok {
				return
			}
			extra, ok := uuidParam(c, "extraId")
			if !ok {
				return
			}
			status := "approved"
			if choice == "decline" {
				status = "declined"
			}
			v, err := h.svc.DecideExtra(c.Request.Context(), u, id, extra, status)
			respond(c, 200, v, err)
		})
	}
	user.GET("/bookings/:id/extras-bill", func(c *gin.Context) {
		proWithID(c, 200, func(u, id uuid.UUID) (*model.ExtrasBill, error) {
			return h.svc.CustomerExtrasBill(c.Request.Context(), u, id)
		})
	})
	user.POST("/extras-bills/:id/payment/intent", func(c *gin.Context) {
		proWithID(c, 200, func(u, id uuid.UUID) (*model.PaymentIntent, error) {
			return h.svc.ExtrasPaymentIntent(c.Request.Context(), u, id)
		})
	})
	user.GET("/me/outstanding", func(c *gin.Context) {
		proGet(c, func(u uuid.UUID) (*model.Outstanding, error) { return h.svc.Outstanding(c.Request.Context(), u) })
	})
	user.POST("/pro/jobs/:id/en-route", func(c *gin.Context) {
		proWithID(c, 200, func(u, id uuid.UUID) (*model.ProJob, error) {
			return h.svc.ProVisitMove(c.Request.Context(), u, id, "en_route", nil)
		})
	})
	user.POST("/pro/jobs/:id/arrived", func(c *gin.Context) {
		visitBody(c, 200, func(u, id uuid.UUID, in model.LocationInput) (*model.ProJob, error) {
			return h.svc.ProVisitMove(c.Request.Context(), u, id, "arrived", &in)
		})
	})
	user.POST("/pro/jobs/:id/photos", func(c *gin.Context) {
		visitBody(c, 201, func(u, id uuid.UUID, in model.PhotoInput) (*model.Photo, error) {
			return h.svc.ProVisitPhoto(c.Request.Context(), u, id, in)
		})
	})
	user.POST("/pro/jobs/:id/start", func(c *gin.Context) {
		visitBody(c, 200, func(u, id uuid.UUID, in model.OTPInput) (*model.ProJob, error) {
			return h.svc.ProVisitStart(c.Request.Context(), u, id, in)
		})
	})
	user.POST("/pro/jobs/:id/finish", func(c *gin.Context) {
		proWithID(c, 200, func(u, id uuid.UUID) (*model.ProJob, error) { return h.svc.ProVisitFinish(c.Request.Context(), u, id) })
	})
	user.GET("/bookings/:id/photos/:mediaId", h.visitPhoto)
	user.GET("/pro/jobs/:id/photos/:mediaId", h.visitPhoto)
}

func visitBody[In, Out any](c *gin.Context, status int, fn func(uuid.UUID, uuid.UUID, In) (Out, error)) {
	uid, ok := userID(c)
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
	v, err := fn(uid, id, in)
	respond(c, status, v, err)
}

func (h *Handler) visitPhoto(c *gin.Context) {
	u, ok := userID(c)
	if !ok {
		return
	}
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	data, ct, err := h.svc.VisitPhotoBytes(c.Request.Context(), u, id, c.Param("mediaId"))
	if err != nil {
		writeErr(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, ct, data)
}
