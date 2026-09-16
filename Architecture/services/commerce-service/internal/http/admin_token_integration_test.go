//go:build integration

package http

// The admin-service token family over a real database: every audit trail a
// token action writes names the token's act, never a header, and the stats
// route counts what was seeded.
//
//	COMMERCE_TEST_DSN=.../commerce_it_test go test -tags=integration ./internal/http/ -run AdminTokenIT -v

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/atpost/commerce-service/internal/kyc"
	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type tokenIT struct {
	r      *gin.Engine
	signer *servicetoken.Signer
	actor  uuid.UUID
	forged uuid.UUID
}

func newTokenIT(t *testing.T) *tokenIT {
	t.Helper()
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"SERVICE_CALLERS":                     "admin-service",
		"SERVICE_CALLER_ADMIN_SERVICE_KID":    "a1",
		"SERVICE_CALLER_ADMIN_SERVICE_PUBKEY": pub,
		"SERVICE_CALLER_ADMIN_SERVICE_OPS":    joinPerms(),
	}
	v, err := ServiceCallersFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	signer, err := servicetoken.NewSignerFromBase64(IssuerAdminService, "a1", priv)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := pii.New(devKeyProvider{}, []byte("audit-test-salt!"))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	svc := service.New(postgres.New(edgePool), nil, "").WithPII(cipher).WithKYC(kyc.StubValidator{})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(FenceMiddleware())
	r.Use(RequireGatewayTrust(integrationInternalKey))
	h := New(svc).WithInternalKey(integrationInternalKey).WithServiceVerifier(v)
	h.RegisterRoutes(r)
	h.RegisterP0Routes(r)
	return &tokenIT{r: r, signer: signer, actor: uuid.New(), forged: uuid.New()}
}

func joinPerms() string {
	out := ""
	for i, p := range AdminPermissions {
		if i > 0 {
			out += ","
		}
		out += p
	}
	return out
}

// do sends a token request for perm; the gateway key and a forged actor ride
// along to prove neither is what the handler records.
func (it *tokenIT) do(t *testing.T, method, path, perm string) (int, string) {
	t.Helper()
	tok, err := it.signer.Mint(AudienceCommerce, "admin-console", []string{perm}, nil, time.Minute,
		servicetoken.WithActor(it.actor.String()))
	if err != nil {
		t.Fatal(err)
	}
	w := serve(it.r, method, path, map[string]string{
		ServiceAuthHeader:        "Bearer " + tok,
		InternalServiceKeyHeader: integrationInternalKey,
		"X-User-Id":              it.forged.String(),
	})
	return w.Code, w.Body.String()
}

func actorsIn(t *testing.T, sql string, id uuid.UUID) []uuid.UUID {
	t.Helper()
	rows, err := edgePool.Query(context.Background(), sql, id)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var a uuid.UUID
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

func wantOnlyActor(t *testing.T, what string, got []uuid.UUID, want uuid.UUID) {
	t.Helper()
	if len(got) != 1 || got[0] != want {
		t.Fatalf("%s actors = %v, want exactly [%s]", what, got, want)
	}
}

func TestAdminTokenIT_SellerDecisionRecordsAct(t *testing.T) {
	it := newTokenIT(t)
	sellerID := seedAuditSeller(t, "submitted", nil)
	code, body := it.do(t, http.MethodPost, InternalAdminPrefix+"/sellers/"+sellerID.String()+"/request-changes", PermSellerApprove)
	if code != http.StatusNoContent {
		t.Fatalf("request-changes: %d %s", code, body)
	}
	wantOnlyActor(t, "seller_onboarding_reviews",
		actorsIn(t, `SELECT actor_user_id FROM seller_onboarding_reviews WHERE seller_id=$1`, sellerID), it.actor)
}

func TestAdminTokenIT_ProductDecisionRecordsAct(t *testing.T) {
	it := newTokenIT(t)
	sellerID := seedAuditSeller(t, "approved", nil)
	productID := uuid.New()
	auditExec(t, `INSERT INTO products (id,seller_id,title,slug,status,approval_status)
	      VALUES ($1,$2,'Token Moderation Product',$3,'draft','submitted')`,
		productID, sellerID, "token-mod-"+productID.String()[:8])
	seedOfferFor(t, productID) // the whole-estate offer checker requires one
	code, body := it.do(t, http.MethodPost, InternalAdminPrefix+"/products/"+productID.String()+"/request-changes", PermProductsModerate)
	if code != http.StatusNoContent {
		t.Fatalf("product request-changes: %d %s", code, body)
	}
	wantOnlyActor(t, "product_moderation_log",
		actorsIn(t, `SELECT actor_user_id FROM product_moderation_log WHERE product_id=$1`, productID), it.actor)
}

func TestAdminTokenIT_KYCVerifyAndCODSettleRecordAct(t *testing.T) {
	it := newTokenIT(t)
	gstin := "29ABCDE1234F1Z5"
	sellerID := seedAuditSeller(t, "submitted", &gstin)
	if code, body := it.do(t, http.MethodPost, InternalAdminPrefix+"/sellers/"+sellerID.String()+"/kyc/verify", PermKYCVerify); code != http.StatusOK {
		t.Fatalf("kyc verify: %d %s", code, body)
	}
	wantOnlyActor(t, "commerce_admin_audit_log (kyc)",
		actorsIn(t, `SELECT actor_user_id FROM commerce_admin_audit_log WHERE target_id=$1`, sellerID), it.actor)

	remittanceID := seedPendingRemittance(t, sellerID, "470.00", time.Now())
	if code, body := it.do(t, http.MethodPost, InternalAdminPrefix+"/cod-remittances/"+remittanceID.String()+"/settle", PermCODSettle); code != http.StatusOK {
		t.Fatalf("cod settle: %d %s", code, body)
	}
	wantOnlyActor(t, "commerce_admin_audit_log (cod)",
		actorsIn(t, `SELECT actor_user_id FROM commerce_admin_audit_log WHERE target_id=$1`, remittanceID), it.actor)
}

// The old key route keeps working end to end and still records X-User-Id.
func TestAdminTokenIT_LegacyKeyRouteStillWorks(t *testing.T) {
	sellerID := seedAuditSeller(t, "submitted", nil)
	legacyActor := uuid.New()
	r := auditEngine(t)
	w := call(t, r, http.MethodPost, "/v1/commerce/internal/sellers/"+sellerID.String()+"/request-changes", legacyActor,
		map[string]any{"changes": "address", "notes": "legacy"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("legacy request-changes: %d %s", w.Code, w.Body.String())
	}
	wantOnlyActor(t, "seller_onboarding_reviews (legacy)",
		actorsIn(t, `SELECT actor_user_id FROM seller_onboarding_reviews WHERE seller_id=$1`, sellerID), legacyActor)
}

func seedPendingRemittance(t *testing.T, sellerID uuid.UUID, net string, createdAt time.Time) uuid.UUID {
	t.Helper()
	orderID, shipmentID, remittanceID := uuid.New(), uuid.New(), uuid.New()
	auditExec(t, `INSERT INTO orders (id,customer_user_id,order_number,status,payment_status,
	         payment_method,currency_code,
	         subtotal,shipping_charges,tax_amount,final_amount,
	         subtotal_minor,shipping_charges_minor,tax_amount_minor,final_amount_minor,created_at)
	      VALUES ($1,$2,$3,'cancelled','pending','cod','INR',0,0,0,0,0,0,0,0,$4)`,
		orderID, uuid.New(), "ORD-TOK-"+orderID.String()[:8], createdAt)
	auditExec(t, `INSERT INTO shipments (id,order_id,seller_id,courier) VALUES ($1,$2,$3,'stub')`,
		shipmentID, orderID, sellerID)
	auditExec(t, `INSERT INTO cod_remittances (id,shipment_id,order_id,seller_id,gross_amount,net_amount,delivered_at)
	      VALUES ($1,$2,$3,$4,$5::numeric,$5::numeric,NOW())`, remittanceID, shipmentID, orderID, sellerID, net)
	return remittanceID
}

func seedOrder(t *testing.T, status string, minor int64, createdAt time.Time) {
	t.Helper()
	id := uuid.New()
	auditExec(t, `INSERT INTO orders (id,customer_user_id,order_number,status,payment_status,
	         payment_method,currency_code,
	         subtotal,shipping_charges,tax_amount,final_amount,
	         subtotal_minor,shipping_charges_minor,tax_amount_minor,final_amount_minor,created_at)
	      VALUES ($1,$2,$3,$4,'paid','cod','INR',0,0,0,0,$5,0,0,$5,$6)`,
		id, uuid.New(), "ORD-STAT-"+id.String()[:8], status, minor, createdAt)
}

func readStats(t *testing.T, it *tokenIT) postgres.AdminStats {
	t.Helper()
	tok, err := it.signer.Mint(AudienceCommerce, "admin-console", []string{PermStatsRead}, nil, time.Minute,
		servicetoken.WithActor(it.actor.String()))
	if err != nil {
		t.Fatal(err)
	}
	w := serve(it.r, http.MethodGet, InternalAdminPrefix+"/stats", map[string]string{ServiceAuthHeader: "Bearer " + tok})
	if w.Code != http.StatusOK {
		t.Fatalf("stats: %d %s", w.Code, w.Body.String())
	}
	var s postgres.AdminStats
	decodeData(t, w, &s)
	return s
}

// Stats counts on seeded data, as deltas: commerce_it_test holds other
// fixtures, and the test must not depend on them.
func TestAdminTokenIT_StatsCountSeededData(t *testing.T) {
	it := newTokenIT(t)
	before := readStats(t, it)

	// +1 pending seller that is also KYC-pending; +1 approved seller whose KYC
	// is format_ok (still awaiting a real verification); +1 verified (neither).
	queued := seedAuditSeller(t, "submitted", nil)
	formatOK := seedAuditSeller(t, "approved", nil)
	auditExec(t, `UPDATE sellers SET verification_status='format_ok' WHERE id=$1`, formatOK)
	verified := seedAuditSeller(t, "approved", nil)
	auditExec(t, `UPDATE sellers SET verification_status='verified' WHERE id=$1`, verified)

	// +1 product in the moderation queue; one approved product for the gap.
	queuedProduct, liveProduct := uuid.New(), uuid.New()
	auditExec(t, `INSERT INTO products (id,seller_id,title,slug,status,approval_status)
	      VALUES ($1,$2,'Stats Queued',$3,'draft','submitted')`, queuedProduct, queued, "stats-q-"+queuedProduct.String()[:8])
	auditExec(t, `INSERT INTO products (id,seller_id,title,slug,status,approval_status)
	      VALUES ($1,$2,'Stats Live',$3,'active','approved')`, liveProduct, verified, "stats-l-"+liveProduct.String()[:8])
	seedOfferFor(t, queuedProduct, liveProduct) // the whole-estate offer checker requires one
	// +1 open gap, and a resolved one that must not count.
	auditExec(t, `INSERT INTO product_compliance_gaps (product_id,seller_id,code,label,reason)
	      VALUES ($1,$2,'stats_code','Stats label','missing')`, liveProduct, verified)
	auditExec(t, `INSERT INTO product_compliance_gaps (product_id,seller_id,code,label,reason,resolved_at)
	      VALUES ($1,$2,'stats_code_2','Stats label','missing',NOW())`, liveProduct, verified)

	// +1 dead-letter job, and a pending one that must not count.
	var deadID, pendingID int64
	if err := edgePool.QueryRow(context.Background(), `INSERT INTO fulfillment_jobs (kind,payload,status,dead_letter_at)
	      VALUES ('fulfill_paid_order','{"stats":true}','dead',NOW()) RETURNING id`).Scan(&deadID); err != nil {
		t.Fatal(err)
	}
	if err := edgePool.QueryRow(context.Background(), `INSERT INTO fulfillment_jobs (kind,payload,status,next_run_at)
	      VALUES ('fulfill_paid_order','{"stats":true}','pending',NOW() + interval '30 days') RETURNING id`).Scan(&pendingID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = edgePool.Exec(context.Background(), `DELETE FROM fulfillment_jobs WHERE id = ANY($1)`, []int64{deadID, pendingID})
	})

	// Two pending remittances across two sellers: 470.00 + 123.45 = 59345 paise.
	// Their orders are cancelled, so they add nothing to orders or GMV.
	seedPendingRemittance(t, formatOK, "470.00", time.Now().Add(-10*24*time.Hour))
	seedPendingRemittance(t, verified, "123.45", time.Now().Add(-10*24*time.Hour))

	// Orders: one paid now (today and 7d), one delivered 3 days ago (7d
	// only), one cancelled and one payment_pending now (neither), one 10
	// days ago (neither).
	now := time.Now()
	seedOrder(t, "paid", 50000, now)
	seedOrder(t, "delivered", 12345, now.Add(-3*24*time.Hour))
	seedOrder(t, "cancelled", 99999, now)
	seedOrder(t, "payment_pending", 77777, now)
	seedOrder(t, "confirmed", 88888, now.Add(-10*24*time.Hour))

	after := readStats(t, it)
	checks := []struct {
		name          string
		before, after int64
		want          int64
	}{
		{"sellers_pending", int64(before.SellersPending), int64(after.SellersPending), 1},
		{"products_pending", int64(before.ProductsPending), int64(after.ProductsPending), 1},
		{"kyc_awaiting_verification", int64(before.KYCAwaitingVerification), int64(after.KYCAwaitingVerification), 2},
		{"dead_letter_jobs", int64(before.DeadLetterJobs), int64(after.DeadLetterJobs), 1},
		{"compliance_gaps_open", int64(before.ComplianceGapsOpen), int64(after.ComplianceGapsOpen), 1},
		{"pending_payout_sellers", int64(before.PendingPayoutSellers), int64(after.PendingPayoutSellers), 2},
		{"pending_payout_amount_paise", before.PendingPayoutAmountPaise, after.PendingPayoutAmountPaise, 59345},
		{"cod_remittances_pending", int64(before.CODRemittancesPending), int64(after.CODRemittancesPending), 2},
		{"orders_today", int64(before.OrdersToday), int64(after.OrdersToday), 1},
		{"orders_7d", int64(before.Orders7d), int64(after.Orders7d), 2},
		{"gmv_today_paise", before.GMVTodayPaise, after.GMVTodayPaise, 50000},
		{"gmv_7d_paise", before.GMV7dPaise, after.GMV7dPaise, 62345},
	}
	for _, c := range checks {
		if got := c.after - c.before; got != c.want {
			t.Errorf("%s: delta %d (before %d, after %d), want %d", c.name, got, c.before, c.after, c.want)
		}
	}
	if after.DayStartsAt.IsZero() || after.GeneratedAt.Before(after.DayStartsAt) {
		t.Errorf("day_starts_at %v / generated_at %v not sensible", after.DayStartsAt, after.GeneratedAt)
	}
}
