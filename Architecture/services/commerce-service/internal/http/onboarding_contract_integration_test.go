//go:build integration

package http

// Getting a shop approved.
//
// `SubmitSellerApplication` flipped any `draft` seller to `submitted` and
// checked nothing else. So a shop with no PAN, no bank account and no pickup
// address could be submitted, and three things went wrong at once, all quietly:
//
//   - the reviewer's queue filled with applications that could not be actioned;
//   - the seller was told "submitted" and waited, when nothing was sent;
//   - a reviewer who approved anyway put live a shop that could take money and
//     had no settlement path.
//
// And the payout step — where a seller supplies the account they are paid into
// — failed on EVERY call since it was written, because its `ON CONFLICT
// (seller_id) WHERE is_primary` matched no index. Migration 021 adds it.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/http/... -v

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// draftShop creates a seller in `draft` with nothing else filled in.
func draftShop(t *testing.T, r interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, userID uuid.UUID) {
	t.Helper()
	w := call(t, r, http.MethodPost, "/v1/commerce/onboarding/start", userID,
		map[string]any{
			"store_name": "Draft Shop " + uuid.New().String()[:6],
			"email":      "draft-" + uuid.New().String()[:6] + "@example.test",
		})
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("start: status %d\n%s", w.Code, w.Body.String())
	}
}

type readiness struct {
	Ready   bool     `json:"ready"`
	Missing []string `json:"missing"`
}

func readinessOf(t *testing.T, r interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, userID uuid.UUID) readiness {
	t.Helper()
	w := call(t, r, http.MethodGet, "/v1/commerce/onboarding/readiness", userID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("readiness: status %d\n%s", w.Code, w.Body.String())
	}
	var env struct {
		Data readiness `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v\n%s", err, w.Body.String())
	}
	return env.Data
}

// sellerIDOf resolves the seller row a user owns.
func sellerIDOf(t *testing.T, userID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := edgePool.QueryRow(context.Background(),
		`SELECT id FROM sellers WHERE user_id = $1`, userID).Scan(&id); err != nil {
		t.Fatalf("seller row: %v", err)
	}
	return id
}

// The defect: an empty application reached a human reviewer.
func TestAnEmptyApplicationCannotBeSubmitted(t *testing.T) {
	r := journeyEngine(t, 4000)
	userID := uuid.New()
	draftShop(t, r, userID)

	w := call(t, r, http.MethodPost, "/v1/commerce/onboarding/submit", userID, nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409 — a shop with no bank account, no KYC and no "+
			"pickup address just reached a reviewer's queue\n%s", w.Code, w.Body.String())
	}

	// And it is still a draft, not "submitted".
	var status string
	if err := edgePool.QueryRow(context.Background(),
		`SELECT status FROM sellers WHERE user_id = $1`, userID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "draft" {
		t.Fatalf("seller status = %q after a refused submission; the seller is told they "+
			"are waiting for a review that will never come", status)
	}
}

// The refusal names everything missing, not the first thing.
func TestTheRefusalNamesEverythingStillMissing(t *testing.T) {
	r := journeyEngine(t, 4000)
	userID := uuid.New()
	draftShop(t, r, userID)

	body := call(t, r, http.MethodPost, "/v1/commerce/onboarding/submit", userID, nil).Body.String()
	for _, want := range []string{"pickup_address", "payout_account", "kyc_document"} {
		if !strings.Contains(body, want) {
			t.Errorf("the refusal does not mention %s — the seller fixes one thing, "+
				"resubmits, and is refused again\n%s", want, body)
		}
	}
}

// The checklist and the guard agree.
func TestTheReadinessChecklistMatchesWhatSubmitEnforces(t *testing.T) {
	r := journeyEngine(t, 4000)
	userID := uuid.New()
	draftShop(t, r, userID)

	ready := readinessOf(t, r, userID)
	if ready.Ready {
		t.Fatal("a shop with nothing filled in reports itself ready")
	}
	body := call(t, r, http.MethodPost, "/v1/commerce/onboarding/submit", userID, nil).Body.String()
	for _, item := range ready.Missing {
		if !strings.Contains(body, item) {
			t.Errorf("the checklist says %q is missing and the refusal does not mention "+
				"it; a seller can complete the checklist and still be refused", item)
		}
	}
}

// ─── The payout step, which never once worked ──────────────────────────

func TestSavingAPayoutAccountWorks(t *testing.T) {
	ctx := context.Background()
	r := journeyEngine(t, 4000)
	userID := uuid.New()
	draftShop(t, r, userID)

	w := call(t, r, http.MethodPut, "/v1/commerce/onboarding/step/payout", userID,
		map[string]any{
			"account_holder_name": "A Seller",
			"bank_name":           "Test Bank",
			"account_number":      "000111222333",
			"ifsc_code":           "TEST0000001",
		})
	if w.Code != http.StatusOK && w.Code != http.StatusNoContent {
		t.Fatalf("status %d — the payout step failed on every call before migration 021, "+
			"because its ON CONFLICT matched no index\n%s", w.Code, w.Body.String())
	}

	var count int
	if err := edgePool.QueryRow(ctx, `
		SELECT count(*) FROM seller_payout_accounts
		 WHERE seller_id = $1 AND is_primary`, sellerIDOf(t, userID)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("%d primary payout accounts stored, want 1", count)
	}
}

// Editing bank details replaces them rather than accumulating a second
// primary — which is what the ON CONFLICT was always trying to express.
func TestChangingBankDetailsReplacesThem(t *testing.T) {
	ctx := context.Background()
	r := journeyEngine(t, 4000)
	userID := uuid.New()
	draftShop(t, r, userID)

	save := func(account string) {
		t.Helper()
		w := call(t, r, http.MethodPut, "/v1/commerce/onboarding/step/payout", userID,
			map[string]any{
				"account_holder_name": "A Seller",
				"bank_name":           "Test Bank",
				"account_number":      account,
				"ifsc_code":           "TEST0000001",
			})
		if w.Code != http.StatusOK && w.Code != http.StatusNoContent {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
	}
	save("000111222333")
	save("999888777666")

	var count int
	var stored string
	if err := edgePool.QueryRow(ctx, `
		SELECT count(*), max(account_number) FROM seller_payout_accounts
		 WHERE seller_id = $1 AND is_primary`, sellerIDOf(t, userID)).Scan(&count, &stored); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("%d primary payout accounts after an edit — money has two destinations "+
			"and a tie-break decides which", count)
	}
	if stored != "999888777666" {
		t.Fatalf("stored account %q, want the new one", stored)
	}
}

// ─── A complete application goes through ───────────────────────────────

// The whole point: a shop that has everything can be submitted.
func TestACompleteApplicationIsSubmitted(t *testing.T) {
	ctx := context.Background()
	r := journeyEngine(t, 4000)
	userID := uuid.New()
	draftShop(t, r, userID)
	sellerID := sellerIDOf(t, userID)

	// Pickup address, through the real route — sealed, like any other.
	if w := call(t, r, http.MethodPut, "/v1/commerce/seller/address", userID,
		map[string]any{
			"contact_name": "Warehouse Desk", "phone": "9000000000",
			"address_line_1": "1 Warehouse Rd", "city": "Bengaluru",
			"state": "KA", "postal_code": "560001",
		}); w.Code != http.StatusNoContent {
		t.Fatalf("address: status %d\n%s", w.Code, w.Body.String())
	}

	// Payout, through the real route.
	if w := call(t, r, http.MethodPut, "/v1/commerce/onboarding/step/payout", userID,
		map[string]any{
			"account_holder_name": "A Seller", "bank_name": "Test Bank",
			"account_number": "000111222333", "ifsc_code": "TEST0000001",
		}); w.Code != http.StatusOK && w.Code != http.StatusNoContent {
		t.Fatalf("payout: status %d\n%s", w.Code, w.Body.String())
	}

	// A KYC document. Inserted directly: the route that writes it verifies the
	// media id against media-service, which this test has no stub for — and
	// that verification has its own proofs.
	if _, err := edgePool.Exec(ctx, `
		INSERT INTO seller_documents (seller_id, document_type, media_id)
		VALUES ($1, 'pan_card', gen_random_uuid())`, sellerID); err != nil {
		t.Fatal(err)
	}

	if ready := readinessOf(t, r, userID); !ready.Ready {
		t.Fatalf("a complete shop reports itself unready, still missing %v", ready.Missing)
	}

	w := call(t, r, http.MethodPost, "/v1/commerce/onboarding/submit", userID, nil)
	if w.Code != http.StatusOK && w.Code != http.StatusNoContent {
		t.Fatalf("submit: status %d — a complete application cannot be submitted, so no "+
			"seller can ever be approved\n%s", w.Code, w.Body.String())
	}

	var status string
	if err := edgePool.QueryRow(ctx,
		`SELECT status FROM sellers WHERE user_id = $1`, userID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "submitted" {
		t.Fatalf("seller status = %q after a successful submit, want submitted", status)
	}
}

// ─── A KYC document belongs to the person submitting it ────────────────

// The serious case in the media-ownership work, end to end.
//
// `seller_documents` is what a human reviewer checks identity against. A
// seller who could point that row at somebody else's uploaded PAN card would
// be approved on it, which makes the review meaningless — and is how one
// person's identity documents end up attached to another person's payout
// account.
func TestAKycDocumentMustBelongToTheSellerSubmittingIt(t *testing.T) {
	ctx := context.Background()
	s := seedSellerSurface(t, 1)
	victimUpload := uuid.New()

	// media-service says this asset belongs to somebody else.
	r := mediaEngine(t, mediaStub(t, victimUpload, s.otherUserID, "image"))

	w := call(t, r, http.MethodPut, "/v1/commerce/onboarding/step/documents", s.sellerUserID,
		map[string]any{
			"documents": []map[string]any{
				{"document_type": "pan_card", "media_id": victimUpload.String()},
			},
		})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403 — a seller just filed somebody else's identity "+
			"document as their own KYC\n%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "NOT_YOUR_MEDIA") {
		t.Fatalf("the refusal does not name the reason\n%s", w.Body.String())
	}

	// And nothing was stored, so the shop is no closer to being submittable
	// on the strength of a document it does not own.
	var count int
	if err := edgePool.QueryRow(ctx,
		`SELECT count(*) FROM seller_documents WHERE seller_id = $1`, s.sellerID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("%d documents stored despite the refusal", count)
	}
}

// The seller's own document is accepted, and satisfies the checklist.
func TestASellersOwnDocumentIsAcceptedAndCountsTowardsReview(t *testing.T) {
	s := seedSellerSurface(t, 1)
	ownUpload := uuid.New()
	r := mediaEngine(t, mediaStub(t, ownUpload, s.sellerUserID, "image"))

	w := call(t, r, http.MethodPut, "/v1/commerce/onboarding/step/documents", s.sellerUserID,
		map[string]any{
			"documents": []map[string]any{
				{"document_type": "pan_card", "media_id": ownUpload.String(),
					"document_number": "ABCDE1234F"},
			},
		})
	if w.Code != http.StatusOK && w.Code != http.StatusNoContent {
		t.Fatalf("status %d — a seller cannot file their own document\n%s",
			w.Code, w.Body.String())
	}

	// The readiness check no longer lists it.
	ready := readinessOf(t, r, s.sellerUserID)
	for _, missing := range ready.Missing {
		if missing == "kyc_document" {
			t.Fatal("the checklist still wants an identity document after one was filed")
		}
	}
}
