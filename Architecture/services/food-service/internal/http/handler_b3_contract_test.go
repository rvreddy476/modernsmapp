package http

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/pricing"
	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/settlement"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/kyc"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Golden contract fixtures for Wave 1 B3: cart totals with taxes_and_charges,
// the order detail money block, the invoice, the admin settlement row, the
// admin payout-account review view and the B3 refusals. Every money figure is
// produced by the real pricing / settlement code from a fixed synthetic cart,
// so a fixture drifts when the arithmetic does. Regenerate deliberately with
// UPDATE_CONTRACTS=1 and review the diff.

var (
	ctCustomer    = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0010")
	ctCart        = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0011")
	ctCartItem    = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0012")
	ctMenuItem    = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0013")
	ctAddon       = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0014")
	ctOrder       = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0015")
	ctOrderItem   = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0016")
	ctOrderAddon  = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0017")
	ctSettlement  = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0018")
	ctPayoutRowID = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0019")
)

// ctPlatformPAN is synthetic, like ctPAN.
const ctPlatformPAN = "ZZZCZ9999Z"

func ctPlatformGSTIN() string {
	d, err := kyc.GSTINCheckDigit("29" + ctPlatformPAN + "1Z")
	if err != nil {
		panic(err)
	}
	return "29" + ctPlatformPAN + "1Z" + string(d)
}

var ctPricedAt = time.Date(2026, 9, 13, 6, 30, 0, 0, time.UTC)

func ctPricingConfig() pricing.Config {
	cfg := pricing.DefaultConfig()
	cfg.PlatformGSTIN = ctPlatformGSTIN()
	return cfg
}

func ctRestaurantTax(category, gstin string) pricing.Restaurant {
	return pricing.Restaurant{TaxCategory: category, State: "Karnataka", GSTIN: gstin}
}

// 2 x Paneer Tikka at 250.00 with Extra cheese 30.00 each, packaging 20.00.
func ctCartFor(category, gstin string) *postgres.Cart {
	rid := ctRestaurant
	cart := &postgres.Cart{ID: ctCart, UserID: ctCustomer, RestaurantID: &rid, Restaurant: "Test Kitchen",
		Items: []postgres.CartItem{{ID: ctCartItem, RestaurantID: ctRestaurant, MenuItemID: ctMenuItem, Name: "Paneer Tikka",
			FoodType: "VEG", Quantity: 2, UnitPricePaise: 25000,
			Addons: []postgres.CartItemAddon{{AddonID: ctAddon, Name: "Extra cheese", Quantity: 1, UnitPricePaise: 3000}}}}}
	postgres.PriceCart(ctPricingConfig(), ctRestaurantTax(category, gstin), cart, 2000, 0, ctPricedAt)
	return cart
}

func ctOrderQuote(category, gstin string) *pricing.Quote {
	q, err := pricing.Price(ctPricingConfig(), ctRestaurantTax(category, gstin), pricing.Cart{
		Items: []pricing.ItemLine{{Ref: "item:" + ctOrderItem.String(), Name: "Paneer Tikka", Quantity: 2, UnitPaise: 25000,
			Addons: []pricing.AddonLine{{Ref: "addon:" + ctOrderAddon.String(), Name: "Extra cheese", Quantity: 2, UnitPaise: 3000}}}},
		PackagingPaise: 2000,
	}, ctPricedAt)
	if err != nil {
		panic(err)
	}
	return q
}

type moneyContractStore struct {
	service.Store
	cart    *postgres.Cart
	err     error
	updated []postgres.PartnerRestaurantInput
}

func (f *moneyContractStore) GetCart(context.Context, uuid.UUID) (*postgres.Cart, error) {
	return f.cart, f.err
}

func (f *moneyContractStore) ApplyCoupon(context.Context, uuid.UUID, string) (*postgres.Cart, error) {
	return nil, f.err
}

func (f *moneyContractStore) PlaceOrder(context.Context, uuid.UUID, postgres.PlaceOrderInput, string) (*postgres.Order, error) {
	return nil, f.err
}

func (f *moneyContractStore) GetOrder(context.Context, uuid.UUID, uuid.UUID) (*postgres.Order, error) {
	q := ctOrderQuote("RESTAURANT_STANDALONE", "")
	t := q.Totals
	itemTax := q.TaxByItem()["item:"+ctOrderItem.String()]
	return &postgres.Order{
		ID: ctOrder, OrderNumber: "FG1000000000001", UserID: ctCustomer, RestaurantID: ctRestaurant, RestaurantName: "Test Kitchen",
		Status: "CONFIRMED", PaymentStatus: "CAPTURED", PaymentMethod: "ONLINE",
		Totals: postgres.PriceBreakdown{ItemSubtotal: 500, AddonTotal: 60, PackagingFee: 20, TaxTotal: pricing.PaiseToRupees(t.TaxTotalPaise),
			DeliveryFee: 29, PlatformFee: 5, FinalAmount: pricing.PaiseToRupees(t.FinalAmountPaise)},
		EstimatedPrepMins: 20, EstimatedDeliveryMins: 29, PlacedAt: ctTime,
		Items: []postgres.OrderItem{{ID: ctOrderItem, Name: "Paneer Tikka", FoodType: "VEG", UnitPrice: 250, Quantity: 2,
			TaxAmount: pricing.PaiseToRupees(itemTax), LineTotal: 500, UnitPricePaise: 25000, TaxAmountPaise: itemTax, LineTotalPaise: 50000}},
		History: []postgres.OrderStatusHistory{{ToStatus: "PLACED", CreatedAt: ctTime}},
		Money:   postgres.OrderMoneyFrom(t, &q.Breakdown, q.Breakdown.NeedsAdviserConfirmation),
	}, nil
}

func (f *moneyContractStore) GetInvoiceData(context.Context, uuid.UUID, uuid.UUID) (*postgres.InvoiceData, error) {
	q := ctOrderQuote("RESTAURANT_SPECIFIED_PREMISES", ctGSTIN())
	b := q.Breakdown
	return &postgres.InvoiceData{
		OrderID: ctOrder, OrderNumber: "FG1000000000001", PlacedAt: ctPricedAt,
		RestaurantName: "Test Kitchen", RestaurantLegalName: "Test Kitchens LLP", RestaurantGSTIN: ctGSTIN(),
		RestaurantState: "Karnataka", RestaurantAddrLine: "1 Test Lane", RestaurantCity: "Bengaluru", RestaurantID: ctRestaurant,
		BuyerName: "Test Customer", BuyerCity: "Bengaluru", BuyerState: "Karnataka", BuyerAddrLine: "2 Test Road",
		FinalAmountPaise: q.Totals.FinalAmountPaise, Breakdown: &b,
		Lines: map[string]postgres.InvoiceLineRef{
			"item:" + ctOrderItem.String():   {Name: "Paneer Tikka", Quantity: 2, UnitPricePaise: 25000},
			"addon:" + ctOrderAddon.String(): {Name: "Extra cheese", Quantity: 2, UnitPricePaise: 3000},
		},
	}, nil
}

func (f *moneyContractStore) AllocateOrderInvoiceNumbers(_ context.Context, _, _ uuid.UUID, _ string, platform, restaurant bool) (string, string, error) {
	var p, r string
	if platform {
		p = "FP/2627/000001"
	}
	if restaurant {
		r = "FR/2627/000001"
	}
	return p, r, nil
}

func (f *moneyContractStore) AdminListRestaurantSettlements(context.Context, postgres.Pagination) ([]map[string]any, error) {
	q := ctOrderQuote("RESTAURANT_SPECIFIED_PREMISES", ctGSTIN())
	b := q.Breakdown
	t := q.Totals
	line := settlement.ComputeRestaurant([]settlement.Order{{
		OrderID: ctOrder.String(), ItemSubtotalPaise: t.ItemSubtotalPaise, AddonTotalPaise: t.AddonTotalPaise,
		PackagingFeePaise: t.PackagingFeePaise, PlatformFeePaise: t.PlatformFeePaise, DeliveryFeePaise: t.DeliveryFeePaise,
		FinalAmountPaise: t.FinalAmountPaise, CommissionBP: 1500, ProcessedRefundPaise: 10000, Breakdown: &b,
	}}, settlement.DefaultRules())
	return []map[string]any{postgres.RestaurantSettlementRowMap(postgres.RestaurantSettlementRecord{
		ID: ctSettlement, RestaurantID: ctRestaurant, RestaurantName: "Test Kitchen", PeriodStart: "2026-09-07", PeriodEnd: "2026-09-13",
		Status: "PENDING", CreatedAt: ctTime, GrossOrderPaise: t.FinalAmountPaise, Line: line, HasBreakdown: true,
	})}, nil
}

func (f *moneyContractStore) UpdatePartnerRestaurant(_ context.Context, ownerID, rid uuid.UUID, in postgres.PartnerRestaurantInput) (*postgres.PartnerRestaurant, error) {
	f.updated = append(f.updated, in)
	return &postgres.PartnerRestaurant{ID: rid, OwnerUserID: ownerID, Name: in.Name, Status: "DRAFT", City: "Bengaluru", CreatedAt: ctTime}, nil
}

func (f *moneyContractStore) AdminListPayoutAccounts(context.Context, bool, postgres.Pagination) ([]postgres.AdminPayoutAccount, error) {
	pending, shared := "verification_pending_ops", postgres.PayoutReviewSharedAccount
	return []postgres.AdminPayoutAccount{{
		ID: ctPayoutRowID,
		PayoutAccount: postgres.PayoutAccount{OwnerType: "DELIVERY_PARTNER", OwnerID: ctPartner, HolderName: "Test Rider",
			AccountNumberMasked: "****6789", IFSC: "SBIN0001234", VerificationStatus: "NOT_VERIFIED", VerificationReason: &pending,
			CreatedAt: ctTime, UpdatedAt: ctTime},
		NeedsReview: true, ReviewReason: &shared,
	}}, nil
}

func moneyRouter(st *moneyContractStore) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(service.New(st)).RegisterRoutes(router)
	return router
}

// b3Fixtures lists the fixtures this file owns, for the masking test.
var b3Fixtures = []string{
	"cart_get_200_section_9_5", "cart_get_200_supplier_liable", "cart_get_200_pricing_blocked",
	"coupons_validate_422_disabled", "order_place_422_tax_category_missing", "order_place_503_platform_gstin_missing",
	"order_get_200", "invoice_get_200", "admin_settlements_restaurants_get_200", "admin_payout_accounts_get_200",
	"restaurant_patch_422_use_location_route",
}

func TestMoneyContracts(t *testing.T) {
	orderPath := "/v1/food/orders/" + ctOrder.String()
	cases := []struct {
		fixture string
		method  string
		path    string
		body    string
		user    uuid.UUID
		admin   bool
		cart    *postgres.Cart
		err     error
		status  int
	}{
		{"cart_get_200_section_9_5", http.MethodGet, "/v1/food/cart", ``, ctCustomer, false, ctCartFor("RESTAURANT_STANDALONE", ""), nil, http.StatusOK},
		{"cart_get_200_supplier_liable", http.MethodGet, "/v1/food/cart", ``, ctCustomer, false, ctCartFor("RESTAURANT_SPECIFIED_PREMISES", ctGSTIN()), nil, http.StatusOK},
		{"cart_get_200_pricing_blocked", http.MethodGet, "/v1/food/cart", ``, ctCustomer, false, ctCartFor("", ""), nil, http.StatusOK},
		{"coupons_validate_422_disabled", http.MethodPost, "/v1/food/coupons/validate", `{"code":"FIGO50"}`, ctCustomer, false, nil,
			pricing.ErrCouponsDisabled, http.StatusUnprocessableEntity},
		{"order_place_422_tax_category_missing", http.MethodPost, "/v1/food/orders", `{"address_id":"` + ctServiceArea.String() + `","payment_method":"upi"}`,
			ctCustomer, false, nil, pricing.ErrRestaurantTaxCategoryMissing, http.StatusUnprocessableEntity},
		{"order_place_503_platform_gstin_missing", http.MethodPost, "/v1/food/orders", `{"address_id":"` + ctServiceArea.String() + `","payment_method":"upi"}`,
			ctCustomer, false, nil, pricing.ErrPlatformGSTINNotConfigured, http.StatusServiceUnavailable},
		{"order_get_200", http.MethodGet, orderPath, ``, ctCustomer, false, nil, nil, http.StatusOK},
		{"invoice_get_200", http.MethodGet, orderPath + "/invoice?format=json", ``, ctCustomer, false, nil, nil, http.StatusOK},
		{"admin_settlements_restaurants_get_200", http.MethodGet, "/v1/food/admin/settlements/restaurants", ``, ctAdmin, true, nil, nil, http.StatusOK},
		{"admin_payout_accounts_get_200", http.MethodGet, "/v1/food/admin/payout-accounts?needs_review=true", ``, ctAdmin, true, nil, nil, http.StatusOK},
		{"restaurant_patch_422_use_location_route", http.MethodPatch, "/v1/food/partner/restaurants/" + ctRestaurant.String(),
			`{"name":"Renamed Kitchen","latitude":13.1,"longitude":77.7}`, ctOwner, false, nil, nil, http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			st := &moneyContractStore{cart: tc.cart, err: tc.err}
			rec := doJSON(moneyRouter(st), tc.method, tc.path, tc.body, tc.user, tc.admin)
			assertContract(t, rec, tc.status, tc.fixture)
			if len(st.updated) != 0 {
				t.Fatalf("%s: the store was written: %+v", tc.fixture, st.updated)
			}
		})
	}
}

func TestPatchRestaurantWithoutLocationFieldsUpdates(t *testing.T) {
	st := &moneyContractStore{}
	rec := doJSON(moneyRouter(st), http.MethodPatch, "/v1/food/partner/restaurants/"+ctRestaurant.String(),
		`{"name":"Renamed Kitchen","description":"New menu","packaging_fee":5}`, ctOwner, false)
	if rec.Code != http.StatusOK || len(st.updated) != 1 || st.updated[0].Name != "Renamed Kitchen" || st.updated[0].PackagingFee != 5 {
		t.Fatalf("status %d updated %+v body %s", rec.Code, st.updated, rec.Body.String())
	}
}

func TestLocationOwnedFields(t *testing.T) {
	for _, body := range []string{
		`{"latitude":1}`, `{"longitude":1}`, `{"address_line1":"x"}`, `{"address_line2":"x"}`,
		`{"city":"x"}`, `{"state":"x"}`, `{"postal_code":"x"}`, `{"google_place_id":"x"}`,
		`{"name":"x","latitude":null}`,
	} {
		fields, err := locationOwnedFields([]byte(body))
		if err != nil || len(fields) == 0 {
			t.Fatalf("%s not refused: %v %v", body, fields, err)
		}
	}
	for _, body := range []string{`{}`, `{"name":"x","phone":"1","email":"a@b","slug":"s","packaging_fee":1,"min_order_amount":2,"description":"d"}`} {
		fields, err := locationOwnedFields([]byte(body))
		if err != nil || len(fields) != 0 {
			t.Fatalf("%s refused: %v %v", body, fields, err)
		}
	}
	if _, err := locationOwnedFields([]byte(`{"name":`)); err == nil {
		t.Fatal("malformed body accepted")
	}
}

func TestInvoiceHTMLCarriesTheAdviserMarker(t *testing.T) {
	rec := doJSON(moneyRouter(&moneyContractStore{}), http.MethodGet, "/v1/food/orders/"+ctOrder.String()+"/invoice", ``, ctCustomer, false)
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("status %d type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Tax rates pending adviser confirmation") || !strings.Contains(body, ctGSTIN()) {
		t.Fatalf("invoice HTML lacks the marker or the restaurant GSTIN")
	}
	if rec.Header().Get("X-Invoice-Number") != "FP/2627/000001" || rec.Header().Get("X-Platform-Invoice-Number") != "FP/2627/000001" ||
		rec.Header().Get("X-Restaurant-Invoice-Number") != "FR/2627/000001" || rec.Header().Get("X-Tax-Adviser-Confirmation") != "pending" {
		t.Fatalf("invoice headers = %v", rec.Header())
	}
}

// Only the invoice may show a GSTIN (it must, in full); carts, orders,
// settlements and payout rows never do. No fixture carries a PAN outside a
// GSTIN, or an account number other than its last four digits.
func TestB3FixturesMaskIdentifiers(t *testing.T) {
	gstins := []string{ctGSTIN(), ctPlatformGSTIN()}
	for _, name := range b3Fixtures {
		raw, err := os.ReadFile(filepath.Join(contractsDir, name+".json"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		visible := raw
		for _, g := range gstins {
			if name != "invoice_get_200" && bytes.Contains(raw, []byte(g)) {
				t.Fatalf("%s exposes a GSTIN", name)
			}
			visible = bytes.ReplaceAll(visible, []byte(g), []byte("<gstin>"))
		}
		for _, secret := range []string{ctPAN, strings.ToLower(ctPAN), ctPlatformPAN, strings.ToLower(ctPlatformPAN), ctAccount, ctAccount[:8]} {
			if bytes.Contains(visible, []byte(secret)) {
				t.Fatalf("%s carries a plaintext identifier", name)
			}
		}
	}
}
