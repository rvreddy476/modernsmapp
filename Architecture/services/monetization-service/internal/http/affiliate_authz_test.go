package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/service"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Review 2026-11: affiliate conversions were readable by any signed-in
// user, the open code lookup leaked earnings and counted clicks, 5xx bodies
// carried raw errors, and the post tips list was public.

var (
	affOwner    = uuid.MustParse("aaaaaaaa-0000-4000-8000-000000000001")
	affStranger = uuid.MustParse("aaaaaaaa-0000-4000-8000-000000000002")
	affBuyer    = uuid.MustParse("bbbbbbbb-0000-4000-8000-000000000001")
	affLinkID   = uuid.MustParse("cccccccc-0000-4000-8000-000000000001")
)

type fakeAffiliates struct {
	link       *postgres.AffiliateLink
	err        error
	clicks     int
	asked      []uuid.UUID // creator ids ListAffiliateConversions was called with
	askedLinks []*uuid.UUID
}

func newFakeAffiliates() *fakeAffiliates {
	return &fakeAffiliates{link: &postgres.AffiliateLink{
		ID: affLinkID, CreatorID: affOwner, ListingID: uuid.New(), LinkCode: "abc123",
		CommissionPct: 5, ClickCount: 99, ConversionCount: 7, TotalEarned: 1234.5, IsActive: true,
		CreatedAt: time.Now(),
	}}
}

func (f *fakeAffiliates) GetAffiliateLinkByCode(_ context.Context, code string) (*postgres.AffiliateLink, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.link != nil && f.link.LinkCode == code {
		return f.link, nil
	}
	return nil, nil
}

func (f *fakeAffiliates) GetAffiliateLinkByID(_ context.Context, id uuid.UUID) (*postgres.AffiliateLink, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.link != nil && f.link.ID == id {
		return f.link, nil
	}
	return nil, nil
}

func (f *fakeAffiliates) RecordAffiliateClick(_ context.Context, code string) (bool, error) {
	if f.link == nil || f.link.LinkCode != code || !f.link.IsActive {
		return false, nil
	}
	f.clicks++
	return true, nil
}

func (f *fakeAffiliates) ListAffiliateConversions(_ context.Context, creatorID uuid.UUID, linkID *uuid.UUID, _, _ int) ([]postgres.AffiliateConversion, error) {
	f.asked = append(f.asked, creatorID)
	f.askedLinks = append(f.askedLinks, linkID)
	if f.err != nil {
		return nil, f.err
	}
	if creatorID != f.link.CreatorID {
		return nil, nil // the SQL join matches nothing
	}
	return []postgres.AffiliateConversion{{
		ID: uuid.New(), AffiliateID: affLinkID, OrderID: uuid.New(), BuyerID: affBuyer,
		CommissionPaise: 4_999, Status: "pending", CreatedAt: time.Now(),
	}}, nil
}

func affReq(t *testing.T, h *Handler, method, path, user string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if user != "" {
		req.Header.Set("X-User-Id", user)
	}
	w := httptest.NewRecorder()
	routerFor(h).ServeHTTP(w, req)
	return w
}

func affHandler(f *fakeAffiliates) *Handler {
	h := New(nil).WithWritesEnabled(true)
	h.affiliates = f
	return h
}

func TestAffiliateConversionsScopedToCaller(t *testing.T) {
	f := newFakeAffiliates()
	h := affHandler(f)

	// Owner, no filter: gets their conversions; buyer_id is not serialised.
	w := affReq(t, h, http.MethodGet, "/v1/monetization/affiliate/conversions", affOwner.String())
	if w.Code != http.StatusOK {
		t.Fatalf("owner: status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "buyer_id") || strings.Contains(w.Body.String(), affBuyer.String()) {
		t.Fatalf("buyer id leaked: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "commission_paise") {
		t.Fatalf("commission missing: %s", w.Body.String())
	}
	if len(f.asked) != 1 || f.asked[0] != affOwner || f.askedLinks[0] != nil {
		t.Fatalf("service must be asked with the caller id, got %v %v", f.asked, f.askedLinks)
	}

	// Owner filtering to their own link.
	w = affReq(t, h, http.MethodGet, "/v1/monetization/affiliate/conversions?affiliate_id="+affLinkID.String(), affOwner.String())
	if w.Code != http.StatusOK {
		t.Fatalf("owner+link: status=%d body=%s", w.Code, w.Body.String())
	}
	if f.askedLinks[1] == nil || *f.askedLinks[1] != affLinkID {
		t.Fatalf("link filter not passed: %v", f.askedLinks[1])
	}

	// Stranger naming the owner's link: 404, and the query is never run.
	before := len(f.asked)
	w = affReq(t, h, http.MethodGet, "/v1/monetization/affiliate/conversions?affiliate_id="+affLinkID.String(), affStranger.String())
	if w.Code != http.StatusNotFound {
		t.Fatalf("stranger+link: status=%d body=%s", w.Code, w.Body.String())
	}
	if len(f.asked) != before {
		t.Fatal("stranger's request reached the conversions query")
	}
	// A link that does not exist looks identical.
	w2 := affReq(t, h, http.MethodGet, "/v1/monetization/affiliate/conversions?affiliate_id="+uuid.NewString(), affStranger.String())
	if w2.Code != http.StatusNotFound {
		t.Fatalf("unknown link: status=%d", w2.Code)
	}
	var a, b map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &a)
	_ = json.Unmarshal(w2.Body.Bytes(), &b)
	if a["error"].(map[string]any)["message"] != b["error"].(map[string]any)["message"] {
		t.Fatal("not-owned and not-found must be indistinguishable")
	}

	// Stranger, no filter: sees nothing of the owner's.
	w = affReq(t, h, http.MethodGet, "/v1/monetization/affiliate/conversions", affStranger.String())
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "commission_paise") {
		t.Fatalf("stranger saw conversions: %d %s", w.Code, w.Body.String())
	}

	// Anonymous: 401.
	if w = affReq(t, h, http.MethodGet, "/v1/monetization/affiliate/conversions", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: status=%d", w.Code)
	}
}

func TestPublicAffiliateLookupHasNoInternalsAndDoesNotCount(t *testing.T) {
	f := newFakeAffiliates()
	h := affHandler(f)

	w := affReq(t, h, http.MethodGet, "/v1/monetization/affiliate/abc123", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	for k := range env.Data {
		switch k {
		case "link_code", "listing_id", "is_active":
		default:
			t.Errorf("public lookup exposes %q", k)
		}
	}
	for _, banned := range []string{"creator_id", "click_count", "conversion_count", "total_earned", "commission"} {
		if strings.Contains(w.Body.String(), banned) {
			t.Errorf("public lookup leaks %s: %s", banned, w.Body.String())
		}
	}
	if f.clicks != 0 {
		t.Fatalf("GET counted %d clicks", f.clicks)
	}

	if w = affReq(t, h, http.MethodGet, "/v1/monetization/affiliate/nope", ""); w.Code != http.StatusNotFound {
		t.Fatalf("unknown code: status=%d", w.Code)
	}

	// The explicit endpoint is the only counter.
	if w = affReq(t, h, http.MethodPost, "/v1/monetization/affiliate/abc123/click", ""); w.Code != http.StatusNoContent {
		t.Fatalf("click: status=%d body=%s", w.Code, w.Body.String())
	}
	if f.clicks != 1 {
		t.Fatalf("clicks=%d, want 1", f.clicks)
	}
	if w = affReq(t, h, http.MethodPost, "/v1/monetization/affiliate/nope/click", ""); w.Code != http.StatusNotFound {
		t.Fatalf("click unknown: status=%d", w.Code)
	}
}

func TestAffiliateServerErrorBodyIsGeneric(t *testing.T) {
	f := newFakeAffiliates()
	f.err = errors.New("ERROR: relation affiliate_links does not exist (SQLSTATE 42P01)")
	h := affHandler(f)
	for _, path := range []string{
		"/v1/monetization/affiliate/abc123",
		"/v1/monetization/affiliate/conversions",
	} {
		w := affReq(t, h, http.MethodGet, path, affOwner.String())
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("%s: status=%d", path, w.Code)
		}
		if strings.Contains(w.Body.String(), "affiliate_links") || strings.Contains(w.Body.String(), "SQLSTATE") {
			t.Fatalf("%s: raw error in body: %s", path, w.Body.String())
		}
	}
}

// --- tips list -------------------------------------------------------------

type fakeTipLister struct {
	viewer uuid.UUID
	err    error
}

func (f *fakeTipLister) ListTipsForPost(_ context.Context, viewerID, _ uuid.UUID, _ string, _ int) ([]postgres.Tip, error) {
	f.viewer = viewerID
	return nil, f.err
}

func TestTipsForPostNeedsSignInAndIsScopedToViewer(t *testing.T) {
	f := &fakeTipLister{}
	h := New(nil).WithWritesEnabled(true)
	h.tipList = f
	path := "/v1/monetization/tips/post/" + uuid.NewString()

	if w := affReq(t, h, http.MethodGet, path, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: status=%d", w.Code)
	}
	if w := affReq(t, h, http.MethodGet, path, affStranger.String()); w.Code != http.StatusOK {
		t.Fatalf("signed in: status=%d body=%s", w.Code, w.Body.String())
	}
	if f.viewer != affStranger {
		t.Fatalf("the viewer id must reach the query, got %s", f.viewer)
	}

	f.err = errors.New("pq: connection refused to 10.0.0.5:5432")
	w := affReq(t, h, http.MethodGet, path, affStranger.String())
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "10.0.0.5") {
		t.Fatalf("5xx must be generic: %d %s", w.Code, w.Body.String())
	}
}

// compile-time: the real service satisfies both seams.
var (
	_ affiliateService = (*service.Service)(nil)
	_ tipLister        = (*service.Service)(nil)
)
