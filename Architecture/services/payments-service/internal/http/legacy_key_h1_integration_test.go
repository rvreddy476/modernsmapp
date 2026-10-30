//go:build integration

package http

// H1 (audit 2026-10-30), end to end: real handler, real service, live
// PostgreSQL (payments_it_test).
//
// In production the platform-wide internal key opens no intent and refunds
// nothing: the request is refused before the store, so no intent row and no
// refund command exists afterwards. Outside production a legacy refund acts for
// the domain of the application it names, and the store's owner check refuses
// it against another domain's intent: the intent's own owner is never copied
// into the request.
//
//	PAYMENTS_TEST_DSN=... go test -tags=integration ./internal/http/... -run H1 -v

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/payments-service/internal/service"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// h1SeedPaid inserts a captured intent owned by owner under application app.
func h1SeedPaid(t *testing.T, pool *pgxpool.Pool, refType, owner, app string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	s := strings.ReplaceAll(uuid.NewString(), "-", "")[:14]
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO payments.payment_intents
		    (id, payer_id, payee_id, reference_type, reference_id, amount, amount_minor,
		     currency, method, status, provider, provider_ref, provider_order_id,
		     provider_payment_id, owner_domain, idempotency_key, created_at, application_id)
		VALUES ($1,$2,$3,$4,$5,607.12,60712,'INR','upi','succeeded','razorpay',$6,$6,$7,$8,$9,NOW(),$10)`,
		id, uuid.New(), uuid.New(), refType, uuid.New(), "order_"+s, "pay_"+s, owner, "it-h1-"+id.String(), app); err != nil {
		t.Fatalf("seed intent: %v", err)
	}
	return id
}

func h1Count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestH1ProductionLegacyKeyMovesNoMoney(t *testing.T) {
	pool := itPool(t)
	store := postgres.New(pool)
	svc := service.New(store, nil)
	v := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	itRegister(t, v, "commerce-service", "c1",
		[]string{servicetoken.OpIntentCreate, servicetoken.OpIntentRead, servicetoken.OpRefundCreate}, []string{servicetoken.RefOrder})
	prod := itRouter(t, svc, v, true)

	t.Run("refund", func(t *testing.T) {
		id := h1SeedPaid(t, pool, servicetoken.RefOrder, "commerce-service", "mstore")
		w := do(prod, http.MethodPost, "/v1/payments/internal/intents/"+id.String()+"/refund",
			[]byte(`{"reason":"h1","application_id":"mstore"}`), withKey(uuid.Nil))
		if w.Code != http.StatusForbidden || errorCode(t, w.Body.Bytes()) != CodeServiceTokenRequired {
			t.Fatalf("status = %d body=%s, want 403 %s", w.Code, w.Body.String(), CodeServiceTokenRequired)
		}
		if n := h1Count(t, pool, `SELECT count(*) FROM payments.refund_commands WHERE intent_id=$1`, id); n != 0 {
			t.Fatalf("refund commands = %d after a refused legacy refund, want 0", n)
		}
		if n := h1Count(t, pool,
			`SELECT count(*) FROM payments.payment_intents WHERE id=$1 AND COALESCE(refund_reserved_minor,0)=0 AND status='succeeded'`, id); n != 1 {
			t.Fatal("the intent changed after a refused legacy refund")
		}
	})

	t.Run("intent creation", func(t *testing.T) {
		idem := "it-h1-create-" + uuid.NewString()
		body := []byte(`{"payer_id":"` + uuid.NewString() + `","payee_id":"` + uuid.NewString() +
			`","reference_type":"order","reference_id":"` + uuid.NewString() +
			`","amount_minor":90000,"currency":"INR","method":"upi","idempotency_key":"` + idem + `","application_id":"mstore"}`)
		w := do(prod, http.MethodPost, "/v1/payments/internal/intents", body, withKey(uuid.Nil))
		if w.Code != http.StatusForbidden || errorCode(t, w.Body.Bytes()) != CodeServiceTokenRequired {
			t.Fatalf("status = %d body=%s, want 403 %s", w.Code, w.Body.String(), CodeServiceTokenRequired)
		}
		if n := h1Count(t, pool, `SELECT count(*) FROM payments.payment_intents WHERE idempotency_key=$1`, idem); n != 0 {
			t.Fatalf("intents = %d after a refused legacy create, want 0", n)
		}
	})
}

func TestH1LegacyRefundIsBoundToTheCallersDomainNotTheIntents(t *testing.T) {
	pool := itPool(t)
	store := postgres.New(pool)
	svc := service.New(store, nil)
	v := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	itRegister(t, v, "commerce-service", "c1", []string{servicetoken.OpIntentRead}, []string{servicetoken.RefOrder})
	dev := itRouter(t, svc, v, false)

	// A commerce intent filed under feast: the application check passes, so
	// only the owner-domain comparison can refuse a legacy refund naming feast
	// (food-service). Copying the intent's owner into the request passed it.
	odd := h1SeedPaid(t, pool, servicetoken.RefOrder, "commerce-service", "feast")
	w := do(dev, http.MethodPost, "/v1/payments/internal/intents/"+odd.String()+"/refund",
		[]byte(`{"reason":"h1-cross","application_id":"feast"}`), withKey(uuid.Nil))
	if w.Code == http.StatusAccepted {
		t.Fatalf("status = %d body=%s: a legacy refund for food-service was accepted on a commerce intent", w.Code, w.Body.String())
	}
	if n := h1Count(t, pool, `SELECT count(*) FROM payments.refund_commands WHERE intent_id=$1`, odd); n != 0 {
		t.Fatalf("refund commands = %d after a cross-domain legacy refund, want 0", n)
	}

	// The matching domain still works outside production.
	own := h1SeedPaid(t, pool, servicetoken.RefOrder, "commerce-service", "mstore")
	w = do(dev, http.MethodPost, "/v1/payments/internal/intents/"+own.String()+"/refund",
		[]byte(`{"reason":"h1-own","application_id":"mstore"}`), withKey(uuid.Nil))
	if w.Code != http.StatusAccepted {
		t.Fatalf("own-domain legacy refund: status = %d body=%s, want 202", w.Code, w.Body.String())
	}
	if n := h1Count(t, pool, `SELECT count(*) FROM payments.refund_commands WHERE intent_id=$1`, own); n != 1 {
		t.Fatalf("refund commands = %d, want 1", n)
	}
	// Keep the worker off it in shared CI databases.
	appItParkRefunds(t, pool, own)
}
