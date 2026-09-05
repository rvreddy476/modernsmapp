//go:build integration

package http

// The shop front, over the real registered routes against a real database.
//
// Every defect this change set exists to fix was invisible from below:
//
//   - `product_media` held zero rows, because no route could put an ordered
//     gallery into it;
//   - `?category_id=` "found nothing", because the handler read `?category=`
//     and every client sent the other spelling;
//   - the home page did not exist at all.
//
// None of those is a bug in a function. They are bugs in what a request
// receives, so they are proved here — one HTTP request, one assertion about
// the JSON a phone would decode.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/http/... -v

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/commerce-service/internal/media"
	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ─── Harness ────────────────────────────────────────────────────────────

// storefrontMedia is media-service: it answers ownership checks for the
// seller the test names, and resolves every id to real-looking renditions.
//
// The rendition NAMES matter and are media-service's own (`thumb_150`,
// `medium_1080`). A stub that invents names would let the URL preference
// silently fall through to `original`, which is precisely the defect the
// resolver had.
func storefrontMedia(t *testing.T, owner uuid.UUID) (*media.Client, *int) {
	t.Helper()
	batches := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/media/batch" {
			batches++
			var req struct {
				IDs []string `json:"ids"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			data := map[string]any{}
			for _, id := range req.IDs {
				data[id] = map[string]any{"variants": map[string]string{
					"thumb_150":   "https://cdn.test/" + id + "/t.jpg",
					"medium_1080": "https://cdn.test/" + id + "/m.jpg",
				}}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
			return
		}
		// GET /v1/media/{id} — the ownership check. Everything is a ready,
		// moderation-passed image owned by `owner`.
		id := r.URL.Path[len("/v1/media/"):]
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"id": id, "uploader_id": owner, "file_type": "image",
			"processing_status": "ready", "moderation_status": "passed",
		}})
	}))
	t.Cleanup(s.Close)
	return media.New(s.URL, "k"), &batches
}

func storefrontEngine(t *testing.T, mediaOwner uuid.UUID) (*gin.Engine, *int) {
	t.Helper()
	cipher, err := pii.New(devKeyProvider{}, []byte("storefront-salt!"))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	mc, batches := storefrontMedia(t, mediaOwner)
	svc := service.New(postgres.New(edgePool), nil, "").WithPII(cipher).WithMedia(mc)
	r := gin.New()
	r.Use(FenceMiddleware())
	h := New(svc).WithInternalKey("storefront-test-key")
	h.RegisterRoutes(r)
	h.RegisterP0Routes(r)
	return r, batches
}

// storefrontFixture is one seller with a small, deliberately varied
// catalogue: a discounted product, a full-price one, and a second seller who
// owns none of it.
type storefrontFixture struct {
	sellerUserID  uuid.UUID
	sellerID      uuid.UUID
	intruderUser  uuid.UUID
	shopper       uuid.UUID
	categoryID    uuid.UUID
	discounted    uuid.UUID // 25% off
	fullPrice     uuid.UUID // no discount
	otherCategory uuid.UUID // a product in a DIFFERENT category
}

func seedStorefront(t *testing.T) storefrontFixture {
	t.Helper()
	ctx := context.Background()
	f := storefrontFixture{
		sellerUserID: uuid.New(), sellerID: uuid.New(),
		intruderUser: uuid.New(), shopper: uuid.New(),
		categoryID: uuid.New(), otherCategory: uuid.New(),
		discounted: uuid.New(), fullPrice: uuid.New(),
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := edgePool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v\nSQL: %s", err, sql)
		}
	}
	exec(`INSERT INTO sellers (id,user_id,store_name,slug,email,state,status)
	      VALUES ($1,$2,'Storefront Store',$3,'sf@example.test','KA','approved')`,
		f.sellerID, f.sellerUserID, "sf-"+f.sellerID.String()[:8])
	// The intruder is a real seller too — the ownership refusal must be
	// about WHOSE product it is, not about having a seller profile at all.
	exec(`INSERT INTO sellers (id,user_id,store_name,slug,email,state,status)
	      VALUES ($1,$2,'Intruder Store',$3,'in@example.test','KA','approved')`,
		uuid.New(), f.intruderUser, "in-"+uuid.NewString()[:8])

	for _, c := range []struct {
		id   uuid.UUID
		name string
	}{{f.categoryID, "SF Primary"}, {f.otherCategory, "SF Secondary"}} {
		exec(`INSERT INTO product_categories (id,name,slug,display_order,is_active)
		      VALUES ($1,$2,$3,10,TRUE)`, c.id, c.name, "sf-"+c.id.String()[:8])
	}

	product := func(id, category uuid.UUID, title string, mrpMinor, sellMinor int64) {
		exec(`INSERT INTO products (id,seller_id,category_id,title,slug,status,approval_status,return_policy_type)
		      VALUES ($1,$2,$3,$4,$5,'active','approved','7_days')`,
			id, f.sellerID, category, title, "p-"+id.String()[:8])
		vid := uuid.New()
		exec(`INSERT INTO product_variants (id,product_id,sku,mrp,selling_price,mrp_minor,selling_price_minor)
		      VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			vid, id, "SKU-"+vid.String()[:8],
			float64(mrpMinor)/100, float64(sellMinor)/100, mrpMinor, sellMinor)
		exec(`INSERT INTO inventory_items (variant_id,seller_id,total_qty,reserved_qty)
		      VALUES ($1,$2,25,0)`, vid, f.sellerID)
	}
	product(f.discounted, f.categoryID, "SF Discounted Widget", 99900, 74900)
	product(f.fullPrice, f.otherCategory, "SF Full Price Widget", 50000, 50000)
	return f
}

// jsonBody is for the requests that need a header `call` does not set — the
// internal-key ones.
func jsonBody(v any) *bytes.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

func decodeData(t *testing.T, w *httptest.ResponseRecorder, into any) {
	t.Helper()
	if w.Code < 200 || w.Code > 299 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("envelope: %v\n%s", err, w.Body.String())
	}
	if err := json.Unmarshal(env.Data, into); err != nil {
		t.Fatalf("data: %v\n%s", err, string(env.Data))
	}
}

type galleryItem struct {
	MediaID      uuid.UUID `json:"media_id"`
	SortOrder    int       `json:"sort_order"`
	ImageURL     string    `json:"image_url"`
	ThumbnailURL string    `json:"thumbnail_url"`
	IsCover      bool      `json:"is_cover"`
}

type productJSON struct {
	ID           uuid.UUID `json:"id"`
	Title        string    `json:"title"`
	ImageURL     string    `json:"image_url"`
	ThumbnailURL string    `json:"thumbnail_url"`
	DiscountPct  *int      `json:"discount_pct"`
	CategoryID   *string   `json:"category_id"`
	CategoryName *string   `json:"category_name"`
	IsFavourite  *bool     `json:"is_favourite"`
}

// ─── Ownership on a gallery write ───────────────────────────────────────

// A seller may not put images on a product they do not own.
//
// This is the check that keeps a seller from restyling a competitor's
// listing, and it is separate from — and additional to — the media-ownership
// check below.
func TestASellerCannotSetTheGalleryOfSomebodyElsesProduct(t *testing.T) {
	f := seedStorefront(t)
	r, _ := storefrontEngine(t, f.intruderUser) // media-service says the intruder owns the asset

	w := call(t, r, http.MethodPost,
		"/v1/commerce/products/"+f.discounted.String()+"/media", f.intruderUser,
		map[string]any{"media_ids": []string{uuid.NewString()}})

	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403 — a seller just attached images to another "+
			"seller's listing\n%s", w.Code, w.Body.String())
	}
	var n int
	_ = edgePool.QueryRow(context.Background(),
		`SELECT count(*) FROM product_media WHERE product_id=$1`, f.discounted).Scan(&n)
	if n != 0 {
		t.Fatalf("%d media rows were written by a refused request", n)
	}
}

// Owning the product is not owning the photograph.
//
// The asset here belongs to somebody else, and the seller reads its id out of
// a competitor's public product JSON. media-service is the only authority on
// that, and the refusal must be a 403 rather than a 500.
func TestASellerCannotAttachAnAssetSomebodyElseUploaded(t *testing.T) {
	f := seedStorefront(t)
	victim := uuid.New()
	r, _ := storefrontEngine(t, victim) // every asset belongs to the victim

	w := call(t, r, http.MethodPost,
		"/v1/commerce/products/"+f.discounted.String()+"/media", f.sellerUserID,
		map[string]any{"media_ids": []string{uuid.NewString()}})

	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403 — a seller attached a competitor's product "+
			"photography to their own listing\n%s", w.Code, w.Body.String())
	}
}

// ─── The cap, and the order ─────────────────────────────────────────────

func TestAGalleryOfNineIsRefusedAndNothingIsWritten(t *testing.T) {
	f := seedStorefront(t)
	r, _ := storefrontEngine(t, f.sellerUserID)

	ids := make([]string, 9)
	for i := range ids {
		ids[i] = uuid.NewString()
	}
	w := call(t, r, http.MethodPost,
		"/v1/commerce/products/"+f.discounted.String()+"/media", f.sellerUserID,
		map[string]any{"media_ids": ids})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 for nine images\n%s", w.Code, w.Body.String())
	}
	var n int
	_ = edgePool.QueryRow(context.Background(),
		`SELECT count(*) FROM product_media WHERE product_id=$1`, f.discounted).Scan(&n)
	if n != 0 {
		t.Fatalf("%d rows written by a refused over-cap request", n)
	}
}

// The gallery goes in in the order it was sent, comes back in that order, and
// the first one is the cover — including after a reorder and after the cover
// itself is deleted.
func TestTheGalleryKeepsItsOrderAndItsCover(t *testing.T) {
	f := seedStorefront(t)
	r, _ := storefrontEngine(t, f.sellerUserID)
	ctx := context.Background()

	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	var set struct {
		Items []galleryItem `json:"items"`
	}
	decodeData(t, call(t, r, http.MethodPost,
		"/v1/commerce/products/"+f.discounted.String()+"/media", f.sellerUserID,
		map[string]any{"media_ids": ids}), &set)

	if len(set.Items) != 3 {
		t.Fatalf("gallery has %d items, want 3", len(set.Items))
	}
	for i, item := range set.Items {
		if item.MediaID.String() != ids[i] {
			t.Fatalf("position %d = %s, want %s", i, item.MediaID, ids[i])
		}
		if item.SortOrder != i {
			t.Fatalf("position %d has sort_order %d", i, item.SortOrder)
		}
		if item.ImageURL == "" || item.ThumbnailURL == "" {
			t.Fatalf("position %d came back with no URLs — a bare media UUID is "+
				"what made this route unusable from a phone", i)
		}
		if (i == 0) != item.IsCover {
			t.Fatalf("position %d: is_cover = %v", i, item.IsCover)
		}
	}

	// The cover is mirrored onto the legacy column, which is what the search
	// index and the order-item snapshot read.
	var primary *uuid.UUID
	if err := edgePool.QueryRow(ctx,
		`SELECT primary_image_media_id FROM products WHERE id=$1`, f.discounted).Scan(&primary); err != nil {
		t.Fatalf("reading the cover: %v", err)
	}
	if primary == nil || primary.String() != ids[0] {
		t.Fatalf("primary_image_media_id = %v, want the first image %s", primary, ids[0])
	}

	// Reorder: the third becomes the cover.
	reordered := []string{ids[2], ids[0], ids[1]}
	var after struct {
		Items []galleryItem `json:"items"`
	}
	decodeData(t, call(t, r, http.MethodPut,
		"/v1/commerce/products/"+f.discounted.String()+"/media/order", f.sellerUserID,
		map[string]any{"media_ids": reordered}), &after)
	if after.Items[0].MediaID.String() != ids[2] || !after.Items[0].IsCover {
		t.Fatalf("after reordering, the cover is %s", after.Items[0].MediaID)
	}

	// A reorder that is not a permutation of what is there is refused rather
	// than silently deleting the ids the client forgot to send.
	w := call(t, r, http.MethodPut,
		"/v1/commerce/products/"+f.discounted.String()+"/media/order", f.sellerUserID,
		map[string]any{"media_ids": []string{ids[0]}})
	if w.Code != http.StatusNotFound {
		t.Fatalf("a partial reorder returned %d; it must not be accepted as a delete\n%s",
			w.Code, w.Body.String())
	}

	// Deleting the cover MOVES the cover; it does not leave the product
	// pointing at an asset that is no longer in the gallery.
	var remaining struct {
		Items []galleryItem `json:"items"`
	}
	decodeData(t, call(t, r, http.MethodDelete,
		"/v1/commerce/products/"+f.discounted.String()+"/media/"+ids[2], f.sellerUserID, nil), &remaining)
	if len(remaining.Items) != 2 || remaining.Items[0].MediaID.String() != ids[0] {
		t.Fatalf("after deleting the cover the gallery is %+v", remaining.Items)
	}
	for i, item := range remaining.Items {
		if item.SortOrder != i {
			t.Fatalf("sort_order was left with a hole: %+v", remaining.Items)
		}
	}
	_ = edgePool.QueryRow(ctx,
		`SELECT primary_image_media_id FROM products WHERE id=$1`, f.discounted).Scan(&primary)
	if primary == nil || primary.String() != ids[0] {
		t.Fatalf("after deleting the cover, primary_image_media_id = %v", primary)
	}
}

// ─── Images and discounts on the browse grid ────────────────────────────

// The whole point: a product with a gallery renders with an image on the
// grid, in ONE media call for the page, with the discount already computed.
func TestTheBrowseGridCarriesImagesAndDiscountsInOneMediaCall(t *testing.T) {
	f := seedStorefront(t)
	r, batches := storefrontEngine(t, f.sellerUserID)

	// Attach a gallery, and NOTHING else — in particular, do not set
	// primary_image_media_id by hand. This is the case that used to render as
	// a grey box: the gallery editor never writes that column.
	decodeData(t, call(t, r, http.MethodPost,
		"/v1/commerce/products/"+f.discounted.String()+"/media", f.sellerUserID,
		map[string]any{"media_ids": []string{uuid.NewString(), uuid.NewString()}}),
		&struct {
			Items []galleryItem `json:"items"`
		}{})

	*batches = 0
	var page struct {
		Items []productJSON `json:"items"`
	}
	decodeData(t, call(t, r, http.MethodGet,
		"/v1/commerce/products?limit=50", f.shopper, nil), &page)

	if *batches != 1 {
		t.Fatalf("the browse page made %d media calls; a page must cost exactly one", *batches)
	}

	var found bool
	for _, p := range page.Items {
		if p.ID != f.discounted {
			continue
		}
		found = true
		if p.ImageURL == "" || p.ThumbnailURL == "" {
			t.Fatalf("the discounted product has no image on the grid: %+v", p)
		}
		if p.ImageURL == p.ThumbnailURL {
			t.Fatal("the display image and the grid thumbnail are the same URL")
		}
		if p.DiscountPct == nil || *p.DiscountPct != 25 {
			t.Fatalf("discount_pct = %v, want 25 (₹999 → ₹749)", p.DiscountPct)
		}
		if p.CategoryName == nil || *p.CategoryName != "SF Primary" {
			t.Fatalf("category_name = %v", p.CategoryName)
		}
	}
	if !found {
		t.Fatal("the discounted product is not in the browse page at all")
	}
}

// ─── The category filter that "found nothing" ───────────────────────────

// `?category_id=` is the spelling every client sends. The handler read
// `?category=`, so the filter was never applied and the response looked like
// "this category is empty" while quietly returning the whole catalogue.
func TestCategoryIdActuallyFilters(t *testing.T) {
	f := seedStorefront(t)
	r, _ := storefrontEngine(t, f.sellerUserID)

	var page struct {
		Items []productJSON `json:"items"`
	}
	decodeData(t, call(t, r, http.MethodGet,
		"/v1/commerce/products?limit=50&category_id="+f.categoryID.String(), uuid.Nil, nil), &page)

	if len(page.Items) == 0 {
		t.Fatal("?category_id= returned nothing for a category that has a product in it")
	}
	for _, p := range page.Items {
		if p.CategoryID == nil || *p.CategoryID != f.categoryID.String() {
			t.Fatalf("?category_id= returned a product from another category: %+v — "+
				"the filter was ignored and the caller got the whole catalogue", p)
		}
	}

	// The category strip's counts must agree with what the filter returns,
	// or a shopper taps a category advertising twelve products and lands on
	// four.
	var cats []struct {
		ID           uuid.UUID `json:"id"`
		ProductCount int       `json:"product_count"`
	}
	decodeData(t, call(t, r, http.MethodGet, "/v1/commerce/categories", uuid.Nil, nil), &cats)
	for _, c := range cats {
		if c.ID != f.categoryID {
			continue
		}
		if c.ProductCount != len(page.Items) {
			t.Fatalf("the strip says %d products, the filter returns %d",
				c.ProductCount, len(page.Items))
		}
	}
}

// ─── Favourites ─────────────────────────────────────────────────────────

// Hearting twice is the same as hearting once, and un-hearting twice is the
// same as un-hearting once. A double-tap, and the retry a flaky phone
// connection produces, are the same request.
func TestTheHeartIsIdempotentInBothDirections(t *testing.T) {
	f := seedStorefront(t)
	r, _ := storefrontEngine(t, f.sellerUserID)
	ctx := context.Background()

	body := map[string]any{"product_id": f.discounted.String()}
	for i := 0; i < 3; i++ {
		if w := call(t, r, http.MethodPost, "/v1/commerce/favourites", f.shopper, body); w.Code != http.StatusOK {
			t.Fatalf("favourite #%d returned %d: %s", i+1, w.Code, w.Body.String())
		}
	}
	var n int
	if err := edgePool.QueryRow(ctx,
		`SELECT count(*) FROM commerce_favourites WHERE user_id=$1 AND product_id=$2`,
		f.shopper, f.discounted).Scan(&n); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if n != 1 {
		t.Fatalf("three taps produced %d rows; the list would show the product %d times", n, n)
	}

	// The list carries full product summaries with images, so the favourites
	// screen renders with the same card component the grid uses.
	var page struct {
		Items []productJSON `json:"items"`
	}
	decodeData(t, call(t, r, http.MethodGet, "/v1/commerce/favourites", f.shopper, nil), &page)
	if len(page.Items) != 1 || page.Items[0].ID != f.discounted {
		t.Fatalf("favourites list = %+v", page.Items)
	}
	if page.Items[0].IsFavourite == nil || !*page.Items[0].IsFavourite {
		t.Fatal("a product in the favourites list did not report is_favourite")
	}

	// And the browse grid reflects it for this shopper, and only for them.
	var grid struct {
		Items []productJSON `json:"items"`
	}
	decodeData(t, call(t, r, http.MethodGet, "/v1/commerce/products?limit=50", f.shopper, nil), &grid)
	for _, p := range grid.Items {
		if p.ID == f.discounted && (p.IsFavourite == nil || !*p.IsFavourite) {
			t.Fatal("the grid did not show the shopper's own heart as filled")
		}
	}
	var anon struct {
		Items []productJSON `json:"items"`
	}
	decodeData(t, call(t, r, http.MethodGet, "/v1/commerce/products?limit=50", uuid.Nil, nil), &anon)
	for _, p := range anon.Items {
		if p.IsFavourite != nil {
			t.Fatalf("an anonymous browse received is_favourite=%v; it has no user to have "+
				"favourites and the client would draw a deliberate empty heart", *p.IsFavourite)
		}
	}

	// Removing twice: both succeed, and the row is gone once.
	for i := 0; i < 2; i++ {
		w := call(t, r, http.MethodDelete,
			"/v1/commerce/favourites/"+f.discounted.String(), f.shopper, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("unfavourite #%d returned %d: %s", i+1, w.Code, w.Body.String())
		}
	}
	_ = edgePool.QueryRow(ctx,
		`SELECT count(*) FROM commerce_favourites WHERE user_id=$1 AND product_id=$2`,
		f.shopper, f.discounted).Scan(&n)
	if n != 0 {
		t.Fatalf("%d favourite rows survive after removal", n)
	}
}

// ─── The landing page ───────────────────────────────────────────────────

// A section with products is present; a section with none is ABSENT, not an
// empty array. And the whole page — every section plus every banner —
// resolves its media in one call.
func TestTheHomePageOmitsEmptySectionsAndResolvesInOneCall(t *testing.T) {
	f := seedStorefront(t)
	r, batches := storefrontEngine(t, f.sellerUserID)

	decodeData(t, call(t, r, http.MethodPost,
		"/v1/commerce/products/"+f.discounted.String()+"/media", f.sellerUserID,
		map[string]any{"media_ids": []string{uuid.NewString()}}),
		&struct {
			Items []galleryItem `json:"items"`
		}{})

	*batches = 0
	var page struct {
		Banners  []map[string]any `json:"banners"`
		Sections []struct {
			Key      string        `json:"key"`
			Title    string        `json:"title"`
			Products []productJSON `json:"products"`
		} `json:"sections"`
	}
	decodeData(t, call(t, r, http.MethodGet, "/v1/commerce/home", f.shopper, nil), &page)

	if *batches > 1 {
		t.Fatalf("the home page made %d media calls; the app's first screen must cost one", *batches)
	}

	keys := map[string]bool{}
	for _, sec := range page.Sections {
		if len(sec.Products) == 0 {
			t.Fatalf("section %q was sent with no products — the client draws a heading "+
				"over a blank strip, which reads as a failed load", sec.Key)
		}
		if sec.Title == "" {
			t.Fatalf("section %q has no title to draw", sec.Key)
		}
		keys[sec.Key] = true
	}
	if !keys["deals"] {
		t.Fatal("there is no deals section, and the catalogue contains a product at 25 percent off")
	}
	if !keys["new_arrivals"] {
		t.Fatal("there is no new_arrivals section on a catalogue that was just created")
	}

	// Deals must contain only actual discounts. A "deal" at full price is the
	// fastest way to teach a shopper to ignore the section.
	for _, sec := range page.Sections {
		if sec.Key != "deals" {
			continue
		}
		for _, p := range sec.Products {
			if p.DiscountPct == nil || *p.DiscountPct <= 0 {
				t.Fatalf("deals contains %q with no discount", p.Title)
			}
		}
	}
}

// ─── Banner administration ──────────────────────────────────────────────

// Merchandising is behind the internal key. A seller who could write banners
// could put their own storefront on every shopper's home screen.
func TestBannersRequireTheInternalKeyAndThenAppearOnHome(t *testing.T) {
	f := seedStorefront(t)
	r, _ := storefrontEngine(t, f.sellerUserID)

	body := map[string]any{
		"title": "SF Test Banner", "subtitle": "integration",
		"target_type": "category", "target_id": f.categoryID.String(),
		"position": 5,
	}

	// No key: refused.
	if w := call(t, r, http.MethodPost, "/v1/commerce/internal/banners", f.sellerUserID, body); w.Code == http.StatusCreated {
		t.Fatal("a seller created a home-screen banner without the internal key")
	}

	// With the key: created, and it reaches the shopper's home page.
	req := httptest.NewRequest(http.MethodPost, "/v1/commerce/internal/banners", jsonBody(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", "storefront-test-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var created struct {
		ID uuid.UUID `json:"id"`
	}
	decodeData(t, w, &created)
	t.Cleanup(func() {
		_, _ = edgePool.Exec(context.Background(), `DELETE FROM commerce_banners WHERE id=$1`, created.ID)
	})

	var page struct {
		Banners []struct {
			ID         uuid.UUID `json:"id"`
			Title      string    `json:"title"`
			TargetType string    `json:"target_type"`
			TargetID   string    `json:"target_id"`
		} `json:"banners"`
	}
	decodeData(t, call(t, r, http.MethodGet, "/v1/commerce/home", uuid.Nil, nil), &page)
	for _, b := range page.Banners {
		if b.ID == created.ID {
			if b.TargetType != "category" || b.TargetID != f.categoryID.String() {
				t.Fatalf("the banner's target did not survive: %+v", b)
			}
			return
		}
	}
	t.Fatal("the created banner is not on the home page")
}

// ─── Commerce as a content authority ────────────────────────────────────

// The answer media-service's delivery gate needs, and did not have.
//
// Without it every product photograph came back `no_visible_post_or_story`
// for every shopper — post-service was being asked about a commerce asset —
// and the catalogue rendered as grey boxes however many images were attached.
func TestCommerceAnswersMediaAccessForLiveProductsOnly(t *testing.T) {
	f := seedStorefront(t)
	r, _ := storefrontEngine(t, f.sellerUserID)
	ctx := context.Background()

	live := uuid.NewString()
	decodeData(t, call(t, r, http.MethodPost,
		"/v1/commerce/products/"+f.discounted.String()+"/media", f.sellerUserID,
		map[string]any{"media_ids": []string{live}}),
		&struct {
			Items []galleryItem `json:"items"`
		}{})

	// A second product, taken back off sale, holding its own image.
	hidden := uuid.NewString()
	decodeData(t, call(t, r, http.MethodPost,
		"/v1/commerce/products/"+f.fullPrice.String()+"/media", f.sellerUserID,
		map[string]any{"media_ids": []string{hidden}}),
		&struct {
			Items []galleryItem `json:"items"`
		}{})
	if _, err := edgePool.Exec(ctx,
		`UPDATE products SET status='draft', approval_status='draft' WHERE id=$1`, f.fullPrice); err != nil {
		t.Fatalf("unpublishing: %v", err)
	}

	ask := func(viewer string, ids ...string) map[string]bool {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/v1/commerce/internal/media-access/batch",
			jsonBody(map[string]any{"viewer_id": viewer, "media_ids": ids}))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Service-Key", "storefront-test-key")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("media-access: %d %s", w.Code, w.Body.String())
		}
		// The BARE shape, not the shared envelope: media-service's authorizer
		// decodes `{"allowed":{…}}` and does not unwrap a `data` wrapper.
		var out struct {
			Allowed map[string]bool `json:"allowed"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decoding: %v\n%s", err, w.Body.String())
		}
		if out.Allowed == nil {
			t.Fatalf("no `allowed` map; media-service treats that as unresolved\n%s", w.Body.String())
		}
		return out.Allowed
	}

	stranger := uuid.NewString()
	unknown := uuid.NewString()

	// Anonymous, and a stranger, may see a LIVE product's photograph. That is
	// the whole point — a product page is public.
	for _, viewer := range []string{"", stranger} {
		got := ask(viewer, live, hidden, unknown)
		if !got[live] {
			t.Fatalf("viewer %q was denied a live product's image; the grid draws a grey box", viewer)
		}
		if got[hidden] {
			t.Fatalf("viewer %q was shown an unpublished product's image — a competitor "+
				"holding a media id reads the seller's pipeline", viewer)
		}
		// Every id asked about must be answered, or media-service reads the
		// silence as "unresolved" and retries forever.
		if _, ok := got[unknown]; !ok {
			t.Fatal("an unreferenced id was omitted from the answer rather than denied")
		}
		if got[unknown] {
			t.Fatal("an id attached to nothing was allowed")
		}
	}

	// The owning seller still sees their own unpublished product's image —
	// otherwise their draft editor is full of grey boxes.
	if got := ask(f.sellerUserID.String(), hidden); !got[hidden] {
		t.Fatal("the seller was denied their own draft product's image")
	}

	// And the single-asset form, which is what the non-batch gate path uses.
	req := httptest.NewRequest(http.MethodPost, "/v1/commerce/internal/media-access",
		jsonBody(map[string]any{"viewer_id": "", "media_id": live}))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", "storefront-test-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var one struct {
		Allowed bool `json:"allowed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &one); err != nil || !one.Allowed {
		t.Fatalf("single-asset media-access: %d %s", w.Code, w.Body.String())
	}
}

// A banner the client cannot open is refused with a sentence, not a
// constraint-violation 500.
func TestABannerWithAnUnopenableTargetIsRefused(t *testing.T) {
	r, _ := storefrontEngine(t, uuid.New())
	req := httptest.NewRequest(http.MethodPost, "/v1/commerce/internal/banners",
		jsonBody(map[string]any{
			"title": "Broken", "target_type": "category", "target_id": "running shoes",
		}))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", "storefront-test-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", w.Code, w.Body.String())
	}
}
