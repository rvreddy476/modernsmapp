package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/doorstep-service/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Admin-internal catalogue and config handlers (admin-service tokens only;
// see adminRoutes for the permission of each). Lists answer {"items": [...]}.

func list[T any](items []T, err error) (model.List[T], error) {
	if items == nil {
		items = []T{}
	}
	return model.List[T]{Items: items}, err
}

// create decodes a body and calls fn with the admitted actor.
func adminCreate[In any, Out any](c *gin.Context, fn func(in In) (*Out, error)) {
	var in In
	if !bindJSON(c, &in) {
		return
	}
	v, err := fn(in)
	respond(c, http.StatusCreated, v, err)
}

func adminPatch[P any, Out any](c *gin.Context, fn func(id uuid.UUID, p P) (*Out, error)) {
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	var p P
	if !bindJSON(c, &p) {
		return
	}
	v, err := fn(id, p)
	respond(c, http.StatusOK, v, err)
}

// ---- cities ----

func (h *Handler) adminListCities(c *gin.Context) {
	v, err := list(h.svc.AdminListCities(c.Request.Context()))
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) adminCreateCity(c *gin.Context) {
	adminCreate(c, func(in model.AdminCityInput) (*model.AdminCity, error) {
		return h.svc.AdminCreateCity(c.Request.Context(), adminActor(c), in)
	})
}

func (h *Handler) adminUpdateCity(c *gin.Context) {
	var p model.AdminCityPatch
	if !bindJSON(c, &p) {
		return
	}
	v, err := h.svc.AdminUpdateCity(c.Request.Context(), adminActor(c), c.Param("code"), p)
	respond(c, http.StatusOK, v, err)
}

// ---- zones ----

func (h *Handler) adminListZones(c *gin.Context) {
	v, err := list(h.svc.AdminListZones(c.Request.Context(), c.Query("city")))
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) adminCreateZone(c *gin.Context) {
	adminCreate(c, func(in model.AdminZoneInput) (*model.AdminZone, error) {
		return h.svc.AdminCreateZone(c.Request.Context(), adminActor(c), in)
	})
}

func (h *Handler) adminUpdateZone(c *gin.Context) {
	adminPatch(c, func(id uuid.UUID, p model.AdminZonePatch) (*model.AdminZone, error) {
		return h.svc.AdminUpdateZone(c.Request.Context(), adminActor(c), id, p)
	})
}

// ---- categories, skills ----

func (h *Handler) adminListCategories(c *gin.Context) {
	v, err := list(h.svc.AdminListCategories(c.Request.Context()))
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) adminCreateCategory(c *gin.Context) {
	adminCreate(c, func(in model.AdminCategoryInput) (*model.AdminCategory, error) {
		return h.svc.AdminCreateCategory(c.Request.Context(), adminActor(c), in)
	})
}

func (h *Handler) adminUpdateCategory(c *gin.Context) {
	adminPatch(c, func(id uuid.UUID, p model.AdminCategoryPatch) (*model.AdminCategory, error) {
		return h.svc.AdminUpdateCategory(c.Request.Context(), adminActor(c), id, p)
	})
}

func (h *Handler) adminListSkills(c *gin.Context) {
	v, err := list(h.svc.AdminListSkills(c.Request.Context()))
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) adminCreateSkill(c *gin.Context) {
	adminCreate(c, func(in model.SkillInput) (*model.Skill, error) {
		return h.svc.AdminCreateSkill(c.Request.Context(), adminActor(c), in)
	})
}

// ---- services, options, add-ons ----

func (h *Handler) adminListServices(c *gin.Context) {
	cat, ok := optionalUUIDQuery(c, "category_id")
	if !ok {
		return
	}
	v, err := list(h.svc.AdminListServices(c.Request.Context(), cat))
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) adminCreateService(c *gin.Context) {
	adminCreate(c, func(in model.AdminServiceInput) (*model.AdminService, error) {
		return h.svc.AdminCreateService(c.Request.Context(), adminActor(c), in)
	})
}

func (h *Handler) adminGetService(c *gin.Context) {
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	v, err := h.svc.AdminServiceTree(c.Request.Context(), id)
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) adminUpdateService(c *gin.Context) {
	adminPatch(c, func(id uuid.UUID, p model.AdminServicePatch) (*model.AdminService, error) {
		return h.svc.AdminUpdateService(c.Request.Context(), adminActor(c), id, p)
	})
}

func (h *Handler) adminCreateOption(c *gin.Context) {
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	adminCreate(c, func(in model.AdminOptionInput) (*model.AdminOption, error) {
		return h.svc.AdminCreateOption(c.Request.Context(), adminActor(c), id, in)
	})
}

func (h *Handler) adminUpdateOption(c *gin.Context) {
	adminPatch(c, func(id uuid.UUID, p model.AdminOptionPatch) (*model.AdminOption, error) {
		return h.svc.AdminUpdateOption(c.Request.Context(), adminActor(c), id, p)
	})
}

func (h *Handler) adminCreateAddonGroup(c *gin.Context) {
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	adminCreate(c, func(in model.AdminAddonGroupInput) (*model.AdminAddonGroup, error) {
		return h.svc.AdminCreateAddonGroup(c.Request.Context(), adminActor(c), id, in)
	})
}

func (h *Handler) adminUpdateAddonGroup(c *gin.Context) {
	adminPatch(c, func(id uuid.UUID, p model.AdminAddonGroupPatch) (*model.AdminAddonGroup, error) {
		return h.svc.AdminUpdateAddonGroup(c.Request.Context(), adminActor(c), id, p)
	})
}

func (h *Handler) adminCreateAddon(c *gin.Context) {
	id, ok := uuidParam(c, "id")
	if !ok {
		return
	}
	adminCreate(c, func(in model.AdminAddonInput) (*model.AdminAddon, error) {
		return h.svc.AdminCreateAddon(c.Request.Context(), adminActor(c), id, in)
	})
}

func (h *Handler) adminUpdateAddon(c *gin.Context) {
	adminPatch(c, func(id uuid.UUID, p model.AdminAddonPatch) (*model.AdminAddon, error) {
		return h.svc.AdminUpdateAddon(c.Request.Context(), adminActor(c), id, p)
	})
}

// ---- prices, rate cards ----

func (h *Handler) adminListPrices(c *gin.Context) {
	item, ok := optionalUUIDQuery(c, "item_id")
	if !ok {
		return
	}
	v, err := list(h.svc.AdminListPrices(c.Request.Context(), c.Query("city"), item))
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) adminCreatePrice(c *gin.Context) {
	adminCreate(c, func(in model.AdminPriceInput) (*model.AdminPrice, error) {
		return h.svc.AdminCreatePrice(c.Request.Context(), adminActor(c), in)
	})
}

func (h *Handler) adminListRateCards(c *gin.Context) {
	cat, ok := optionalUUIDQuery(c, "category_id")
	if !ok {
		return
	}
	v, err := list(h.svc.AdminListRateCards(c.Request.Context(), c.Query("city"), cat))
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) adminCreateRateCard(c *gin.Context) {
	adminCreate(c, func(in model.AdminRateCardInput) (*model.AdminRateCard, error) {
		return h.svc.AdminCreateRateCard(c.Request.Context(), adminActor(c), in)
	})
}

func (h *Handler) adminUpdateRateCard(c *gin.Context) {
	adminPatch(c, func(id uuid.UUID, p model.AdminRateCardPatch) (*model.AdminRateCard, error) {
		return h.svc.AdminUpdateRateCard(c.Request.Context(), adminActor(c), id, p)
	})
}

// ---- config ----

func (h *Handler) adminListSlotConfigs(c *gin.Context) {
	v, err := list(h.svc.AdminListSlotConfigs(c.Request.Context(), c.Query("city")))
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) adminCreateSlotConfig(c *gin.Context) {
	adminCreate(c, func(in model.AdminSlotConfigInput) (*model.AdminSlotConfig, error) {
		return h.svc.AdminCreateSlotConfig(c.Request.Context(), adminActor(c), in)
	})
}

func (h *Handler) adminUpdateSlotConfig(c *gin.Context) {
	adminPatch(c, func(id uuid.UUID, p model.AdminSlotConfigPatch) (*model.AdminSlotConfig, error) {
		return h.svc.AdminUpdateSlotConfig(c.Request.Context(), adminActor(c), id, p)
	})
}

func (h *Handler) adminListCancellationRules(c *gin.Context) {
	v, err := list(h.svc.AdminListCancellationRules(c.Request.Context(), c.Query("city")))
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) adminCreateCancellationRule(c *gin.Context) {
	adminCreate(c, func(in model.AdminCancellationRuleInput) (*model.AdminCancellationRule, error) {
		return h.svc.AdminCreateCancellationRule(c.Request.Context(), adminActor(c), in)
	})
}

func (h *Handler) adminUpdateCancellationRule(c *gin.Context) {
	adminPatch(c, func(id uuid.UUID, p model.AdminCancellationRulePatch) (*model.AdminCancellationRule, error) {
		return h.svc.AdminUpdateCancellationRule(c.Request.Context(), adminActor(c), id, p)
	})
}

func (h *Handler) adminListCommissionRules(c *gin.Context) {
	v, err := list(h.svc.AdminListCommissionRules(c.Request.Context(), c.Query("city")))
	respond(c, http.StatusOK, v, err)
}

func (h *Handler) adminCreateCommissionRule(c *gin.Context) {
	adminCreate(c, func(in model.AdminCommissionRuleInput) (*model.AdminCommissionRule, error) {
		return h.svc.AdminCreateCommissionRule(c.Request.Context(), adminActor(c), in)
	})
}

func (h *Handler) adminUpdateCommissionRule(c *gin.Context) {
	adminPatch(c, func(id uuid.UUID, p model.AdminCommissionRulePatch) (*model.AdminCommissionRule, error) {
		return h.svc.AdminUpdateCommissionRule(c.Request.Context(), adminActor(c), id, p)
	})
}

// ---- audit ----

func (h *Handler) adminAuditLogs(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	v, err := list(h.svc.AdminAuditLogs(c.Request.Context(), c.Query("entity"), limit))
	respond(c, http.StatusOK, v, err)
}
