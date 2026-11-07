package http

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/user-service/internal/store"
)

// US-L3: each review refusal has its own answer; a duplicate is 409
// ALREADY_REVIEWED, not a 500 with Postgres's text.
func TestReviewErrorResponse(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{store.ErrPageNotFound, http.StatusNotFound, "NOT_FOUND"},
		{store.ErrPageNotReviewable, http.StatusUnprocessableEntity, "PAGE_NOT_REVIEWABLE"},
		{store.ErrCannotReviewOwn, http.StatusForbidden, "CANNOT_REVIEW_OWN_PAGE"},
		{fmt.Errorf("wrapped: %w", store.ErrAlreadyReviewed), http.StatusConflict, "ALREADY_REVIEWED"},
	} {
		if status, code, _ := reviewErrorResponse(tc.err); status != tc.status || code != tc.code {
			t.Errorf("%v: %d %s, want %d %s", tc.err, status, code, tc.status, tc.code)
		}
	}
	pgText := errors.New(`ERROR: duplicate key value violates unique constraint "business_reviews_page_id_reviewer_id_key" (SQLSTATE 23505)`)
	status, code, msg := reviewErrorResponse(pgText)
	if status != http.StatusInternalServerError || code != "INTERNAL_ERROR" || strings.Contains(msg, "duplicate key") {
		t.Fatalf("an unexpected error: %d %s %q; the database text must not reach the client", status, code, msg)
	}
}
