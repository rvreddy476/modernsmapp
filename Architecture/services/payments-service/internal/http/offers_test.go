package http

// The bank-offer routes against an in-memory service: who reaches them, and
// the PATCH body's three states (absent = no change, null = clear, value = set).

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/atpost/payments-service/internal/service"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type fakeOffers struct {
	mu       sync.Mutex
	reads    []string
	amounts  []int64
	creates  []postgres.PaymentOffer
	patches  []postgres.OfferPatch
	scopes   []string
	writers  []postgres.OfferWrite
	createFn func(o postgres.PaymentOffer) error
}

func (f *fakeOffers) ApplicableOffers(_ context.Context, app string, amount int64) ([]service.PublicOffer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads, f.amounts = append(f.reads, app), append(f.amounts, amount)
	return []service.PublicOffer{{ID: uuid.New(), Title: "10% off"}}, nil
}

func (f *fakeOffers) ListOffers(_ context.Context, app string) ([]postgres.PaymentOffer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scopes = append(f.scopes, app)
	return []postgres.PaymentOffer{}, nil
}

func (f *fakeOffers) CreateOffer(_ context.Context, o postgres.PaymentOffer, w postgres.OfferWrite) (*postgres.PaymentOffer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createFn != nil {
		if err := f.createFn(o); err != nil {
			return nil, err
		}
	}
	f.creates, f.writers = append(f.creates, o), append(f.writers, w)
	o.ID = uuid.New()
	return &o, nil
}

func (f *fakeOffers) UpdateOffer(_ context.Context, id uuid.UUID, app string, p postgres.OfferPatch, w postgres.OfferWrite) (*postgres.PaymentOffer, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.patches, f.scopes, f.writers = append(f.patches, p), append(f.scopes, app), append(f.writers, w)
	return &postgres.PaymentOffer{ID: id}, true, nil
}

type offerRig struct {
	r        *gin.Engine
	fake     *fakeOffers
	admin    *servicetoken.Signer
	commerce tokenCaller
	food     tokenCaller
}

func newOfferRig(t *testing.T) *offerRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	v := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := servicetoken.NewSignerFromBase64(IssuerAdminService, "a1", priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCallerPolicy(IssuerAdminService, AdminPermissions, nil); err != nil {
		t.Fatalf("payments:offers.manage must be a valid admin permission: %v", err)
	}
	if err := v.RegisterBase64(IssuerAdminService, "a1", pub, AdminPermissions, nil); err != nil {
		t.Fatal(err)
	}
	commerceOps := []string{servicetoken.OpIntentCreate, servicetoken.OpIntentRead, OpOffersRead}
	if err := ValidateCallerPolicy("commerce-service", commerceOps, []string{servicetoken.RefOrder}); err != nil {
		t.Fatalf("payments:offers.read must be a valid caller op: %v", err)
	}
	commerce := newTokenCaller(t, v, "commerce-service", "c1", commerceOps, []string{servicetoken.RefOrder})
	food := newTokenCaller(t, v, "food-service", "f1",
		[]string{servicetoken.OpIntentRead, OpOffersRead}, []string{servicetoken.RefFoodOrder})
	fake := &fakeOffers{}
	r := gin.New()
	h := New(newFake()).WithServiceAuth(v).
		WithCallerApplications(map[string][]string{"commerce-service": {"mstore"}, "food-service": {"feast"}}).
		WithAdmin(&fakeAdmin{}, 10*time.Minute).WithOffers(fake)
	if err := h.RegisterRoutes(r); err != nil {
		t.Fatal(err)
	}
	return &offerRig{r: r, fake: fake, admin: signer, commerce: commerce, food: food}
}

func TestOffersRead_ScopeAndApplication(t *testing.T) {
	rig := newOfferRig(t)
	w := do(rig.r, http.MethodGet, "/v1/payments/internal/offers?application=mstore&amount_minor=100000", nil,
		rig.commerce.header(t, OpOffersRead))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d %s", w.Code, w.Body.String())
	}
	if rig.fake.reads[0] != "mstore" || rig.fake.amounts[0] != 100000 {
		t.Fatalf("read = %v %v", rig.fake.reads, rig.fake.amounts)
	}
	for _, tc := range []struct {
		name string
		path string
		hdr  map[string]string
		want int
	}{
		{"token without the scope", "/v1/payments/internal/offers?application=mstore", rig.commerce.header(t, servicetoken.OpIntentRead), http.StatusForbidden},
		{"another application", "/v1/payments/internal/offers?application=feast", rig.commerce.header(t, OpOffersRead), http.StatusForbidden},
		{"no application", "/v1/payments/internal/offers", rig.commerce.header(t, OpOffersRead), http.StatusBadRequest},
		{"bad amount", "/v1/payments/internal/offers?application=mstore&amount_minor=-5", rig.commerce.header(t, OpOffersRead), http.StatusBadRequest},
		{"no credential", "/v1/payments/internal/offers?application=mstore", nil, http.StatusUnauthorized},
		{"admin-service on the service family", "/v1/payments/internal/offers?application=mstore",
			mintHeader(t, rig.admin, servicetoken.AudiencePayments, []string{PermOffersManage}, uuid.NewString(), time.Minute), http.StatusForbidden},
	} {
		if w := do(rig.r, http.MethodGet, tc.path, nil, tc.hdr); w.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (%s)", tc.name, w.Code, tc.want, w.Body.String())
		}
	}
	if w := do(rig.r, http.MethodGet, "/v1/payments/internal/offers?application=feast", nil, rig.food.header(t, OpOffersRead)); w.Code != http.StatusOK {
		t.Fatalf("food reading feast = %d", w.Code)
	}
}

func TestOffersAdmin_PermissionAndActor(t *testing.T) {
	rig := newOfferRig(t)
	actor := uuid.New()
	ok := mintHeader(t, rig.admin, servicetoken.AudiencePayments, []string{PermOffersManage}, actor.String(), time.Minute)
	if w := do(rig.r, http.MethodGet, InternalAdminPrefix+"/offers?application_id=mstore", nil, ok); w.Code != http.StatusOK {
		t.Fatalf("list = %d %s", w.Code, w.Body.String())
	}
	wrongPerm := mintHeader(t, rig.admin, servicetoken.AudiencePayments, []string{PermApplicationsManage}, actor.String(), time.Minute)
	if w := do(rig.r, http.MethodGet, InternalAdminPrefix+"/offers", nil, wrongPerm); w.Code != http.StatusForbidden {
		t.Fatalf("wrong permission = %d", w.Code)
	}
	if w := do(rig.r, http.MethodGet, InternalAdminPrefix+"/offers", nil, rig.commerce.header(t, OpOffersRead)); w.Code != http.StatusForbidden {
		t.Fatalf("commerce on the admin family = %d", w.Code)
	}
	body := []byte(`{"application":"mstore","provider":"razorpay","provider_offer_id":"offer_ANZoaxsOww2X53",
		"title":"10% off with HDFC credit cards","payment_method":"card","discount_type":"percentage",
		"discount_value":1000,"max_discount_minor":150000,"min_amount_minor":500000,"funded_by":"bank"}`)
	if w := do(rig.r, http.MethodPost, InternalAdminPrefix+"/offers", body, ok); w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	c := rig.fake.creates[0]
	if c.Application != "mstore" || c.ProviderOfferID != "offer_ANZoaxsOww2X53" || c.MaxDiscountMinor == nil ||
		*c.MaxDiscountMinor != 150000 || c.EndsAt != nil || !c.Active || c.StartsAt.IsZero() {
		t.Fatalf("create = %+v", c)
	}
	if wr := rig.fake.writers[0]; wr.OperatorID != actor.String() || wr.Credential != "service_token:admin-service" {
		t.Fatalf("writer = %+v", wr)
	}
	// An app-scoped admin cannot register another application's offer.
	if w := do(rig.r, http.MethodPost, InternalAdminPrefix+"/offers?application_id=feast", body, ok); w.Code != http.StatusForbidden {
		t.Fatalf("cross-application create = %d", w.Code)
	}
	if w := do(rig.r, http.MethodPost, InternalAdminPrefix+"/offers", []byte(`{"application":"mstore"}`), ok); w.Code != http.StatusBadRequest {
		t.Fatalf("incomplete create = %d", w.Code)
	}
}

func TestOffersAdmin_PatchIsThreeState(t *testing.T) {
	rig := newOfferRig(t)
	hdr := mintHeader(t, rig.admin, servicetoken.AudiencePayments, []string{PermOffersManage}, uuid.NewString(), time.Minute)
	path := InternalAdminPrefix + "/offers/" + uuid.NewString()

	// Absent keys: nothing changes but what is named.
	if w := do(rig.r, http.MethodPatch, path, []byte(`{"active":false}`), hdr); w.Code != http.StatusOK {
		t.Fatalf("deactivate = %d %s", w.Code, w.Body.String())
	}
	p := rig.fake.patches[0]
	if p.Active == nil || *p.Active || p.Title != nil || p.Description != nil || p.MaxDiscountMinor != nil ||
		p.ClearMaxDiscount || p.EndsAt != nil || p.ClearEndsAt || p.StartsAt != nil || p.ClearStartsAt ||
		p.MinAmountMinor != nil || p.DiscountValue != nil {
		t.Fatalf("deactivate patch touched more: %+v", p)
	}

	// Explicit null clears the optional fields.
	if w := do(rig.r, http.MethodPatch, path,
		[]byte(`{"max_discount_minor":null,"ends_at":null,"description":null,"min_amount_minor":null,"starts_at":null}`), hdr); w.Code != http.StatusOK {
		t.Fatalf("clear = %d %s", w.Code, w.Body.String())
	}
	p = rig.fake.patches[1]
	if !p.ClearMaxDiscount || p.MaxDiscountMinor != nil || !p.ClearEndsAt || p.EndsAt != nil ||
		p.Description == nil || *p.Description != "" || p.MinAmountMinor == nil || *p.MinAmountMinor != 0 ||
		!p.ClearStartsAt || p.Active != nil || p.Title != nil {
		t.Fatalf("clear patch = %+v", p)
	}

	// Values set.
	if w := do(rig.r, http.MethodPatch, path,
		[]byte(`{"max_discount_minor":2500,"ends_at":"2026-12-31T18:30:00Z","title":"New"}`), hdr); w.Code != http.StatusOK {
		t.Fatalf("set = %d %s", w.Code, w.Body.String())
	}
	p = rig.fake.patches[2]
	if p.ClearMaxDiscount || p.MaxDiscountMinor == nil || *p.MaxDiscountMinor != 2500 || p.ClearEndsAt ||
		p.EndsAt == nil || p.Title == nil || *p.Title != "New" {
		t.Fatalf("set patch = %+v", p)
	}

	for _, tc := range []struct{ name, body string }{
		{"identity cannot change", `{"provider_offer_id":"offer_ZZZZZZZZZ"}`},
		{"application cannot change", `{"application":"feast"}`},
		{"required field cannot be cleared", `{"title":null}`},
		{"active cannot be null", `{"active":null}`},
		{"unknown field", `{"delete":true}`},
	} {
		if w := do(rig.r, http.MethodPatch, path, []byte(tc.body), hdr); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d", tc.name, w.Code)
		}
	}
	if len(rig.fake.patches) != 3 {
		t.Fatalf("a refused patch reached the store: %d", len(rig.fake.patches))
	}
}
