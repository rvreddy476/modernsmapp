package http

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/onboarding"
	"github.com/atpost/food-service/internal/pricing"
	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/pii"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Golden contract fixtures for the lane B8 partner routes the Feast Kitchen
// app reads: onboarding read-backs, readiness, the partner restaurant, order
// and menu item, menu extras, the kitchen queue and earnings with their paise
// siblings. Regenerate deliberately with UPDATE_CONTRACTS=1 and review the diff.

var (
	ctB8Category      = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0030")
	ctB8EmptyCategory = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0031")
	ctB8Variant       = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0032")
	ctB8Group         = uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0033")
)

type b8ContractStore struct {
	service.Store
	err error
}

func (f *b8ContractStore) GetRestaurantReadiness(_ context.Context, _, rid uuid.UUID) (*postgres.RestaurantReadiness, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &postgres.RestaurantReadiness{RestaurantID: rid, Status: "DRAFT",
		Missing: []string{onboarding.StepFSSAI, onboarding.StepPayoutAccount}}, nil
}

func (f *b8ContractStore) GetRestaurantCompliance(_ context.Context, _, rid uuid.UUID) (*postgres.RestaurantCompliance, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &postgres.RestaurantCompliance{RestaurantID: rid, TaxCategory: "CLOUD_KITCHEN_TAKEAWAY", LegalName: "Test Kitchens LLP",
		PANMasked: pii.MaskPAN(ctPAN), PANHolderType: "INDIVIDUAL", ComplianceSubmittedAt: ctTime}, nil
}

func (f *b8ContractStore) GetRestaurantLocation(_ context.Context, _, rid uuid.UUID) (*postgres.RestaurantLocation, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &postgres.RestaurantLocation{RestaurantID: rid, Latitude: 12.9716, Longitude: 77.5946, AddressLine1: "1 Test Lane",
		AddressLine2: "Near Test Park", City: "Bengaluru", State: "Karnataka", PostalCode: "560001",
		GooglePlaceID: "ChIJtestplace0001", DeliveryRadiusKM: 6.5, ServiceAreaID: ctServiceArea}, nil
}

func (f *b8ContractStore) GetOperatingHours(_ context.Context, _, rid uuid.UUID) (*postgres.OperatingHours, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &postgres.OperatingHours{RestaurantID: rid, Timezone: "Asia/Kolkata", Windows: []postgres.OperatingHoursWindow{
		{DayOfWeek: 0, IsClosed: true},
		{DayOfWeek: 1, OpensAt: "11:00", ClosesAt: "15:00"},
		{DayOfWeek: 1, OpensAt: "18:00", ClosesAt: "23:00"},
		{DayOfWeek: 5, OpensAt: "18:00", ClosesAt: "02:00", Overnight: true},
	}}, nil
}

func (f *b8ContractStore) GetRestaurantFSSAI(_ context.Context, _, rid uuid.UUID) (*postgres.RestaurantFSSAIView, error) {
	if f.err != nil {
		return nil, f.err
	}
	number, expires, docExpires, reason, verifiedAt, status := "10099999000000", "2027-03-31", "2027-03-30T18:30:00Z", "Licence photo is unreadable", ctTime, "REJECTED"
	media, admin := ctMedia, ctAdmin
	doc := &postgres.RestaurantDocument{ID: ctDocument, RestaurantID: rid, DocumentType: "FSSAI", DocumentNumber: &number, MediaID: &media,
		Status: status, RejectionReason: &reason, ExpiresAt: &docExpires, VerifiedBy: &admin, VerifiedAt: &verifiedAt, CreatedAt: ctTime}
	return &postgres.RestaurantFSSAIView{RestaurantID: rid, LicenceNumber: number, ExpiresAt: &expires, Document: doc,
		DocumentStatus: &status, ReviewReason: &reason}, nil
}

func (f *b8ContractStore) GetPartnerRestaurant(_ context.Context, ownerID, rid uuid.UUID) (*postgres.PartnerRestaurant, error) {
	if f.err != nil {
		return nil, f.err
	}
	// 11 digits with a leading 0: no Aadhaar-shaped run for the KYC fixture scan.
	phone, email, legal, display := "08040000000", "kitchen@example.test", "Test Kitchens LLP", "Test Kitchen"
	return &postgres.PartnerRestaurant{ID: rid, PartnerID: ctPartner, OwnerUserID: ownerID, Name: "Test Kitchen", Slug: "test-kitchen",
		Description: "South Indian", Status: "DRAFT", City: "Bengaluru", State: "Karnataka", MinOrderAmount: 99, PackagingFee: 10,
		CreatedAt: ctTime, Phone: &phone, Email: &email, LegalName: &legal, DisplayName: &display}, nil
}

func (f *b8ContractStore) GetPartnerOrder(ctx context.Context, _, orderID uuid.UUID) (*postgres.Order, error) {
	if f.err != nil {
		return nil, f.err
	}
	o, err := (&moneyContractStore{}).GetOrder(ctx, ctCustomer, orderID)
	if err != nil {
		return nil, err
	}
	o.DeliveryCode = "4321" // PartnerOrderOf must drop it
	return o, nil
}

func ctB8MenuItem() postgres.MenuItem {
	media := ctMedia
	item := postgres.MenuItem{ID: ctMenuItem, RestaurantID: ctRestaurant, CategoryID: ctB8Category, Name: "Paneer Tikka",
		Description: "Charred cottage cheese", FoodType: "VEG", BasePrice: 250, DiscountPrice: 225.5,
		ImageURL: onboarding.MediaServeURL("", media), ImageMediaID: &media, PreparationMinutes: 20, IsAvailable: true, TaxPercentage: 5}
	item.FillPaise()
	return item
}

func (f *b8ContractStore) GetPartnerMenuItem(_ context.Context, _, itemID uuid.UUID) (*postgres.PartnerMenuItem, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &postgres.PartnerMenuItem{
		MenuItem: ctB8MenuItem(),
		Variants: []postgres.MenuVariant{{ID: ctB8Variant, MenuItemID: itemID, Name: "Half", Price: 150, PricePaise: postgres.PaiseOf(150), IsAvailable: true}},
		AddonGroups: []postgres.MenuAddonGroup{{ID: ctB8Group, MenuItemID: itemID, Name: "Extras", MinSelect: 0, MaxSelect: 2,
			Addons: []postgres.MenuAddon{{ID: ctAddon, AddonGroupID: ctB8Group, Name: "Extra cheese", Price: 30, PricePaise: postgres.PaiseOf(30), IsAvailable: true}}}},
	}, nil
}

func (f *b8ContractStore) ListMenuCategories(context.Context, uuid.UUID, uuid.UUID) ([]postgres.MenuCategory, error) {
	if f.err != nil {
		return nil, f.err
	}
	one, zero := 1, 0
	return []postgres.MenuCategory{
		{ID: ctB8Category, Name: "Mains", SortOrder: 1, Items: []postgres.MenuItem{ctB8MenuItem()}, ItemCount: &one},
		{ID: ctB8EmptyCategory, Name: "Desserts", SortOrder: 2, Items: []postgres.MenuItem{}, ItemCount: &zero},
	}, nil
}

func (f *b8ContractStore) CreateMenuVariant(_ context.Context, _, itemID uuid.UUID, in postgres.MenuPriceInput) (*postgres.MenuVariant, error) {
	p, err := postgres.ResolvePricePaise(in.PricePaise, in.Price, true)
	if err != nil {
		return nil, err
	}
	return &postgres.MenuVariant{ID: ctB8Variant, MenuItemID: itemID, Name: *in.Name, Price: pricing.PaiseToRupees(*p), PricePaise: *p, IsAvailable: true}, nil
}

func (f *b8ContractStore) CreateAddonGroup(_ context.Context, _, itemID uuid.UUID, in postgres.MenuAddonGroupInput) (*postgres.MenuAddonGroup, error) {
	if err := postgres.ValidateAddonGroupRule(*in.MinSelect, *in.MaxSelect); err != nil {
		return nil, err
	}
	return &postgres.MenuAddonGroup{ID: ctB8Group, MenuItemID: itemID, Name: *in.Name, MinSelect: *in.MinSelect, MaxSelect: *in.MaxSelect,
		Addons: []postgres.MenuAddon{}}, nil
}

func (f *b8ContractStore) CreateAddon(_ context.Context, _, _, groupID uuid.UUID, in postgres.MenuPriceInput) (*postgres.MenuAddon, error) {
	p, err := postgres.ResolvePricePaise(in.PricePaise, in.Price, true)
	if err != nil {
		return nil, err
	}
	return &postgres.MenuAddon{ID: ctAddon, AddonGroupID: groupID, Name: *in.Name, Price: pricing.PaiseToRupees(*p), PricePaise: *p, IsAvailable: true}, nil
}

func (f *b8ContractStore) ListKitchenQueue(context.Context, uuid.UUID, uuid.UUID) ([]postgres.KitchenOrder, error) {
	deadline := time.Date(2026, 9, 13, 6, 35, 0, 0, time.UTC)
	instruction, deadlineText, secs := "Less spicy", "2026-09-13 06:35:00+00", 300
	k := postgres.KitchenOrder{ID: ctOrder, OrderNumber: "FG1000000000001", Status: "CONFIRMED", FinalAmount: 649.12, ItemCount: 2,
		CustomerInstruction: &instruction, PlacedAt: "2026-09-13 06:30:00+00", AcceptDeadlineAt: &deadlineText, SecondsToBreach: &secs}
	k.FillDerived(&deadline)
	return []postgres.KitchenOrder{k}, nil
}

func (f *b8ContractStore) PartnerRestaurantSummary(_ context.Context, _, rid uuid.UUID) (map[string]any, error) {
	return postgres.PartnerSummaryMap(rid, 12, 10, 1, 7790.44, 1168.57, 649.12), nil
}

func (f *b8ContractStore) PartnerRestaurantSettlements(_ context.Context, _, rid uuid.UUID) ([]map[string]any, error) {
	return []map[string]any{postgres.PartnerSettlementMap(rid, postgres.PartnerSettlementRow{
		ID: ctSettlement.String(), PeriodStart: "2026-09-07", PeriodEnd: "2026-09-13", Status: "PENDING", CreatedAt: ctTime,
		Gross: 7790.44, Commission: 1168.57, RefundAdjustment: 649.12, Payout: 5972.75,
	})}, nil
}

func b8Router(st *b8ContractStore) *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := service.New(st).WithOnboardingClock(func() time.Time { return time.Date(2026, 9, 13, 12, 0, 0, 0, onboarding.IST) })
	router := gin.New()
	New(svc).RegisterRoutes(router)
	return router
}

// b8Fixtures lists the fixtures this file owns.
var b8Fixtures = []string{
	"readiness_get_200", "compliance_get_200", "compliance_get_404_not_saved", "compliance_get_404_not_owner",
	"location_get_200", "operating_hours_get_200", "fssai_get_200", "partner_restaurant_get_200", "partner_order_get_200",
	"menu_item_get_200", "menu_categories_get_200", "menu_variant_post_201", "menu_variant_post_422_price_mismatch",
	"addon_group_post_201", "addon_post_201", "kitchen_queue_get_200", "reports_summary_get_200", "settlements_get_200",
}

func TestB8Contracts(t *testing.T) {
	restaurant := "/v1/food/partner/restaurants/" + ctRestaurant.String()
	item := "/v1/food/partner/menu/items/" + ctMenuItem.String()
	cases := []struct {
		fixture, method, path, body string
		err                         error
		status                      int
	}{
		{"readiness_get_200", http.MethodGet, restaurant + "/readiness", ``, nil, http.StatusOK},
		{"compliance_get_200", http.MethodGet, restaurant + "/compliance", ``, nil, http.StatusOK},
		{"compliance_get_404_not_saved", http.MethodGet, restaurant + "/compliance", ``, &postgres.StepNotSavedError{Step: onboarding.StepCompliance}, http.StatusNotFound},
		{"compliance_get_404_not_owner", http.MethodGet, restaurant + "/compliance", ``, pgx.ErrNoRows, http.StatusNotFound},
		{"location_get_200", http.MethodGet, restaurant + "/location", ``, nil, http.StatusOK},
		{"operating_hours_get_200", http.MethodGet, restaurant + "/operating-hours", ``, nil, http.StatusOK},
		{"fssai_get_200", http.MethodGet, restaurant + "/fssai", ``, nil, http.StatusOK},
		{"partner_restaurant_get_200", http.MethodGet, restaurant, ``, nil, http.StatusOK},
		{"partner_order_get_200", http.MethodGet, "/v1/food/partner/orders/" + ctOrder.String(), ``, nil, http.StatusOK},
		{"menu_item_get_200", http.MethodGet, item, ``, nil, http.StatusOK},
		{"menu_categories_get_200", http.MethodGet, restaurant + "/menu/categories", ``, nil, http.StatusOK},
		{"menu_variant_post_201", http.MethodPost, item + "/variants", `{"name":"Half","price_paise":15000}`, nil, http.StatusCreated},
		{"menu_variant_post_422_price_mismatch", http.MethodPost, item + "/variants", `{"name":"Half","price":150,"price_paise":15500}`, nil, http.StatusUnprocessableEntity},
		{"addon_group_post_201", http.MethodPost, item + "/addon-groups", `{"name":"Extras","min_select":0,"max_select":2}`, nil, http.StatusCreated},
		{"addon_post_201", http.MethodPost, item + "/addon-groups/" + ctB8Group.String() + "/addons", `{"name":"Extra cheese","price":30}`, nil, http.StatusCreated},
		{"kitchen_queue_get_200", http.MethodGet, restaurant + "/kitchen-queue", ``, nil, http.StatusOK},
		{"reports_summary_get_200", http.MethodGet, restaurant + "/reports/summary", ``, nil, http.StatusOK},
		{"settlements_get_200", http.MethodGet, restaurant + "/settlements", ``, nil, http.StatusOK},
	}
	if len(cases) != len(b8Fixtures) {
		t.Fatalf("cases %d, fixtures listed %d", len(cases), len(b8Fixtures))
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			rec := doJSON(b8Router(&b8ContractStore{err: tc.err}), tc.method, tc.path, tc.body, ctOwner, false)
			assertContract(t, rec, tc.status, tc.fixture)
			body := rec.Body.String()
			for _, secret := range []string{ctPAN, strings.ToLower(ctPAN), ctAccount, ctAccount[:8], `"delivery_code"`, "4321"} {
				if strings.Contains(body, secret) {
					t.Fatalf("%s: response carries %q", tc.fixture, secret)
				}
			}
		})
	}
}
