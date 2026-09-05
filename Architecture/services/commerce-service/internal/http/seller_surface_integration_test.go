//go:build integration

package http

// The seller surface, over the real registered routes.
//
// Everything here goes through the same gin engine cmd/server builds â the
// same handlers, the same service, the same store, against a live PostgreSQL.
// That matters because three of the four defects proven below were wiring
// defects: the store did the right thing and the layer above it asked for the
// wrong thing, or asked nobody at all.
//
//	POST   /v1/commerce/sellers/:id/products     leaked drafts and rejections
//	PATCH  /v1/commerce/seller/variants/:id/stock did not exist â stock was set-once
//	GET    /v1/commerce/seller/products          did not exist
//	PUT    /v1/commerce/seller/address           did not exist â no pickup origin
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/http/... -v

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/atpost/commerce-service/internal/media"
	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// âââ A seller with a catalogue in every state ââââââââââââââââââââââââââ

type sellerSurface struct {
	sellerUserID uuid.UUID
	sellerID     uuid.UUID
	variantID    uuid.UUID
	otherUserID  uuid.UUID // a second seller, for the cross-tenant checks
	otherVariant uuid.UUID
}

func seedSellerSurface(t *testing.T, stock int) sellerSurface {
	t.Helper()
	ctx := context.Background()
	s := sellerSurface{
		sellerUserID: uuid.New(), sellerID: uuid.New(),
		variantID: uuid.New(), otherUserID: uuid.New(), otherVariant: uuid.New(),
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := edgePool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v\nSQL: %s", err, sql)
		}
	}

	seedSeller := func(sellerID, userID, variantID uuid.UUID, tag string, withDrafts bool) {
		exec(`INSERT INTO sellers (id,user_id,store_name,slug,email,state,status)
		      VALUES ($1,$2,$3,$4,$5,'KA','approved')`,
			sellerID, userID, tag+" Store", tag+"-"+sellerID.String()[:8],
			tag+"@example.test")

		live := uuid.New()
		exec(`INSERT INTO products (id,seller_id,title,slug,status,approval_status,return_policy_type)
		      VALUES ($1,$2,$3,$4,'active','approved','7_days')`,
			live, sellerID, tag+" Live Product", tag+"-live-"+live.String()[:8])
		exec(`INSERT INTO product_variants (id,product_id,sku,mrp,selling_price,mrp_minor,selling_price_minor)
		      VALUES ($1,$2,$3,100,100,10000,10000)`,
			variantID, live, "SKU-"+variantID.String()[:8])
		exec(`INSERT INTO inventory_items (variant_id,seller_id,total_qty,reserved_qty)
		      VALUES ($1,$2,$3,0)`, variantID, sellerID, stock)
		seedOfferFor(t, live)

		if !withDrafts {
			return
		}
		for _, r := range []struct{ title, status, approval string }{
			{tag + " Secret Draft", "draft", "draft"},
			{tag + " Rejected By Moderation", "draft", "rejected"},
		} {
			id := uuid.New()
			exec(`INSERT INTO products (id,seller_id,title,slug,status,approval_status,return_policy_type)
			      VALUES ($1,$2,$3,$4,$5,$6,'7_days')`,
				id, sellerID, r.title, "d-"+id.String()[:8], r.status, r.approval)
			seedOfferFor(t, id)
		}
	}

	seedSellerSurfaceTag := "Surface"
	seedSeller(s.sellerID, s.sellerUserID, s.variantID, seedSellerSurfaceTag, true)
	seedSeller(uuid.New(), s.otherUserID, s.otherVariant, "Other", false)
	return s
}

func call(t *testing.T, r interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, method, path string, actor uuid.UUID, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if actor != uuid.Nil {
		req.Header.Set("X-User-Id", actor.String())
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// âââ The storefront leak âââââââââââââââââââââââââââââââââââââââââââââââ

// GET /sellers/:sellerId/products is unauthenticated. It must show only what
// is on sale.
func TestThePublicStorefrontRouteHidesUnreleasedProducts(t *testing.T) {
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 10)

	// No X-User-Id at all â this is what an anonymous caller sees.
	w := call(t, r, http.MethodGet, "/v1/commerce/sellers/"+s.sellerID.String()+"/products",
		uuid.Nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, secret := range []string{"Secret Draft", "Rejected By Moderation"} {
		if bytes.Contains([]byte(body), []byte(secret)) {
			t.Fatalf("the anonymous storefront response contains %q â a competitor holding "+
				"this seller id reads their unreleased pipeline and their moderation "+
				"failures\n%s", secret, body)
		}
	}
	if !bytes.Contains([]byte(body), []byte("Surface Live Product")) {
		t.Fatalf("the storefront hid the product that is actually on sale\n%s", body)
	}
}

// The seller's own dashboard route shows everything, for their own catalogue
// only, resolved from the caller rather than the URL.
func TestTheSellerDashboardRouteShowsTheirOwnDraftsAndOnlyTheirs(t *testing.T) {
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 10)

	w := call(t, r, http.MethodGet, "/v1/commerce/seller/products", s.sellerUserID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.Bytes()
	for _, own := range []string{"Surface Secret Draft", "Surface Rejected By Moderation"} {
		if !bytes.Contains(body, []byte(own)) {
			t.Fatalf("the seller cannot see their own %q; a rejected product they cannot "+
				"see is one they cannot fix\n%s", own, w.Body.String())
		}
	}
	if bytes.Contains(body, []byte("Other Live Product")) {
		t.Fatalf("the dashboard returned another seller's catalogue\n%s", w.Body.String())
	}
}

// A caller with no seller profile gets 403, not another seller's rows and not
// a 500.
func TestTheSellerDashboardRefusesANonSeller(t *testing.T) {
	r := journeyEngine(t, 4000)
	seedSellerSurface(t, 10)

	w := call(t, r, http.MethodGet, "/v1/commerce/seller/products", uuid.New(), nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403\n%s", w.Code, w.Body.String())
	}
}

// âââ Stock, which was set-once âââââââââââââââââââââââââââââââââââââââââ

type stockBody struct {
	TotalQty    int `json:"total_qty"`
	ReservedQty int `json:"reserved_qty"`
	Available   int `json:"available"`
}

func TestASellerCanRestockOverHTTP(t *testing.T) {
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 0) // sold out

	w := call(t, r, http.MethodPatch,
		"/v1/commerce/seller/variants/"+s.variantID.String()+"/stock",
		s.sellerUserID, map[string]any{"delta": 25, "reason": "purchase"})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 â a sold-out seller has no way back\n%s",
			w.Code, w.Body.String())
	}
	got := decodeStock(t, w.Body.Bytes())
	if got.TotalQty != 25 || got.Available != 25 {
		t.Fatalf("stock = %+v, want 25 total / 25 available", got)
	}

	// And the read-back route agrees.
	w = call(t, r, http.MethodGet,
		"/v1/commerce/seller/variants/"+s.variantID.String()+"/stock", s.sellerUserID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("read-back status %d: %s", w.Code, w.Body.String())
	}
	read := decodeStock(t, w.Body.Bytes())
	if read.TotalQty != 25 {
		t.Fatalf("read-back total = %d, want 25", read.TotalQty)
	}
}

// Ownership failures are 403, not 500. Before writeCommerceError learned these
// sentinels every cross-tenant attempt surfaced as an internal error, which
// tells the caller nothing and pages the on-call for an authorisation event.
func TestAdjustingAnotherSellersStockIsForbiddenNotAnInternalError(t *testing.T) {
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 10)

	w := call(t, r, http.MethodPatch,
		"/v1/commerce/seller/variants/"+s.otherVariant.String()+"/stock",
		s.sellerUserID, map[string]any{"delta": -10, "reason": "correction"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403\n%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("NOT_YOUR_VARIANT")) {
		t.Fatalf("body does not name the reason\n%s", w.Body.String())
	}

	// The other seller's stock is untouched.
	var total int
	if err := edgePool.QueryRow(context.Background(),
		`SELECT total_qty FROM inventory_items WHERE variant_id=$1`, s.otherVariant).
		Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 10 {
		t.Fatalf("the other seller's stock is now %d, want 10", total)
	}
}

// An omitted delta is a 400, not a silent zero-op that reports success.
func TestAnOmittedDeltaIsRejected(t *testing.T) {
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 10)

	w := call(t, r, http.MethodPatch,
		"/v1/commerce/seller/variants/"+s.variantID.String()+"/stock",
		s.sellerUserID, map[string]any{"reason": "recount"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400\n%s", w.Code, w.Body.String())
	}
}

// A reason code outside the allow-list is a 400 that names the codes, not a
// 500 from the inventory_adjustments CHECK constraint.
func TestAnUnknownReasonCodeIsABadRequest(t *testing.T) {
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 10)

	w := call(t, r, http.MethodPatch,
		"/v1/commerce/seller/variants/"+s.variantID.String()+"/stock",
		s.sellerUserID, map[string]any{"delta": 1, "reason": "shrinkage"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 â a CHECK violation would surface as 500\n%s",
			w.Code, w.Body.String())
	}
}

// âââ The pickup address ââââââââââââââââââââââââââââââââââââââââââââââââ

// The seller's pickup point, written sealed, over the real route.
func TestASellerCanSaveTheirPickupAddressOverHTTP(t *testing.T) {
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 10)

	w := call(t, r, http.MethodPut, "/v1/commerce/seller/address", s.sellerUserID,
		map[string]any{
			"address_type": "pickup", "contact_name": "Warehouse Desk",
			"phone": "9000000000", "address_line_1": "1 Warehouse Rd",
			"city": "Bengaluru", "state": "KA", "postal_code": "560068",
		})
	if w.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204\n%s", w.Code, w.Body.String())
	}

	var enc []byte
	var ver *int
	var plainPin string
	if err := edgePool.QueryRow(context.Background(),
		`SELECT address_line_1_enc, pii_key_version, postal_code
		   FROM seller_addresses WHERE seller_id=$1 AND address_type='pickup'`,
		s.sellerID).Scan(&enc, &ver, &plainPin); err != nil {
		t.Fatalf("no pickup address was stored: %v", err)
	}
	if len(enc) == 0 || ver == nil || *ver <= 0 {
		t.Fatalf("stored unsealed: ciphertext=%d bytes key_version=%v â the gated PII scrub "+
			"would clear the plaintext and leave no address at all", len(enc), ver)
	}
	if plainPin != "560068" {
		t.Fatalf("postal_code = %q, want 560068 â this is the courier's origin", plainPin)
	}
}

// A pickup address with no state is refused at the edge: the seller's state is
// half of the GST place-of-supply comparison.
func TestAPickupAddressWithoutAStateIsRejected(t *testing.T) {
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 10)

	w := call(t, r, http.MethodPut, "/v1/commerce/seller/address", s.sellerUserID,
		map[string]any{
			"contact_name": "Desk", "phone": "9000000000",
			"address_line_1": "1 Warehouse Rd", "city": "Bengaluru", "postal_code": "560068",
		})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 â an interstate sale would be billed CGST+SGST\n%s",
			w.Code, w.Body.String())
	}
}

// âââ A database incident is not an authorisation failure âââââââââââââââ

// Eighteen handlers mapped ANY error from GetSellerProfile to 403 NO_SELLER
// (one to 404 NOT_FOUND). A dropped connection, a statement timeout, a
// failed-over primary â each was reported to the seller as "seller account
// not found", and logged as nothing at all.
//
// A seller whose dashboard says their account does not exist during a database
// incident does not retry. They open a ticket saying their account is gone.
func TestADatabaseFailureIsNotReportedAsAMissingSellerAccount(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("COMMERCE_TEST_DSN")
	dead, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	dead.Close() // every query from here on fails as a transport error

	cipher, err := pii.New(devKeyProvider{}, []byte("outage-test-salt"))
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.Use(FenceMiddleware())
	New(service.New(postgres.New(dead), nil, "").WithPII(cipher)).RegisterRoutes(r)

	for _, path := range []string{
		"/v1/commerce/seller/orders",
		"/v1/commerce/sellers/me",
	} {
		w := call(t, r, http.MethodGet, path, uuid.New(), nil)
		if w.Code == http.StatusForbidden || w.Code == http.StatusNotFound {
			t.Errorf("%s returned %d during a database outage â the seller is told their "+
				"account does not exist\n%s", path, w.Code, w.Body.String())
			continue
		}
		if w.Code != http.StatusInternalServerError {
			t.Errorf("%s returned %d, want 500\n%s", path, w.Code, w.Body.String())
		}
	}
}

// And the ordinary case still answers 403: this user is simply not a seller.
func TestANonSellerStillGetsForbidden(t *testing.T) {
	r := journeyEngine(t, 4000)
	seedSellerSurface(t, 1)

	w := call(t, r, http.MethodGet, "/v1/commerce/seller/orders", uuid.New(), nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403\n%s", w.Code, w.Body.String())
	}
}

// âââ Media ownership, over the real routes âââââââââââââââââââââââââââââ

// mediaStub stands in for media-service. It answers for exactly one asset and
// records nothing else, because what is under test is commerce's refusal, not
// media-service.
func mediaStub(t *testing.T, assetID, uploaderID uuid.UUID, fileType string) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, assetID.String()) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"id": assetID, "uploader_id": uploaderID, "file_type": fileType,
			"processing_status": "ready", "moderation_status": "passed",
		}})
	}))
	t.Cleanup(s.Close)
	return s.URL
}

func mediaEngine(t *testing.T, mediaURL string) *gin.Engine {
	t.Helper()
	cipher, err := pii.New(devKeyProvider{}, []byte("media-ownership-test-salt-0123"))
	if err != nil {
		t.Fatal(err)
	}
	svc := service.New(postgres.New(edgePool), nil, "").
		WithCourier(stubCourier{chargeMinor: 4000}).
		WithPII(cipher).
		WithMedia(media.New(mediaURL, "k"))
	r := gin.New()
	r.Use(FenceMiddleware())
	// Both halves of the real route table. The seller surface — readiness,
	// stock, the catalogue — lives in RegisterP0Routes, and a harness that
	// registers only one half answers 404 for routes production serves.
	h := New(svc)
	h.RegisterRoutes(r)
	h.RegisterP0Routes(r)
	return r
}

// The defect: a seller reads a media id out of a competitor's public product
// JSON and hangs it off their own listing.
func TestASellerCannotPublishAnotherAccountsPhotography(t *testing.T) {
	s := seedSellerSurface(t, 5)
	victimUpload := uuid.New()
	r := mediaEngine(t, mediaStub(t, victimUpload, s.otherUserID, "image"))

	w := call(t, r, http.MethodPost, "/v1/commerce/products", s.sellerUserID, map[string]any{
		"title":                  "Borrowed Photography",
		"primary_image_media_id": victimUpload,
		"tax_class_id":           gstClass(t),
		"variants":               []map[string]any{{"sku": "SKU-" + uuid.New().String()[:8], "mrp": 100, "selling_price": 100, "stock_qty": 1}},
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403 â the listing was created using another account's "+
			"upload\n%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("NOT_YOUR_MEDIA")) {
		t.Fatalf("body does not name the reason\n%s", w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte(s.otherUserID.String())) {
		t.Fatalf("the refusal named the real uploader â an ownership oracle\n%s", w.Body.String())
	}
}

// The seller's own upload still works, so the check is a gate and not a wall.
func TestASellerCanPublishTheirOwnPhotography(t *testing.T) {
	s := seedSellerSurface(t, 5)
	ownUpload := uuid.New()
	r := mediaEngine(t, mediaStub(t, ownUpload, s.sellerUserID, "image"))

	w := call(t, r, http.MethodPost, "/v1/commerce/products", s.sellerUserID, map[string]any{
		"title":                  "My Own Photography",
		"primary_image_media_id": ownUpload,
		"tax_class_id":           gstClass(t),
		"variants":               []map[string]any{{"sku": "SKU-" + uuid.New().String()[:8], "mrp": 100, "selling_price": 100, "stock_qty": 1}},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201 â a seller cannot use their own image\n%s",
			w.Code, w.Body.String())
	}
}

// A media-service outage must block the write, not wave it through â and must
// read as retryable rather than as the seller's fault.
func TestAMediaServiceOutageBlocksTheWriteAsRetryable(t *testing.T) {
	s := seedSellerSurface(t, 5)
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := dead.URL
	dead.Close()
	r := mediaEngine(t, url)

	w := call(t, r, http.MethodPost, "/v1/commerce/products", s.sellerUserID, map[string]any{
		"title":                  "During An Outage",
		"primary_image_media_id": uuid.New(),
		"tax_class_id":           gstClass(t),
		"variants":               []map[string]any{{"sku": "SKU-" + uuid.New().String()[:8], "mrp": 100, "selling_price": 100, "stock_qty": 1}},
	})
	if w.Code == http.StatusCreated {
		t.Fatal("an unverified media reference was stored because media-service was down")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 â the seller did nothing wrong and must be told to "+
			"retry\n%s", w.Code, w.Body.String())
	}
}

// A product with no image at all is unaffected: the field is optional and an
// unset optional must not become a refusal.
func TestAProductWithNoImageIsUnaffected(t *testing.T) {
	s := seedSellerSurface(t, 5)
	r := mediaEngine(t, mediaStub(t, uuid.New(), s.sellerUserID, "image"))

	w := call(t, r, http.MethodPost, "/v1/commerce/products", s.sellerUserID, map[string]any{
		"title":        "No Image Yet",
		"tax_class_id": gstClass(t),
		"variants":     []map[string]any{{"sku": "SKU-" + uuid.New().String()[:8], "mrp": 100, "selling_price": 100, "stock_qty": 1}},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201\n%s", w.Code, w.Body.String())
	}
}

// âââ Product images can be rendered ââââââââââââââââââââââââââââââââââââ

// Commerce returned a bare media UUID and nothing else, so no product screen
// could draw an image. The Android core:commerce module has no dependency on
// core:media (the resolver lives there) and giving it one would pull the whole
// ExoPlayer stack into a module that needs a URL string. Resolving server-side
// fixes it once for every client, iOS included.
func TestAProductListCarriesRenderableImageURLs(t *testing.T) {
	ctx := context.Background()
	s := seedSellerSurface(t, 5)
	mediaID := uuid.New()

	// Give the live product an image.
	if _, err := edgePool.Exec(ctx,
		`UPDATE products SET primary_image_media_id = $2
		  WHERE seller_id = $1 AND status = 'active'`, s.sellerID, mediaID); err != nil {
		t.Fatal(err)
	}

	batch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			// media-service's REAL rendition names — see
			// internal/processing/image.go there. This stub used to say
			// "thumbnail" and "medium", names media-service has never
			// produced, and it passed anyway: the resolver's preference list
			// used the same invented names, so a fake and a bug agreed with
			// each other while every real product tile served the
			// full-resolution `original`.
			mediaID.String(): map[string]any{"variants": map[string]string{
				"thumb_150":   "https://cdn.example/t.jpg",
				"medium_1080": "https://cdn.example/m.jpg",
			}},
		}})
	}))
	defer batch.Close()

	r := mediaEngine(t, batch.URL)
	w := call(t, r, http.MethodGet,
		"/v1/commerce/sellers/"+s.sellerID.String()+"/products", uuid.Nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("https://cdn.example/m.jpg")) {
		t.Fatalf("the product list carries no image URL â every product screen draws a "+
			"placeholder\n%s", w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("https://cdn.example/t.jpg")) {
		t.Fatalf("no thumbnail URL for the grid\n%s", w.Body.String())
	}
}

// And when media-service is down the catalogue still loads. This is the
// opposite rule from the write path, on purpose: a catalogue that will not
// render because the image service is down is worse than grey boxes.
func TestTheCatalogueStillLoadsWhenMediaServiceIsDown(t *testing.T) {
	ctx := context.Background()
	s := seedSellerSurface(t, 5)
	if _, err := edgePool.Exec(ctx,
		`UPDATE products SET primary_image_media_id = $2
		  WHERE seller_id = $1 AND status = 'active'`, s.sellerID, uuid.New()); err != nil {
		t.Fatal(err)
	}

	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := dead.URL
	dead.Close()

	r := mediaEngine(t, url)
	w := call(t, r, http.MethodGet,
		"/v1/commerce/sellers/"+s.sellerID.String()+"/products", uuid.Nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d â the whole catalogue failed because the image service is "+
			"down\n%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("Surface Live Product")) {
		t.Fatalf("the product itself is missing from the response\n%s", w.Body.String())
	}
}

// gstClass returns the id of an ordinary GST rate.
//
// Every product-create call needs one, because a product without a tax class
// is one checkout refuses with PRODUCT_TAX_UNCONFIGURED â so the create route
// refuses it up front rather than letting a seller list something no buyer can
// complete.
func gstClass(t *testing.T) string {
	t.Helper()
	var id string
	if err := edgePool.QueryRow(context.Background(),
		`SELECT id FROM tax_classes WHERE name = 'GST 18%'`).Scan(&id); err != nil {
		t.Fatalf("tax class: %v", err)
	}
	return id
}

// decodeStock reads a stock level out of the API envelope.
//
// Through `data`, because that is what the client's ApiEnvelope requires. An
// earlier revision of these tests read the body directly, which is exactly how
// six seller routes shipped answering with the raw payload â the test agreed
// with the bug rather than with the client.
func decodeStock(t *testing.T, body []byte) stockBody {
	t.Helper()
	var env struct {
		Data stockBody `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	return env.Data
}
