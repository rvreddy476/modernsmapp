package http

// The sell-scope goldens (migration 045), rendered through the real handlers
// and the real service over a fake store, so the ordinary `go test ./...`
// sweep pins them. The same files are registered in
// contracts_fixtures_integration_test.go, where they render against the
// seeded database; the world below mirrors that seed id for id.
//
//	go test ./internal/http/ -run TestSellScopeGoldens -update-sell-scope

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var updateSellScope = flag.Bool("update-sell-scope", false, "rewrite the sell-scope goldens from the handlers")

// The seed's ids (contracts_fixtures_integration_test.go).
var (
	scSellerUser  = uuid.MustParse("00000000-0000-4000-8000-0000000c0002")
	scDraftUser   = uuid.MustParse("00000000-0000-4000-8000-0000000c0004")
	scBooksUser   = uuid.MustParse("00000000-0000-4000-8000-0000000c0007")
	scSeller      = uuid.MustParse("00000000-0000-4000-8000-0000000c0010")
	scDraftSeller = uuid.MustParse("00000000-0000-4000-8000-0000000c0011")
	scBooksSeller = uuid.MustParse("00000000-0000-4000-8000-0000000c0013")
	scTextbooks   = uuid.MustParse("00000000-0000-4000-8000-00000000c101")
	scElectronics = uuid.MustParse("00000000-0000-4000-8000-00000000c001")
	scTax0        = uuid.MustParse("00000000-0000-4000-8000-0000000c0e00")
	scMediaKYC    = uuid.MustParse("00000000-0000-4000-8000-0000000d0004")
	scMedia2      = uuid.MustParse("00000000-0000-4000-8000-0000000d0002")
)

// The PANs and GSTINs the fixtures use. 27ABCPE1234F1ZB carries the books
// seller's PAN; 27PQRCS6789Z1Z0 is somebody else's.
const (
	scBooksPAN       = "ABCPE1234F"
	scBooksGSTIN     = "27ABCPE1234F1ZB"
	scOtherGSTIN     = "27PQRCS6789Z1Z0"
	scSellerGSTIN    = "29AAACM1234A1Z4"
	scBooksStoreName = "Asha Books"
)

type scFakeStore struct {
	sellers map[uuid.UUID]*postgres.Seller // by user id
}

func (f *scFakeStore) GetSellerByUserID(_ context.Context, u uuid.UUID) (*postgres.Seller, error) {
	if s, ok := f.sellers[u]; ok {
		return s, nil
	}
	return nil, postgres.ErrNoSellerRow
}

func (f *scFakeStore) SaveOnboardingSellScope(_ context.Context, u uuid.UUID, w postgres.SellScopeWrite) error {
	s, ok := f.sellers[u]
	if !ok {
		return postgres.ErrNoSellerRow
	}
	if s.Status != "draft" && s.Status != "changes_required" {
		return postgres.ErrSellScopeLocked
	}
	s.SellScope = w.Scope
	return nil
}

func (f *scFakeStore) UpgradeSellScopeToAllCategories(_ context.Context, id uuid.UUID, gstin string) error {
	for _, s := range f.sellers {
		if s.ID == id && s.SellScope == postgres.SellScopeBooksOnly {
			s.SellScope, s.GSTNumber = postgres.SellScopeAllCategories, &gstin
			return nil
		}
	}
	return postgres.ErrSellScopeNotBooksOnly
}

func (f *scFakeStore) CategoryIsBookCategory(_ context.Context, id uuid.UUID) (bool, error) {
	return id == scTextbooks, nil
}

func (f *scFakeStore) BookCategoryIDs(context.Context) ([]uuid.UUID, error) {
	return []uuid.UUID{scTextbooks}, nil
}

func (f *scFakeStore) TaxClassIsZeroRated(_ context.Context, id uuid.UUID) (bool, error) {
	return id == scTax0, nil
}

type scWorld struct {
	svc *service.Service
	r   *gin.Engine
}

func newSellScopeWorld(t *testing.T) *scWorld {
	t.Helper()
	gin.SetMode(gin.TestMode)
	keys, _, err := pii.LocalKeyProvider(
		[]byte("0123456789abcdef0123456789abcdef"),
		[]byte("fedcba9876543210fedcba9876543210"), nil)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := pii.New(keys, []byte("sell-scope-salt!"))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := cipher.SealIdentifier(context.Background(), pii.ScopeKYC, "pan", scBooksPAN)
	if err != nil {
		t.Fatal(err)
	}
	gst := scSellerGSTIN
	st := &scFakeStore{sellers: map[uuid.UUID]*postgres.Seller{
		scSellerUser: {ID: scSeller, UserID: scSellerUser, Status: "approved", SellScope: "all_categories", GSTNumber: &gst},
		scDraftUser:  {ID: scDraftSeller, UserID: scDraftUser, Status: "draft", SellScope: "all_categories"},
		scBooksUser:  {ID: scBooksSeller, UserID: scBooksUser, Status: "draft", SellScope: "books_only", PANEnc: sealed.Enc},
	}}
	svc := (&service.Service{}).WithPII(cipher).WithSellScopeStore(st)
	r := gin.New()
	New(svc).RegisterOnboardingRoutes(r)
	return &scWorld{svc: svc, r: r}
}

func (w *scWorld) do(method, path string, user uuid.UUID, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", user.String())
	rec := httptest.NewRecorder()
	w.r.ServeHTTP(rec, req)
	return rec
}

func indentGolden(t *testing.T, raw []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "  "); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, raw)
	}
	out.WriteByte('\n')
	return out.Bytes()
}

func TestSellScopeGoldens(t *testing.T) {
	w := newSellScopeWorld(t)
	put := func(user uuid.UUID, body any) (int, []byte) {
		rec := w.do(http.MethodPut, "/v1/commerce/onboarding/step/sell-scope", user, body)
		return rec.Code, rec.Body.Bytes()
	}
	// Order matters, as in the integration run: the upgrade is last.
	cases := []struct {
		name string
		run  func() (int, []byte)
	}{
		{"seller/onboarding_sell_scope_put_200_books_only", func() (int, []byte) {
			return put(scBooksUser, map[string]any{"sell_scope": "books_only", "pan": "abcpe1234f", "business_name": scBooksStoreName})
		}},
		{"seller/onboarding_sell_scope_put_400_pan_invalid", func() (int, []byte) {
			return put(scBooksUser, map[string]any{"sell_scope": "books_only", "pan": "ABCDE12345", "business_name": scBooksStoreName})
		}},
		{"seller/onboarding_sell_scope_put_400_gstin_required", func() (int, []byte) {
			return put(scDraftUser, map[string]any{"sell_scope": "all_categories"})
		}},
		{"seller/onboarding_sell_scope_put_409_locked", func() (int, []byte) {
			return put(scSellerUser, map[string]any{"sell_scope": "all_categories", "gstin": scSellerGSTIN})
		}},
		{"seller/seller_sell_scope_get_200_books_only", func() (int, []byte) {
			rec := w.do(http.MethodGet, "/v1/commerce/seller/sell-scope", scBooksUser, nil)
			return rec.Code, rec.Body.Bytes()
		}},
		{"seller/product_post_403_seller_scope_books_only", func() (int, []byte) {
			// The create route needs the real store to resolve the caller;
			// the refusal is the service's, rendered by the route's mapper.
			_, err := w.svc.CreateProduct(context.Background(), service.CreateProductInput{
				SellerID: scBooksSeller, SellScope: "books_only", ActorUserID: scBooksUser,
				CategoryID: &scElectronics, TaxClassID: &scTax0, Title: "Momentum Power Bank",
				Variants: []service.CreateVariantInput{{SKU: "ASHA-PB-1", MRPMinor: 199900, SellingPriceMinor: 149900}},
			})
			if err == nil {
				t.Fatal("a books-only shop created an electronics listing")
			}
			code, body := renderProductWriteError(t, err)
			// As the encoder wrote it: compact, one trailing newline.
			return code, append(compactJSON(t, body), '\n')
		}},
		{"seller/onboarding_documents_put_400_back_not_accepted", func() (int, []byte) {
			rec := w.do(http.MethodPut, "/v1/commerce/onboarding/step/documents", scBooksUser, map[string]any{
				"documents": []map[string]any{{"document_type": "bank_statement", "media_id": scMediaKYC.String(), "back_media_id": scMedia2.String()}},
			})
			return rec.Code, rec.Body.Bytes()
		}},
		{"seller/seller_sell_scope_upgrade_post_400_gstin_pan_mismatch", func() (int, []byte) {
			rec := w.do(http.MethodPost, "/v1/commerce/seller/sell-scope/upgrade", scBooksUser, map[string]any{"gstin": scOtherGSTIN})
			return rec.Code, rec.Body.Bytes()
		}},
		{"seller/seller_sell_scope_upgrade_post_409_not_books_only", func() (int, []byte) {
			rec := w.do(http.MethodPost, "/v1/commerce/seller/sell-scope/upgrade", scSellerUser, map[string]any{"gstin": scSellerGSTIN})
			return rec.Code, rec.Body.Bytes()
		}},
		{"seller/seller_sell_scope_upgrade_post_200", func() (int, []byte) {
			rec := w.do(http.MethodPost, "/v1/commerce/seller/sell-scope/upgrade", scBooksUser, map[string]any{"gstin": scBooksGSTIN})
			return rec.Code, rec.Body.Bytes()
		}},
	}
	for _, tc := range cases {
		status, raw := tc.run()
		got := indentGolden(t, raw)
		if want := ctStatusFromName(tc.name); status != want {
			t.Errorf("%s: answered %d, the fixture name says %d\n%s", tc.name, status, want, got)
			continue
		}
		path := filepath.Join(contractsDir, tc.name+".json")
		if *updateSellScope {
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: %v (run with -update-sell-scope)", tc.name, err)
			continue
		}
		if !bytes.Equal(want, got) {
			t.Errorf("%s: golden differs from the handler.\n--- golden\n%s\n--- handler\n%s", tc.name, want, got)
		}
	}
}

// renderProductWriteError indents; the table re-indents, so hand it compact.
func compactJSON(t *testing.T, b []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := json.Compact(&out, b); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// The PAN never comes back in full on any of these routes.
func TestNoSellScopeAnswerCarriesTheFullPAN(t *testing.T) {
	w := newSellScopeWorld(t)
	for _, rec := range []*httptest.ResponseRecorder{
		w.do(http.MethodPut, "/v1/commerce/onboarding/step/sell-scope", scBooksUser,
			map[string]any{"sell_scope": "books_only", "pan": scBooksPAN, "business_name": scBooksStoreName}),
		w.do(http.MethodPut, "/v1/commerce/onboarding/step/sell-scope", scBooksUser,
			map[string]any{"sell_scope": "books_only", "pan": "ABCPX1234F", "business_name": scBooksStoreName}),
		w.do(http.MethodGet, "/v1/commerce/seller/sell-scope", scBooksUser, nil),
		w.do(http.MethodPost, "/v1/commerce/seller/sell-scope/upgrade", scBooksUser, map[string]any{"gstin": scOtherGSTIN}),
	} {
		if bytes.Contains(rec.Body.Bytes(), []byte("ABCPE1234F")) || bytes.Contains(rec.Body.Bytes(), []byte("ABCPX1234F")) ||
			bytes.Contains(rec.Body.Bytes(), []byte("PQRCS6789Z")) {
			t.Fatalf("a full PAN left the service: %s", rec.Body.String())
		}
	}
}
