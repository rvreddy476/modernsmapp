package http

// What a client is told when something is not there, or when what they sent
// is not allowed.
//
// The default arm of writeCommerceError is 500 with "something went wrong".
// That is right for a genuine surprise and wrong for everything that reached
// it by omission — and two of the three cases below reached it by omission,
// so `GET /products/<unknown uuid>` and a mistyped `document_type` both told
// the caller the platform was broken.

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// status runs one error through the real edge mapper.
func status(t *testing.T, err error) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/commerce/probe", nil)
	writeCommerceError(c, err)
	return w.Code, w.Body.String()
}

func TestAMissingProductIsNotFoundRatherThanBroken(t *testing.T) {
	code, body := status(t, fmt.Errorf("get product: %w", postgres.ErrProductNotFound))
	if code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 — an unknown product id reported the service as broken\n%s", code, body)
	}
}

// The sweep. A store read that never learned to translate pgx's own
// "no rows in result set" used to land in the default arm.
func TestRawNoRowsIsNotFoundRatherThanBroken(t *testing.T) {
	code, body := status(t, fmt.Errorf("some unmapped read: %w", pgx.ErrNoRows))
	if code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 for a bare pgx.ErrNoRows\n%s", code, body)
	}
}

// An unsupported KYC document type is the caller's mistake, and the answer
// has to name what IS supported — otherwise the seller has no way to fix it.
func TestAnUnsupportedDocumentTypeIsABadRequestThatNamesTheAlternatives(t *testing.T) {
	err := fmt.Errorf("%w: %q (allowed: gst_certificate, pan_card, aadhaar, passport, "+
		"business_registration, address_proof, cancelled_cheque, other)",
		service.ErrInvalidDocumentType, "drivers_license")

	code, body := status(t, err)
	if code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 — a check-constraint violation was surfacing as 500\n%s", code, body)
	}
	for _, want := range []string{"pan_card", "cancelled_cheque", "aadhaar"} {
		if !contains(body, want) {
			t.Errorf("the 400 body does not name %q; the seller cannot tell what to send\n%s", want, body)
		}
	}
}

// The last-resort arm: a constraint the client tripped is a 400, and the
// constraint name — which names our tables — does NOT go to the client.
func TestAnUnclaimedConstraintViolationIsABadRequestWithoutLeakingTheConstraint(t *testing.T) {
	pgErr := &pgconn.PgError{
		Code:           "23514",
		ConstraintName: "seller_documents_document_type_check",
		Message:        `new row for relation "seller_documents" violates check constraint`,
	}
	code, body := status(t, fmt.Errorf("save documents: %w", error(pgErr)))
	if code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 for a CHECK violation on a client-supplied value\n%s", code, body)
	}
	if contains(body, "seller_documents") {
		t.Errorf("the response names an internal constraint/table:\n%s", body)
	}
}

// And a genuinely unknown error is still a 500 — the mapping above must not
// have turned the default arm into a catch-all 4xx.
func TestAnUnknownErrorIsStillFiveHundred(t *testing.T) {
	code, body := status(t, errors.New("the disk caught fire"))
	if code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500 for a genuinely unexpected error\n%s", code, body)
	}
	if contains(body, "disk caught fire") {
		t.Errorf("the unexpected error's message was echoed to the client:\n%s", body)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		(func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		})()
}
