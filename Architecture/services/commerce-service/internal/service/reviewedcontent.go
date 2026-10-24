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
// attributes, the matrix, the gallery and a variant's options and image.
//
// A variant's PRICE is deliberately not on that list. The MSeller rule is
// "price and stock changes go live at once; title, images or category go
// back through checks": a seller matching a competitor's price must not take
// their listing off sale to do it. What a price change gets instead is
// validation — positive, whole paise (the store), and never above the MRP
// (variantPriceAfterPatch) — so going live at once is not going live
// unchecked.

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

// ErrSellingAboveMRP is returned when a write would leave a variant's selling
// price above its MRP.
//
// The MRP is the most a buyer may be charged (Legal Metrology (Packaged
// Commodities) Rules, 2011), and the storefront renders "MRP ₹X, now ₹Y" from
// the pair: a selling price above it is both unlawful to charge and a
// negative discount on the product card. The web editor refused it locally;
// nothing on the server did, and a price change now goes live without a
// reviewer seeing it, so the check has to be here.
var ErrSellingAboveMRP = errors.New("commerce: the selling price cannot be above the MRP")

// priceWithinMRP is the one comparison every price write makes. Non-positive
// amounts are left to the checks that refuse them by name (ErrPriceNotPositive).
func priceWithinMRP(mrpMinor, sellingMinor int64) error {
	if mrpMinor > 0 && sellingMinor > 0 && sellingMinor > mrpMinor {
		return fmt.Errorf("%w: selling price %d paise, MRP %d paise", ErrSellingAboveMRP, sellingMinor, mrpMinor)
	}
	return nil
}

// ErrReturnPolicyInvalid is returned when a listing names a return window the
// schema does not have. The values ARE products.return_policy_type's CHECK
// (setup.sql); a value outside it reached the database and came back as the
// generic INVALID_VALUE, which does not say which field or what is allowed.
// The web editor offered "14_days" for exactly that reason.
var ErrReturnPolicyInvalid = errors.New("commerce: return_policy_type must be one of no_return, 7_days, 15_days, 30_days, custom")

// ReturnPolicyTypes is the CHECK's list, in the order a form offers it.
var ReturnPolicyTypes = []string{"no_return", "7_days", "15_days", "30_days", "custom"}

func validReturnPolicyType(v string) bool {
	for _, t := range ReturnPolicyTypes {
		if v == t {
			return true
		}
	}
	return false
}

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
// body would change on `cur`: the options that place the variant in the
// matrix, its image, and its SKU (which checkVariantPatch then locks outright
// on an approved listing).
//
// Price, status, stock, barcode, weight, currency and cost price are not
// reviewed content: repricing, pausing a size or correcting a parcel weight
// must not take the listing down. Price is validated instead — see
// variantPriceAfterPatch.
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

// requestedMinor resolves the price a body sends for one column pair — paise
// preferred, rupees as the legacy fallback, the same preference the store's
// normaliseVariantMoney applies. `sent` is false when the body names neither
// form; `readable` is false for a value the store will refuse with its own
// error (a string, a null).
func requestedMinor(updates map[string]any, rupeeCol string) (minor int64, sent bool, readable bool) {
	rawMinor, hasMinor := updates[rupeeCol+"_minor"]
	rawRupees, hasRupees := updates[rupeeCol]
	switch {
	case hasMinor:
		minor, readable = anyToPaise(rawMinor, 1)
	case hasRupees:
		minor, readable = anyToPaise(rawRupees, 100)
	default:
		return 0, false, false
	}
	return minor, true, readable
}

// storedMinor is a variant's stored paise for one pair: the minor column when
// it holds a value, the rupee mirror otherwise (the read path's own rule).
func storedMinor(minor *int64, rupees float64) int64 {
	if minor != nil && *minor > 0 {
		return *minor
	}
	return int64(math.Round(rupees * 100)) // money-exempt: fallback for a row with no minor column
}

// variantPriceAfterPatch refuses a PATCH /variants/:id body that would leave
// the selling price above the MRP — comparing the price the variant would
// HAVE afterwards, so lowering the MRP alone below the stored selling price
// is caught as surely as raising the selling price alone.
//
// A body that touches neither price is not checked: pausing a size on a
// legacy row whose pair is already inverted must not start failing. An
// unreadable value is left to the store, which refuses it by name.
func variantPriceAfterPatch(cur *postgres.ProductVariant, updates map[string]any) error {
	mrp, mrpSent, mrpOK := requestedMinor(updates, "mrp")
	sell, sellSent, sellOK := requestedMinor(updates, "selling_price")
	if !mrpSent && !sellSent {
		return nil
	}
	if (mrpSent && !mrpOK) || (sellSent && !sellOK) {
		return nil
	}
	if !mrpSent {
		mrp = storedMinor(cur.MRPMinor, cur.MRP)
	}
	if !sellSent {
		sell = storedMinor(cur.SellingPriceMinor, cur.SellingPrice)
	}
	return priceWithinMRP(mrp, sell)
}

// checkVariantPatch is everything PATCH /variants/:id decides from the
// product, the variant and the body alone, before the review gate: the
// status vocabulary, the SKU (blank refused, an unchanged re-send dropped,
// locked once the listing has been approved), and the price pair. It
// normalises `updates` in place and returns the reviewed fields the body
// would change, for gateApprovedEdit.
func checkVariantPatch(product *postgres.Product, variant *postgres.ProductVariant, updates map[string]any) ([]string, error) {
	if raw, ok := updates["status"]; ok {
		status, _ := raw.(string)
		if !variantSettableStatuses[status] {
			return nil, fmt.Errorf("%w: got %q", ErrVariantStatusInvalid, status)
		}
	}

	changed := variantSubstantiveChanges(variant, updates)
	if raw, ok := updates["sku"]; ok {
		sku, _ := raw.(string)
		sku = strings.TrimSpace(sku)
		if sku == "" {
			return nil, fmt.Errorf("%w: sku", ErrVariantFieldRequired)
		}
		if sku == variant.SKU {
			// Re-sending the SKU the variant already has is not an edit.
			delete(updates, "sku")
		} else {
			// Locked once live: the editable-without-revalidation arm of
			// ProductEditability is exactly "still a draft".
			editable, needsRevalidation := postgres.ProductEditability(product.ApprovalStatus)
			if !editable || needsRevalidation {
				return nil, ErrSKULocked
			}
			updates["sku"] = sku
		}
	}

	if err := variantPriceAfterPatch(variant, updates); err != nil {
		return nil, err
	}
	return changed, nil
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
