package http

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/atpost/food-service/database"
	"github.com/atpost/food-service/internal/pricing"
	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/kyc"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Wave 1 B3 over the routes, on TEST_PG_DSN (food_it_test, -p 1).

func b3IntegrationRouter(t *testing.T) (*gin.Engine, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping food-service HTTP integration tests")
	}
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatal("TEST_PG_DSN must parse and name a database ending in _test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	pricingCfg := pricing.DefaultConfig()
	d, err := kyc.GSTINCheckDigit("29ZZZCZ9999Z1Z")
	if err != nil {
		t.Fatal(err)
	}
	pricingCfg.PlatformGSTIN = "29ZZZCZ9999Z1Z" + string(d)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(service.New(postgres.New(pool).WithPricingConfig(pricingCfg))).RegisterRoutes(router)
	return router, pool
}

func TestPatchRestaurantLocationRefusedOverRoutes(t *testing.T) {
	r, pool := b3IntegrationRouter(t)
	ctx := context.Background()
	owner := uuid.New()
	rid, base := createRestaurantViaAPI(t, r, owner)
	expectStatus(t, doJSON(r, http.MethodPut, base+"/location", itLocation, owner, false), http.StatusOK, "location")

	for _, body := range []string{
		`{"name":"Moved Kitchen","latitude":13.5,"longitude":78.1}`,
		`{"name":"Moved Kitchen","address_line1":"9 Other Road","city":"Chennai"}`,
	} {
		rec := doJSON(r, http.MethodPatch, base, body, owner, false)
		expectStatus(t, rec, http.StatusUnprocessableEntity, "patch location fields")
		if errorCode(t, rec) != "FOOD_USE_LOCATION_ROUTE" {
			t.Fatalf("code = %s", errorCode(t, rec))
		}
	}
	// The slug is derived from the name and unique across restaurants, and
	// food_it_test keeps rows between runs, so the new name is unique per run.
	renamed := "Renamed IT Kitchen " + uuid.NewString()[:8]
	expectStatus(t, doJSON(r, http.MethodPatch, base, `{"name":"`+renamed+`","packaging_fee":7}`, owner, false), http.StatusOK, "patch profile")

	var lat, lng, areaLat float64
	var line1, city, name string
	if err := pool.QueryRow(ctx, `
		SELECT r.latitude::float8, r.longitude::float8, r.address_line1, r.city, r.name,
			(SELECT center_latitude::float8 FROM food.restaurant_service_areas WHERE restaurant_id = r.id AND is_active)
		FROM food.restaurants r WHERE r.id = $1`, rid).Scan(&lat, &lng, &line1, &city, &name, &areaLat); err != nil {
		t.Fatal(err)
	}
	if lat != 12.9716 || lng != 77.5946 || line1 != "1 Test Lane" || city != "Bengaluru" || areaLat != 12.9716 || name != renamed {
		t.Fatalf("restaurant = %v %v %s %s %s (area %v)", lat, lng, line1, city, name, areaLat)
	}
}

var fpInvoiceRe = regexp.MustCompile(`^FP/\d{4}/\d{6}$`)

func TestInvoiceFromARealOrderOverRoutes(t *testing.T) {
	r, pool := b3IntegrationRouter(t)
	ctx := context.Background()
	owner, customer := uuid.New(), uuid.New()
	rid, _ := createRestaurantViaAPI(t, r, owner)
	if _, err := pool.Exec(ctx, `
		UPDATE food.restaurants
		SET status = 'ACTIVE', is_open = TRUE, is_accepting_orders = TRUE, latitude = 12.9716, longitude = 77.5946,
			state = 'Karnataka', tax_category = 'RESTAURANT_SPECIFIED_PREMISES', gstin = $2, legal_name = 'IT Kitchens LLP'
		WHERE id = $1`, rid, itGSTIN(t)); err != nil {
		t.Fatal(err)
	}
	seedAvailableMenuItem(t, pool, rid)
	var itemID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM food.menu_items WHERE restaurant_id = $1`, rid).Scan(&itemID); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, doJSON(r, http.MethodPost, "/v1/food/cart/items", `{"menu_item_id":"`+itemID.String()+`","quantity":2}`, customer, false), http.StatusCreated, "add to cart")
	addr := doJSON(r, http.MethodPost, "/v1/food/addresses", `{"address_line1":"2 Test Road","city":"Bengaluru","latitude":12.98,"longitude":77.5946}`, customer, false)
	expectStatus(t, addr, http.StatusCreated, "address")
	placed := doJSON(r, http.MethodPost, "/v1/food/orders", `{"address_id":"`+dataField(t, addr, "id").(string)+`","payment_method":"upi"}`, customer, false)
	expectStatus(t, placed, http.StatusCreated, "place order")
	orderID := dataField(t, placed, "id").(string)
	// 2 x 60.00 at 18% (restaurant-liable) = 21.60; fees 5.00 + 29.00 at 18% = 6.12.
	if final := dataField(t, placed, "money", "totals_paise", "final_amount_paise"); final != float64(18172) {
		t.Fatalf("final_amount_paise = %v", final)
	}

	rec := doJSON(r, http.MethodGet, "/v1/food/orders/"+orderID+"/invoice?format=json", ``, customer, false)
	expectStatus(t, rec, http.StatusOK, "invoice json")
	var doc struct {
		Sections []struct {
			Issuer        string `json:"issuer"`
			InvoiceNumber string `json:"invoice_number"`
			IssuerGSTIN   string `json:"issuer_gstin"`
			TotalPaise    int64  `json:"total_paise"`
		} `json:"sections"`
		GrandTotalPaise int64  `json:"grand_total_paise"`
		AdviserMarker   string `json:"adviser_marker"`
	}
	if err := json.Unmarshal(decodeEnvelope(t, rec).Data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Sections) != 2 || doc.Sections[0].Issuer != "RESTAURANT" || doc.Sections[0].IssuerGSTIN != itGSTIN(t) ||
		doc.Sections[1].Issuer != "PLATFORM" || !fpInvoiceRe.MatchString(doc.Sections[1].InvoiceNumber) ||
		doc.GrandTotalPaise != 18172 || doc.Sections[0].TotalPaise+doc.Sections[1].TotalPaise != 18172 ||
		doc.AdviserMarker != pricing.AdviserNotice {
		t.Fatalf("invoice = %+v", doc)
	}

	html := doJSON(r, http.MethodGet, "/v1/food/orders/"+orderID+"/invoice", ``, customer, false)
	expectStatus(t, html, http.StatusOK, "invoice html")
	if !strings.Contains(html.Body.String(), pricing.AdviserNotice) || html.Header().Get("X-Invoice-Number") != doc.Sections[1].InvoiceNumber {
		t.Fatalf("html invoice number %q, want %q", html.Header().Get("X-Invoice-Number"), doc.Sections[1].InvoiceNumber)
	}
}
