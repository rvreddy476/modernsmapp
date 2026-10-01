//go:build integration

package http

// Only approved sellers reach a buyer — over the real routes.
//
// Founder, 1 Oct 2026: "Only approved sellers show in the retail ...
// non-approved or status pending should not come into the customer portal."
//
// For every seller status other than 'approved', with store_status left at
// its default 'active' (the hole: that default is what every surface used to
// check), a PERFECT listing — active, approved, public, priced, in stock,
// categorised, with a gallery image, a banner pointing at it and a shopper's
// favourite — must be absent from every request a buyer can make. Then the
// real admin routes: approve shows it everywhere and announces it to search;
// suspend hides it everywhere and withdraws it from search.
//
//	COMMERCE_TEST_DSN=.../commerce_it_test go test -tags=integration ./internal/http/ -run ApprovedSellersIT -v

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type aoRig struct {
	r      *gin.Engine
	signer *servicetoken.Signer
	admin  uuid.UUID
}

func newAORig(t *testing.T, mediaOwner uuid.UUID) *aoRig {
	t.Helper()
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"SERVICE_CALLERS":                     "admin-service",
		"SERVICE_CALLER_ADMIN_SERVICE_KID":    "a1",
		"SERVICE_CALLER_ADMIN_SERVICE_PUBKEY": pub,
		"SERVICE_CALLER_ADMIN_SERVICE_OPS":    joinPerms(),
	}
	v, err := ServiceCallersFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	signer, err := servicetoken.NewSignerFromBase64(IssuerAdminService, "a1", priv)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := pii.New(devKeyProvider{}, []byte("approved-only-s!"))
	if err != nil {
		t.Fatal(err)
	}
	mc, _ := storefrontMedia(t, mediaOwner)
	svc := service.New(postgres.New(edgePool), nil, "").WithPII(cipher).WithMedia(mc)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(FenceMiddleware())
	h := New(svc).WithInternalKey(integrationInternalKey).WithServiceVerifier(v)
	h.RegisterRoutes(r)
	h.RegisterP0Routes(r)
	return &aoRig{r: r, signer: signer, admin: uuid.New()}
}

// adminPost calls a token-family admin route the way admin-service does.
func (rg *aoRig) adminPost(t *testing.T, path, perm string, body any) *httptest.ResponseRecorder {
	t.Helper()
	tok, err := rg.signer.Mint(AudienceCommerce, "admin-console", []string{perm}, nil, time.Minute,
		servicetoken.WithActor(rg.admin.String()))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(ServiceAuthHeader, "Bearer "+tok)
	w := httptest.NewRecorder()
	rg.r.ServeHTTP(w, req)
	return w
}

type aoListing struct {
	sellerID, sellerUser, productID, variantID uuid.UUID
	categoryID, mediaID, bannerID, shopper     uuid.UUID
	title                                      string
}

func seedAOListing(t *testing.T, sellerStatus string) aoListing {
	t.Helper()
	ctx := context.Background()
	l := aoListing{
		sellerID: uuid.New(), sellerUser: uuid.New(), productID: uuid.New(), variantID: uuid.New(),
		categoryID: uuid.New(), mediaID: uuid.New(), bannerID: uuid.New(), shopper: uuid.New(),
	}
	l.title = "AO HTTP probe " + l.productID.String()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := edgePool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v\nSQL: %s", err, sql)
		}
	}
	exec(`INSERT INTO sellers (id,user_id,store_name,slug,email,state,status,store_status)
	      VALUES ($1,$2,'AO Store',$3,'ao@example.test','KA',$4,'active')`,
		l.sellerID, l.sellerUser, "ao-"+l.sellerID.String()[:8], sellerStatus)
	exec(`INSERT INTO product_categories (id,name,slug,display_order,is_active) VALUES ($1,$2,$3,10,TRUE)`,
		l.categoryID, "AOH "+l.categoryID.String()[:8], "aoh-"+l.categoryID.String()[:8])
	exec(`INSERT INTO products (id,seller_id,category_id,title,slug,status,approval_status,visibility,return_policy_type,published_at)
	      VALUES ($1,$2,$3,$4,$5,'active','approved','public','7_days',NOW())`,
		l.productID, l.sellerID, l.categoryID, l.title, "aoh-"+l.productID.String()[:8])
	exec(`INSERT INTO product_variants (id,product_id,sku,mrp,selling_price,mrp_minor,selling_price_minor)
	      VALUES ($1,$2,$3,1000,500,100000,50000)`, l.variantID, l.productID, "AOH-"+l.variantID.String()[:8])
	exec(`INSERT INTO inventory_items (variant_id,seller_id,total_qty,reserved_qty) VALUES ($1,$2,25,0)`, l.variantID, l.sellerID)
	seedOfferFor(t, l.productID)
	exec(`INSERT INTO product_media (product_id,media_id,media_type,sort_order) VALUES ($1,$2,'image',0)`, l.productID, l.mediaID)
	exec(`INSERT INTO commerce_favourites (user_id,product_id) VALUES ($1,$2)`, l.shopper, l.productID)
	exec(`INSERT INTO commerce_banners (id,title,target_type,target_id,position,active)
	      VALUES ($1,'AO probe','product',$2,-2000000000,TRUE)`, l.bannerID, l.productID.String())
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = edgePool.Exec(bg, `DELETE FROM commerce_banners WHERE id=$1`, l.bannerID)
		_, _ = edgePool.Exec(bg, `DELETE FROM commerce_favourites WHERE product_id=$1`, l.productID)
		_, _ = edgePool.Exec(bg, `DELETE FROM cart_items WHERE product_id=$1`, l.productID)
		_, _ = edgePool.Exec(bg, `UPDATE products SET category_id=NULL WHERE id=$1`, l.productID)
		_, _ = edgePool.Exec(bg, `DELETE FROM product_categories WHERE id=$1`, l.categoryID)
	})
	return l
}

func aoDecode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return env
}

func aoItemsHave(items any, id uuid.UUID) bool {
	list, _ := items.([]any)
	for _, it := range list {
		if m, ok := it.(map[string]any); ok && m["id"] == id.String() {
			return true
		}
	}
	return false
}

// buyerView makes every buyer request and reports, per surface, whether the
// listing was visible / accepted there.
func (rg *aoRig) buyerView(t *testing.T, l aoListing) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	get := func(path string, actor uuid.UUID) *httptest.ResponseRecorder {
		t.Helper()
		return call(t, rg.r, http.MethodGet, path, actor, nil)
	}
	pid := l.productID.String()

	// Single-product reads: 200 visible, 404 PRODUCT_NOT_FOUND hidden.
	for _, p := range []string{
		"/v1/commerce/products/" + pid,
		"/v1/commerce/products/" + pid + "/media",
		"/v1/commerce/products/" + pid + "/attributes",
		"/v1/commerce/products/" + pid + "/variants",
		"/v1/commerce/products/" + pid + "/reviews",
		"/v1/commerce/products/" + pid + "/preview",
		"/v1/commerce/variants/" + l.variantID.String() + "/price-tiers",
	} {
		w := get(p, l.shopper)
		switch w.Code {
		case http.StatusOK:
			out["GET "+p] = true
		case http.StatusNotFound:
			if code := aoDecode(t, w)["error"].(map[string]any)["code"]; code != "PRODUCT_NOT_FOUND" {
				t.Fatalf("GET %s: 404 with code %v, want PRODUCT_NOT_FOUND (indistinguishable from a missing id)", p, code)
			}
			out["GET "+p] = false
		default:
			t.Fatalf("GET %s: %d %s", p, w.Code, w.Body.String())
		}
	}

	list := func(name, path string, actor uuid.UUID) {
		t.Helper()
		w := get(path, actor)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, w.Code, w.Body.String())
		}
		data := aoDecode(t, w)["data"]
		if m, ok := data.(map[string]any); ok {
			data = m["items"]
		}
		out[name] = aoItemsHave(data, l.productID)
	}
	list("GET /products?seller=", "/v1/commerce/products?seller="+l.sellerID.String(), uuid.Nil)
	list("GET /products?category_id=", "/v1/commerce/products?category_id="+l.categoryID.String(), uuid.Nil)
	list("GET /products?q=&in_stock&min_price", "/v1/commerce/products?in_stock=true&min_price=1&q="+l.productID.String(), uuid.Nil)
	list("GET /products?offset=&q=", "/v1/commerce/products?offset=0&q="+l.productID.String(), uuid.Nil)
	list("GET /sellers/:id/products", "/v1/commerce/sellers/"+l.sellerID.String()+"/products", uuid.Nil)
	list("GET /favourites", "/v1/commerce/favourites", l.shopper)

	// Home: every section, and the banners.
	w := get("/v1/commerce/home", uuid.Nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /home: %d %s", w.Code, w.Body.String())
	}
	home := aoDecode(t, w)["data"].(map[string]any)
	inSections := false
	for _, s := range home["sections"].([]any) {
		if aoItemsHave(s.(map[string]any)["products"], l.productID) {
			inSections = true
		}
	}
	out["GET /home sections"] = inSections
	out["GET /home banner"] = aoItemsHave(home["banners"], l.bannerID)

	// Category counts, flat and tree.
	for name, path := range map[string]string{
		"GET /categories count":      "/v1/commerce/categories",
		"GET /categories?tree count": "/v1/commerce/categories?tree=true",
	} {
		w := get(path, uuid.Nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: %d", path, w.Code)
		}
		out[name] = false
		for _, c := range aoDecode(t, w)["data"].([]any) {
			m := c.(map[string]any)
			if m["id"] == l.categoryID.String() {
				out[name] = m["product_count"] == float64(1)
			}
		}
	}

	// Writes a buyer makes: heart, add to cart.
	fw := call(t, rg.r, http.MethodPost, "/v1/commerce/favourites", uuid.New(), map[string]any{"product_id": pid})
	switch fw.Code {
	case http.StatusOK:
		out["POST /favourites"] = true
	case http.StatusNotFound:
		out["POST /favourites"] = false
	default:
		t.Fatalf("POST /favourites: %d %s", fw.Code, fw.Body.String())
	}
	cw := call(t, rg.r, http.MethodPost, "/v1/commerce/cart/items", uuid.New(),
		map[string]any{"variant_id": l.variantID.String(), "quantity": 1})
	switch cw.Code {
	case http.StatusOK, http.StatusCreated:
		out["POST /cart/items"] = true
	case http.StatusConflict:
		if code := aoDecode(t, cw)["error"].(map[string]any)["code"]; code != "PRODUCT_UNAVAILABLE" {
			t.Fatalf("POST /cart/items: 409 %v, want PRODUCT_UNAVAILABLE", code)
		}
		out["POST /cart/items"] = false
	default:
		t.Fatalf("POST /cart/items: %d %s", cw.Code, cw.Body.String())
	}

	// What search-service and media-service are told.
	sw := get("/v1/commerce/internal/products/"+pid+"/search-doc", uuid.Nil)
	if sw.Code != http.StatusOK {
		t.Fatalf("search-doc: %d %s", sw.Code, sw.Body.String())
	}
	out["GET /internal/products/:id/search-doc visible"] = aoDecode(t, sw)["data"].(map[string]any)["visible"] == true
	mw := call(t, rg.r, http.MethodPost, "/v1/commerce/internal/media-access", uuid.Nil,
		map[string]any{"viewer_id": "", "media_id": l.mediaID.String()})
	if mw.Code != http.StatusOK {
		t.Fatalf("media-access: %d %s", mw.Code, mw.Body.String())
	}
	out["POST /internal/media-access (anonymous)"] = aoDecode(t, mw)["allowed"] == true
	return out
}

func aoWant(t *testing.T, got map[string]bool, want bool, why string) {
	t.Helper()
	for surface, v := range got {
		if v != want {
			t.Errorf("%s: visible=%v, want %v — %s", surface, v, want, why)
		}
	}
}

func TestApprovedSellersIT_NonApprovedSellerAppearsNowhereABuyerCanReach(t *testing.T) {
	for _, status := range []string{"draft", "submitted", "under_review", "changes_required", "rejected", "suspended", "disabled"} {
		t.Run(status, func(t *testing.T) {
			l := seedAOListing(t, status)
			rg := newAORig(t, l.sellerUser)
			aoWant(t, rg.buyerView(t, l), false, "seller status "+status+" (store_status 'active')")

			// The owner keeps their own listing: the editor reads these routes.
			for _, p := range []string{
				"/v1/commerce/products/" + l.productID.String(),
				"/v1/commerce/products/" + l.productID.String() + "/preview",
				"/v1/commerce/products/" + l.productID.String() + "/variants",
				"/v1/commerce/products/" + l.productID.String() + "/media",
			} {
				if w := call(t, rg.r, http.MethodGet, p, l.sellerUser, nil); w.Code != http.StatusOK {
					t.Errorf("owner GET %s: %d, want 200 — the seller must still reach their own listing", p, w.Code)
				}
			}
		})
	}
}

func aoOutboxCount(t *testing.T, eventType string, productID uuid.UUID, since time.Time) int {
	t.Helper()
	var n int
	if err := edgePool.QueryRow(context.Background(), `
		SELECT count(*) FROM outbox_events
		 WHERE event_type = $1 AND payload->'payload'->>'product_id' = $2 AND created_at >= $3`,
		eventType, productID.String(), since).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestApprovedSellersIT_ApproveShowsAndAnnouncesSuspendHidesAndWithdraws(t *testing.T) {
	l := seedAOListing(t, "submitted")
	rg := newAORig(t, l.sellerUser)
	base := "/v1/commerce/internal/admin/sellers/" + l.sellerID.String()

	aoWant(t, rg.buyerView(t, l), false, "submitted, not yet approved")

	var since time.Time
	if err := edgePool.QueryRow(context.Background(), `SELECT clock_timestamp()`).Scan(&since); err != nil {
		t.Fatal(err)
	}
	if w := rg.adminPost(t, base+"/approve", PermSellerApprove, map[string]any{"notes": "ok"}); w.Code/100 != 2 {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	}
	aoWant(t, rg.buyerView(t, l), true, "approved: the perfect listing is everywhere")
	if n := aoOutboxCount(t, "commerce.product.published", l.productID, since); n != 1 {
		t.Errorf("approve published %d commerce.product.published events for the listing, want 1 — search would never hear it", n)
	}

	if err := edgePool.QueryRow(context.Background(), `SELECT clock_timestamp()`).Scan(&since); err != nil {
		t.Fatal(err)
	}
	if w := rg.adminPost(t, base+"/suspend", PermSellerSuspend, map[string]any{"reason": "review"}); w.Code/100 != 2 {
		t.Fatalf("suspend: %d %s", w.Code, w.Body.String())
	}
	aoWant(t, rg.buyerView(t, l), false, "suspended: gone from every surface again")
	if n := aoOutboxCount(t, "commerce.product.unpublished", l.productID, since); n != 1 {
		t.Errorf("suspend published %d commerce.product.unpublished events for the listing, want 1 — "+
			"search-service would keep showing a suspended seller's product", n)
	}
}
