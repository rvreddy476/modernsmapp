package service

// What counts as changing an approved listing's REVIEWED content, and the one
// gate every such edit goes through.
//
// The revalidation rule (see RevalidationRequiredError in productwrite.go)
// applied to PATCH /products/:id and to nothing else, and it fired on field
// PRESENCE rather than on change. Two consequences, both reported from the
// seller console:
//
//   - every save of a live listing sent it back to review, because the web
//     editor re-sends the whole form and "the request carries `title`" was
//     read as "the title changed";
//   - the gallery routes and PATCH /variants/:id never asked at all, so the
//     photographs a reviewer approved and the price they saw could be
//     replaced on a live listing with no review and no notice.
//
// This file fixes both halves. Every comparison below is old-vs-new against
// what the product HOLDS, and the same gate decides for the columns, the
// attributes, the matrix, the gallery and a variant's price.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ErrSKULocked is returned when a variant's SKU is changed after the listing
// has been approved.
//
// Locked once live, editable while draft. A SKU is the key everything
// downstream holds a variant by — the bulk importer merges on it, order
// lines and invoices print it, the seller's own warehouse labels carry it —
// so a live listing's SKU changing underneath those is a reconciliation
// problem for every one of them. While the listing is still a draft nothing
// has been printed or merged, and a typo is just a typo.
var ErrSKULocked = errors.New("commerce: a variant's SKU is locked once the listing has been approved; archive the variant and add a new one")

// ErrVariantStatusInvalid is returned when PATCH /variants/:id names a status
// the schema does not allow. The values ARE the column's CHECK (migration
// 018): a value outside it reached the database and came back as a 500.
var ErrVariantStatusInvalid = errors.New("commerce: a variant's status is one of active, inactive, out_of_stock")

// variantSettableStatuses is what a seller may write through PATCH.
// `archived` is deliberately absent: that is DELETE /variants/:id, which is
// the soft delete and must stay the only way to reach it.
var variantSettableStatuses = map[string]bool{"active": true, "inactive": true, "out_of_stock": true}

// ─── The gate ───────────────────────────────────────────────────────────

// gateApprovedEdit decides whether an edit naming `changed` reviewed fields
// may be applied to `product`, and whether applying it must send the
// listing back to review.
//
//	nothing changed                       apply in place, whatever the state
//	draft (any editable, unapproved)      apply in place
//	in front of a reviewer, or archived   ErrProductNotEditable (409)
//	approved, changed, no ack             RevalidationRequiredError (409)
//	approved, changed, acked              apply AND revalidate
//
// `changed` is computed by the caller from old-vs-new values, so an edit
// that re-sends what is already there is "nothing changed" and never costs
// an approval — and never needs the listing to be editable either: pausing
// a size on a listing that is in front of a reviewer touches nothing the
// reviewer reads, and refusing it would be a new restriction, not a fix.
func gateApprovedEdit(product *postgres.Product, changed []string, ack bool) (revalidate bool, err error) {
	if len(changed) == 0 {
		return false, nil
	}
	editable, needsRevalidation := postgres.ProductEditability(product.ApprovalStatus)
	if !editable {
		return false, fmt.Errorf("%w: approval_status=%s", ErrProductNotEditable, product.ApprovalStatus)
	}
	if !needsRevalidation {
		return false, nil
	}
	if !ack {
		return false, &RevalidationRequiredError{Fields: changed}
	}
	return true, nil
}

// ─── Attributes ─────────────────────────────────────────────────────────

// attributeSetsChange reports whether writing `sets` would change any of the
// product's stored answers.
func (s *Service) attributeSetsChange(ctx context.Context, productID uuid.UUID, sets []postgres.AttributeValueSet) (bool, error) {
	if len(sets) == 0 {
		return false, nil
	}
	stored, err := s.store.ProductAttributeValuesByDefinition(ctx, productID)
	if err != nil {
		return false, err
	}
	for _, set := range sets {
		if attributeValuesDiffer(stored[set.DefinitionID], set.Values) {
			return true, nil
		}
	}
	return false, nil
}

// attributeValuesDiffer compares one definition's stored rows with the rows a
// write would put there, in position order — the order is part of the value
// for a multi_enum.
func attributeValuesDiffer(stored, sent []postgres.ProductAttributeValue) bool {
	if len(stored) != len(sent) {
		return true
	}
	for i := range sent {
		if !sameAttributeValue(stored[i], sent[i]) {
			return true
		}
	}
	return false
}

func sameAttributeValue(a, b postgres.ProductAttributeValue) bool {
	return sameTextPtr(a.ValueText, b.ValueText) &&
		sameNumPtr(a.ValueNum, b.ValueNum) &&
		sameBoolPtr(a.ValueBool, b.ValueBool) &&
		sameDatePtr(a.ValueDate, b.ValueDate) &&
		sameUUIDPtr(a.ValueMediaID, b.ValueMediaID) &&
		sameTextPtr(a.UnitCode, b.UnitCode)
}

func sameTextPtr(a, b *string) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	}
	return *a == *b
}

// sameNumPtr compares at the column's own precision: value_num is
// NUMERIC(24,6), so a sent 0.1234567 is stored as 0.123457 and must not read
// as a change on the next save.
func sameNumPtr(a, b *float64) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	}
	return math.Round(*a*1e6) == math.Round(*b*1e6)
}

func sameBoolPtr(a, b *bool) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	}
	return *a == *b
}

// sameDatePtr compares calendar dates: the column is DATE, so the time of
// day a client's clock happened to put on the value is not stored.
func sameDatePtr(a, b *time.Time) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	}
	return a.Format("2006-01-02") == b.Format("2006-01-02")
}

func sameUUIDPtr(a, b *uuid.UUID) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	}
	return *a == *b
}

// ─── The matrix ─────────────────────────────────────────────────────────

// variationChanges reports whether applying `u` would change the product's
// axes or any variant's options. A nil update is a patch that did not
// mention the matrix.
func (s *Service) variationChanges(ctx context.Context, productID uuid.UUID, u *postgres.VariationUpdate) (bool, error) {
	if u == nil {
		return false, nil
	}
	axes, err := s.store.ProductVariationAxes(ctx, productID)
	if err != nil {
		return false, err
	}
	options, err := s.store.ProductVariantOptions(ctx, productID)
	if err != nil {
		return false, err
	}
	return variationDiffers(axes, options, u), nil
}

// variationDiffers is the pure comparison: axes in position order, and each
// variant's options as a set.
func variationDiffers(storedAxes []postgres.ProductVariationAxisRow, storedOptions map[uuid.UUID][]postgres.VariantOption, u *postgres.VariationUpdate) bool {
	if len(storedAxes) != len(u.Axes) {
		return true
	}
	sentAxes := append([]postgres.VariationAxis(nil), u.Axes...)
	sort.Slice(sentAxes, func(i, j int) bool { return sentAxes[i].Position < sentAxes[j].Position })
	for i, a := range storedAxes {
		if a.DefinitionID != sentAxes[i].DefinitionID {
			return true
		}
	}
	// Every variant the update names, against what it holds. A variant with
	// options stored and none sent is a change; one with none either way is
	// not. Variants the update does not name are untouched by the write.
	for variantID, sent := range u.VariantOptions {
		if optionSetKey(storedOptions[variantID]) != optionSetKey(sent) {
			return true
		}
	}
	return false
}

func optionSetKey(opts []postgres.VariantOption) string {
	parts := make([]string, 0, len(opts))
	for _, o := range opts {
		parts = append(parts, o.DefinitionID.String()+"="+o.ValueCode)
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

// ─── The gallery ────────────────────────────────────────────────────────

// galleryChanges reports whether `next` differs from the gallery the product
// holds — as an ordered list, because the first image is the cover and a
// reviewer saw a particular one.
func galleryChanges(current, next []uuid.UUID) bool {
	if len(current) != len(next) {
		return true
	}
	for i := range next {
		if current[i] != next[i] {
			return true
		}
	}
	return false
}

// galleryEditGate is the gate for every gallery write: ownership, then
// "would this change the gallery at all?", then gateApprovedEdit.
//
// It returns the product so the caller can publish the visibility change
// after the write without a second read. The ownership refusal is the one
// the gallery routes have always mapped to 403 (ErrNotOrderOwner, reused —
// see assertProductSeller).
func (s *Service) galleryEditGate(ctx context.Context, productID, actorUserID uuid.UUID, next []uuid.UUID, ack bool) (*postgres.Product, bool, error) {
	product, err := s.store.GetProductByID(ctx, productID)
	if err != nil || product == nil {
		return nil, false, fmt.Errorf("product not found")
	}
	seller, err := s.GetSellerProfile(ctx, actorUserID)
	if err != nil || seller == nil || product.SellerID != seller.ID {
		return nil, false, ErrNotOrderOwner
	}
	current, err := s.store.ProductMediaIDs(ctx, productID)
	if err != nil {
		return nil, false, err
	}
	changed := []string{}
	if galleryChanges(current, next) {
		changed = append(changed, "gallery")
	}
	revalidate, err := gateApprovedEdit(product, changed, ack)
	if err != nil {
		return nil, false, err
	}
	return product, revalidate, nil
}

// afterGalleryRevalidation is what UpdateProduct does after a revalidation
// bounce, for the gallery routes: the listing has left sale, and search must
// stop offering it.
func (s *Service) afterGalleryRevalidation(ctx context.Context, product *postgres.Product) {
	s.publish(ctx, "commerce.product.updated", map[string]any{
		"product_id": product.ID, "seller_id": product.SellerID, "revalidated": true,
	})
	s.publishProductVisibility(ctx, product.ID)
}

// ─── A variant's reviewed fields ────────────────────────────────────────

// variantSubstantiveChanges names the reviewed fields a PATCH /variants/:id
// body would change on `cur`: the price a reviewer saw, the options that
// place the variant in the matrix, its image, and its SKU.
//
// Status, stock, barcode, weight, currency and cost price are not reviewed
// content: pausing a size or correcting a parcel weight must not take the
// listing down.
func variantSubstantiveChanges(cur *postgres.ProductVariant, updates map[string]any) []string {
	out := []string{}
	add := func(name string, changed bool) {
		if changed {
			out = append(out, name)
		}
	}
	if raw, ok := updates["sku"]; ok {
		sent, _ := raw.(string)
		add("sku", strings.TrimSpace(sent) != cur.SKU)
	}
	add("price", requestedMinorChanges(updates, "mrp", cur.MRPMinor, cur.MRP) ||
		requestedMinorChanges(updates, "selling_price", cur.SellingPriceMinor, cur.SellingPrice))
	add("options",
		optionTextChanges(updates, "option_1_name", cur.Option1Name) ||
			optionTextChanges(updates, "option_1_value", cur.Option1Value) ||
			optionTextChanges(updates, "option_2_name", cur.Option2Name) ||
			optionTextChanges(updates, "option_2_value", cur.Option2Value) ||
			optionTextChanges(updates, "option_3_name", cur.Option3Name) ||
			optionTextChanges(updates, "option_3_value", cur.Option3Value))
	if raw, ok := updates["image_media_id"]; ok {
		sent, _ := raw.(string)
		have := ""
		if cur.ImageMediaID != nil {
			have = cur.ImageMediaID.String()
		}
		add("image", strings.TrimSpace(sent) != have)
	}
	return out
}

// requestedMinorChanges resolves the price a body sends for one column pair
// — paise preferred, rupees as the legacy fallback, the same preference the
// store's normaliseVariantMoney applies — and compares it with the stored
// paise. A body that sends neither form leaves the price alone.
//
// A value that cannot be read (a string, a negative) is reported as a change:
// the store refuses it a moment later with its own error, and treating it as
// "no change" here would let the refusal be skipped on an approved listing.
func requestedMinorChanges(updates map[string]any, rupeeCol string, storedMinor *int64, storedRupees float64) bool {
	rawMinor, hasMinor := updates[rupeeCol+"_minor"]
	rawRupees, hasRupees := updates[rupeeCol]
	if !hasMinor && !hasRupees {
		return false
	}
	var sent int64
	var ok bool
	if hasMinor {
		sent, ok = anyToPaise(rawMinor, 1)
	} else {
		sent, ok = anyToPaise(rawRupees, 100)
	}
	if !ok {
		return true
	}
	have := int64(math.Round(storedRupees * 100)) // money-exempt: fallback for a row with no minor column
	if storedMinor != nil {
		have = *storedMinor
	}
	return sent != have
}

// anyToPaise reads a JSON number (float64 after a map decode, or an integer
// from an internal caller) as paise, scaling rupees by 100.
func anyToPaise(raw any, scale float64) (int64, bool) {
	switch v := raw.(type) {
	case float64:
		return int64(math.Round(v * scale)), true
	case int:
		return int64(math.Round(float64(v) * scale)), true
	case int64:
		return int64(math.Round(float64(v) * scale)), true
	default:
		return 0, false
	}
}

func optionTextChanges(updates map[string]any, key string, stored *string) bool {
	raw, ok := updates[key]
	if !ok {
		return false
	}
	sent, _ := raw.(string)
	return textChanges(&sent, stored)
}
