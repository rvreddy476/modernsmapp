//go:build integration

package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/atpost/user-service/internal/pages"
	"github.com/google/uuid"
)

// US-L3 against Postgres: a review needs an approved page and a reviewer who
// does not own or manage it, and a second review by the same person is
// ErrAlreadyReviewed. Requires TEST_PG_DSN (see channels_owner_integration_test.go).
func TestIntegrationPageReviewGate(t *testing.T) {
	st, pool := channelOwnerIT(t)
	ctx := context.Background()

	seed := func(t *testing.T, status string) (pageID, owner uuid.UUID) {
		t.Helper()
		owner, pageID = uuid.New(), uuid.New()
		handle := "rv" + strings.ReplaceAll(pageID.String(), "-", "")[:16]
		if _, err := pool.Exec(ctx,
			`INSERT INTO users (id, display_name, created_at, updated_at) VALUES ($1, 'Review Owner', now(), now())`,
			owner); err != nil {
			t.Fatalf("seed owner: %v", err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, owner) })
		if _, err := pool.Exec(ctx,
			`INSERT INTO business_pages (id, user_id, page_handle, page_name, category, status)
			 VALUES ($1, $2, $3, $3, 'shop', $4)`, pageID, owner, handle, status); err != nil {
			t.Fatalf("seed page: %v", err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM business_pages WHERE id = $1`, pageID) })
		return pageID, owner
	}
	review := func(pageID, reviewer uuid.UUID) error {
		return st.SubmitReview(ctx, &BusinessReview{PageID: pageID, ReviewerID: reviewer, Rating: 4, ReviewText: "ok"})
	}
	count := func(t *testing.T, pageID uuid.UUID) (n int) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT review_count FROM business_pages WHERE id = $1`, pageID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	approved, owner := seed(t, pages.StatusApproved)
	if err := review(approved, owner); !errors.Is(err, ErrCannotReviewOwn) {
		t.Fatalf("owner reviewing their own page: %v, want ErrCannotReviewOwn", err)
	}
	stranger := uuid.New()
	if err := review(approved, stranger); err != nil {
		t.Fatalf("a stranger reviewing an approved page: %v", err)
	}
	if err := review(approved, stranger); !errors.Is(err, ErrAlreadyReviewed) {
		t.Fatalf("second review by the same person: %v, want ErrAlreadyReviewed", err)
	}
	if n := count(t, approved); n != 1 {
		t.Fatalf("review_count %d, want 1", n)
	}

	for _, status := range []string{pages.StatusDraft, pages.StatusPendingReview, pages.StatusSuspended, pages.StatusDisabled} {
		pageID, _ := seed(t, status)
		if err := review(pageID, uuid.New()); !errors.Is(err, ErrPageNotReviewable) {
			t.Fatalf("review of a %s page: %v, want ErrPageNotReviewable", status, err)
		}
	}
}
