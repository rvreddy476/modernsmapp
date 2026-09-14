//go:build integration

package http

// The refund operator routes with production credential rules, end to end:
// real handler, real service, live PostgreSQL (payments_it_test).
//
// In production the legacy internal key is refused before any store access,
// so a refused resolve leaves the command parked and writes no audit row. A
// token carrying payments:refund.admin is scoped to the intents its issuer
// owns: food-service sees and resolves food refunds, and a commerce command
// reads as absent. Outside production the key still lists and resolves every
// domain, as the Feast dev seeder needs.
//
//	PAYMENTS_TEST_DSN=... go test -tags=integration ./internal/http/... -run Production -v

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/payments-service/internal/service"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	itPoolOnce sync.Once
	itPoolVal  *pgxpool.Pool
	itPoolErr  error
)

// itPool skips rather than exiting, so the package's unit tests still run
// under -tags=integration without a database.
func itPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("PAYMENTS_TEST_DSN")
	if dsn == "" {
		t.Skip("PAYMENTS_TEST_DSN not set")
	}
	itPoolOnce.Do(func() { itPoolVal, itPoolErr = pgxpool.New(context.Background(), dsn) })
	if itPoolErr != nil {
		t.Fatalf("connect: %v", itPoolErr)
	}
	return itPoolVal
}

type itCaller struct {
	issuer string
	refs   []string
	signer *servicetoken.Signer
}

func (c itCaller) header(t *testing.T, ops ...string) map[string]string {
	t.Helper()
	tok, err := c.signer.Mint(servicetoken.AudiencePayments, c.issuer, ops, c.refs, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{ServiceAuthHeader: "Bearer " + tok}
}

// itRegister adds a caller to v the way main.go does from SERVICE_CALLER_*.
func itRegister(t *testing.T, v *servicetoken.Verifier, issuer, kid string, ops, refs []string) itCaller {
	t.Helper()
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := servicetoken.NewSignerFromBase64(issuer, kid, priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.RegisterBase64(issuer, kid, pub, ops, refs); err != nil {
		t.Fatal(err)
	}
	return itCaller{issuer: issuer, refs: refs, signer: signer}
}

func itRouter(t *testing.T, svc Service, v *servicetoken.Verifier, production bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(svc).WithInternalKey(testInternalKey).WithServiceAuth(v).WithProduction(production)
	if err := h.RegisterRoutes(r); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	return r
}

// itSeedParked is a captured intent owned by owner, with a refund the worker parked.
func itSeedParked(t *testing.T, pool *pgxpool.Pool, svc *service.Service, store *postgres.Store, refType, owner string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	s := strings.ReplaceAll(uuid.NewString(), "-", "")[:14]
	if _, err := pool.Exec(ctx, `
		INSERT INTO payments.payment_intents
		    (id, payer_id, payee_id, reference_type, reference_id, amount, amount_minor,
		     currency, method, status, provider, provider_ref, provider_order_id,
		     provider_payment_id, owner_domain, idempotency_key, created_at, application_id)
		VALUES ($1,$2,$3,$4,$5,607.12,60712,'INR','upi','succeeded','razorpay',$6,$6,$7,$8,$9,NOW(),$10)`,
		id, uuid.New(), uuid.New(), refType, uuid.New(), "order_"+s, "pay_"+s, owner, "it-prod-"+id.String(),
		legacyApplications[refType]); err != nil {
		t.Fatalf("seed intent: %v", err)
	}
	cmd, err := svc.RequestRefund(ctx, service.RefundRequest{
		IntentID: id, AmountMinor: 60712, Reason: "production credential proof",
		ProviderIdempotencyKey: "it-prod-refund-" + id.String(), CallerDomain: owner,
		ApplicationID: legacyApplications[refType],
	})
	if err != nil {
		t.Fatalf("request refund: %v", err)
	}
	parked, err := store.ParkRefundCommand(ctx, cmd.ID, "provider_rejected", "integration: production credential proof")
	if err != nil || !parked {
		t.Fatalf("park: parked=%v err=%v", parked, err)
	}
	// Leave nothing parked behind for the alarm gauge or a later run's list.
	t.Cleanup(func() {
		_, err := store.ResolveRefundCommand(context.Background(), postgres.ResolveRefundInput{
			CommandID: cmd.ID, Resolution: "test_data", Note: "integration cleanup",
			OperatorID: "it-cleanup", Credential: "internal_key",
		})
		if err != nil && !errors.Is(err, postgres.ErrRefundCommandNotParked) {
			t.Logf("cleanup resolve %s: %v", cmd.ID, err)
		}
	})
	return cmd.ID
}

func itCommand(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) (status, resolution string) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT status, COALESCE(resolution,'') FROM payments.refund_commands WHERE id = $1`, id).
		Scan(&status, &resolution); err != nil {
		t.Fatalf("read command: %v", err)
	}
	return status, resolution
}

func itResolvedAudits(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM payments.payment_audit_log
		  WHERE event = 'refund_command_resolved' AND metadata->>'command_id' = $1`, id.String()).Scan(&n); err != nil {
		t.Fatalf("count audits: %v", err)
	}
	return n
}

// itListAll pages the operator list to the end and returns every item.
func itListAll(t *testing.T, r *gin.Engine, query string, headers func() map[string]string) []postgres.NeedsAttentionRefund {
	t.Helper()
	var all []postgres.NeedsAttentionRefund
	cursor := ""
	for page := 0; page < 500; page++ {
		path := refundListPath + "?limit=200" + query
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		w := do(r, http.MethodGet, path, nil, headers())
		if w.Code != http.StatusOK {
			t.Fatalf("list %s: status = %d body=%s", path, w.Code, w.Body.String())
		}
		var env struct {
			Data struct {
				Items      []postgres.NeedsAttentionRefund `json:"items"`
				NextCursor *string                         `json:"next_cursor"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		all = append(all, env.Data.Items...)
		if env.Data.NextCursor == nil {
			return all
		}
		cursor = *env.Data.NextCursor
	}
	t.Fatal("the list did not end within 500 pages")
	return nil
}

func itContains(items []postgres.NeedsAttentionRefund, id uuid.UUID) bool {
	for _, it := range items {
		if it.ID == id {
			return true
		}
	}
	return false
}

func TestProductionRefundOperatorRoutes(t *testing.T) {
	pool := itPool(t)
	store := postgres.New(pool)
	svc := service.New(store, nil)

	v := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	food := itRegister(t, v, "food-service", "f1", []string{servicetoken.OpRefundCreate, OpRefundAdmin}, []string{servicetoken.RefFoodOrder})
	commerce := itRegister(t, v, "commerce-service", "c1",
		[]string{servicetoken.OpIntentCreate, servicetoken.OpIntentRead, servicetoken.OpRefundCreate}, []string{servicetoken.RefOrder})

	foodCmd := itSeedParked(t, pool, svc, store, servicetoken.RefFoodOrder, "food-service")
	commerceCmd := itSeedParked(t, pool, svc, store, servicetoken.RefOrder, "commerce-service")
	prod := itRouter(t, svc, v, true)
	operator := map[string]string{"X-User-Id": uuid.NewString()}

	t.Run("the internal key is refused on list and resolve, and nothing is written", func(t *testing.T) {
		w := do(prod, http.MethodGet, refundListPath+"?ref_type=food_order", nil, withKey(uuid.Nil))
		if w.Code != http.StatusForbidden || errorCode(t, w.Body.Bytes()) != CodeServiceTokenRequired {
			t.Fatalf("list: status = %d body=%s, want 403 %s", w.Code, w.Body.String(), CodeServiceTokenRequired)
		}
		w = do(prod, http.MethodPost, resolvePath(foodCmd), testDataBody, withKey(uuid.New()))
		if w.Code != http.StatusForbidden || errorCode(t, w.Body.Bytes()) != CodeServiceTokenRequired {
			t.Fatalf("resolve: status = %d body=%s, want 403 %s", w.Code, w.Body.String(), CodeServiceTokenRequired)
		}
		if status, res := itCommand(t, pool, foodCmd); status != postgres.RefundStatusNeedsAttention || res != "" {
			t.Fatalf("command = %s/%q after a refused resolve, want needs_attention and no resolution", status, res)
		}
		if n := itResolvedAudits(t, pool, foodCmd); n != 0 {
			t.Fatalf("audit rows = %d after a refused resolve, want 0", n)
		}
	})

	t.Run("a token without the refund admin operation is refused", func(t *testing.T) {
		h := with(commerce.header(t, servicetoken.OpIntentRead, servicetoken.OpRefundCreate), operator)
		if w := do(prod, http.MethodGet, refundListPath+"?ref_type=order", nil, h); w.Code != http.StatusForbidden {
			t.Fatalf("list: status = %d, want 403", w.Code)
		}
		if w := do(prod, http.MethodPost, resolvePath(commerceCmd), testDataBody, h); w.Code != http.StatusForbidden {
			t.Fatalf("resolve: status = %d, want 403", w.Code)
		}
		if status, _ := itCommand(t, pool, commerceCmd); status != postgres.RefundStatusNeedsAttention {
			t.Fatalf("commerce command = %s, want still needs_attention", status)
		}
	})

	t.Run("a food token lists only food intents", func(t *testing.T) {
		for _, q := range []string{"", "&ref_type=food_order"} {
			items := itListAll(t, prod, q, func() map[string]string { return food.header(t, OpRefundAdmin) })
			if !itContains(items, foodCmd) {
				t.Errorf("query %q: the food command is missing", q)
			}
			if itContains(items, commerceCmd) {
				t.Errorf("query %q: a food token listed a commerce command", q)
			}
			for _, it := range items {
				if it.ReferenceType != servicetoken.RefFoodOrder {
					t.Errorf("query %q: a food token listed %s (reference_type %s)", q, it.ID, it.ReferenceType)
				}
			}
		}
		if w := do(prod, http.MethodGet, refundListPath+"?ref_type=order", nil, food.header(t, OpRefundAdmin)); w.Code != http.StatusForbidden {
			t.Fatalf("ref_type=order with a food token: status = %d, want 403", w.Code)
		}
	})

	t.Run("a food token cannot resolve a commerce command", func(t *testing.T) {
		w := do(prod, http.MethodPost, resolvePath(commerceCmd), testDataBody, with(food.header(t, OpRefundAdmin), operator))
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d body=%s, want 404", w.Code, w.Body.String())
		}
		if status, _ := itCommand(t, pool, commerceCmd); status != postgres.RefundStatusNeedsAttention {
			t.Fatalf("commerce command = %s, want still needs_attention", status)
		}
		if n := itResolvedAudits(t, pool, commerceCmd); n != 0 {
			t.Fatalf("audit rows = %d, want 0", n)
		}
	})

	t.Run("a food token resolves a food command", func(t *testing.T) {
		w := do(prod, http.MethodPost, resolvePath(foodCmd), testDataBody, with(food.header(t, OpRefundAdmin), operator))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		if status, res := itCommand(t, pool, foodCmd); status != postgres.RefundStatusResolved || res != "test_data" {
			t.Fatalf("command = %s/%q, want resolved/test_data", status, res)
		}
		if n := itResolvedAudits(t, pool, foodCmd); n != 1 {
			t.Fatalf("audit rows = %d, want 1", n)
		}
	})

	t.Run("outside production the internal key lists and resolves every domain", func(t *testing.T) {
		dev := itRouter(t, svc, v, false)
		items := itListAll(t, dev, "", func() map[string]string { return withKey(uuid.Nil) })
		if !itContains(items, commerceCmd) {
			t.Fatal("the internal key did not list the commerce command")
		}
		w := do(dev, http.MethodPost, resolvePath(commerceCmd), testDataBody, withKey(uuid.New()))
		if w.Code != http.StatusOK {
			t.Fatalf("resolve: status = %d body=%s", w.Code, w.Body.String())
		}
		if status, res := itCommand(t, pool, commerceCmd); status != postgres.RefundStatusResolved || res != "test_data" {
			t.Fatalf("command = %s/%q, want resolved/test_data", status, res)
		}
		if n := itResolvedAudits(t, pool, commerceCmd); n != 1 {
			t.Fatalf("audit rows = %d, want 1", n)
		}
	})
}
