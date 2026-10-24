//go:build integration

package http

// Four seller-console defects on the product listing write path, through the
// REAL registered routes and a REAL database:
//
//  3. a SKU change after create was silently dropped — now written while the
//     listing is a draft and refused with 409 SKU_LOCKED once it is approved;
//  4. the opening stock of a variant added after create was ignored — the
//     body carried no stock figure, and the inventory row was always 0;
//  5. any save of a live listing forced re-review, because the substantive
//     check fired on field PRESENCE: the web editor re-sends the whole form;
//  6. the gallery could be changed on a live listing with no review at all
//     (now reviewed), and a variant's price had no server-side check (it
//     goes live at once, per the MSeller rule, and is refused above the MRP);
//  1. a return window outside the CHECK came back as the generic
//     INVALID_VALUE (now 400 RETURN_POLICY_INVALID, naming the field).
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/http/... -run ListingFix -v

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// listingFixture is a writeFixture with the two helpers every test below
// needs: the listing's first variant, and the review state of the row.
type listingFixture struct {
	*writeFixture
}

func newListingFixture(t *testing.T) *listingFixture {
	return &listingFixture{writeFixture: newWriteFixture(t)}
}

func (f *listingFixture) approve(t *testing.T, id string) {
	t.Helper()
	if _, err := edgePool.Exec(context.Background(),
		`UPDATE products SET approval_status='approved', status='active', published_at=NOW() WHERE id=$1`,
		uuid.MustParse(id)); err != nil {
		t.Fatalf("approve: %v", err)
	}
	seedOfferFor(t, uuid.MustParse(id))
}

func (f *listingFixture) state(t *testing.T, id string) (approval, status string, published *string) {
	t.Helper()
	if err := edgePool.QueryRow(context.Background(),
		`SELECT approval_status, status, published_at::text FROM products WHERE id=$1`,
		uuid.MustParse(id)).Scan(&approval, &status, &published); err != nil {
		t.Fatalf("state: %v", err)
	}
	return
}

func (f *listingFixture) firstVariant(t *testing.T, id string) (variantID, sku string) {
	t.Helper()
	if err := edgePool.QueryRow(context.Background(),
		`SELECT id, sku FROM product_variants WHERE product_id=$1 ORDER BY created_at LIMIT 1`,
		uuid.MustParse(id)).Scan(&variantID, &sku); err != nil {
		t.Fatalf("first variant: %v", err)
	}
	return
}

func (f *listingFixture) mustBeLive(t *testing.T, id, when string) {
	t.Helper()
	approval, status, published := f.state(t, id)
	if approval != "approved" || status != "active" || published == nil {
		t.Fatalf("%s: the listing must still be on sale; got approval=%s status=%s published_at=%v",
			when, approval, status, published)
	}
}

func (f *listingFixture) mustBeBackInReview(t *testing.T, id, when string) {
	t.Helper()
	approval, status, published := f.state(t, id)
	if approval != "submitted" || status != "draft" || published != nil {
		t.Fatalf("%s: the listing must be back in review and off sale; got approval=%s status=%s published_at=%v",
			when, approval, status, published)
	}
}

func decodeMeta(t *testing.T, body []byte) (revalidated bool, notice string) {
	t.Helper()
	var env struct {
		Meta *struct {
			Revalidated bool   `json:"revalidated"`
			Notice      string `json:"notice"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	if env.Meta == nil {
		return false, ""
	}
	return env.Meta.Revalidated, env.Meta.Notice
}

// ─── 3. The SKU ─────────────────────────────────────────────────────────

func TestListingFixSKUIsEditableWhileDraftAndLockedOnceApproved(t *testing.T) {
	f := newListingFixture(t)
	r := journeyEngine(t, 4000)
	ctx := context.Background()

	id := f.createDraft(t, r, "SKU typo")
	variantID, original := f.firstVariant(t, id)

	t.Run("a draft's SKU change is written, not dropped", func(t *testing.T) {
		want := "FIXED-" + uuid.New().String()[:8]
		w := call(t, r, http.MethodPatch, "/v1/commerce/variants/"+variantID, f.actor,
			map[string]any{"sku": want})
		if w.Code != http.StatusOK {
			t.Fatalf("status %d\n%s", w.Code, w.Body.String())
		}
		var stored string
		_ = edgePool.QueryRow(ctx, `SELECT sku FROM product_variants WHERE id=$1`, uuid.MustParse(variantID)).Scan(&stored)
		if stored != want {
			t.Fatalf("the SKU change was dropped: stored %q, sent %q (was %q)", stored, want, original)
		}
	})

	t.Run("a SKU another variant of the shop holds is 409 SKU_TAKEN", func(t *testing.T) {
		other := f.createDraft(t, r, "Other listing")
		_, taken := f.firstVariant(t, other)
		w := call(t, r, http.MethodPatch, "/v1/commerce/variants/"+variantID, f.actor,
			map[string]any{"sku": taken})
		if w.Code != http.StatusConflict {
			t.Fatalf("status %d, want 409\n%s", w.Code, w.Body.String())
		}
		if code, _, _ := errorEnvelope(t, w.Body.Bytes()); code != "SKU_TAKEN" {
			t.Fatalf("code %q, want SKU_TAKEN\n%s", code, w.Body.String())
		}
	})

	t.Run("once approved the SKU is locked, with a code and the field", func(t *testing.T) {
		f.approve(t, id)
		var before string
		_ = edgePool.QueryRow(ctx, `SELECT sku FROM product_variants WHERE id=$1`, uuid.MustParse(variantID)).Scan(&before)

		w := call(t, r, http.MethodPatch, "/v1/commerce/variants/"+variantID, f.actor,
			map[string]any{"sku": "LATE-" + uuid.New().String()[:8], "revalidate": true})
		if w.Code != http.StatusConflict {
			t.Fatalf("status %d, want 409\n%s", w.Code, w.Body.String())
		}
		code, _, details := errorEnvelope(t, w.Body.Bytes())
		if code != "SKU_LOCKED" || details["field"] != "sku" {
			t.Fatalf("want SKU_LOCKED on field sku\n%s", w.Body.String())
		}
		var after string
		_ = edgePool.QueryRow(ctx, `SELECT sku FROM product_variants WHERE id=$1`, uuid.MustParse(variantID)).Scan(&after)
		if after != before {
			t.Fatalf("the refused SKU change was applied anyway: %q -> %q", before, after)
		}
		f.mustBeLive(t, id, "after a refused SKU change")
	})

	t.Run("re-sending the SKU it already has is not a change", func(t *testing.T) {
		var sku string
		_ = edgePool.QueryRow(ctx, `SELECT sku FROM product_variants WHERE id=$1`, uuid.MustParse(variantID)).Scan(&sku)
		w := call(t, r, http.MethodPatch, "/v1/commerce/variants/"+variantID, f.actor,
			map[string]any{"sku": sku, "barcode": "8901234567890"})
		if w.Code != http.StatusOK {
			t.Fatalf("status %d\n%s", w.Code, w.Body.String())
		}
		f.mustBeLive(t, id, "after re-sending the stored SKU")
	})
}

// ─── 4. Stock on a variant added after create ───────────────────────────

func TestListingFixAnAddedVariantKeepsItsOpeningStock(t *testing.T) {
	f := newListingFixture(t)
	r := journeyEngine(t, 4000)
	ctx := context.Background()
	id := f.createDraft(t, r, "Needs a size XL")

	w := call(t, r, http.MethodPost, "/v1/commerce/products/"+id+"/variants", f.actor, map[string]any{
		"sku": "XL-" + uuid.New().String()[:8], "mrp_minor": 149900, "selling_price_minor": 129900, "stock_qty": 40,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d\n%s", w.Code, w.Body.String())
	}
	var env struct {
		Data struct {
			ID           string `json:"id"`
			AvailableQty *int   `json:"available_qty"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v\n%s", err, w.Body.String())
	}
	if env.Data.AvailableQty == nil || *env.Data.AvailableQty != 40 {
		t.Fatalf("the 201 must report the opening stock: got available_qty=%v\n%s", env.Data.AvailableQty, w.Body.String())
	}
	var total int
	if err := edgePool.QueryRow(ctx, `SELECT total_qty FROM inventory_items WHERE variant_id=$1`,
		uuid.MustParse(env.Data.ID)).Scan(&total); err != nil {
		t.Fatalf("inventory row: %v", err)
	}
	if total != 40 {
		t.Fatalf("inventory_items.total_qty = %d, want 40", total)
	}

	// A negative is refused with the field named; nothing is created.
	before, beforeV, _ := f.countsFor(t)
	w = call(t, r, http.MethodPost, "/v1/commerce/products/"+id+"/variants", f.actor, map[string]any{
		"sku": "NEG-" + uuid.New().String()[:8], "mrp_minor": 149900, "selling_price_minor": 129900, "stock_qty": -1,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a negative stock returned %d, want 400\n%s", w.Code, w.Body.String())
	}
	if _, _, details := errorEnvelope(t, w.Body.Bytes()); details["field"] != "stock_qty" {
		t.Fatalf("the refusal must name stock_qty\n%s", w.Body.String())
	}
	if after, afterV, _ := f.countsFor(t); after != before || afterV != beforeV {
		t.Fatal("a refused variant was created anyway")
	}
}

// ─── 5. A save that changes nothing substantive stays live ──────────────

func TestListingFixAnUnchangedResendDoesNotForceReview(t *testing.T) {
	f := newListingFixture(t)
	r := journeyEngine(t, 4000)

	// The create carries an attribute answer, so the re-send below can carry
	// it too — the editor re-sends every answer on every save.
	body := f.body("Live listing", map[string]any{
		"description":        "A fine book.",
		"return_policy_type": "15_days", "return_policy_days": 15,
		"attributes": []map[string]any{{"code": f.codes["pages"], "value": 264}},
	})
	w := call(t, r, http.MethodPost, "/v1/commerce/products", f.actor, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d\n%s", w.Code, w.Body.String())
	}
	id := createdProductID(t, w.Body.Bytes())
	f.approve(t, id)

	t.Run("the whole form, unchanged, applies in place", func(t *testing.T) {
		w := call(t, r, http.MethodPatch, "/v1/commerce/products/"+id, f.actor, map[string]any{
			"title": "Live listing", "description": "A fine book.",
			"product_type": "physical", "condition": "new",
			"return_policy_type": "15_days", "return_policy_days": 15,
			"category_id": f.category.String(), "tax_class_id": f.taxClass,
			"weight_grams": 300, // review-neutral, and a real change
			"attributes":   []map[string]any{{"code": f.codes["pages"], "value": 264}},
		})
		if w.Code != http.StatusOK {
			t.Fatalf("an unchanged re-send was refused: %d\n%s", w.Code, w.Body.String())
		}
		var env struct {
			Data struct {
				Revalidated bool `json:"revalidated"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		if env.Data.Revalidated {
			t.Fatalf("an unchanged re-send must not revalidate\n%s", w.Body.String())
		}
		f.mustBeLive(t, id, "after an unchanged re-send")
	})

	t.Run("one changed reviewed field is named, and only that one", func(t *testing.T) {
		w := call(t, r, http.MethodPatch, "/v1/commerce/products/"+id, f.actor, map[string]any{
			"title": "Live listing", "description": "A different book.",
			"attributes": []map[string]any{{"code": f.codes["pages"], "value": 264}},
		})
		if w.Code != http.StatusConflict {
			t.Fatalf("status %d, want 409\n%s", w.Code, w.Body.String())
		}
		code, _, details := errorEnvelope(t, w.Body.Bytes())
		fields, _ := details["fields"].([]any)
		if code != "REVALIDATION_REQUIRED" || len(fields) != 1 || fields[0] != "description" {
			t.Fatalf("want REVALIDATION_REQUIRED naming only description\n%s", w.Body.String())
		}
		f.mustBeLive(t, id, "after a refused edit")
	})

	t.Run("a changed attribute answer is substantive", func(t *testing.T) {
		w := call(t, r, http.MethodPatch, "/v1/commerce/products/"+id, f.actor, map[string]any{
			"attributes": []map[string]any{{"code": f.codes["pages"], "value": 300}},
		})
		if w.Code != http.StatusConflict {
			t.Fatalf("status %d, want 409\n%s", w.Code, w.Body.String())
		}
		_, _, details := errorEnvelope(t, w.Body.Bytes())
		fields, _ := details["fields"].([]any)
		if len(fields) != 1 || fields[0] != "attributes" {
			t.Fatalf("want the refusal to name attributes\n%s", w.Body.String())
		}
	})
}

// ─── 1. A return window outside the CHECK is refused by name ────────────

func TestListingFixReturnWindowOutsideTheCheckIsNamed(t *testing.T) {
	f := newListingFixture(t)
	r := journeyEngine(t, 4000)
	id := f.createDraft(t, r, "Returns listing")

	w := call(t, r, http.MethodPatch, "/v1/commerce/products/"+id, f.actor,
		map[string]any{"return_policy_type": "14_days", "return_policy_days": 14})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400\n%s", w.Code, w.Body.String())
	}
	code, _, details := errorEnvelope(t, w.Body.Bytes())
	if code != "RETURN_POLICY_INVALID" || details["field"] != "return_policy_type" {
		t.Fatalf("want RETURN_POLICY_INVALID on return_policy_type (was the generic INVALID_VALUE)\n%s", w.Body.String())
	}

	before, beforeV, _ := f.countsFor(t)
	w = call(t, r, http.MethodPost, "/v1/commerce/products", f.actor,
		f.body("Fourteen days", map[string]any{"return_policy_type": "14_days", "return_policy_days": 14}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create: status %d, want 400\n%s", w.Code, w.Body.String())
	}
	if code, _, _ := errorEnvelope(t, w.Body.Bytes()); code != "RETURN_POLICY_INVALID" {
		t.Fatalf("create: want RETURN_POLICY_INVALID\n%s", w.Body.String())
	}
	if after, afterV, _ := f.countsFor(t); after != before || afterV != beforeV {
		t.Fatal("a refused create wrote rows anyway")
	}

	// Every window the web editor offers is accepted.
	for _, rp := range []struct {
		typ  string
		days int
	}{{"no_return", 0}, {"7_days", 7}, {"15_days", 15}, {"30_days", 30}} {
		w := call(t, r, http.MethodPatch, "/v1/commerce/products/"+id, f.actor,
			map[string]any{"return_policy_type": rp.typ, "return_policy_days": rp.days})
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d\n%s", rp.typ, w.Code, w.Body.String())
		}
	}
}

// ─── 6. The gallery is reviewed content ─────────────────────────────────

func TestListingFixGalleryEditsAreReviewed(t *testing.T) {
	f := newListingFixture(t)
	r := journeyEngine(t, 4000)
	id := f.createDraft(t, r, "Pictured listing")
	a, b, c := uuid.New(), uuid.New(), uuid.New()

	// A draft's gallery is set freely.
	w := call(t, r, http.MethodPost, "/v1/commerce/products/"+id+"/media", f.actor,
		map[string]any{"media_ids": []string{a.String(), b.String()}})
	if w.Code != http.StatusOK {
		t.Fatalf("set gallery on a draft: %d\n%s", w.Code, w.Body.String())
	}
	f.approve(t, id)

	t.Run("re-sending the gallery it has applies in place", func(t *testing.T) {
		w := call(t, r, http.MethodPost, "/v1/commerce/products/"+id+"/media", f.actor,
			map[string]any{"media_ids": []string{a.String(), b.String()}})
		if w.Code != http.StatusOK {
			t.Fatalf("status %d\n%s", w.Code, w.Body.String())
		}
		f.mustBeLive(t, id, "after re-sending the same gallery")
	})

	t.Run("a different gallery is refused until acknowledged", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			method string
			path   string
			body   any
		}{
			{"replace", http.MethodPost, "/media", map[string]any{"media_ids": []string{a.String(), c.String()}}},
			{"append", http.MethodPost, "/media", map[string]any{"media_id": c.String()}},
			{"reorder", http.MethodPut, "/media/order", map[string]any{"media_ids": []string{b.String(), a.String()}}},
			{"delete", http.MethodDelete, "/media/" + b.String(), nil},
		} {
			w := call(t, r, tc.method, "/v1/commerce/products/"+id+tc.path, f.actor, tc.body)
			if w.Code != http.StatusConflict {
				t.Fatalf("%s: status %d, want 409\n%s", tc.name, w.Code, w.Body.String())
			}
			code, _, details := errorEnvelope(t, w.Body.Bytes())
			fields, _ := details["fields"].([]any)
			if code != "REVALIDATION_REQUIRED" || len(fields) != 1 || fields[0] != "gallery" {
				t.Fatalf("%s: want REVALIDATION_REQUIRED naming gallery\n%s", tc.name, w.Body.String())
			}
		}
		var n int
		_ = edgePool.QueryRow(context.Background(), `SELECT count(*) FROM product_media WHERE product_id=$1`, uuid.MustParse(id)).Scan(&n)
		if n != 2 {
			t.Fatalf("a refused gallery edit changed the gallery: %d images", n)
		}
		f.mustBeLive(t, id, "after refused gallery edits")
	})

	t.Run("acknowledged, it applies and the listing returns to review", func(t *testing.T) {
		w := call(t, r, http.MethodPost, "/v1/commerce/products/"+id+"/media", f.actor,
			map[string]any{"media_ids": []string{a.String(), c.String()}, "revalidate": true})
		if w.Code != http.StatusOK {
			t.Fatalf("status %d\n%s", w.Code, w.Body.String())
		}
		f.mustBeBackInReview(t, id, "after an acknowledged gallery change")
		var cover uuid.UUID
		_ = edgePool.QueryRow(context.Background(), `SELECT primary_image_media_id FROM products WHERE id=$1`, uuid.MustParse(id)).Scan(&cover)
		if cover != a {
			t.Fatalf("cover = %s, want %s", cover, a)
		}
	})

	t.Run("DELETE takes the acknowledgement as a query parameter", func(t *testing.T) {
		f.approve(t, id)
		w := call(t, r, http.MethodDelete, "/v1/commerce/products/"+id+"/media/"+c.String()+"?revalidate=true", f.actor, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d\n%s", w.Code, w.Body.String())
		}
		f.mustBeBackInReview(t, id, "after an acknowledged delete")
	})
}

// The MSeller rule for the variant half: "price and stock changes go live at
// once; title, images or category go back through checks". A price change on
// an approved listing therefore applies in place with no acknowledgement —
// and is validated instead: never above the MRP.
func TestListingFixVariantPriceEditsGoLiveAtOnce(t *testing.T) {
	f := newListingFixture(t)
	r := journeyEngine(t, 4000)
	ctx := context.Background()
	id := f.createDraft(t, r, "Priced listing")
	variantID, _ := f.firstVariant(t, id)
	f.approve(t, id)

	priceOf := func() (mrp, sell int64) {
		_ = edgePool.QueryRow(ctx, `SELECT mrp_minor, selling_price_minor FROM product_variants WHERE id=$1`,
			uuid.MustParse(variantID)).Scan(&mrp, &sell)
		return
	}

	t.Run("re-sending the stored price applies in place", func(t *testing.T) {
		w := call(t, r, http.MethodPatch, "/v1/commerce/variants/"+variantID, f.actor,
			map[string]any{"mrp_minor": 129900, "selling_price_minor": 99900, "weight_grams": 250})
		if w.Code != http.StatusOK {
			t.Fatalf("status %d\n%s", w.Code, w.Body.String())
		}
		if reval, _ := decodeMeta(t, w.Body.Bytes()); reval {
			t.Fatalf("an unchanged price must not revalidate\n%s", w.Body.String())
		}
		f.mustBeLive(t, id, "after re-sending the stored price")
	})

	t.Run("a new price lands at once and the listing stays on sale", func(t *testing.T) {
		w := call(t, r, http.MethodPatch, "/v1/commerce/variants/"+variantID, f.actor,
			map[string]any{"selling_price_minor": 89900})
		if w.Code != http.StatusOK {
			t.Fatalf("status %d, want 200\n%s", w.Code, w.Body.String())
		}
		var env struct {
			Data struct {
				ID                string `json:"id"`
				SellingPriceMinor int64  `json:"selling_price_minor"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		if env.Data.ID != variantID || env.Data.SellingPriceMinor != 89900 {
			t.Fatalf("data must be the repriced variant\n%s", w.Body.String())
		}
		if reval, _ := decodeMeta(t, w.Body.Bytes()); reval {
			t.Fatalf("a price change must not send the listing to review\n%s", w.Body.String())
		}
		if _, sell := priceOf(); sell != 89900 {
			t.Fatalf("price = %d, want 89900", sell)
		}
		f.mustBeLive(t, id, "after a price change")
	})

	t.Run("a selling price above the MRP is 400 PRICE_ABOVE_MRP and nothing is written", func(t *testing.T) {
		for _, body := range []map[string]any{
			{"selling_price_minor": 129901},                      // above the stored MRP
			{"mrp_minor": 79900},                                 // MRP below the stored price
			{"mrp_minor": 100000, "selling_price_minor": 100100}, // both, inverted
		} {
			w := call(t, r, http.MethodPatch, "/v1/commerce/variants/"+variantID, f.actor, body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%v: status %d, want 400\n%s", body, w.Code, w.Body.String())
			}
			if code, _, details := errorEnvelope(t, w.Body.Bytes()); code != "PRICE_ABOVE_MRP" || details["field"] != "selling_price_minor" {
				t.Fatalf("%v: want PRICE_ABOVE_MRP on selling_price_minor\n%s", body, w.Body.String())
			}
			if mrp, sell := priceOf(); mrp != 129900 || sell != 89900 {
				t.Fatalf("%v: a refused price was written: mrp=%d sell=%d", body, mrp, sell)
			}
		}
		f.mustBeLive(t, id, "after refused prices")
	})

	t.Run("an option change is still reviewed content", func(t *testing.T) {
		w := call(t, r, http.MethodPatch, "/v1/commerce/variants/"+variantID, f.actor,
			map[string]any{"option_1_name": "Edition", "option_1_value": "Signed"})
		if w.Code != http.StatusConflict {
			t.Fatalf("status %d, want 409\n%s", w.Code, w.Body.String())
		}
		code, _, details := errorEnvelope(t, w.Body.Bytes())
		fields, _ := details["fields"].([]any)
		if code != "REVALIDATION_REQUIRED" || len(fields) != 1 || fields[0] != "options" {
			t.Fatalf("want REVALIDATION_REQUIRED naming options\n%s", w.Body.String())
		}
		f.mustBeLive(t, id, "after a refused option change")
	})

	t.Run("adding a variant priced above its MRP is refused", func(t *testing.T) {
		w := call(t, r, http.MethodPost, "/v1/commerce/products/"+id+"/variants", f.actor, map[string]any{
			"sku": "UP-" + uuid.New().String()[:8], "mrp_minor": 99900, "selling_price_minor": 109900, "stock_qty": 1,
		})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status %d, want 400\n%s", w.Code, w.Body.String())
		}
		if code, _, _ := errorEnvelope(t, w.Body.Bytes()); code != "PRICE_ABOVE_MRP" {
			t.Fatalf("want PRICE_ABOVE_MRP\n%s", w.Body.String())
		}
	})

	t.Run("a status outside the schema is 400, named, and paused is not one", func(t *testing.T) {
		w := call(t, r, http.MethodPatch, "/v1/commerce/variants/"+variantID, f.actor,
			map[string]any{"status": "paused"})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status %d, want 400\n%s", w.Code, w.Body.String())
		}
		if code, _, _ := errorEnvelope(t, w.Body.Bytes()); code != "VARIANT_STATUS_INVALID" {
			t.Fatalf("code %q, want VARIANT_STATUS_INVALID\n%s", code, w.Body.String())
		}
		// And the schema's own word for it is accepted, in place: pausing a
		// size is not reviewed content.
		w = call(t, r, http.MethodPatch, "/v1/commerce/variants/"+variantID, f.actor,
			map[string]any{"status": "inactive"})
		if w.Code != http.StatusOK {
			t.Fatalf("status %d\n%s", w.Code, w.Body.String())
		}
	})
}
