package http

// The attribute-schema surfaces: one public read, and the admin CRUD behind
// the internal key that authors what it reads.
//
// ─── WHY THE PUBLIC ONE CARRIES AN ETag ─────────────────────────────────
//
// A create screen fetches this before it can draw anything, so it is on the
// critical path of every listing. The document changes when somebody publishes
// — which is rare — and is otherwise identical on every call for weeks. Without
// a validator every app cold start re-downloads a form definition it already
// has; with one, the second call is 304 and a few bytes of headers.
//
// ─── WHY AN UNKNOWN CATEGORY IS 404 AND NOT AN EMPTY FORM ───────────────
//
// Nothing else in this service checks that a category id names a row: `POST
// /products` lets the foreign key decide, and every browse filter treats an
// unknown id as "no matches". That is survivable for a filter and not for a
// form. An empty form and a mistyped id render as the same screen, so the
// client shows a create page with no fields on it and the seller submits a
// listing that is missing everything the category asks for.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GetCategoryAttributeSchema GET /v1/commerce/categories/:categoryId/attribute-schema
//
// The form definition for a category, with inheritance resolved, options and
// units inlined, grouped and ordered the way it should be drawn.
//
// Public, for the same reason the tax-class table is: these are the questions
// a listing must answer, and a buyer-facing surface that renders a spec sheet
// needs the labels and the units as much as the seller's create screen does.
// It carries no seller data of any kind.
//
//	?scope=item   facts about the goods (page count, author)
//	?scope=offer  facts about this seller's listing of them
//	?scope=all    both — the default
func (h *Handler) GetCategoryAttributeSchema(c *gin.Context) {
	categoryID, ok := parseUUID(c, "categoryId")
	if !ok {
		return
	}
	doc, err := h.svc.AttributeSchemaFor(c.Request.Context(), categoryID, c.Query("scope"))
	if err != nil {
		writeAttributeError(c, err)
		return
	}

	c.Writer.Header().Set("ETag", doc.ETag)
	// Vary on scope? No — the scope is a query parameter, and a cache keys on
	// the full URL including the query string. Vary is for headers.
	c.Writer.Header().Set("Cache-Control", "public, max-age=60")
	if service.ETagMatches(c.GetHeader("If-None-Match"), doc.ETag) {
		c.Status(http.StatusNotModified)
		return
	}
	api.JSON(c.Writer, http.StatusOK, doc, nil)
}

// ─── Admin: definitions ─────────────────────────────────────────────────

// AdminListAttributeDefinitions GET /v1/commerce/internal/attribute-definitions
func (h *Handler) AdminListAttributeDefinitions(c *gin.Context) {
	includeInactive := c.Query("include_inactive") == "true"
	defs, err := h.svc.ListAttributeDefinitions(c.Request.Context(), includeInactive)
	if err != nil {
		writeAttributeError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": defs}, nil)
}

type createAttributeDefinitionReq struct {
	Code          string   `json:"code"`
	Label         string   `json:"label"`
	HelpText      *string  `json:"help_text"`
	Placeholder   *string  `json:"placeholder"`
	DataType      string   `json:"data_type"`
	UnitFamily    *string  `json:"unit_family"`
	DefaultUnit   *string  `json:"default_unit"`
	MinNum        *float64 `json:"min_num"`
	MaxNum        *float64 `json:"max_num"`
	MinLen        *int     `json:"min_len"`
	MaxLen        *int     `json:"max_len"`
	Regex         *string  `json:"regex"`
	MaxValues     *int     `json:"max_values"`
	DisplayGroup  string   `json:"display_group"`
	AppliesTo     string   `json:"applies_to"`
	IsVariantAxis bool     `json:"is_variant_axis"`
	IsFilterable  bool     `json:"is_filterable"`
	IsSearchable  bool     `json:"is_searchable"`
}

// AdminCreateAttributeDefinition POST /v1/commerce/internal/attribute-definitions
func (h *Handler) AdminCreateAttributeDefinition(c *gin.Context) {
	var req createAttributeDefinitionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"INVALID_BODY", err.Error(), nil)
		return
	}
	d := &postgres.AttributeDefinition{
		Code: req.Code, Label: req.Label, HelpText: req.HelpText, Placeholder: req.Placeholder,
		DataType: req.DataType, UnitFamily: req.UnitFamily, DefaultUnit: req.DefaultUnit,
		MinNum: req.MinNum, MaxNum: req.MaxNum, MinLen: req.MinLen, MaxLen: req.MaxLen,
		Regex: req.Regex, MaxValues: req.MaxValues,
		DisplayGroup: req.DisplayGroup, AppliesTo: req.AppliesTo,
		IsVariantAxis: req.IsVariantAxis, IsFilterable: req.IsFilterable, IsSearchable: req.IsSearchable,
	}
	out, err := h.svc.CreateAttributeDefinition(c.Request.Context(), d, actorID(c))
	if err != nil {
		writeAttributeError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, out, nil)
}

// AdminGetAttributeDefinition GET /v1/commerce/internal/attribute-definitions/:defId
func (h *Handler) AdminGetAttributeDefinition(c *gin.Context) {
	id, ok := parseUUID(c, "defId")
	if !ok {
		return
	}
	d, err := h.svc.GetAttributeDefinition(c.Request.Context(), id)
	if err != nil {
		writeAttributeError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, d, nil)
}

// AdminPatchAttributeDefinition PATCH /v1/commerce/internal/attribute-definitions/:defId
//
// The body is read as a raw JSON object rather than into a struct, because
// PATCH means "the keys that are present". A struct of pointers cannot tell
// `{"regex": null}` — clear it — from `{}` — leave it alone.
func (h *Handler) AdminPatchAttributeDefinition(c *gin.Context) {
	id, ok := parseUUID(c, "defId")
	if !ok {
		return
	}
	raw, ok := bindRawPatch(c)
	if !ok {
		return
	}
	out, err := h.svc.PatchAttributeDefinition(c.Request.Context(), id, raw, ackImpact(c), actorID(c))
	if err != nil {
		writeAttributeError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// AdminAttributeDefinitionImpact GET /v1/commerce/internal/attribute-definitions/:defId/impact
func (h *Handler) AdminAttributeDefinitionImpact(c *gin.Context) {
	id, ok := parseUUID(c, "defId")
	if !ok {
		return
	}
	imp, err := h.svc.ImpactOf(c.Request.Context(), id)
	if err != nil {
		writeAttributeError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, imp, nil)
}

// ─── Admin: enum values ─────────────────────────────────────────────────

// AdminListEnumValues GET /v1/commerce/internal/attribute-definitions/:defId/enum-values
func (h *Handler) AdminListEnumValues(c *gin.Context) {
	id, ok := parseUUID(c, "defId")
	if !ok {
		return
	}
	values, err := h.svc.ListEnumValues(c.Request.Context(), id)
	if err != nil {
		writeAttributeError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": values}, nil)
}

type createEnumValueReq struct {
	Code          string     `json:"code"`
	Label         string     `json:"label"`
	SwatchHex     *string    `json:"swatch_hex"`
	SwatchMediaID *uuid.UUID `json:"swatch_media_id"`
	SortOrder     int        `json:"sort_order"`
}

// AdminCreateEnumValue POST /v1/commerce/internal/attribute-definitions/:defId/enum-values
func (h *Handler) AdminCreateEnumValue(c *gin.Context) {
	id, ok := parseUUID(c, "defId")
	if !ok {
		return
	}
	var req createEnumValueReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"INVALID_BODY", err.Error(), nil)
		return
	}
	v := &postgres.AttributeEnumValue{
		Code: req.Code, Label: req.Label, SwatchHex: req.SwatchHex,
		SwatchMediaID: req.SwatchMediaID, SortOrder: req.SortOrder,
	}
	out, err := h.svc.CreateEnumValue(c.Request.Context(), id, v, actorID(c))
	if err != nil {
		writeAttributeError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, out, nil)
}

// AdminPatchEnumValue PATCH /v1/commerce/internal/attribute-definitions/:defId/enum-values/:valueId
//
// There is no DELETE beside it, deliberately: an option a product already
// chose has to stay resolvable, so retiring is `{"is_active": false}` here.
func (h *Handler) AdminPatchEnumValue(c *gin.Context) {
	defID, ok := parseUUID(c, "defId")
	if !ok {
		return
	}
	valueID, ok := parseUUID(c, "valueId")
	if !ok {
		return
	}
	raw, ok := bindRawPatch(c)
	if !ok {
		return
	}
	out, err := h.svc.PatchEnumValue(c.Request.Context(), defID, valueID, raw, ackImpact(c), actorID(c))
	if err != nil {
		writeAttributeError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

type reorderEnumValuesReq struct {
	Order []uuid.UUID `json:"order"`
}

// AdminReorderEnumValues PUT /v1/commerce/internal/attribute-definitions/:defId/enum-values/order
func (h *Handler) AdminReorderEnumValues(c *gin.Context) {
	id, ok := parseUUID(c, "defId")
	if !ok {
		return
	}
	var req reorderEnumValuesReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.ReorderEnumValues(c.Request.Context(), id, req.Order, actorID(c)); err != nil {
		writeAttributeError(c, err)
		return
	}
	values, err := h.svc.ListEnumValues(c.Request.Context(), id)
	if err != nil {
		writeAttributeError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": values}, nil)
}

// ─── Admin: category bindings ───────────────────────────────────────────

// AdminGetCategoryAttributes GET /v1/commerce/internal/categories/:categoryId/attributes
//
// The category's OWN bindings, not the inherited form. The console edits these;
// the public schema endpoint serves the resolved result.
func (h *Handler) AdminGetCategoryAttributes(c *gin.Context) {
	id, ok := parseUUID(c, "categoryId")
	if !ok {
		return
	}
	bindings, err := h.svc.GetCategoryAttributeBindings(c.Request.Context(), id)
	if err != nil {
		writeAttributeError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": bindings}, nil)
}

type putCategoryAttributesReq struct {
	Items []struct {
		DefinitionID  uuid.UUID `json:"definition_id"`
		IsRequired    bool      `json:"is_required"`
		IsExcluded    bool      `json:"is_excluded"`
		IsVariantAxis *bool     `json:"is_variant_axis"`
		DisplayGroup  *string   `json:"display_group"`
		SortOrder     int       `json:"sort_order"`
	} `json:"items"`
}

// AdminPutCategoryAttributes PUT /v1/commerce/internal/categories/:categoryId/attributes
func (h *Handler) AdminPutCategoryAttributes(c *gin.Context) {
	id, ok := parseUUID(c, "categoryId")
	if !ok {
		return
	}
	var req putCategoryAttributesReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"INVALID_BODY", err.Error(), nil)
		return
	}
	bindings := make([]postgres.CategoryAttribute, 0, len(req.Items))
	for _, it := range req.Items {
		bindings = append(bindings, postgres.CategoryAttribute{
			CategoryID: id, DefinitionID: it.DefinitionID,
			IsRequired: it.IsRequired, IsExcluded: it.IsExcluded,
			IsVariantAxis: it.IsVariantAxis, DisplayGroup: it.DisplayGroup,
			SortOrder: it.SortOrder,
		})
	}
	out, err := h.svc.PutCategoryAttributeBindings(c.Request.Context(), id, bindings, ackImpact(c), actorID(c))
	if err != nil {
		writeAttributeError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": out}, nil)
}

// ─── Admin: categories ──────────────────────────────────────────────────

type createCategoryReq struct {
	ParentID     *uuid.UUID `json:"parent_id"`
	Name         string     `json:"name"`
	Slug         string     `json:"slug"`
	Description  *string    `json:"description"`
	DisplayOrder int        `json:"display_order"`
	IsActive     *bool      `json:"is_active"`
	IsFeatured   bool       `json:"is_featured"`
	IsListable   *bool      `json:"is_listable"`
}

// AdminCreateCategory POST /v1/commerce/internal/categories
func (h *Handler) AdminCreateCategory(c *gin.Context) {
	var req createCategoryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"INVALID_BODY", err.Error(), nil)
		return
	}
	n := &postgres.CategoryTreeNode{
		ParentID: req.ParentID, Name: req.Name, Slug: req.Slug,
		Description: req.Description, DisplayOrder: req.DisplayOrder,
		IsActive: true, IsFeatured: req.IsFeatured, IsListable: true,
	}
	if req.IsActive != nil {
		n.IsActive = *req.IsActive
	}
	if req.IsListable != nil {
		n.IsListable = *req.IsListable
	}
	out, err := h.svc.CreateCategory(c.Request.Context(), n)
	if err != nil {
		writeAttributeError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, out, nil)
}

// AdminPatchCategory PATCH /v1/commerce/internal/categories/:categoryId
//
// There is no DELETE. Products point at categories, and a delete either
// cascades into the catalogue or is refused by a foreign key at a moment
// nobody expects. `{"is_active": false}` removes it from every surface.
func (h *Handler) AdminPatchCategory(c *gin.Context) {
	id, ok := parseUUID(c, "categoryId")
	if !ok {
		return
	}
	raw, ok := bindRawPatch(c)
	if !ok {
		return
	}
	out, err := h.svc.PatchCategory(c.Request.Context(), id, raw)
	if err != nil {
		writeAttributeError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// ─── Admin: publish ─────────────────────────────────────────────────────

// AdminPublishAttributeSchema POST /v1/commerce/internal/attribute-schema/publish
func (h *Handler) AdminPublishAttributeSchema(c *gin.Context) {
	state, err := h.svc.PublishAttributeSchema(c.Request.Context())
	if err != nil {
		writeAttributeError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, state, nil)
}

// AdminAttributeSchemaState GET /v1/commerce/internal/attribute-schema
func (h *Handler) AdminAttributeSchemaState(c *gin.Context) {
	state, err := h.svc.AttributeSchemaState(c.Request.Context())
	if err != nil {
		writeAttributeError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, state, nil)
}

// ─── Shared plumbing ────────────────────────────────────────────────────

// bindRawPatch reads the body as a JSON object, preserving which keys were
// sent. See AdminPatchAttributeDefinition for why that matters.
func bindRawPatch(c *gin.Context) (map[string]json.RawMessage, bool) {
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(c.Request.Body).Decode(&raw); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"INVALID_BODY", "body must be a JSON object: "+err.Error(), nil)
		return nil, false
	}
	return raw, true
}

// ackImpact reads `?ack_impact=<count>`.
//
// A malformed value is treated as absent rather than as zero. "ack_impact=abc"
// meaning "acknowledge that nothing is affected" is precisely the accident the
// guard exists to catch.
func ackImpact(c *gin.Context) *int {
	raw, present := c.GetQuery("ack_impact")
	if !present {
		return nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return nil
	}
	return &n
}

// writeAttributeError maps the attribute surface's refusals.
//
// A separate mapper rather than another dozen cases in writeCommerceError: the
// errors here are about a schema an operator is editing, and the two families
// share nothing but the envelope. Anything it does not recognise falls through
// to writeCommerceError, which ends at a 500 with no internal detail in it.
func writeAttributeError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	w := c.Writer

	var ack *service.ImpactAckError
	if errors.As(err, &ack) {
		details := gin.H{
			"what":       ack.What,
			"ack_impact": ack.Required,
			"impacts":    ack.Impacts,
			"how_to_ack": "re-send the same request with ?ack_impact=" + strconv.Itoa(ack.Required),
		}
		if ack.Provided != nil {
			details["provided"] = *ack.Provided
		}
		api.ErrorWithContext(ctx, w, http.StatusConflict, "IMPACT_ACK_REQUIRED", ack.Error(), details)
		return
	}

	var invalid *service.AttributeValidationError
	if errors.As(err, &invalid) {
		api.ErrorWithContext(ctx, w, http.StatusBadRequest, "INVALID_ATTRIBUTE_DEFINITION",
			invalid.Reason, gin.H{"field": invalid.Field})
		return
	}

	switch {
	case errors.Is(err, postgres.ErrCategoryNotFound):
		api.ErrorWithContext(ctx, w, http.StatusNotFound, "CATEGORY_NOT_FOUND", "category not found", nil)
	case errors.Is(err, postgres.ErrAttributeDefinitionNotFound):
		api.ErrorWithContext(ctx, w, http.StatusNotFound, "ATTRIBUTE_DEFINITION_NOT_FOUND",
			"attribute definition not found", nil)
	case errors.Is(err, postgres.ErrAttributeEnumValueNotFound):
		api.ErrorWithContext(ctx, w, http.StatusNotFound, "ATTRIBUTE_ENUM_VALUE_NOT_FOUND",
			"attribute option not found", nil)
	case errors.Is(err, service.ErrInvalidAttributeScope):
		api.ErrorWithContext(ctx, w, http.StatusBadRequest, "INVALID_SCOPE",
			"scope must be one of item, offer, all", nil)
	case errors.Is(err, service.ErrAttributeCodeImmutable):
		api.ErrorWithContext(ctx, w, http.StatusConflict, "ATTRIBUTE_CODE_IMMUTABLE", err.Error(), nil)
	case errors.Is(err, service.ErrAttributeCodeTaken):
		api.ErrorWithContext(ctx, w, http.StatusConflict, "ATTRIBUTE_CODE_TAKEN", err.Error(), nil)
	case errors.Is(err, service.ErrEnumCodeDuplicate):
		api.ErrorWithContext(ctx, w, http.StatusConflict, "ENUM_CODE_DUPLICATE", err.Error(), nil)
	case errors.Is(err, service.ErrCategoryCycle):
		api.ErrorWithContext(ctx, w, http.StatusConflict, "CATEGORY_CYCLE", err.Error(), nil)
	default:
		writeCommerceError(c, err)
	}
}
