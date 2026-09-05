//go:build integration

package http

// The product write path, through the REAL registered routes and a REAL
// database.
//
// Three defects are proved here, and one of them matters more than the others:
//
//  1. THE CREATE WAS NOT TRANSACTIONAL. TestAFailedCreateLeavesNothingBehind
//     is the one to read. The old path inserted the product, then looped the
//     variants, then upserted inventory per variant — none of it in a
//     transaction, with the inventory error swallowed into a slog.Warn. A
//     create that failed on the second variant left the product and the first
//     variant standing, and the seller was shown an error.
//
//  2. THERE WAS NO UPDATE ENDPOINT, and the store method that would have
//     served it built SQL by interpolating a caller's map keys as column
//     names.
//
//  3. `category_id` WAS NEVER VALIDATED. An unknown category means no
//     attribute schema, so the seller gets an empty form and no explanation.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/http/... -run ProductWrite -v

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// ─── Fixture ────────────────────────────────────────────────────────────

// writeFixture is a seller, a category, and a small form bound to it: an
// integer with bounds, a text with a length limit and a pattern, an enum, and
// a measure with a unit family. Between them they exercise every branch of
// the validator a real create form goes through.
type writeFixture struct {
	actor    uuid.UUID
	seller   uuid.UUID
	category uuid.UUID
	taxClass string
	codes    map[string]string
	defs     map[string]uuid.UUID
}

func newWriteFixture(t *testing.T) *writeFixture {
	t.Helper()
	ctx := context.Background()
	f := &writeFixture{
		actor: uuid.New(), seller: uuid.New(), category: uuid.New(),
		codes: map[string]string{}, defs: map[string]uuid.UUID{},
	}
	tag := f.seller.String()[:8]

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := edgePool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v\nSQL: %s", err, sql)
		}
	}

	exec(`INSERT INTO product_categories (id,parent_id,name,slug,display_order,is_active)
	      VALUES ($1,NULL,'Write Cat',$2,1,TRUE)`, f.category, "write-cat-"+tag)
	exec(`INSERT INTO sellers (id,user_id,store_name,slug,email,state,status)
	      VALUES ($1,$2,'Write Store',$3,'write@example.test','KA','approved')`,
		f.seller, f.actor, "write-store-"+tag)

	for _, spec := range []struct {
		key, dataType  string
		minNum, maxNum any
		maxLen         any
		regex          any
	}{
		{key: "pages", dataType: "integer", minNum: 1.0, maxNum: 5000.0},
		{key: "author", dataType: "text", maxLen: 12},
		{key: "isbn", dataType: "text", regex: `^\d{13}$`},
		{key: "binding", dataType: "enum"},
		{key: "languages", dataType: "multi_enum"},
		{key: "weight", dataType: "measure"},
	} {
		id := uuid.New()
		code := spec.key + "_" + tag
		f.defs[spec.key], f.codes[spec.key] = id, code

		var family, unit any
		maxValues := any(nil)
		switch spec.dataType {
		case "measure":
			family, unit = "mass", "g"
		case "multi_enum":
			maxValues = 2
		}
		exec(`INSERT INTO attribute_definitions
		      (id,code,label,data_type,unit_family,default_unit,min_num,max_num,max_len,regex,
		       max_values,display_group,applies_to)
		      VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'Product Details','item')`,
			id, code, strings.ToUpper(spec.key[:1])+spec.key[1:], spec.dataType,
			family, unit, spec.minNum, spec.maxNum, spec.maxLen, spec.regex, maxValues)
		exec(`INSERT INTO category_attributes (category_id,definition_id,sort_order,is_required)
		      VALUES ($1,$2,10,$3)`, f.category, id, spec.key == "pages")
	}
	// `pages` is bound as REQUIRED on purpose: the draft test below saves a
	// product without it, which is the whole argument about completeness.
	exec(`INSERT INTO attribute_enum_values (definition_id,code,label)
	      VALUES ($1,'paperback','Paperback'), ($1,'hardback','Hardback')`, f.defs["binding"])
	exec(`INSERT INTO attribute_enum_values (definition_id,code,label)
	      VALUES ($1,'en','English'), ($1,'hi','Hindi'), ($1,'ta','Tamil')`, f.defs["languages"])

	f.taxClass = gstClass(t)

	t.Cleanup(func() {
		exec(`DELETE FROM inventory_items WHERE seller_id=$1`, f.seller)
		exec(`DELETE FROM product_attributes WHERE product_id IN
		      (SELECT id FROM products WHERE seller_id=$1)`, f.seller)
		exec(`DELETE FROM product_variants WHERE product_id IN
		      (SELECT id FROM products WHERE seller_id=$1)`, f.seller)
		exec(`DELETE FROM products WHERE seller_id=$1`, f.seller)
		exec(`DELETE FROM sellers WHERE id=$1`, f.seller)
		for _, id := range f.defs {
			exec(`DELETE FROM attribute_enum_values WHERE definition_id=$1`, id)
			exec(`DELETE FROM category_attributes WHERE definition_id=$1`, id)
			exec(`DELETE FROM attribute_definitions WHERE id=$1`, id)
		}
		exec(`DELETE FROM product_categories WHERE id=$1`, f.category)
	})
	return f
}

// body is a create request with one variant, plus whatever the caller adds.
func (f *writeFixture) body(title string, extra map[string]any) map[string]any {
	b := map[string]any{
		"title":        title,
		"tax_class_id": f.taxClass,
		"category_id":  f.category.String(),
		"variants": []map[string]any{{
			"sku":                 "WR-" + uuid.New().String()[:10],
			"mrp_minor":           129900,
			"selling_price_minor": 99900,
			"stock_qty":           7,
		}},
	}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

func (f *writeFixture) countsFor(t *testing.T) (products, variants, inventory int) {
	t.Helper()
	ctx := context.Background()
	q := func(sql string) int {
		var n int
		if err := edgePool.QueryRow(ctx, sql, f.seller).Scan(&n); err != nil {
			t.Fatalf("count: %v\nSQL: %s", err, sql)
		}
		return n
	}
	return q(`SELECT count(*) FROM products WHERE seller_id=$1`),
		q(`SELECT count(*) FROM product_variants v JOIN products p ON p.id=v.product_id WHERE p.seller_id=$1`),
		q(`SELECT count(*) FROM inventory_items WHERE seller_id=$1`)
}

// ─── 1. The create that must leave nothing behind ───────────────────────

// THE defect that matters.
//
// Two variants share a SKU, and `product_variants.sku` is UNIQUE. The second
// insert fails. Before this change the product and the first variant — and
// the first variant's inventory row — were already committed, because each
// statement was its own transaction and the loop simply returned. The seller
// saw an error and reasonably assumed nothing had happened; what had actually
// happened was a listing they could not see, could not edit, and whose SKU
// was now taken.
func TestProductWriteAFailedCreateLeavesNothingBehind(t *testing.T) {
	f := newWriteFixture(t)
	r := journeyEngine(t, 4000)

	before, beforeV, beforeI := f.countsFor(t)

	sku := "DUPE-" + uuid.New().String()[:10]
	body := f.body("Half a listing", map[string]any{
		"variants": []map[string]any{
			{"sku": sku, "mrp_minor": 129900, "selling_price_minor": 99900, "stock_qty": 5},
			{"sku": sku, "mrp_minor": 149900, "selling_price_minor": 119900, "stock_qty": 3},
		},
	})

	w := call(t, r, http.MethodPost, "/v1/commerce/products", f.actor, body)
	// 409 and named, not 500. A duplicate SKU is the seller's mistake and the
	// likeliest way this create fails; it reached the unmapped-error arm and
	// told them the service was broken.
	if w.Code != http.StatusConflict {
		t.Fatalf("a create with a duplicate SKU returned %d; want 409\n%s", w.Code, w.Body.String())
	}
	if code, _, _ := errorEnvelope(t, w.Body.Bytes()); code != "SKU_TAKEN" {
		t.Fatalf("code %q, want SKU_TAKEN\n%s", code, w.Body.String())
	}

	after, afterV, afterI := f.countsFor(t)
	if after != before {
		t.Fatalf("the failed create left %d product row(s) behind (was %d, now %d) — "+
			"a listing the seller was told did not happen", after-before, before, after)
	}
	if afterV != beforeV {
		t.Fatalf("the failed create left %d variant row(s) behind", afterV-beforeV)
	}
	if afterI != beforeI {
		t.Fatalf("the failed create left %d inventory row(s) behind", afterI-beforeI)
	}

	// And the SKU is free again. If the first variant had survived, the
	// seller's corrected retry would fail on a SKU they cannot see.
	var taken int
	if err := edgePool.QueryRow(context.Background(),
		`SELECT count(*) FROM product_variants WHERE sku=$1`, sku).Scan(&taken); err != nil {
		t.Fatalf("count sku: %v", err)
	}
	if taken != 0 {
		t.Fatalf("the SKU %q is still taken by the failed create's leftovers", sku)
	}
}

// The other half of the same rule: a create whose ATTRIBUTE values are wrong
// must not leave a product either. The attribute write is the last statement
// in the transaction, so it is the one most likely to be reached after
// everything else has succeeded.
func TestProductWriteARefusedAttributeValueLeavesNoProduct(t *testing.T) {
	f := newWriteFixture(t)
	r := journeyEngine(t, 4000)

	before, beforeV, beforeI := f.countsFor(t)

	w := call(t, r, http.MethodPost, "/v1/commerce/products",
		f.actor, f.body("Bad specs", map[string]any{
			"attributes": []map[string]any{
				{"code": f.codes["pages"], "value": "many"},
			},
		}))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422\n%s", w.Code, w.Body.String())
	}

	after, afterV, afterI := f.countsFor(t)
	if after != before || afterV != beforeV || afterI != beforeI {
		t.Fatalf("a refused attribute value left rows behind: products %d→%d, variants %d→%d, inventory %d→%d",
			before, after, beforeV, afterV, beforeI, afterI)
	}
}

// A create that SUCCEEDS gives every variant a stock row.
//
// The inventory failure used to be swallowed into a slog.Warn, so a variant
// could exist with no `inventory_items` row at all — and every availability
// read in this service derives from that table, so the listing rendered, went
// into a cart, and was refused at checkout.
func TestProductWriteASuccessfulCreateGivesEveryVariantItsStockRow(t *testing.T) {
	f := newWriteFixture(t)
	r := journeyEngine(t, 4000)

	w := call(t, r, http.MethodPost, "/v1/commerce/products",
		f.actor, f.body("Two variants", map[string]any{
			"variants": []map[string]any{
				{"sku": "A-" + uuid.New().String()[:10], "mrp_minor": 100, "selling_price_minor": 90, "stock_qty": 4},
				{"sku": "B-" + uuid.New().String()[:10], "mrp_minor": 200, "selling_price_minor": 180, "stock_qty": 0},
			},
		}))
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d\n%s", w.Code, w.Body.String())
	}
	id := createdProductID(t, w.Body.Bytes())

	var withStock, total int
	if err := edgePool.QueryRow(context.Background(), `
		SELECT count(i.variant_id), count(v.id)
		  FROM product_variants v
		  LEFT JOIN inventory_items i ON i.variant_id = v.id
		 WHERE v.product_id = $1`, uuid.MustParse(id)).Scan(&withStock, &total); err != nil {
		t.Fatalf("inventory check: %v", err)
	}
	if total != 2 || withStock != 2 {
		t.Fatalf("%d variant(s), %d with an inventory row; a variant with no stock row is a "+
			"listing the storefront shows and the checkout refuses", total, withStock)
	}
}

// ─── 2. category_id ─────────────────────────────────────────────────────

func TestProductWriteAnUnknownCategoryIsRefusedOnBothRoutes(t *testing.T) {
	f := newWriteFixture(t)
	r := journeyEngine(t, 4000)

	unknown := uuid.NewString()

	t.Run("create", func(t *testing.T) {
		before, _, _ := f.countsFor(t)
		w := call(t, r, http.MethodPost, "/v1/commerce/products",
			f.actor, f.body("Filed under nothing", map[string]any{"category_id": unknown}))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status %d, want 400\n%s", w.Code, w.Body.String())
		}
		code, _, _ := errorEnvelope(t, w.Body.Bytes())
		if code != "CATEGORY_NOT_FOUND" {
			t.Fatalf("code %q, want CATEGORY_NOT_FOUND — the seller has to be told WHICH value "+
				"was wrong, not that one of them was\n%s", code, w.Body.String())
		}
		if after, _, _ := f.countsFor(t); after != before {
			t.Fatalf("the refused create left a product behind")
		}
	})

	t.Run("patch", func(t *testing.T) {
		id := f.createDraft(t, r, "Movable")
		w := call(t, r, http.MethodPatch, "/v1/commerce/products/"+id,
			f.actor, map[string]any{"category_id": unknown})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status %d, want 400\n%s", w.Code, w.Body.String())
		}
		code, _, _ := errorEnvelope(t, w.Body.Bytes())
		if code != "CATEGORY_NOT_FOUND" {
			t.Fatalf("code %q, want CATEGORY_NOT_FOUND\n%s", code, w.Body.String())
		}
	})
}

// createDraft makes a product through the real route and returns its id.
func (f *writeFixture) createDraft(t *testing.T, r interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, title string) string {
	t.Helper()
	w := call(t, r, http.MethodPost, "/v1/commerce/products", f.actor, f.body(title, nil))
	if w.Code != http.StatusCreated {
		t.Fatalf("create %q: status %d\n%s", title, w.Code, w.Body.String())
	}
	return createdProductID(t, w.Body.Bytes())
}

// ─── 3. Ownership and state on PATCH ────────────────────────────────────

func TestProductWritePatchIsOwnerOnly(t *testing.T) {
	f := newWriteFixture(t)
	r := journeyEngine(t, 4000)
	id := f.createDraft(t, r, "Mine")

	stranger := uuid.New()
	w := call(t, r, http.MethodPatch, "/v1/commerce/products/"+id,
		stranger, map[string]any{"title": "Now mine"})
	// A caller with no seller account at all is 403 NO_SELLER; a different
	// seller is 403 NOT_YOUR_PRODUCT. Both are refusals, and neither is a
	// 200.
	if w.Code != http.StatusForbidden {
		t.Fatalf("a stranger's patch returned %d, want 403\n%s", w.Code, w.Body.String())
	}
	var title string
	if err := edgePool.QueryRow(context.Background(),
		`SELECT title FROM products WHERE id=$1`, uuid.MustParse(id)).Scan(&title); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if title != "Mine" {
		t.Fatalf("the stranger's patch was applied: title is now %q", title)
	}
}

// The state machine, at the edge. Each refused state has a different reason
// and the same answer: not now.
func TestProductWritePatchRefusesTheStatesThatMustNotChange(t *testing.T) {
	f := newWriteFixture(t)
	r := journeyEngine(t, 4000)

	for _, tc := range []struct {
		status string
		want   int
		why    string
	}{
		{"draft", http.StatusOK, "the state a create leaves behind"},
		{"rejected", http.StatusOK, "fix and resubmit is the only useful response"},
		{"changes_requested", http.StatusOK, "the reviewer asked for exactly this"},
		{"submitted", http.StatusConflict, "a reviewer is reading it right now"},
		{"under_review", http.StatusConflict, "likewise"},
		{"hidden", http.StatusConflict, "an operator took it down"},
		{"archived", http.StatusConflict, "retired, and referenced by order history"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			id := f.createDraft(t, r, "State "+tc.status)
			if _, err := edgePool.Exec(context.Background(),
				`UPDATE products SET approval_status=$2 WHERE id=$1`,
				uuid.MustParse(id), tc.status); err != nil {
				t.Fatalf("set state: %v", err)
			}
			seedOfferFor(t, uuid.MustParse(id))
			w := call(t, r, http.MethodPatch, "/v1/commerce/products/"+id,
				f.actor, map[string]any{"description": "an edit"})
			if w.Code != tc.want {
				t.Fatalf("approval_status=%s returned %d, want %d (%s)\n%s",
					tc.status, w.Code, tc.want, tc.why, w.Body.String())
			}
			if tc.want == http.StatusConflict {
				code, _, _ := errorEnvelope(t, w.Body.Bytes())
				if code != "PRODUCT_NOT_EDITABLE" {
					t.Fatalf("code %q, want PRODUCT_NOT_EDITABLE\n%s", code, w.Body.String())
				}
			}
		})
	}
}

// ─── The approved-product rule ──────────────────────────────────────────

// An approved product's substantive edit is REFUSED until the caller
// acknowledges that applying it costs the approval — and then it says so.
//
// The alternative that must not happen is either of the other two: keeping
// the approval (a moderation bypass — get something bland approved, then
// rewrite it) or dropping it silently (the seller pressed save on a spelling
// fix and their live listing left the catalogue with no warning).
func TestProductWriteEditingAnApprovedProductCostsItsApprovalAndSaysSo(t *testing.T) {
	f := newWriteFixture(t)
	r := journeyEngine(t, 4000)
	ctx := context.Background()

	approve := func(id string) {
		t.Helper()
		if _, err := edgePool.Exec(ctx,
			`UPDATE products SET approval_status='approved', status='active', published_at=NOW()
			  WHERE id=$1`, uuid.MustParse(id)); err != nil {
			t.Fatalf("approve: %v", err)
		}
		seedOfferFor(t, uuid.MustParse(id))
	}
	stateOf := func(id string) (approval, status string, published *string) {
		t.Helper()
		if err := edgePool.QueryRow(ctx,
			`SELECT approval_status, status, published_at::text FROM products WHERE id=$1`,
			uuid.MustParse(id)).Scan(&approval, &status, &published); err != nil {
			t.Fatalf("state: %v", err)
		}
		return
	}

	t.Run("a substantive edit is refused without the acknowledgement", func(t *testing.T) {
		id := f.createDraft(t, r, "Live listing")
		approve(id)

		w := call(t, r, http.MethodPatch, "/v1/commerce/products/"+id,
			f.actor, map[string]any{"title": "Something else entirely"})
		if w.Code != http.StatusConflict {
			t.Fatalf("status %d, want 409\n%s", w.Code, w.Body.String())
		}
		code, _, details := errorEnvelope(t, w.Body.Bytes())
		if code != "REVALIDATION_REQUIRED" {
			t.Fatalf("code %q, want REVALIDATION_REQUIRED\n%s", code, w.Body.String())
		}
		// It names the fields, so the seller is told what it is about to
		// cost them rather than being told "no".
		fields, _ := details["fields"].([]any)
		if len(fields) != 1 || fields[0] != "title" {
			t.Fatalf("the refusal must name the offending fields, got %v", fields)
		}

		// And nothing was written.
		approval, status, _ := stateOf(id)
		if approval != "approved" || status != "active" {
			t.Fatalf("the refused edit changed the listing's state to %s/%s", approval, status)
		}
		var title string
		_ = edgePool.QueryRow(ctx, `SELECT title FROM products WHERE id=$1`,
			uuid.MustParse(id)).Scan(&title)
		if title != "Live listing" {
			t.Fatalf("the refused edit was applied anyway: title is %q", title)
		}
	})

	t.Run("with the acknowledgement it applies and returns to review", func(t *testing.T) {
		id := f.createDraft(t, r, "Live listing two")
		approve(id)

		w := call(t, r, http.MethodPatch, "/v1/commerce/products/"+id,
			f.actor, map[string]any{"title": "Rewritten", "revalidate": true})
		if w.Code != http.StatusOK {
			t.Fatalf("status %d\n%s", w.Code, w.Body.String())
		}
		// The response SAYS the listing is off sale. A seller who is not
		// told here finds out from their sales figures.
		var env struct {
			Data struct {
				Revalidated bool   `json:"revalidated"`
				Notice      string `json:"notice"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode: %v\n%s", err, w.Body.String())
		}
		if !env.Data.Revalidated || env.Data.Notice == "" {
			t.Fatalf("the response does not say the listing left the catalogue: %s", w.Body.String())
		}

		approval, status, published := stateOf(id)
		if approval != "submitted" || status != "draft" || published != nil {
			t.Fatalf("after an acknowledged edit the listing must be back in review and off sale; "+
				"got approval=%s status=%s published_at=%v", approval, status, published)
		}
	})

	t.Run("a review-neutral edit applies in place", func(t *testing.T) {
		id := f.createDraft(t, r, "Live listing three")
		approve(id)

		// No reviewer reads a meta description, and a seller correcting a
		// parcel weight must not have to take their listing down to do it.
		w := call(t, r, http.MethodPatch, "/v1/commerce/products/"+id, f.actor, map[string]any{
			"meta_description": "Buy this book",
			"weight_grams":     260,
			"search_keywords":  []string{"book"},
		})
		if w.Code != http.StatusOK {
			t.Fatalf("status %d\n%s", w.Code, w.Body.String())
		}
		approval, status, published := stateOf(id)
		if approval != "approved" || status != "active" || published == nil {
			t.Fatalf("a review-neutral edit took the listing off sale: approval=%s status=%s published=%v",
				approval, status, published)
		}
	})
}

// ─── 4. Attribute values: per-field 422s, keyed by code ─────────────────

// Every failing field, in one response, keyed by the attribute CODE — so a
// form can put each message under the control that produced it.
//
// One flat message makes a seller with twenty answers bisect their own
// submission: told "invalid attribute value", they fix a guess, resubmit, and
// are told the seventh field is wrong.
func TestProductWriteAttributeValuesFailPerFieldKeyedByCode(t *testing.T) {
	f := newWriteFixture(t)
	r := journeyEngine(t, 4000)

	w := call(t, r, http.MethodPost, "/v1/commerce/products",
		f.actor, f.body("Everything wrong", map[string]any{
			"attributes": []map[string]any{
				{"code": f.codes["pages"], "value": 99999},                          // above max_num
				{"code": f.codes["author"], "value": "A name far too long to fit"},  // above max_len
				{"code": f.codes["isbn"], "value": "not-an-isbn"},                   // fails the regex
				{"code": f.codes["binding"], "value": "scroll"},                     // not an option
				{"code": f.codes["languages"], "value": []string{"en", "hi", "ta"}}, // above max_values
				{"code": f.codes["weight"], "value": 250, "unit_code": "km"},        // wrong unit family
				{"code": "no_such_field", "value": 1},                               // not on this form
			},
		}))

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422 — the body was well-formed and every KEY was accepted; "+
			"what failed was the content of specific fields\n%s", w.Code, w.Body.String())
	}
	code, _, details := errorEnvelope(t, w.Body.Bytes())
	if code != "ATTRIBUTE_VALUES_INVALID" {
		t.Fatalf("code %q, want ATTRIBUTE_VALUES_INVALID\n%s", code, w.Body.String())
	}

	raw, _ := json.Marshal(details["fields"])
	var fields []struct {
		Code   string `json:"code"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode fields: %v\n%s", err, w.Body.String())
	}

	got := map[string]string{}
	for _, fe := range fields {
		if fe.Reason == "" {
			t.Fatalf("field %q carries no reason; a control with no message under it is a "+
				"control the seller cannot fix", fe.Code)
		}
		got[fe.Code] = fe.Reason
	}
	for _, want := range []string{
		f.codes["pages"], f.codes["author"], f.codes["isbn"],
		f.codes["binding"], f.codes["languages"], f.codes["weight"], "no_such_field",
	} {
		if _, ok := got[want]; !ok {
			t.Fatalf("no verdict for %q — only %d of 7 fields were reported, so the client has "+
				"to resubmit to discover the rest: %v", want, len(got), got)
		}
	}
	t.Logf("per-field 422 body:\n%s", w.Body.String())
}

// The same rule on PATCH, and the same shape.
func TestProductWritePatchAttributeValuesAlsoFailPerField(t *testing.T) {
	f := newWriteFixture(t)
	r := journeyEngine(t, 4000)
	id := f.createDraft(t, r, "Patch me")

	w := call(t, r, http.MethodPatch, "/v1/commerce/products/"+id, f.actor, map[string]any{
		"attributes": []map[string]any{
			{"code": f.codes["pages"], "value": 0},
			{"code": f.codes["binding"], "value": "scroll"},
		},
	})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422\n%s", w.Code, w.Body.String())
	}
	_, _, details := errorEnvelope(t, w.Body.Bytes())
	fields, _ := details["fields"].([]any)
	if len(fields) != 2 {
		t.Fatalf("want both fields reported, got %v\n%s", fields, w.Body.String())
	}
}

// ─── 5. A draft may be incomplete ───────────────────────────────────────

// `pages` is bound REQUIRED on this category. A create that omits it must
// still save.
//
// A create route that demanded every required field would make drafts
// impossible, and a seller who cannot save a half-filled form does not go away
// and come back with the missing data — they type "n/a" and "0" into fourteen
// controls to get past the gate. Completeness is the submit-for-review gate's
// question.
func TestProductWriteADraftSavesHappilyWhileIncomplete(t *testing.T) {
	f := newWriteFixture(t)
	r := journeyEngine(t, 4000)

	w := call(t, r, http.MethodPost, "/v1/commerce/products",
		f.actor, f.body("A work in progress", map[string]any{
			// `author` only. `pages` is required by the category and absent.
			"attributes": []map[string]any{
				{"code": f.codes["author"], "value": "R K Narayan"},
			},
		}))
	if w.Code != http.StatusCreated {
		t.Fatalf("an incomplete draft was refused with %d; a draft that cannot be saved is a "+
			"catalogue full of placeholders\n%s", w.Code, w.Body.String())
	}
	id := createdProductID(t, w.Body.Bytes())

	var approval string
	var stored int
	if err := edgePool.QueryRow(context.Background(), `
		SELECT p.approval_status,
		       (SELECT count(*) FROM product_attributes a
		         WHERE a.product_id = p.id AND a.definition_id IS NOT NULL)
		  FROM products p WHERE p.id=$1`, uuid.MustParse(id)).Scan(&approval, &stored); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if approval != "draft" {
		t.Fatalf("a create must land in draft, got %q", approval)
	}
	if stored != 1 {
		t.Fatalf("the one answer the seller DID give was not stored (%d typed rows)", stored)
	}

	// And a value that is present is still checked. Incomplete is fine;
	// wrong is not.
	bad := call(t, r, http.MethodPatch, "/v1/commerce/products/"+id, f.actor, map[string]any{
		"attributes": []map[string]any{{"code": f.codes["pages"], "value": "many"}},
	})
	if bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a wrong value on a draft returned %d, want 422\n%s", bad.Code, bad.Body.String())
	}
}

// ─── 6. schema_version ──────────────────────────────────────────────────

// The vintage of the form that produced these values is recorded at write
// time, so a later reader knows which bounds they were checked against.
//
// It takes the advisory lock because it compares the stored stamp against the
// live published version, and another test bumping that singleton row between
// the write and the read is a flake this suite has already fixed once.
func TestProductWriteRecordsTheSchemaVersionTheValuesWereCheckedAgainst(t *testing.T) {
	lockSchemaState(t)
	f := newWriteFixture(t)
	r := journeyEngine(t, 4000)
	ctx := context.Background()

	published := func() int {
		t.Helper()
		var v int
		if err := edgePool.QueryRow(ctx,
			`SELECT COALESCE((SELECT published_version FROM attribute_schema_state WHERE singleton), 1)`).
			Scan(&v); err != nil {
			t.Fatalf("published version: %v", err)
		}
		return v
	}
	stamp := func(id string) int {
		t.Helper()
		var v int
		if err := edgePool.QueryRow(ctx,
			`SELECT schema_version FROM products WHERE id=$1`, uuid.MustParse(id)).Scan(&v); err != nil {
			t.Fatalf("schema_version: %v", err)
		}
		return v
	}

	want := published()

	// A create that carries answers is stamped.
	w := call(t, r, http.MethodPost, "/v1/commerce/products",
		f.actor, f.body("Stamped on create", map[string]any{
			"attributes": []map[string]any{{"code": f.codes["pages"], "value": 328}},
		}))
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d\n%s", w.Code, w.Body.String())
	}
	created := createdProductID(t, w.Body.Bytes())
	if got := stamp(created); got != want {
		t.Fatalf("created product stamped schema_version=%d, want the published %d", got, want)
	}

	// A create with NO answers is not stamped: nothing was validated, and
	// claiming a version would tell a reconciliation pass to skip it.
	plain := f.createDraft(t, r, "No answers")
	if got := stamp(plain); got != 0 {
		t.Fatalf("a product with no typed values claimed schema_version=%d; 0 means "+
			"\"never validated\", which is the truth here", got)
	}

	// And a patch that writes answers stamps it.
	pw := call(t, r, http.MethodPatch, "/v1/commerce/products/"+plain, f.actor, map[string]any{
		"attributes": []map[string]any{{"code": f.codes["author"], "value": "Anon"}},
	})
	if pw.Code != http.StatusOK {
		t.Fatalf("patch status %d\n%s", pw.Code, pw.Body.String())
	}
	if got := stamp(plain); got != want {
		t.Fatalf("after a patch that wrote values, schema_version=%d, want %d", got, want)
	}
}

// ─── 7. The happy patch ─────────────────────────────────────────────────

// The endpoint that did not exist, doing what it exists for.
func TestProductWriteAPatchActuallyEditsTheProduct(t *testing.T) {
	f := newWriteFixture(t)
	r := journeyEngine(t, 4000)
	id := f.createDraft(t, r, "Before")

	w := call(t, r, http.MethodPatch, "/v1/commerce/products/"+id, f.actor, map[string]any{
		"title":       "After",
		"description": "A longer description than before",
		"attributes": []map[string]any{
			{"code": f.codes["pages"], "value": 328},
			{"code": f.codes["binding"], "value": "paperback"},
			{"code": f.codes["languages"], "value": []string{"en", "hi"}},
			{"code": f.codes["weight"], "value": 250, "unit_code": "g"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d\n%s", w.Code, w.Body.String())
	}

	var title string
	var doc []byte
	if err := edgePool.QueryRow(context.Background(),
		`SELECT title, attributes_doc FROM products WHERE id=$1`,
		uuid.MustParse(id)).Scan(&title, &doc); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if title != "After" {
		t.Fatalf("title is %q", title)
	}
	// The search projection was rebuilt inside the same write. A doc that
	// disagrees with the rows is worse than no doc, because the index
	// believes it.
	var parsed map[string]any
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("attributes_doc: %v", err)
	}
	if parsed[f.codes["pages"]] != 328.0 {
		t.Fatalf("attributes_doc did not pick up the page count: %s", doc)
	}
	if langs, ok := parsed[f.codes["languages"]].([]any); !ok || len(langs) != 2 {
		t.Fatalf("a multi_enum must project as an array of its selected codes: %s", doc)
	}
	if wt, ok := parsed[f.codes["weight"]].(map[string]any); !ok || wt["unit"] != "g" {
		t.Fatalf("a measure must project with its unit: %s", doc)
	}

	// Clearing a value: nil removes the answer rather than storing an empty
	// one — the only way a seller undoes a mistake.
	cw := call(t, r, http.MethodPatch, "/v1/commerce/products/"+id, f.actor, map[string]any{
		"attributes": []map[string]any{{"code": f.codes["binding"], "value": nil}},
	})
	if cw.Code != http.StatusOK {
		t.Fatalf("clear status %d\n%s", cw.Code, cw.Body.String())
	}
	var remaining int
	if err := edgePool.QueryRow(context.Background(), `
		SELECT count(*) FROM product_attributes
		 WHERE product_id=$1 AND definition_id=$2`,
		uuid.MustParse(id), f.defs["binding"]).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("clearing a field left %d row(s) behind", remaining)
	}
}
