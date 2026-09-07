package postgres

import (
	"context"
	"testing"

	"github.com/atpost/shared/identityroles"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests exercise the seam that matters: a partner lifecycle write and
// its identity role intent land in ONE transaction in food's own database.
// Nothing here talks to identity — delivery is the worker's job and is covered
// in shared/identityroles.
//
// Run with:
//
//	TEST_PG_DSN=postgres://postgres:postgres@127.0.0.1:5432/<scratch>?sslmode=disable go test ./internal/store/postgres/
//
// NEVER point TEST_PG_DSN at `app`.

func foodTestStoreWithRoles(t *testing.T) (*Store, *pgxpool.Pool, func()) {
	t.Helper()
	st, cleanup := foodTestStore(t)
	pool := st.db
	if _, err := pool.Exec(context.Background(),
		`TRUNCATE `+identityroles.TableName("")+` RESTART IDENTITY`); err != nil {
		t.Fatalf("truncate intents (is setup.sql's DDL applied?): %v", err)
	}
	return st.WithRoleIntents(identityroles.NewOutbox("", "food-service")), pool, cleanup
}

type foodIntent struct {
	op   string
	role string
}

func readFoodIntents(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) []foodIntent {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT op, role_name FROM `+identityroles.TableName("")+`
		  WHERE user_id = $1 ORDER BY id`, userID)
	if err != nil {
		t.Fatalf("read intents: %v", err)
	}
	defer rows.Close()
	var out []foodIntent
	for rows.Next() {
		var r foodIntent
		if err := rows.Scan(&r.op, &r.role); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func TestUpsertDeliveryPartnerEnqueuesGrant(t *testing.T) {
	st, pool, cleanup := foodTestStoreWithRoles(t)
	defer cleanup()
	ctx := context.Background()

	userID := uuid.New()
	p, err := st.UpsertDeliveryPartner(ctx, userID, DeliveryPartnerInput{
		FullName: "Grant At Pending", Phone: "+919111111101",
	})
	if err != nil {
		t.Fatalf("UpsertDeliveryPartner: %v", err)
	}
	if p.Status != "PENDING_REVIEW" {
		t.Fatalf("status = %q, want PENDING_REVIEW", p.Status)
	}
	got := readFoodIntents(t, pool, userID)
	if len(got) != 1 || got[0].op != "grant" || got[0].role != identityroles.RoleDeliveryPartner {
		t.Fatalf("intents = %+v, want one grant of delivery_partner — the role must be "+
			"granted at PENDING_REVIEW, or the partner cannot reach the document upload "+
			"that gets them approved", got)
	}
}

func TestDeliveryPartnerAdminStatusIntents(t *testing.T) {
	cases := []struct {
		status string
		wantOp string // "" means no second intent
		why    string
	}{
		{"REJECTED", "revoke", "terminal denial"},
		{"CLOSED", "revoke", "partner removed"},
		{"SUSPENDED", "", "a pause; they still need the partner area to appeal"},
		{"OFFLINE", "", "availability, not membership"},
		{"ACTIVE", "grant", "idempotent re-grant"},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			st, pool, cleanup := foodTestStoreWithRoles(t)
			defer cleanup()
			ctx := context.Background()

			userID := uuid.New()
			p, err := st.UpsertDeliveryPartner(ctx, userID, DeliveryPartnerInput{
				FullName: "Lifecycle", Phone: "+9191111112" + tc.status[:2],
			})
			if err != nil {
				t.Fatalf("UpsertDeliveryPartner: %v", err)
			}
			if err := st.AdminSetDeliveryPartnerStatus(ctx, uuid.New(), p.ID, tc.status, "because"); err != nil {
				t.Fatalf("AdminSetDeliveryPartnerStatus(%s): %v", tc.status, err)
			}
			got := readFoodIntents(t, pool, userID)
			if tc.wantOp == "" {
				if len(got) != 1 {
					t.Fatalf("intents = %+v, want only the create-time grant — %s", got, tc.why)
				}
				return
			}
			if len(got) != 2 || got[1].op != tc.wantOp {
				t.Fatalf("intents = %+v, want a second %q — %s", got, tc.wantOp, tc.why)
			}
		})
	}
}

func TestRestaurantOwnerRevokeOnlyWhenLastRestaurant(t *testing.T) {
	// owner_user_id is NOT unique in food.restaurant_partners. Rejecting one
	// restaurant must not strip `restaurant_owner` from someone who still runs
	// another — the trap this whole helper exists for.
	st, pool, cleanup := foodTestStoreWithRoles(t)
	defer cleanup()
	ctx := context.Background()

	ownerID := uuid.New()
	first, err := st.CreatePartnerRestaurant(ctx, ownerID, PartnerRestaurantInput{
		Name: "First Kitchen", AddressLine1: "1 Road", City: "Bengaluru", Slug: "first-kitchen-" + ownerID.String()[:8],
	})
	if err != nil {
		t.Fatalf("CreatePartnerRestaurant (first): %v", err)
	}
	if _, err := st.CreatePartnerRestaurant(ctx, ownerID, PartnerRestaurantInput{
		Name: "Second Kitchen", AddressLine1: "2 Road", City: "Bengaluru", Slug: "second-kitchen-" + ownerID.String()[:8],
	}); err != nil {
		t.Fatalf("CreatePartnerRestaurant (second): %v", err)
	}

	// Reject the first. The owner still has the second, so NO revoke.
	// PartnerRestaurant.ID is the RESTAURANT id, which is what
	// AdminApproveRestaurant takes; PartnerID is the partner row it updates.
	if err := st.AdminApproveRestaurant(ctx, uuid.New(), first.ID, false, "no"); err != nil {
		t.Fatalf("AdminApproveRestaurant(reject): %v", err)
	}
	got := readFoodIntents(t, pool, ownerID)
	for _, in := range got {
		if in.op == "revoke" {
			t.Fatalf("intents = %+v — revoked restaurant_owner while the owner still runs "+
				"another live restaurant", got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("intents = %+v, want two create-time grants", got)
	}
}

func TestNoFoodIntentsWhenQueueUnattached(t *testing.T) {
	// The degrade path: no identity configured, every write still works.
	st, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := st.UpsertDeliveryPartner(ctx, uuid.New(), DeliveryPartnerInput{
		FullName: "No Identity", Phone: "+919111111199",
	}); err != nil {
		t.Fatalf("UpsertDeliveryPartner with no role queue must still succeed: %v", err)
	}
}
