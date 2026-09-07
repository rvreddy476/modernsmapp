package store

import (
	"context"
	"testing"

	"github.com/atpost/shared/identityroles"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests exercise the seam that actually matters: a partner lifecycle
// write and its identity role intent land in ONE transaction, in this
// service's own database. Nothing here talks to identity — delivery is the
// worker's job and is covered in shared/identityroles.
//
// Run with:
//
//	TEST_PG_DSN=postgres://postgres:postgres@127.0.0.1:5432/<scratch>?sslmode=disable go test ./internal/store/
//
// NEVER point TEST_PG_DSN at `app`. Integration fixtures aimed at the live
// database are what left 4773 junk sellers in commerce_db.

// riderTestStoreWithRoles is riderTestStore plus the role queue attached, and
// a clean intents table.
func riderTestStoreWithRoles(t *testing.T) (*Store, *pgxpool.Pool, func()) {
	t.Helper()
	st, cleanup := riderTestStore(t)
	pool := st.DB()
	if _, err := pool.Exec(context.Background(),
		`TRUNCATE `+identityroles.TableName("rider")+` RESTART IDENTITY`); err != nil {
		t.Fatalf("truncate intents (is setup.sql's DDL applied?): %v", err)
	}
	return st.WithRoleIntents(identityroles.NewOutbox("rider", "rider-service")), pool, cleanup
}

type intentRow struct {
	op     string
	userID string
	role   string
}

func readIntents(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID) []intentRow {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT op, user_id::text, role_name FROM `+identityroles.TableName("rider")+`
		  WHERE user_id = $1 ORDER BY id`, userID)
	if err != nil {
		t.Fatalf("read intents: %v", err)
	}
	defer rows.Close()
	var out []intentRow
	for rows.Next() {
		var r intentRow
		if err := rows.Scan(&r.op, &r.userID, &r.role); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func TestCreatePartnerEnqueuesGrantAtDraft(t *testing.T) {
	st, pool, cleanup := riderTestStoreWithRoles(t)
	defer cleanup()
	ctx := context.Background()

	userID := uuid.New()
	p, err := st.CreatePartner(ctx, CreatePartnerInput{
		UserID: userID, PartnerType: "individual_driver",
		FullName: "Grant At Draft", Phone: "+919000000001",
	})
	if err != nil {
		t.Fatalf("CreatePartner: %v", err)
	}
	if p.Status != "draft" {
		t.Fatalf("status = %q, want draft", p.Status)
	}
	got := readIntents(t, pool, userID)
	if len(got) != 1 || got[0].op != "grant" || got[0].role != identityroles.RoleRiderPartner {
		t.Fatalf("intents = %+v, want one grant of rider_partner — the role must be "+
			"granted at draft, or the partner cannot reach the KYC upload that gets them approved", got)
	}
}

func TestPartnerLifecycleIntents(t *testing.T) {
	cases := []struct {
		name   string
		status string
		wantOp string // "" means no intent at all
		why    string
	}{
		{"rejected revokes", "rejected", "revoke", "terminal denial"},
		{"blocked revokes", "blocked", "revoke", "hard removal"},
		{"suspended does not revoke", "suspended", "", "a pause — they still need the partner area to appeal"},
		{"pending_verification re-grants", "pending_verification", "grant", "still on the journey"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, pool, cleanup := riderTestStoreWithRoles(t)
			defer cleanup()
			ctx := context.Background()

			userID := uuid.New()
			p, err := st.CreatePartner(ctx, CreatePartnerInput{
				UserID: userID, PartnerType: "individual_driver",
				FullName: "Lifecycle", Phone: "+91900000" + tc.status[:4],
			})
			if err != nil {
				t.Fatalf("CreatePartner: %v", err)
			}
			// The create-time grant is intent #1; we care about #2.
			if err := st.UpdatePartnerStatus(ctx, p.ID, tc.status); err != nil {
				t.Fatalf("UpdatePartnerStatus(%s): %v", tc.status, err)
			}
			got := readIntents(t, pool, userID)
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

func TestApprovePartnerReGrants(t *testing.T) {
	st, pool, cleanup := riderTestStoreWithRoles(t)
	defer cleanup()
	ctx := context.Background()

	userID := uuid.New()
	p, err := st.CreatePartner(ctx, CreatePartnerInput{
		UserID: userID, PartnerType: "fleet_owner",
		FullName: "Approve Me", Phone: "+919000000002",
	})
	if err != nil {
		t.Fatalf("CreatePartner: %v", err)
	}
	if err := st.SetPartnerApprovedAt(ctx, p.ID); err != nil {
		t.Fatalf("SetPartnerApprovedAt: %v", err)
	}
	got := readIntents(t, pool, userID)
	if len(got) != 2 || got[1].op != "grant" {
		t.Fatalf("intents = %+v, want a second grant. Approval re-grants deliberately: "+
			"identity's grant is idempotent, and this is the repair for a create-time "+
			"intent that dead-lettered during an outage", got)
	}
}

func TestSuspendedByFraudJobDoesNotRevoke(t *testing.T) {
	st, pool, cleanup := riderTestStoreWithRoles(t)
	defer cleanup()
	ctx := context.Background()

	userID := uuid.New()
	p, err := st.CreatePartner(ctx, CreatePartnerInput{
		UserID: userID, PartnerType: "individual_driver",
		FullName: "Fraud Suspect", Phone: "+919000000003",
	})
	if err != nil {
		t.Fatalf("CreatePartner: %v", err)
	}
	if err := st.SetPartnerSuspended(ctx, p.ID, "auto:fraud_score>=90"); err != nil {
		t.Fatalf("SetPartnerSuspended: %v", err)
	}
	got := readIntents(t, pool, userID)
	if len(got) != 1 {
		t.Fatalf("intents = %+v, want only the create-time grant. A nightly heuristic "+
			"must not strip a partner's role — there is no unblock route to give it back", got)
	}
}

func TestNoIntentsWhenQueueUnattached(t *testing.T) {
	// The degrade path: a deployment without identity configured builds the
	// Store with no queue, and every lifecycle write must still work.
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	p, err := st.CreatePartner(ctx, CreatePartnerInput{
		UserID: uuid.New(), PartnerType: "individual_driver",
		FullName: "No Identity", Phone: "+919000000004",
	})
	if err != nil {
		t.Fatalf("CreatePartner with no role queue must still succeed: %v", err)
	}
	if err := st.UpdatePartnerStatus(ctx, p.ID, "rejected"); err != nil {
		t.Fatalf("UpdatePartnerStatus with no role queue must still succeed: %v", err)
	}
}

func TestUpdatePartnerStatusStillReportsNotFound(t *testing.T) {
	// The rewrite moved from Exec+RowsAffected to QueryRow+RETURNING; the
	// not-found contract the handler's 404 depends on must survive.
	st, _, cleanup := riderTestStoreWithRoles(t)
	defer cleanup()
	if err := st.UpdatePartnerStatus(context.Background(), uuid.New(), "approved"); err == nil {
		t.Fatal("want an error for an unknown partner id")
	} else if err.Error() == "" {
		t.Fatal("empty error")
	}
}
