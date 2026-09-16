//go:build integration

package http

// Admin actions carry a real actor, and the three that had no trail — COD
// settle, KYC verify, banner writes — each append exactly one row to
// commerce_admin_audit_log (migration 036).
//
//	COMMERCE_TEST_DSN=.../commerce_it_test go test -tags=integration ./internal/http/ -run AdminAudit -v

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/commerce-service/internal/kyc"
	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func auditEngine(t *testing.T) *gin.Engine {
	t.Helper()
	cipher, err := pii.New(devKeyProvider{}, []byte("audit-test-salt!"))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	svc := service.New(postgres.New(edgePool), nil, "").WithPII(cipher).WithKYC(kyc.StubValidator{})
	r := gin.New()
	r.Use(FenceMiddleware())
	h := New(svc).WithInternalKey(integrationInternalKey)
	h.RegisterRoutes(r)
	h.RegisterP0Routes(r)
	return r
}

func auditExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := edgePool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("seed: %v\nSQL: %s", err, sql)
	}
}

type auditRow struct {
	Actor  uuid.UUID
	Action string
	Before string
	After  string
}

func auditRowsFor(t *testing.T, target uuid.UUID) []auditRow {
	t.Helper()
	rows, err := edgePool.Query(context.Background(), `
		SELECT actor_user_id, action, COALESCE(before_state::text,''), COALESCE(after_state::text,'')
		FROM commerce_admin_audit_log WHERE target_id=$1 ORDER BY created_at, id`, target)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var r auditRow
		if err := rows.Scan(&r.Actor, &r.Action, &r.Before, &r.After); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func seedAuditSeller(t *testing.T, status string, gstin *string) (sellerID uuid.UUID) {
	t.Helper()
	sellerID = uuid.New()
	auditExec(t, `INSERT INTO sellers (id,user_id,store_name,slug,email,state,status,gst_number)
	      VALUES ($1,$2,'Audit Store',$3,'audit@example.test','KA',$4,$5)`,
		sellerID, uuid.New(), "audit-"+sellerID.String()[:8], status, gstin)
	return sellerID
}

func TestAdminAuditCODSettleRecordsOneRowWithTheActor(t *testing.T) {
	r := auditEngine(t)
	ctx := context.Background()
	sellerID := seedAuditSeller(t, "approved", nil)
	orderID, shipmentID, remittanceID := uuid.New(), uuid.New(), uuid.New()
	auditExec(t, `INSERT INTO orders (id,customer_user_id,order_number,status,payment_status,
	         payment_method,currency_code,
	         subtotal,shipping_charges,tax_amount,final_amount,
	         subtotal_minor,shipping_charges_minor,tax_amount_minor,final_amount_minor)
	      VALUES ($1,$2,$3,'delivered','paid','cod','INR',0,0,0,0,50000,0,0,50000)`,
		orderID, uuid.New(), "ORD-AUD-"+orderID.String()[:8])
	auditExec(t, `INSERT INTO shipments (id,order_id,seller_id,courier) VALUES ($1,$2,$3,'stub')`,
		shipmentID, orderID, sellerID)
	auditExec(t, `INSERT INTO cod_remittances (id,shipment_id,order_id,seller_id,gross_amount,net_amount,delivered_at)
	      VALUES ($1,$2,$3,$4,500.00,470.00,NOW())`, remittanceID, shipmentID, orderID, sellerID)

	path := "/v1/commerce/internal/cod-remittances/" + remittanceID.String() + "/settle"

	// No actor: refused, nothing written, still pending.
	if w := call(t, r, http.MethodPost, path, uuid.Nil, map[string]any{}); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), CodeActorRequired) {
		t.Fatalf("no actor: %d %s, want 400 %s", w.Code, w.Body.String(), CodeActorRequired)
	}
	var status string
	_ = edgePool.QueryRow(ctx, `SELECT status FROM cod_remittances WHERE id=$1`, remittanceID).Scan(&status)
	if status != "pending" || len(auditRowsFor(t, remittanceID)) != 0 {
		t.Fatalf("a refused settle changed state: status=%s audit=%d", status, len(auditRowsFor(t, remittanceID)))
	}

	actor := uuid.New()
	batch := uuid.New()
	if w := call(t, r, http.MethodPost, path, actor, map[string]any{"payout_batch_id": batch.String()}); w.Code != http.StatusOK {
		t.Fatalf("settle: %d %s", w.Code, w.Body.String())
	}
	_ = edgePool.QueryRow(ctx, `SELECT status FROM cod_remittances WHERE id=$1`, remittanceID).Scan(&status)
	if status != "settled" {
		t.Fatalf("status %s, want settled", status)
	}
	rows := auditRowsFor(t, remittanceID)
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want exactly 1", len(rows))
	}
	got := rows[0]
	if got.Actor != actor || got.Action != "cod_remittance_settle" {
		t.Fatalf("audit row %+v, want actor %s action cod_remittance_settle", got, actor)
	}
	var before, after map[string]any
	_ = json.Unmarshal([]byte(got.Before), &before)
	_ = json.Unmarshal([]byte(got.After), &after)
	if before["status"] != "pending" || after["status"] != "settled" ||
		after["payout_batch_id"] != batch.String() || after["net_amount"] != "470.00" {
		t.Fatalf("before/after not captured: before=%s after=%s", got.Before, got.After)
	}

	// A repeat changes nothing and writes nothing.
	if w := call(t, r, http.MethodPost, path, actor, map[string]any{}); w.Code != http.StatusOK {
		t.Fatalf("repeat settle: %d %s", w.Code, w.Body.String())
	}
	if n := len(auditRowsFor(t, remittanceID)); n != 1 {
		t.Fatalf("a no-op settle wrote an audit row: %d rows", n)
	}
}

func TestAdminAuditKYCVerifyRecordsTheVerdictNotTheDocument(t *testing.T) {
	r := auditEngine(t)
	ctx := context.Background()
	gstin := "29ABCDE1234F1Z5"
	sellerID := seedAuditSeller(t, "submitted", &gstin)
	path := "/v1/commerce/internal/sellers/" + sellerID.String() + "/kyc/verify"

	if w := call(t, r, http.MethodPost, path, uuid.Nil, nil); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), CodeActorRequired) {
		t.Fatalf("no actor: %d %s, want 400 %s", w.Code, w.Body.String(), CodeActorRequired)
	}
	var vs *string
	_ = edgePool.QueryRow(ctx, `SELECT verification_status FROM sellers WHERE id=$1`, sellerID).Scan(&vs)
	if vs != nil && *vs == "format_ok" || len(auditRowsFor(t, sellerID)) != 0 {
		t.Fatal("a refused KYC verify changed state")
	}

	actor := uuid.New()
	if w := call(t, r, http.MethodPost, path, actor, nil); w.Code != http.StatusOK {
		t.Fatalf("verify: %d %s", w.Code, w.Body.String())
	}
	rows := auditRowsFor(t, sellerID)
	if len(rows) != 1 || rows[0].Actor != actor || rows[0].Action != "seller_kyc_verify" {
		t.Fatalf("audit rows %+v, want exactly one seller_kyc_verify by %s", rows, actor)
	}
	if !strings.Contains(rows[0].After, `"format_ok"`) || !strings.Contains(rows[0].After, `"gstin"`) {
		t.Fatalf("verdict not captured: %s", rows[0].After)
	}
	// The document number must not be in the audit, in any form.
	for _, s := range []string{rows[0].Before, rows[0].After} {
		if strings.Contains(strings.ToUpper(s), gstin) || strings.Contains(s, "format only") {
			t.Fatalf("audit row carries the document or adapter message: %s", s)
		}
	}
}

func TestAdminAuditBannerWritesRecordCreateUpdateDelete(t *testing.T) {
	r := auditEngine(t)
	actor := uuid.New()
	body := map[string]any{"title": "Audit Banner", "target_type": "search", "target_id": "lamps", "position": 9}

	// Refused without an actor, nothing created.
	if w := call(t, r, http.MethodPost, "/v1/commerce/internal/banners", uuid.Nil, body); w.Code != http.StatusBadRequest {
		t.Fatalf("no-actor create: %d %s", w.Code, w.Body.String())
	}

	w := call(t, r, http.MethodPost, "/v1/commerce/internal/banners", actor, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID uuid.UUID `json:"id"`
	}
	decodeData(t, w, &created)
	path := "/v1/commerce/internal/banners/" + created.ID.String()

	body["title"] = "Audit Banner v2"
	if w := call(t, r, http.MethodPut, path, uuid.Nil, body); w.Code != http.StatusBadRequest {
		t.Fatalf("no-actor update: %d", w.Code)
	}
	if w := call(t, r, http.MethodPut, path, actor, body); w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, r, http.MethodDelete, path, uuid.Nil, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("no-actor delete: %d", w.Code)
	}
	if w := call(t, r, http.MethodDelete, path, actor, nil); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}

	rows := auditRowsFor(t, created.ID)
	want := []string{"banner_create", "banner_update", "banner_delete"}
	if len(rows) != len(want) {
		t.Fatalf("audit rows = %d (%+v), want %v", len(rows), rows, want)
	}
	for i, a := range want {
		if rows[i].Action != a || rows[i].Actor != actor {
			t.Fatalf("row %d = %+v, want %s by %s", i, rows[i], a, actor)
		}
	}
	if rows[0].Before != "" || !strings.Contains(rows[1].Before, "Audit Banner\"") ||
		!strings.Contains(rows[1].After, "Audit Banner v2") || !strings.Contains(rows[2].Before, "Audit Banner v2") {
		t.Fatalf("before/after snapshots wrong: %+v", rows)
	}
}

// Seller and product decisions already had a trail; a nil actor used to be
// written into it as 00000000-…. Now it is refused and nothing is written.
func TestAdminAuditDecisionsRefuseANilActorAndWriteNothing(t *testing.T) {
	r := auditEngine(t)
	ctx := context.Background()
	sellerID := seedAuditSeller(t, "submitted", nil)

	for _, actor := range []uuid.UUID{uuid.Nil} {
		w := call(t, r, http.MethodPost, "/v1/commerce/internal/sellers/"+sellerID.String()+"/approve", actor, map[string]any{})
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), CodeActorRequired) {
			t.Fatalf("approve with nil actor: %d %s", w.Code, w.Body.String())
		}
	}
	var n int
	_ = edgePool.QueryRow(ctx, `SELECT count(*) FROM seller_onboarding_reviews WHERE seller_id=$1`, sellerID).Scan(&n)
	var status string
	_ = edgePool.QueryRow(ctx, `SELECT status FROM sellers WHERE id=$1`, sellerID).Scan(&status)
	if n != 0 || status != "submitted" {
		t.Fatalf("refused approve wrote: reviews=%d status=%s", n, status)
	}
	var nilActorRows int
	_ = edgePool.QueryRow(ctx, `SELECT count(*) FROM commerce_admin_audit_log
		WHERE actor_user_id='00000000-0000-0000-0000-000000000000'`).Scan(&nilActorRows)
	if nilActorRows != 0 {
		t.Fatalf("%d audit rows carry the nil actor", nilActorRows)
	}
}

// The trail is append-only at the database, not by convention.
func TestAdminAuditLogIsAppendOnly(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()
	auditExec(t, `INSERT INTO commerce_admin_audit_log (actor_user_id,action,target_type,target_id)
	      VALUES ($1,'banner_delete','banner',$2)`, uuid.New(), id)
	if _, err := edgePool.Exec(ctx, `UPDATE commerce_admin_audit_log SET reason='x' WHERE target_id=$1`, id); err == nil {
		t.Fatal("an audit row was updated")
	}
	if _, err := edgePool.Exec(ctx, `DELETE FROM commerce_admin_audit_log WHERE target_id=$1`, id); err == nil {
		t.Fatal("an audit row was deleted")
	}
	if _, err := edgePool.Exec(ctx, `INSERT INTO commerce_admin_audit_log (actor_user_id,action,target_type,target_id)
	      VALUES ('00000000-0000-0000-0000-000000000000','banner_delete','banner',$1)`, uuid.New()); err == nil {
		t.Fatal("the nil actor was accepted by the table")
	}
}
