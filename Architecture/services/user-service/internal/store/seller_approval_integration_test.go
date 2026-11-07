//go:build integration

package store

import (
	"context"
	"strings"
	"testing"

	"github.com/atpost/user-service/internal/pages"
	"github.com/google/uuid"
)

// US-M3 against Postgres: ApplySellerApproval links only the owner's page,
// approves only out of pending_review, never revives a disabled page, and
// DiscoverPages lists approved pages. Requires TEST_PG_DSN (see
// channels_owner_integration_test.go).
func TestIntegrationSellerApprovalRespectsOwnerAndLifecycle(t *testing.T) {
	st, pool := channelOwnerIT(t)
	ctx := context.Background()

	seedPage := func(t *testing.T, status string) (pageID, owner uuid.UUID) {
		t.Helper()
		owner, pageID = uuid.New(), uuid.New()
		handle := "sp" + strings.ReplaceAll(pageID.String(), "-", "")[:16]
		if _, err := pool.Exec(ctx,
			`INSERT INTO users (id, display_name, created_at, updated_at) VALUES ($1, 'Page Owner', now(), now())`,
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
	read := func(t *testing.T, pageID uuid.UUID) (string, *uuid.UUID) {
		t.Helper()
		var status string
		var seller *uuid.UUID
		if err := pool.QueryRow(ctx, `SELECT status, seller_id FROM business_pages WHERE id = $1`, pageID).Scan(&status, &seller); err != nil {
			t.Fatal(err)
		}
		return status, seller
	}

	t.Run("own page under review is approved and linked", func(t *testing.T) {
		pageID, owner := seedPage(t, pages.StatusPendingReview)
		seller := uuid.New()
		out, err := st.ApplySellerApproval(ctx, pageID, seller, owner, uuid.New())
		if err != nil || out != SellerApprovalLinkedApproved {
			t.Fatalf("got (%q, %v)", out, err)
		}
		status, linked := read(t, pageID)
		if status != pages.StatusApproved || linked == nil || *linked != seller {
			t.Fatalf("status %q seller %v", status, linked)
		}
		found, err := st.DiscoverPages(ctx, "", strings.ReplaceAll(pageID.String(), "-", "")[:16], 10, 0)
		if err != nil || len(found) != 1 || found[0].ID != pageID {
			t.Fatalf("an approved page is missing from Discover: (%v, %v)", found, err)
		}
	})
	t.Run("disabled page stays disabled", func(t *testing.T) {
		pageID, owner := seedPage(t, pages.StatusDisabled)
		if _, err := st.ApplySellerApproval(ctx, pageID, uuid.New(), owner, uuid.New()); err != nil {
			t.Fatal(err)
		}
		if status, _ := read(t, pageID); status != pages.StatusDisabled {
			t.Fatalf("a disabled page was revived to %q", status)
		}
	})
	t.Run("someone else's page is untouched", func(t *testing.T) {
		pageID, _ := seedPage(t, pages.StatusPendingReview)
		out, err := st.ApplySellerApproval(ctx, pageID, uuid.New(), uuid.New(), uuid.New())
		if err != nil || out != SellerApprovalNotOwner {
			t.Fatalf("got (%q, %v)", out, err)
		}
		if status, linked := read(t, pageID); status != pages.StatusPendingReview || linked != nil {
			t.Fatalf("a stranger's approval changed the page: status %q seller %v", status, linked)
		}
	})
	t.Run("missing page", func(t *testing.T) {
		out, err := st.ApplySellerApproval(ctx, uuid.New(), uuid.New(), uuid.New(), uuid.New())
		if err != nil || out != SellerApprovalPageMissing {
			t.Fatalf("got (%q, %v)", out, err)
		}
	})
}
