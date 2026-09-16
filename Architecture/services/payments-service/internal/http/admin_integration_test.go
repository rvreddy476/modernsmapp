//go:build integration

package http

// The admin console family end to end: real handler, real service, live
// PostgreSQL (payments_it_test). Two test applications keep the seeded rows
// apart; stats are asserted as deltas so earlier runs do not matter.
//
//	PAYMENTS_TEST_DSN=... go test -tags=integration ./internal/http/ -run AdminIntegration -v -count=1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/atpost/payments-service/internal/service"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	admItAppA = "it_admin_a"
	admItAppB = "it_admin_b"
)

type admItRig struct {
	pool   *pgxpool.Pool
	store  *postgres.Store
	svc    *service.Service
	r      *gin.Engine
	signer *servicetoken.Signer
}

func newAdmItRig(t *testing.T) *admItRig {
	t.Helper()
	pool := itPool(t)
	store := postgres.New(pool)
	svc := service.New(store, nil)
	appItEnsure(t, store, admItAppA, postgres.ApplicationStatusActive, "upi", "card")
	appItEnsure(t, store, admItAppB, postgres.ApplicationStatusActive, "upi", "card")

	v := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := servicetoken.NewSignerFromBase64(IssuerAdminService, "a1", priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.RegisterBase64(IssuerAdminService, "a1", pub, AdminPermissions, nil); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(svc).WithInternalKey(testInternalKey).WithServiceAuth(v).WithAdmin(svc, 10*time.Minute)
	if err := h.RegisterRoutes(r); err != nil {
		t.Fatal(err)
	}
	return &admItRig{pool: pool, store: store, svc: svc, r: r, signer: signer}
}

func (g *admItRig) as(t *testing.T, actor uuid.UUID, perm string) map[string]string {
	t.Helper()
	tok, err := g.signer.Mint(servicetoken.AudiencePayments, "admin-console", []string{perm}, nil, time.Minute,
		servicetoken.WithActor(actor.String()))
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{ServiceAuthHeader: "Bearer " + tok}
}

// intent seeds one intent of app with status, created ago.
func (g *admItRig) intent(t *testing.T, app, status string, amount int64, ago time.Duration) uuid.UUID {
	t.Helper()
	id := uuid.New()
	s := strings.ReplaceAll(uuid.NewString(), "-", "")[:14]
	if _, err := g.pool.Exec(context.Background(), `
		INSERT INTO payments.payment_intents
		    (id, payer_id, payee_id, reference_type, reference_id, amount, amount_minor, currency, method, status,
		     provider, provider_ref, provider_order_id, provider_payment_id, owner_domain, idempotency_key,
		     created_at, updated_at, application_id)
		VALUES ($1,$2,$3,'food_order',$4,$5::numeric/100,$5,'INR','upi',$6,'razorpay',$7,$7,$8,'food-service',$9,
		        NOW() - $10::interval, NOW(), $11)`,
		id, uuid.New(), uuid.New(), uuid.New(), amount, status, "order_"+s, "pay_"+s, "it-admin-"+id.String(),
		fmt.Sprintf("%d seconds", int(ago.Seconds())), app); err != nil {
		t.Fatalf("seed intent: %v", err)
	}
	return id
}

// refund opens a refund command on a succeeded intent; parked parks it, else
// it is left pending with a last error, as a failed attempt leaves it.
func (g *admItRig) refund(t *testing.T, app string, parked bool) (intentID, cmdID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	intentID = g.intent(t, app, "succeeded", 60712, time.Minute)
	cmd, err := g.svc.RequestRefund(ctx, service.RefundRequest{
		IntentID: intentID, AmountMinor: 60712, Reason: "admin integration",
		ProviderIdempotencyKey: "it-admin-refund-" + intentID.String(), CallerDomain: "food-service", ApplicationID: app,
	})
	if err != nil {
		t.Fatalf("request refund: %v", err)
	}
	if parked {
		if ok, err := g.store.ParkRefundCommand(ctx, cmd.ID, "provider_rejected", "integration: admin console"); err != nil || !ok {
			t.Fatalf("park: %v %v", ok, err)
		}
	} else if _, err := g.pool.Exec(ctx,
		`UPDATE payments.refund_commands SET last_error = 'integration: provider 502', next_attempt_at = NOW() + INTERVAL '30 days'
		  WHERE id = $1`, cmd.ID); err != nil {
		t.Fatalf("fail attempt: %v", err)
	}
	t.Cleanup(func() {
		// Nothing left parked or due for another run's worker or alarm.
		_, _ = g.pool.Exec(context.Background(),
			`UPDATE payments.refund_commands SET status = 'resolved', resolution = 'test_data', resolution_note = 'cleanup',
			        resolved_by = 'it-cleanup', resolved_at = NOW(), last_error = NULL
			  WHERE id = $1 AND status IN ('pending','submitted','needs_attention')`, cmd.ID)
	})
	return intentID, cmd.ID
}

func (g *admItRig) stats(t *testing.T, query string) postgres.AdminStats {
	t.Helper()
	w := do(g.r, http.MethodGet, InternalAdminPrefix+"/stats"+query, nil, g.as(t, uuid.New(), PermStatsRead))
	if w.Code != http.StatusOK {
		t.Fatalf("stats: %d %s", w.Code, w.Body.String())
	}
	var env struct {
		Data postgres.AdminStats `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	return env.Data
}

func (g *admItRig) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := g.pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func TestAdminIntegration_StatsAndApplicationFilter(t *testing.T) {
	g := newAdmItRig(t)
	beforeA := g.stats(t, "?application_id="+admItAppA)
	beforeAll := g.stats(t, "")

	// Application A: one of each.
	g.refund(t, admItAppA, true)                                           // refunds_needing_attention
	g.refund(t, admItAppA, false)                                          // refund_failed_alerts_open
	g.intent(t, admItAppA, "failed", 1000, time.Hour)                      // failed_payments_24h
	stuck := g.intent(t, admItAppA, "pending", 2000, time.Hour)            // stuck_intents
	fresh := g.intent(t, admItAppA, "pending", 2000, time.Minute)          // inside the window: not stuck
	captured := g.intent(t, admItAppA, "succeeded", 50000, 30*time.Minute) // captured_today_minor
	if _, err := g.pool.Exec(context.Background(),
		`INSERT INTO payments.payment_audit_log (intent_id, event, old_status, new_status, metadata)
		 VALUES ($1,'provider_webhook','pending','succeeded','{"provider":"razorpay","event_id":"it-admin"}')`, captured); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// No pending row left behind for another test's reconciler window.
		if _, err := g.pool.Exec(context.Background(),
			`UPDATE payments.payment_intents SET status = 'failed', updated_at = NOW() - INTERVAL '2 days'
			  WHERE id = ANY($1) AND status = 'pending'`, []uuid.UUID{stuck, fresh}); err != nil {
			t.Errorf("cleanup pending intents: %v", err)
		}
	})
	// Application B: a failed payment only.
	g.intent(t, admItAppB, "failed", 1000, time.Hour)

	afterA := g.stats(t, "?application_id="+admItAppA)
	afterAll := g.stats(t, "")

	delta := func(a, b postgres.AdminStatCounts) postgres.AdminStatCounts {
		return postgres.AdminStatCounts{
			RefundsNeedingAttention: a.RefundsNeedingAttention - b.RefundsNeedingAttention,
			FailedPayments24h:       a.FailedPayments24h - b.FailedPayments24h,
			RefundFailedAlertsOpen:  a.RefundFailedAlertsOpen - b.RefundFailedAlertsOpen,
			StuckIntents:            a.StuckIntents - b.StuckIntents,
			CapturedTodayMinor:      a.CapturedTodayMinor - b.CapturedTodayMinor,
		}
	}
	want := postgres.AdminStatCounts{RefundsNeedingAttention: 1, FailedPayments24h: 1, RefundFailedAlertsOpen: 1,
		StuckIntents: 1, CapturedTodayMinor: 50000}
	if got := delta(afterA.Applications[admItAppA], beforeA.Applications[admItAppA]); got != want {
		t.Fatalf("A per-application delta = %+v, want %+v", got, want)
	}
	if got := delta(afterA.Total, beforeA.Total); got != want {
		t.Fatalf("A-filtered total delta = %+v, want %+v (B must not count)", got, want)
	}
	if _, ok := afterA.Applications[admItAppB]; ok {
		t.Fatalf("filtered stats include %s: %+v", admItAppB, afterA.Applications)
	}
	if got := delta(afterAll.Applications[admItAppB], beforeAll.Applications[admItAppB]); got != (postgres.AdminStatCounts{FailedPayments24h: 1}) {
		t.Fatalf("B delta = %+v", got)
	}
	wantAll := want
	wantAll.FailedPayments24h = 2
	if got := delta(afterAll.Total, beforeAll.Total); got != wantAll {
		t.Fatalf("total delta = %+v, want %+v", got, wantAll)
	}

	t.Run("lookups honour application_id", func(t *testing.T) {
		actor := uuid.New()
		if w := do(g.r, http.MethodGet, InternalAdminPrefix+"/intents/"+stuck.String()+"?application_id="+admItAppB, nil, g.as(t, actor, PermIntentsRead)); w.Code != http.StatusNotFound {
			t.Fatalf("A's intent under B = %d", w.Code)
		}
		if w := do(g.r, http.MethodGet, InternalAdminPrefix+"/intents/"+stuck.String()+"?application_id="+admItAppA, nil, g.as(t, actor, PermIntentsRead)); w.Code != http.StatusOK {
			t.Fatalf("A's intent under A = %d %s", w.Code, w.Body.String())
		}
		var refID string
		if err := g.pool.QueryRow(context.Background(), `SELECT reference_id::text FROM payments.payment_intents WHERE id = $1`, stuck).Scan(&refID); err != nil {
			t.Fatal(err)
		}
		for app, n := range map[string]int{admItAppA: 1, admItAppB: 0} {
			w := do(g.r, http.MethodGet, InternalAdminPrefix+"/intents?ref_type=food_order&ref_id="+refID+"&application_id="+app, nil, g.as(t, actor, PermIntentsRead))
			var env struct {
				Data struct {
					Items []postgres.AdminIntent `json:"items"`
				} `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || w.Code != http.StatusOK || len(env.Data.Items) != n {
				t.Fatalf("list by reference under %s = %d items (%d), want %d; %s", app, len(env.Data.Items), w.Code, n, w.Body.String())
			}
		}
		w := do(g.r, http.MethodGet, InternalAdminPrefix+"/reconciliation?application_id="+admItAppA, nil, g.as(t, actor, PermReconciliationRead))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), stuck.String()) {
			t.Fatalf("reconciliation for A = %d, missing the stuck intent: %s", w.Code, w.Body.String())
		}
		w = do(g.r, http.MethodGet, InternalAdminPrefix+"/reconciliation?application_id="+admItAppB, nil, g.as(t, actor, PermReconciliationRead))
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), stuck.String()) {
			t.Fatalf("reconciliation for B = %d, shows A's intent: %s", w.Code, w.Body.String())
		}
	})
}

func TestAdminIntegration_WritesAuditTheActor(t *testing.T) {
	g := newAdmItRig(t)
	actor := uuid.New()
	intentID, cmd := g.refund(t, admItAppA, true)
	resolve := InternalAdminPrefix + "/refunds/" + cmd.String() + "/resolve"
	body := []byte(`{"resolution":"written_off","note":"integration: customer made whole elsewhere"}`)
	audits := func() int {
		return g.count(t, `SELECT count(*) FROM payments.payment_audit_log
		                    WHERE event = 'refund_command_resolved' AND metadata->>'command_id' = $1`, cmd.String())
	}

	t.Run("refund lookup and the needs-attention list honour application_id", func(t *testing.T) {
		if w := do(g.r, http.MethodGet, InternalAdminPrefix+"/refunds/"+cmd.String()+"?application_id="+admItAppA, nil, g.as(t, actor, PermRefundsRead)); w.Code != http.StatusOK {
			t.Fatalf("refund under A = %d %s", w.Code, w.Body.String())
		}
		if w := do(g.r, http.MethodGet, InternalAdminPrefix+"/refunds/"+cmd.String()+"?application_id="+admItAppB, nil, g.as(t, actor, PermRefundsRead)); w.Code != http.StatusNotFound {
			t.Fatalf("refund under B = %d", w.Code)
		}
		wA := do(g.r, http.MethodGet, InternalAdminPrefix+"/refunds/needs-attention?limit=200&application_id="+admItAppA, nil, g.as(t, actor, PermRefundsRead))
		wB := do(g.r, http.MethodGet, InternalAdminPrefix+"/refunds/needs-attention?limit=200&application_id="+admItAppB, nil, g.as(t, actor, PermRefundsRead))
		if !strings.Contains(wA.Body.String(), cmd.String()) || strings.Contains(wB.Body.String(), cmd.String()) {
			t.Fatalf("needs-attention filter: A has=%v B has=%v", strings.Contains(wA.Body.String(), cmd.String()), strings.Contains(wB.Body.String(), cmd.String()))
		}
	})

	t.Run("resolving under another application writes nothing", func(t *testing.T) {
		w := do(g.r, http.MethodPost, resolve+"?application_id="+admItAppB, body, g.as(t, actor, PermRefundIssue))
		if w.Code != http.StatusNotFound || audits() != 0 {
			t.Fatalf("status = %d audits = %d", w.Code, audits())
		}
	})

	t.Run("resolve records the act in one audit row, in the transaction", func(t *testing.T) {
		hdr := g.as(t, actor, PermRefundIssue)
		hdr["X-User-Id"] = uuid.NewString() // ignored
		w := do(g.r, http.MethodPost, resolve+"?application_id="+admItAppA, body, hdr)
		if w.Code != http.StatusOK {
			t.Fatalf("resolve = %d %s", w.Code, w.Body.String())
		}
		var actorID, operator, credential, resolvedBy string
		if err := g.pool.QueryRow(context.Background(),
			`SELECT COALESCE(a.actor_id::text,''), a.metadata->>'operator_id', a.metadata->>'credential', c.resolved_by
			   FROM payments.payment_audit_log a JOIN payments.refund_commands c ON c.id = $1
			  WHERE a.event = 'refund_command_resolved' AND a.metadata->>'command_id' = $2`, cmd, cmd.String()).
			Scan(&actorID, &operator, &credential, &resolvedBy); err != nil {
			t.Fatal(err)
		}
		if audits() != 1 || actorID != actor.String() || operator != actor.String() || resolvedBy != actor.String() ||
			credential != "service_token:admin-service" {
			t.Fatalf("audits=%d actor_id=%s operator=%s resolved_by=%s credential=%s", audits(), actorID, operator, resolvedBy, credential)
		}
		// A replay writes nothing.
		if w := do(g.r, http.MethodPost, resolve, body, g.as(t, actor, PermRefundIssue)); w.Code != http.StatusOK || audits() != 1 {
			t.Fatalf("replay = %d audits = %d", w.Code, audits())
		}
		w = do(g.r, http.MethodGet, InternalAdminPrefix+"/audit/payments?application_id="+admItAppA+"&intent_id="+intentID.String()+"&event=refund_command_resolved",
			nil, g.as(t, actor, PermAuditRead))
		var env struct {
			Data struct {
				Items []postgres.PaymentAuditEntry `json:"items"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || len(env.Data.Items) != 1 ||
			env.Data.Items[0].ActorID == nil || *env.Data.Items[0].ActorID != actor || env.Data.Items[0].ApplicationID != admItAppA {
			t.Fatalf("payment audit read = %s", w.Body.String())
		}
		w = do(g.r, http.MethodGet, InternalAdminPrefix+"/audit/payments?application_id="+admItAppB+"&intent_id="+intentID.String(), nil, g.as(t, actor, PermAuditRead))
		if strings.Contains(w.Body.String(), intentID.String()) {
			t.Fatalf("B's audit read shows A's intent: %s", w.Body.String())
		}
	})

	t.Run("registry update records the act in one audit row", func(t *testing.T) {
		rows := func() int {
			return g.count(t, `SELECT count(*) FROM payments.application_audit_log
			                    WHERE application_key = $1 AND operator_id = $2 AND credential = 'service_token:admin-service'`,
				admItAppA, actor.String())
		}
		name := "Admin IT " + uuid.NewString()[:8]
		patch := []byte(`{"display_name":"` + name + `","enabled_methods":["card","upi"]}`)
		w := do(g.r, http.MethodPatch, InternalAdminPrefix+"/applications/"+admItAppA, patch, g.as(t, actor, PermApplicationsManage))
		if w.Code != http.StatusOK || rows() != 1 {
			t.Fatalf("patch = %d rows = %d %s", w.Code, rows(), w.Body.String())
		}
		if w := do(g.r, http.MethodPatch, InternalAdminPrefix+"/applications/"+admItAppA, patch, g.as(t, actor, PermApplicationsManage)); w.Code != http.StatusOK ||
			!strings.Contains(w.Body.String(), `"changed":false`) || rows() != 1 {
			t.Fatalf("replay = %d rows = %d %s", w.Code, rows(), w.Body.String())
		}
		if got := g.count(t, `SELECT count(*) FROM payments.applications WHERE key = $1 AND display_name = $2 AND status = 'active'`, admItAppA, name); got != 1 {
			t.Fatalf("registry row not updated (or status touched)")
		}
		if w := do(g.r, http.MethodPatch, InternalAdminPrefix+"/applications/it_admin_missing", patch, g.as(t, actor, PermApplicationsManage)); w.Code != http.StatusNotFound {
			t.Fatalf("patch of a missing application = %d", w.Code)
		}
		w = do(g.r, http.MethodGet, InternalAdminPrefix+"/audit/applications?application_id="+admItAppA+"&limit=1", nil, g.as(t, actor, PermAuditRead))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), actor.String()) {
			t.Fatalf("application audit read = %d %s", w.Code, w.Body.String())
		}
	})
}
