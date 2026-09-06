package http

// PATCH /v1/commerce/products/:productId — the endpoint that did not exist.
//
// A seller could create a product and could never change it. There was no
// update route at all: `Store.UpdateProduct` existed, had zero callers, and
// took a `map[string]any` whose KEYS it interpolated into the SQL as column
// names. Wiring a handler to that map would have put every column in
// `products` — approval_status, seller_id, view_count — one un-audited request
// body away from being written, because a map cannot say which keys are
// permitted.
//
// ─── THE ALLOWLIST IS THE REQUEST SHAPE ─────────────────────────────────
//
// The body is decoded key by key against `patchableProductFields`, and a key
// that is not in that table is REFUSED, by name, with 400
// FIELD_NOT_PATCHABLE. Not ignored.
//
// Ignoring is the tempting choice and it is the wrong one. A client that
// sends `{"approval_status": "approved"}` and receives 200 has been told its
// request was honoured. It was not. The seller's screen then shows an
// approved product that the database says is a draft, and the disagreement
// surfaces days later as "my listing vanished". Refusing by name costs the
// client one round trip during development and tells it the truth.
//
// ─── AND WHY null IS NOT THE SAME AS ABSENT ─────────────────────────────
//
// `{"warranty_info": null}` clears the warranty note; omitting the key leaves
// it alone. Decoding into a struct of pointers cannot express that —
// encoding/json leaves the pointer nil for both — so the body is decoded into
// `map[string]json.RawMessage` first, and presence is a fact about the map
// rather than about the decoded value.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// patchField decodes one allowlisted key into the typed patch.
type patchField func(raw json.RawMessage, p *postgres.ProductPatch) error

// decodeAs decodes a JSON value into T and hands the caller a pointer to it.
//
// `null` decodes to the zero value, which is what the "clear this field"
// semantics need: an empty string becomes SQL NULL in the store's assignment
// builder, and a zero number is a real zero.
func decodeAs[T any](raw json.RawMessage, assign func(*T)) error {
	var v T
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
	}
	assign(&v)
	return nil
}

// patchableProductFields IS the allowlist, as data.
//
// The absent names are as deliberate as the present ones; the reasoning for
// each omission is on postgres.ProductPatch, which this table fills in.
var patchableProductFields = map[string]patchField{
	"category_id": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *uuid.UUID) { p.CategoryID = v })
	},
	"brand_id": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *uuid.UUID) { p.BrandID = v })
	},
	"tax_class_id": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *uuid.UUID) { p.TaxClassID = v })
	},
	"title": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *string) { p.Title = v })
	},
	"short_title": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *string) { p.ShortTitle = v })
	},
	"description": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *string) { p.Description = v })
	},
	"short_description": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *string) { p.ShortDescription = v })
	},
	"brand_name": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *string) { p.BrandName = v })
	},
	"manufacturer_name": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *string) { p.ManufacturerName = v })
	},
	"product_type": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *string) { p.ProductType = v })
	},
	"condition": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *string) { p.Condition = v })
	},
	"primary_image_media_id": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *uuid.UUID) { p.PrimaryImageMediaID = v })
	},
	"video_media_id": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *uuid.UUID) { p.VideoMediaID = v })
	},
	"weight_grams": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *int) { p.WeightGrams = v })
	},
	"length_cm": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *float64) { p.LengthCm = v })
	},
	"width_cm": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *float64) { p.WidthCm = v })
	},
	"height_cm": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *float64) { p.HeightCm = v })
	},
	"country_of_origin": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *string) { p.CountryOfOrigin = v })
	},
	"warranty_info": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *string) { p.WarrantyInfo = v })
	},
	"return_policy_type": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *string) { p.ReturnPolicyType = v })
	},
	"return_policy_days": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *int) { p.ReturnPolicyDays = v })
	},
	"hsn_code": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *string) { p.HSNCode = v })
	},
	"search_keywords": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *[]string) { p.SearchKeywords = v })
	},
	"meta_title": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *string) { p.MetaTitle = v })
	},
	"meta_description": func(r json.RawMessage, p *postgres.ProductPatch) error {
		return decodeAs(r, func(v *string) { p.MetaDescription = v })
	},
}

// patchControlKeys are body keys that are part of the request but are not
// product columns: the attribute answers, and the revalidation
// acknowledgement an approved product's edit needs.
var patchControlKeys = map[string]bool{
	"attributes": true,
	"revalidate": true,
	// The variation matrix, added in the step that gave a product declared
	// axes. `variation_axes` is what the product varies on and `variants`
	// carries each existing variant's value on each axis — they travel
	// together and are only read when `variation_axes` is present, because a
	// matrix change replaces the whole picture. See
	// service.VariationPatchInput.
	"variation_axes": true,
	"variants":       true,
}

// notNullable are the NOT NULL columns, where a JSON null is a bad request
// rather than a clear.
//
// Letting `{"title": null}` through would reach the database as a NOT NULL
// violation and surface as a generic "one of the supplied values is not
// permitted" — which does not say which one.
var notNullable = map[string]bool{
	"title": true, "product_type": true, "condition": true,
	"return_policy_type": true, "return_policy_days": true,
}

// UpdateProduct PATCH /v1/commerce/products/:productId
//
// Seller-only, owner-only, and only while the product is in a state that
// permits editing (see postgres.ProductEditability). An approved product's
// substantive edit is refused until the caller acknowledges that applying it
// returns the listing to review — see service.RevalidationRequiredError for
// why that is a refusal rather than a silent un-approval.
func (h *Handler) UpdateProduct(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	productID, ok := parseUUID(c, "productId")
	if !ok {
		return
	}

	var body map[string]json.RawMessage
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"INVALID_BODY", err.Error(), nil)
		return
	}

	in := service.UpdateProductInput{ActorUserID: userID, ProductID: productID}

	// Unknown and non-patchable keys, ALL of them, before anything is
	// applied. One at a time would make a client fix them one round trip
	// each, which is the same argument the per-field attribute errors make.
	refused := []string{}
	for key, raw := range body {
		if patchControlKeys[key] {
			continue
		}
		decode, allowed := patchableProductFields[key]
		if !allowed {
			refused = append(refused, key)
			continue
		}
		if notNullable[key] && string(raw) == "null" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
				"INVALID_BODY", fmt.Sprintf("%q cannot be cleared; it is required on every product", key),
				gin.H{"field": key})
			return
		}
		if err := decode(raw, &in.Fields); err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
				"INVALID_BODY", fmt.Sprintf("%q could not be decoded: %s", key, err.Error()),
				gin.H{"field": key})
			return
		}
	}
	if len(refused) > 0 {
		sort.Strings(refused)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"FIELD_NOT_PATCHABLE",
			"this endpoint does not update "+strings.Join(quoteAll(refused), ", ")+
				"; a product's ownership, slug, review state, publication state and counters are "+
				"not seller-editable",
			gin.H{"fields": refused, "patchable": patchableFieldNames()})
		return
	}

	if raw, present := body["attributes"]; present {
		in.AttributesPresent = true
		if string(raw) != "null" {
			if err := json.Unmarshal(raw, &in.Attributes); err != nil {
				api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
					"INVALID_BODY", "attributes could not be decoded: "+err.Error(), nil)
				return
			}
		}
	}
	// The matrix is read only when `variation_axes` is present. `variants`
	// on its own is not a matrix change — it is a client that sent half the
	// pair — and silently treating it as one would replace the axes with
	// nothing, which is the most destructive reading of an ambiguous body.
	if raw, present := body["variation_axes"]; present {
		v := &service.VariationPatchInput{}
		if string(raw) != "null" {
			if err := json.Unmarshal(raw, &v.Axes); err != nil {
				api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
					"INVALID_BODY", "variation_axes could not be decoded: "+err.Error(), nil)
				return
			}
		}
		if vr, ok := body["variants"]; ok && string(vr) != "null" {
			if err := json.Unmarshal(vr, &v.Variants); err != nil {
				api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
					"INVALID_BODY", "variants could not be decoded: "+err.Error(), nil)
				return
			}
		}
		in.Variation = v
	} else if _, orphan := body["variants"]; orphan {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"INVALID_BODY",
			`"variants" changes each variant's options, which only mean anything against the `+
				`axes the product declares; send "variation_axes" alongside it (an empty array `+
				`clears the matrix)`, nil)
		return
	}

	if raw, present := body["revalidate"]; present {
		if err := json.Unmarshal(raw, &in.AckRevalidation); err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
				"INVALID_BODY", "revalidate must be true or false", nil)
			return
		}
	}

	out, err := h.svc.UpdateProduct(c.Request.Context(), in)
	if err != nil {
		writeProductWriteError(c, err)
		return
	}

	resp := gin.H{"product": out.Product}
	if out.Revalidated {
		// Said out loud, in the same response as the success. A seller whose
		// live listing has just left the catalogue finds out here, not from
		// their sales figures.
		resp["revalidated"] = true
		resp["notice"] = "this edit changed reviewed content, so the listing has returned to " +
			"review and is no longer on sale until it is approved again"
	}
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

func quoteAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

// patchableFieldNames is served in the refusal body so a client that guessed
// wrong can see the whole allowlist without reading this file.
func patchableFieldNames() []string {
	out := make([]string, 0, len(patchableProductFields))
	for k := range patchableProductFields {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ─── Errors ─────────────────────────────────────────────────────────────

// writeProductWriteError maps the refusals the product write path raises.
//
// Anything it does not recognise falls through to writeCommerceError, which
// has the rest of the domain mapping and ends at a 500 with no internal
// detail in it.
func writeProductWriteError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	w := c.Writer

	// Per-field, keyed by attribute CODE, so a form can put each message
	// under the control that produced it. 422 rather than 400: the body was
	// well-formed JSON and every key was one this endpoint accepts — what
	// failed is the CONTENT of specific fields, which is exactly the
	// distinction 422 exists to make, and a client can branch on it to
	// decide between "I sent nonsense" and "the seller typed something the
	// category will not accept".
	var invalid *service.AttributeValuesInvalidError
	if errors.As(err, &invalid) {
		api.ErrorWithContext(ctx, w, http.StatusUnprocessableEntity, "ATTRIBUTE_VALUES_INVALID",
			"one or more attribute values were refused", gin.H{"fields": invalid.Fields})
		return
	}

	// The variation matrix, per problem, keyed by the variant's SKU and the
	// axis code. 422 for the same reason the attribute errors are: the body
	// parsed and every key was one this route accepts — what failed is the
	// content, and a client branches on that to decide whether it sent
	// nonsense or the seller picked something the catalogue will not take.
	var badMatrix *service.VariationInvalidError
	if errors.As(err, &badMatrix) {
		api.ErrorWithContext(ctx, w, http.StatusUnprocessableEntity, "VARIATION_INVALID",
			"the variation matrix was refused", gin.H{"problems": badMatrix.Problems})
		return
	}

	// The two constraints the database holds regardless of what the
	// validation pass thought. Reaching either means the two disagree, which
	// is worth a specific message rather than a 500 — see
	// postgres.ErrUndeclaredVariationAxis.
	switch {
	case errors.Is(err, postgres.ErrDuplicateVariantCombination):
		api.ErrorWithContext(ctx, w, http.StatusConflict, "VARIANT_COMBINATION_TAKEN",
			"this listing already has a variant for that combination of options; a shop may offer "+
				"each combination once (another shop offering the same one is fine)", nil)
		return
	case errors.Is(err, postgres.ErrUndeclaredVariationAxis):
		api.ErrorWithContext(ctx, w, http.StatusUnprocessableEntity, "VARIATION_INVALID",
			postgres.ErrUndeclaredVariationAxis.Error(), nil)
		return
	}

	var reval *service.RevalidationRequiredError
	if errors.As(err, &reval) {
		api.ErrorWithContext(ctx, w, http.StatusConflict, "REVALIDATION_REQUIRED", reval.Error(),
			gin.H{
				"fields":     reval.Fields,
				"how_to_ack": `re-send the same request with "revalidate": true`,
				"effect": "the edit is applied and the listing returns to review " +
					"(approval_status=submitted, status=draft); it is off sale until approved again",
			})
		return
	}

	switch {
	case errors.Is(err, service.ErrUnknownProductCategory):
		// 400, not 404: the category is a FIELD of the body, not the
		// resource this route addresses. A 404 here would read as "no such
		// product" and send the seller looking for a listing that is fine.
		api.ErrorWithContext(ctx, w, http.StatusBadRequest, "CATEGORY_NOT_FOUND",
			"no such category; GET /v1/commerce/categories lists them, and a product filed "+
				"under an unknown category has no attribute schema, so its form would be empty",
			nil)
	case errors.Is(err, service.ErrProductNotEditable):
		// 409: nothing about the request is malformed. The listing is in a
		// state whose resolution is somebody else's action.
		api.ErrorWithContext(ctx, w, http.StatusConflict, "PRODUCT_NOT_EDITABLE",
			"this product cannot be edited right now: it is either in front of a reviewer, "+
				"hidden by an operator, or archived", nil)
	case errors.Is(err, service.ErrNoSellerProfile):
		api.ErrorWithContext(ctx, w, http.StatusForbidden, "NO_SELLER", "seller account not found", nil)
	case errors.Is(err, postgres.ErrDuplicateSKU):
		// 409, and named. `product_variants.sku` is globally UNIQUE, so this
		// is the likeliest way a real create fails — a seller pasting a code
		// they have already used. It reached the default arm and was reported
		// as an internal error, so the seller was told the service was broken
		// rather than that one field needed changing.
		api.ErrorWithContext(ctx, w, http.StatusConflict, "SKU_TAKEN",
			"one of these SKUs is already in use; a SKU has to be unique across the catalogue", nil)
	default:
		writeCommerceError(c, err)
	}
}
