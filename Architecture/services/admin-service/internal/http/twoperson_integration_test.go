//go:build integration

package http

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/atpost/admin-service/database"
	"github.com/atpost/admin-service/internal/approvals"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The two-person flow and the denial trail against a real Postgres:
//
//	POSTGRES_DSN=postgres://…/admin_it_test go test -tags integration -p 1 ./internal/http/
func openIntegrationDB(t *testing.T) (*pgxpool.Pool, *postgres.Store) {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("POSTGRES_DSN does not parse")
	}
	if !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatalf("refusing to run against database %q: the name must end in _test", cfg.Database)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return pool, postgres.New(pool)
}

// dbRig is twoHolders over the real store and the real audit recorder, with a
// fresh remittance id so rows from earlier runs never match.
func dbRig(t *testing.T) (*rig, *pgxpool.Pool, string, string) {
	t.Helper()
	pool, store := openIntegrationDB(t)
	rg := twoHolders(t, store)
	rg.gate.audit = store
	rem := uuid.NewString()
	return rg, pool, "/v1/admin/commerce/cod-remittances/" + rem + "/settle", rem
}

func auditRows(t *testing.T, pool *pgxpool.Pool, entityID string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT admin_actor || '|' || action || '|' || outcome || '|' || CASE WHEN outcome = 'denied' THEN payload->>'code' ELSE COALESCE(payload->>'approval', '') END
		  FROM admin.audit_log WHERE entity_id = $1 ORDER BY created_at, id`, entityID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func TestIntegrationTwoPersonLifecycle(t *testing.T) {
	rg, pool, path, rem := dbRig(t)
	ctx := context.Background()

	w := rg.do(http.MethodPost, path, settleBody, adminA)
	if w.Code != http.StatusAccepted {
		t.Fatalf("request: %d %s", w.Code, w.Body.String())
	}
	a := approvalFrom(t, w)

	// The stored JSONB payload still verifies (canonical hashing survives JSONB).
	if w := approve(rg, a.ID, adminA); w.Code != http.StatusForbidden || !hasCode(w, CodeSelfApproval) {
		t.Fatalf("self-approval: %d %s", w.Code, w.Body.String())
	}
	if w := rg.do(http.MethodGet, "/v1/admin/approvals", "", adminB); !strings.Contains(w.Body.String(), a.ID) {
		t.Fatalf("inbox for adminB misses the approval: %s", w.Body.String())
	}
	if w := rg.do(http.MethodGet, "/v1/admin/approvals", "", adminA); strings.Contains(w.Body.String(), a.ID) {
		t.Fatal("the requester's inbox offers their own request")
	}

	w = approve(rg, a.ID, adminB)
	if w.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	}
	if w := approve(rg, a.ID, adminB); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"replayed":true`) {
		t.Fatalf("retry: %d %s", w.Code, w.Body.String())
	}
	if hits, actor, _, _ := rg.seen.snapshot(); hits != 1 || actor != adminB {
		t.Fatalf("executed %d times as %s", hits, actor)
	}

	var status, approver, outcome string
	var resultStatus int
	if err := pool.QueryRow(ctx, `SELECT status, approver::text, result_outcome, result_status FROM admin.approvals WHERE id = $1`, a.ID).
		Scan(&status, &approver, &outcome, &resultStatus); err != nil {
		t.Fatal(err)
	}
	if status != "executed" || approver != adminB || outcome != "success" || resultStatus != 200 {
		t.Fatalf("row: %s %s %s %d", status, approver, outcome, resultStatus)
	}

	want := []string{
		adminA + "|cod.settle|pending|requested",
		adminA + "|cod.settle|denied|" + CodeSelfApproval,
		adminB + "|cod.settle|success|approved",
		adminB + "|cod.settle|success|approved",
	}
	if got := auditRows(t, pool, rem); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("audit rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// The database refuses a self-decided row whatever the application does.
	if _, err := pool.Exec(ctx, `UPDATE admin.approvals SET approver = requester WHERE id = $1`, a.ID); err == nil {
		t.Fatal("approver = requester was stored")
	}
}

func TestIntegrationTamperedPayloadIsRefused(t *testing.T) {
	rg, pool, path, _ := dbRig(t)
	ctx := context.Background()
	a := approvalFrom(t, rg.do(http.MethodPost, path, settleBody, adminA))

	if _, err := pool.Exec(ctx, `UPDATE admin.approvals SET payload = jsonb_set(payload, '{payout_batch_id}', '"00000000-0000-4000-8000-000000000001"') WHERE id = $1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if w := approve(rg, a.ID, adminB); w.Code != http.StatusConflict || !hasCode(w, CodePayloadHashMismatch) {
		t.Fatalf("tampered: %d %s", w.Code, w.Body.String())
	}
	if hits, _, _, _ := rg.seen.snapshot(); hits != 0 {
		t.Fatal("a tampered approval executed")
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM admin.approvals WHERE id = $1`, a.ID).Scan(&status); err != nil || status != "rejected" {
		t.Fatalf("status %q err %v", status, err)
	}
}

func TestIntegrationExpiredApprovalIsRefused(t *testing.T) {
	rg, pool, path, _ := dbRig(t)
	ctx := context.Background()
	a := approvalFrom(t, rg.do(http.MethodPost, path, settleBody, adminA))

	if _, err := pool.Exec(ctx, `UPDATE admin.approvals SET created_at = NOW() - INTERVAL '25 hours', expires_at = NOW() - INTERVAL '1 hour' WHERE id = $1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if w := approve(rg, a.ID, adminB); w.Code != http.StatusGone || !hasCode(w, CodeApprovalExpired) {
		t.Fatalf("expired: %d %s", w.Code, w.Body.String())
	}
	if w := rg.do(http.MethodGet, "/v1/admin/approvals", "", adminB); strings.Contains(w.Body.String(), a.ID) {
		t.Fatal("expired approval listed")
	}
	if hits, _, _, _ := rg.seen.snapshot(); hits != 0 {
		t.Fatal("an expired approval executed")
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM admin.approvals WHERE id = $1`, a.ID).Scan(&status); err != nil || status != approvals.StatusExpired {
		t.Fatalf("status %q err %v", status, err)
	}
}

func TestIntegrationSoleHolderAndDenialsAreInTheTrail(t *testing.T) {
	rg, pool, path, rem := dbRig(t)
	rg.holders.n = 0

	if w := rg.do(http.MethodPost, path, settleBody, adminA); w.Code != http.StatusOK {
		t.Fatalf("sole holder: %d %s", w.Code, w.Body.String())
	}
	if w := rg.do(http.MethodPost, path, settleBody, adminA, noMFA); !hasCode(w, "MFA_REQUIRED") {
		t.Fatalf("no MFA: %d", w.Code)
	}
	if w := rg.do(http.MethodPost, path, settleBody, adminA, noStepUp); !hasCode(w, "STEP_UP_REQUIRED") {
		t.Fatalf("no step-up: %d", w.Code)
	}
	if w := rg.do(http.MethodPost, path, settleBody, nobody); !hasCode(w, CodePermissionDenied) {
		t.Fatalf("no permission: %d", w.Code)
	}

	// Gate denials happen before the handler names the target, so they are
	// recorded against the request path; the sole-holder execution against the remittance.
	if got := auditRows(t, pool, rem); len(got) != 1 || got[0] != adminA+"|cod.settle|success|sole_holder" {
		t.Fatalf("remittance rows %v", got)
	}
	var denied int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM admin.audit_log
		 WHERE entity_type = 'route' AND entity_id = $3
		   AND outcome = 'denied' AND request_id = 'req-123' AND created_at > NOW() - INTERVAL '1 minute'
		   AND admin_actor IN ($1, $2)`, adminA, nobody, path).Scan(&denied); err != nil {
		t.Fatal(err)
	}
	if denied != 3 {
		t.Fatalf("denied rows = %d, want 3", denied)
	}
}
