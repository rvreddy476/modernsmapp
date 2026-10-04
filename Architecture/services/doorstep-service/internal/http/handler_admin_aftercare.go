package http

import (
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"net/http"
)

func (h *Handler) aftercareAdminRoutes() []adminRoute {
	routes := []adminRoute{}
	for _, item := range []struct{ kind, perm string }{{"incidents", PermIncidentsRead}, {"tickets", PermTicketsAct}, {"ratings", PermRatingsModerate}, {"settlements", PermSettlementsRead}} {
		routes = append(routes, adminRoute{http.MethodGet, "/" + item.kind, item.perm, func(c *gin.Context) {
			v, e := list(h.svc.AdminCareList(c.Request.Context(), item.kind, c.Query("status"), c.Query("period_start")))
			careRespond(c, 200, v, e)
		}})
	}
	routes = append(routes,
		adminRoute{http.MethodGet, "/professionals/:id/tax-registration", PermProsApprove, func(c *gin.Context) {
			c.Header("Cache-Control", "no-store, private")
			adminCareID(c, func(id uuid.UUID) (any, error) { return h.svc.AdminTaxRegistration(c.Request.Context(), id) })
		}},
		adminRoute{http.MethodPost, "/professionals/:id/tax-registration", PermProsApprove, func(c *gin.Context) {
			c.Header("Cache-Control", "no-store, private")
			adminCareBody(c, func(id uuid.UUID, in service.TaxRegistrationInput) (any, error) {
				return h.svc.AdminSetTaxRegistration(c.Request.Context(), adminActor(c), id, in)
			})
		}},
		adminRoute{http.MethodPost, "/incidents/:id/acknowledge", PermIncidentsAct, func(c *gin.Context) {
			adminCareID(c, func(id uuid.UUID) (any, error) {
				return h.svc.AdminAcknowledgeIncident(c.Request.Context(), adminActor(c), id)
			})
		}},
		adminRoute{http.MethodPost, "/incidents/:id/resolve", PermIncidentsAct, func(c *gin.Context) {
			adminCareBody(c, func(id uuid.UUID, in service.IncidentResolution) (any, error) {
				return h.svc.AdminResolveVisitIncident(c.Request.Context(), adminActor(c), id, in)
			})
		}},
		adminRoute{http.MethodPost, "/tickets/:id/status", PermTicketsAct, func(c *gin.Context) {
			adminCareBody(c, func(id uuid.UUID, in service.TicketStatusInput) (any, error) {
				return h.svc.AdminTicketStatus(c.Request.Context(), adminActor(c), id, in)
			})
		}},
		adminRoute{http.MethodPost, "/ratings/:id/hide", PermRatingsModerate, func(c *gin.Context) {
			adminCareBody(c, func(id uuid.UUID, in model.ReasonInput) (any, error) {
				return h.svc.AdminHideVisitRating(c.Request.Context(), adminActor(c), id, in)
			})
		}},
	)
	return routes
}
func adminCareID(c *gin.Context, fn func(uuid.UUID) (any, error)) {
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	v, e := fn(id)
	careRespond(c, 200, v, e)
}
func adminCareBody[In any](c *gin.Context, fn func(uuid.UUID, In) (any, error)) {
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	var in In
	if !bindJSON(c, &in) {
		return
	}
	v, e := fn(id, in)
	careRespond(c, 200, v, e)
}
