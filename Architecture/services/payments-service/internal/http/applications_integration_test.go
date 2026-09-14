//go:build integration

package http

// Application scoping end to end (migration 010): real handler, real service
// with the stub gateway, live PostgreSQL (payments_it_test).
//
//	PAYMENTS_TEST_DSN=... go test -tags=integration ./internal/http/ -run Application -v -count=1

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/atpost/payments-service/internal/gateway"
	"github.com/atpost/payments-service/internal/service"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const appsPath = "/v1/payments/internal/applications"

func appItRouter(t *testing.T, svc Service, v *servicetoken.Verifier, production bool, apps map[string][]string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(svc).WithInternalKey(testInternalKey).WithServiceAuth(v).WithProduction(production).WithCallerApplications(apps)
	if err := h.RegisterRoutes(r); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	return r
}

// appItEnsure upserts a test application into the registry.
func appItEnsure(t *testing.T, store *postgres.Store, key, status string, methods ...string) {
	t.Helper()
	if _, err := store.PutApplication(context.Background(), postgres.PutApplicationInput{
		Key: key, DisplayName: "integration " + key, Status: status, MerchantDisplayName: "Integration Merchant",
		EnabledMethods: methods, OperatorID: "app-it-setup", Credential: "integration",
	}); err != nil {
		t.Fatalf("ensure application %s: %v", key, err)
	}
}

func appItScalar(t *testing.T, pool *pgxpool.Pool, query string, args ...any) string {
	t.Helper()
	var v string
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return v
}

func appItBody(app, method, key string) []byte {
	b, _ := json.Marshal(map[string]any{
		"payer_id": uuid.New(), "payee_id": uuid.New(), "reference_type": "food_order", "reference_id": uuid.New(),
		"amount_minor": 45000, "currency": "INR", "method": method, "idempotency_key": key,
		"application_id": app, "channel": "feast_rider_android",
	})
	return b
}

func appItParkRefunds(t *testing.T, pool *pgxpool.Pool, intentID uuid.UUID) {
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`UPDATE payments.refund_commands SET next_attempt_at = NOW() + INTERVAL '30 days'
			  WHERE intent_id = $1 AND status IN ('pending','submitted')`, intentID)
	})
}

func TestApplicationScopingEndToEnd(t *testing.T) {
	pool := itPool(t)
	store := postgres.New(pool)
	svc := service.New(store, &gateway.StubGateway{})
	appItEnsure(t, store, "it_disabled", postgres.ApplicationStatusDisabled, "upi", "card")
	appItEnsure(t, store, "it_upi_only", postgres.ApplicationStatusActive, "upi")

	v := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	food := itRegister(t, v, "food-service", "f1", moneyOps, []string{servicetoken.RefFoodOrder})
	lab := itRegister(t, v, "lab-service", "l1", moneyOps, []string{servicetoken.RefFoodOrder})
	r := appItRouter(t, svc, v, false, map[string][]string{
		"food-service": {"feast"},
		"lab-service":  {"feast", "it_disabled", "it_upi_only"},
	})
	rows := func(key string) string {
		return appItScalar(t, pool, `SELECT count(*)::text FROM payments.payment_intents WHERE idempotency_key = $1`, key)
	}

	var feastIntent uuid.UUID
	t.Run("the food caller creates a feast payment: 201, echoed and stored", func(t *testing.T) {
		key := "app-it-http-" + uuid.NewString()
		w := do(r, http.MethodPost, internalIntents, appItBody("feast", "upi", key), food.header(t, servicetoken.OpIntentCreate))
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		var env intentEnvelope
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if env.Data.ApplicationID != "feast" || env.Data.Channel != "feast_rider_android" {
			t.Fatalf("echo = %+v", env.Data)
		}
		feastIntent = env.Data.ID
		if got := appItScalar(t, pool, `SELECT application_id || '/' || channel FROM payments.payment_intents WHERE id = $1`, feastIntent); got != "feast/feast_rider_android" {
			t.Fatalf("stored = %q", got)
		}
	})

	for _, tc := range []struct {
		name   string
		caller itCaller
		app    string
		method string
		code   string
	}{
		{"the food caller with mstore", food, "mstore", "upi", CodeApplicationNotAllowed},
		{"an unregistered application", food, "zz_not_registered", "upi", CodeApplicationUnknown},
		{"a disabled application", lab, "it_disabled", "upi", CodeApplicationDisabled},
		{"a method the application does not accept", lab, "it_upi_only", "card", CodeMethodNotEnabled},
	} {
		t.Run(tc.name+" is refused with 422 "+tc.code+" and writes nothing", func(t *testing.T) {
			key := "app-it-http-" + uuid.NewString()
			w := do(r, http.MethodPost, internalIntents, appItBody(tc.app, tc.method, key), tc.caller.header(t, servicetoken.OpIntentCreate))
			if w.Code != http.StatusUnprocessableEntity || errorCode(t, w.Body.Bytes()) != tc.code {
				t.Fatalf("status = %d body=%s, want 422 %s", w.Code, w.Body.String(), tc.code)
			}
			if n := rows(key); n != "0" {
				t.Fatalf("intent rows = %s, want 0", n)
			}
		})
	}

	t.Run("no application from a caller with exactly one uses it", func(t *testing.T) {
		key := "app-it-http-" + uuid.NewString()
		body, _ := json.Marshal(map[string]any{
			"payer_id": uuid.New(), "payee_id": uuid.New(), "reference_type": "food_order", "reference_id": uuid.New(),
			"amount_minor": 45000, "method": "upi", "idempotency_key": key,
		})
		w := do(r, http.MethodPost, internalIntents, body, food.header(t, servicetoken.OpIntentCreate))
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		if got := appItScalar(t, pool, `SELECT application_id FROM payments.payment_intents WHERE idempotency_key = $1`, key); got != "feast" {
			t.Fatalf("stored application_id = %q, want feast", got)
		}
	})

	t.Run("refund: another application is 422 and nothing is reserved; the right one is carried onto the command", func(t *testing.T) {
		if feastIntent == uuid.Nil {
			t.Skip("no intent from the first subtest")
		}
		appItParkRefunds(t, pool, feastIntent)
		if _, err := pool.Exec(context.Background(), `UPDATE payments.payment_intents SET status = 'succeeded' WHERE id = $1`, feastIntent); err != nil {
			t.Fatal(err)
		}
		path := internalIntents + "/" + feastIntent.String() + "/refund"
		refund := func(app string) []byte {
			b, _ := json.Marshal(map[string]any{"amount_minor": 45000, "reason": "app-it", "idempotency_key": "app-it-refund-" + uuid.NewString(), "application_id": app})
			return b
		}
		w := do(r, http.MethodPost, path, refund("mstore"), food.header(t, servicetoken.OpRefundCreate))
		if w.Code != http.StatusUnprocessableEntity || errorCode(t, w.Body.Bytes()) != CodeApplicationMismatch {
			t.Fatalf("mismatch: status = %d body=%s, want 422 %s", w.Code, w.Body.String(), CodeApplicationMismatch)
		}
		if n := appItScalar(t, pool, `SELECT count(*)::text || '/' || (SELECT refund_reserved_minor FROM payments.payment_intents WHERE id = $1)
		                                FROM payments.refund_commands WHERE intent_id = $1`, feastIntent); n != "0/0" {
			t.Fatalf("commands/reserved = %s, want 0/0", n)
		}
		w = do(r, http.MethodPost, path, refund("feast"), food.header(t, servicetoken.OpRefundCreate))
		if w.Code != http.StatusAccepted {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		var env struct {
			Data struct {
				CommandID     uuid.UUID `json:"command_id"`
				ApplicationID string    `json:"application_id"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		if env.Data.ApplicationID != "feast" {
			t.Fatalf("echo = %+v", env.Data)
		}
		if got := appItScalar(t, pool, `SELECT application_id FROM payments.refund_commands WHERE id = $1`, env.Data.CommandID); got != "feast" {
			t.Fatalf("command application_id = %q", got)
		}
	})
}

type txPage struct {
	Data struct {
		Items      []postgres.Transaction `json:"items"`
		NextCursor *string                `json:"next_cursor"`
	} `json:"data"`
}

func appItPages(t *testing.T, r *gin.Engine, path string, headers func() map[string]string) []postgres.Transaction {
	t.Helper()
	var all []postgres.Transaction
	cursor := ""
	for page := 0; page < 2000; page++ {
		p := path
		if cursor != "" {
			p += "&cursor=" + cursor
		}
		w := do(r, http.MethodGet, p, nil, headers())
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status = %d body=%s", p, w.Code, w.Body.String())
		}
		var env txPage
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		all = append(all, env.Data.Items...)
		if env.Data.NextCursor == nil {
			return all
		}
		cursor = *env.Data.NextCursor
	}
	t.Fatal("the transactions list did not end")
	return nil
}

func TestApplicationTransactionsRouteEndToEnd(t *testing.T) {
	pool := itPool(t)
	ctx := context.Background()
	store := postgres.New(pool)
	svc := service.New(store, &gateway.StubGateway{})
	appItEnsure(t, store, "it_tx_app", postgres.ApplicationStatusActive, "upi", "card")
	appItEnsure(t, store, "it_tx_other", postgres.ApplicationStatusActive, "upi", "card")

	create := func(app, owner string) uuid.UUID {
		t.Helper()
		res, err := store.CreateIntent(ctx, postgres.PaymentIntent{
			PayerID: uuid.New(), PayeeID: uuid.New(), ReferenceType: "food_order", ReferenceID: uuid.New(),
			Amount: 450, AmountMinorRaw: 45000, Currency: "INR", Method: "upi",
			OwnerDomain: owner, IdempotencyKey: "app-it-tx-" + uuid.NewString(), ApplicationID: app,
		})
		if err != nil {
			t.Fatal(err)
		}
		return res.Intent.ID
	}
	payments := []uuid.UUID{create("it_tx_app", "food-service"), create("it_tx_app", "food-service"), create("it_tx_app", "food-service")}
	commerceOwned := create("it_tx_app", "commerce-service")
	otherApp := create("it_tx_other", "food-service")
	if _, err := pool.Exec(ctx, `UPDATE payments.payment_intents SET status = 'succeeded' WHERE id = ANY($1)`, payments[:2]); err != nil {
		t.Fatal(err)
	}
	appItParkRefunds(t, pool, payments[0])
	var refunds []uuid.UUID
	for i := 0; i < 2; i++ {
		cmd, err := svc.RequestRefund(ctx, service.RefundRequest{IntentID: payments[0], AmountMinor: 1000, Reason: "app-it tx",
			ProviderIdempotencyKey: "app-it-tx-refund-" + uuid.NewString(), CallerDomain: "food-service", ApplicationID: "it_tx_app"})
		if err != nil {
			t.Fatal(err)
		}
		refunds = append(refunds, cmd.ID)
	}

	v := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	food := itRegister(t, v, "food-service", "f1", moneyOps, []string{servicetoken.RefFoodOrder})
	feastOnly := itRegister(t, v, "feast-reader", "r1", []string{servicetoken.OpIntentRead}, []string{servicetoken.RefFoodOrder})
	r := appItRouter(t, svc, v, false, map[string][]string{"food-service": {"it_tx_app"}, "feast-reader": {"feast"}})
	legacy := func() map[string]string { return withKey(uuid.Nil) }
	ids := func(items []postgres.Transaction) map[uuid.UUID]postgres.Transaction {
		m := map[uuid.UUID]postgres.Transaction{}
		for _, it := range items {
			if _, dup := m[it.ID]; dup {
				t.Fatalf("transaction %s appeared on two pages", it.ID)
			}
			m[it.ID] = it
		}
		return m
	}

	t.Run("only this application's payments and refunds, paged newest first", func(t *testing.T) {
		items := appItPages(t, r, appsPath+"/it_tx_app/transactions?limit=2", legacy)
		got := ids(items)
		for i, it := range items {
			if it.ApplicationID != "it_tx_app" {
				t.Fatalf("item %s belongs to %q", it.ID, it.ApplicationID)
			}
			if i > 0 && it.CreatedAt.After(items[i-1].CreatedAt) {
				t.Fatalf("page order: %s (%s) after %s (%s)", it.ID, it.CreatedAt, items[i-1].ID, items[i-1].CreatedAt)
			}
		}
		for _, id := range append(append([]uuid.UUID{commerceOwned}, payments...), refunds...) {
			if _, ok := got[id]; !ok {
				t.Errorf("seeded transaction %s is missing", id)
			}
		}
		if _, ok := got[otherApp]; ok {
			t.Fatal("another application's payment was listed")
		}
		for _, id := range refunds {
			if got[id].Type != postgres.TransactionRefund || got[id].IntentID != payments[0] {
				t.Errorf("refund %s = %+v", id, got[id])
			}
		}
	})
	t.Run("type and status filters", func(t *testing.T) {
		for _, it := range appItPages(t, r, appsPath+"/it_tx_app/transactions?type=refund&limit=200", legacy) {
			if it.Type != postgres.TransactionRefund {
				t.Fatalf("type=refund listed a %s", it.Type)
			}
		}
		succeeded := ids(appItPages(t, r, appsPath+"/it_tx_app/transactions?type=payment&status=succeeded&limit=200", legacy))
		for _, it := range succeeded {
			if it.Type != postgres.TransactionPayment || it.Status != "succeeded" {
				t.Fatalf("status filter listed %+v", it)
			}
		}
		if _, ok := succeeded[payments[0]]; !ok {
			t.Fatal("a succeeded payment is missing from status=succeeded")
		}
		if _, ok := succeeded[payments[2]]; ok {
			t.Fatal("a pending payment was listed under status=succeeded")
		}
	})
	t.Run("a token sees its allowed application, and only its own domain's intents in it", func(t *testing.T) {
		got := ids(appItPages(t, r, appsPath+"/it_tx_app/transactions?limit=200", func() map[string]string {
			return food.header(t, servicetoken.OpIntentRead)
		}))
		if _, ok := got[payments[1]]; !ok {
			t.Fatal("the food token did not see a food payment")
		}
		if _, ok := got[commerceOwned]; ok {
			t.Fatal("the food token saw a commerce-owned payment")
		}
		if w := do(r, http.MethodGet, appsPath+"/it_tx_other/transactions", nil, food.header(t, servicetoken.OpIntentRead)); w.Code != http.StatusForbidden {
			t.Fatalf("food token on it_tx_other: status = %d, want 403", w.Code)
		}
	})
	t.Run("a token allowed only feast is refused mstore", func(t *testing.T) {
		w := do(r, http.MethodGet, appsPath+"/mstore/transactions", nil, feastOnly.header(t, servicetoken.OpIntentRead))
		if w.Code != http.StatusForbidden || errorCode(t, w.Body.Bytes()) != CodeApplicationNotAllowed {
			t.Fatalf("status = %d body=%s, want 403 %s", w.Code, w.Body.String(), CodeApplicationNotAllowed)
		}
		if w := do(r, http.MethodGet, appsPath+"/feast/transactions?limit=1", nil, feastOnly.header(t, servicetoken.OpIntentRead)); w.Code != http.StatusOK {
			t.Fatalf("feast: status = %d body=%s", w.Code, w.Body.String())
		}
	})
}

func TestApplicationRegistryPutEndToEnd(t *testing.T) {
	pool := itPool(t)
	ctx := context.Background()
	store := postgres.New(pool)
	svc := service.New(store, &gateway.StubGateway{})
	const key = "it_put_app"
	appItEnsure(t, store, key, postgres.ApplicationStatusActive, "upi", "card") // a previous run may have left it disabled

	v := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	admin := itRegister(t, v, "ops-console", "o1", []string{servicetoken.OpIntentRead, OpApplicationAdmin}, []string{servicetoken.RefOrder})
	prod := appItRouter(t, svc, v, true, nil)
	dev := appItRouter(t, svc, v, false, nil)
	operator := map[string]string{"X-User-Id": "app-it-operator-" + uuid.NewString()[:8]}
	audits := func() int64 {
		var n int64
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM payments.application_audit_log WHERE application_key = $1`, key).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	nonce := uuid.NewString()[:8]
	body := func(status string, methods ...string) []byte {
		b, _ := json.Marshal(map[string]any{"display_name": "IT put " + nonce, "status": status,
			"merchant_display_name": "IT Merchant " + nonce, "enabled_methods": methods, "settings": map[string]any{"nonce": nonce}})
		return b
	}
	var env struct {
		Data postgres.ApplicationWrite `json:"data"`
	}

	before := audits()
	t.Run("production refuses the internal key and writes nothing", func(t *testing.T) {
		w := do(prod, http.MethodPut, appsPath+"/"+key, body("active", "upi", "card"), with(withKey(uuid.Nil), operator))
		if w.Code != http.StatusForbidden || errorCode(t, w.Body.Bytes()) != CodeServiceTokenRequired {
			t.Fatalf("status = %d body=%s, want 403 %s", w.Code, w.Body.String(), CodeServiceTokenRequired)
		}
		if a := audits(); a != before {
			t.Fatalf("audit rows %d → %d after a refused write", before, a)
		}
		if name := appItScalar(t, pool, `SELECT display_name FROM payments.applications WHERE key = $1`, key); name == "IT put "+nonce {
			t.Fatal("the refused write changed the registry")
		}
	})

	t.Run("an admin token writes once, audited; a replay changes nothing", func(t *testing.T) {
		w := do(prod, http.MethodPut, appsPath+"/"+key, body("active", "upi", "card"), with(admin.header(t, OpApplicationAdmin), operator))
		if w.Code != http.StatusOK && w.Code != http.StatusCreated {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || !env.Data.Changed || env.Data.Application.MerchantDisplayName != "IT Merchant "+nonce {
			t.Fatalf("write = %+v, %v", env.Data, err)
		}
		after := audits()
		if after != before+1 {
			t.Fatalf("audit rows %d → %d, want exactly one more", before, after)
		}
		if got := appItScalar(t, pool, `SELECT operator_id || '|' || credential || '|' || action || '|' || (after->>'merchant_display_name')
		                                  FROM payments.application_audit_log WHERE application_key = $1 ORDER BY id DESC LIMIT 1`, key); got !=
			operator["X-User-Id"]+"|service_token:ops-console|updated|IT Merchant "+nonce {
			t.Fatalf("audit row = %q", got)
		}

		// The same body, methods in another order: a replay.
		w = do(prod, http.MethodPut, appsPath+"/"+key, body("active", "card", "upi"), with(admin.header(t, OpApplicationAdmin), operator))
		if w.Code != http.StatusOK {
			t.Fatalf("replay: status = %d body=%s", w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || env.Data.Changed || env.Data.Created {
			t.Fatalf("replay = %+v, %v; want changed=false created=false", env.Data, err)
		}
		if a := audits(); a != after {
			t.Fatalf("audit rows %d → %d after a replay, want unchanged", after, a)
		}
	})

	t.Run("disabling refuses new payments and leaves existing ones alone", func(t *testing.T) {
		key0 := "app-it-put-" + uuid.NewString()
		w := do(dev, http.MethodPost, internalIntents, appItBody(key, "upi", key0), withKey(uuid.Nil))
		if w.Code != http.StatusCreated {
			t.Fatalf("create while active: status = %d body=%s", w.Code, w.Body.String())
		}
		var created intentEnvelope
		_ = json.Unmarshal(w.Body.Bytes(), &created)
		existing := created.Data.ID
		appItParkRefunds(t, pool, existing)
		if _, err := pool.Exec(ctx, `UPDATE payments.payment_intents SET status = 'succeeded' WHERE id = $1`, existing); err != nil {
			t.Fatal(err)
		}

		if w := do(prod, http.MethodPut, appsPath+"/"+key, body("disabled", "upi", "card"), with(admin.header(t, OpApplicationAdmin), operator)); w.Code != http.StatusOK {
			t.Fatalf("disable: status = %d body=%s", w.Code, w.Body.String())
		}
		w = do(dev, http.MethodPost, internalIntents, appItBody(key, "upi", "app-it-put-"+uuid.NewString()), withKey(uuid.Nil))
		if w.Code != http.StatusUnprocessableEntity || errorCode(t, w.Body.Bytes()) != CodeApplicationDisabled {
			t.Fatalf("create while disabled: status = %d body=%s, want 422 %s", w.Code, w.Body.String(), CodeApplicationDisabled)
		}

		if w := do(dev, http.MethodGet, internalIntents+"/"+existing.String(), nil, withKey(uuid.Nil)); w.Code != http.StatusOK {
			t.Fatalf("read existing: status = %d", w.Code)
		}
		refund, _ := json.Marshal(map[string]any{"amount_minor": 45000, "reason": "app-it disabled", "application_id": key,
			"idempotency_key": "app-it-put-refund-" + uuid.NewString()})
		if w := do(dev, http.MethodPost, internalIntents+"/"+existing.String()+"/refund", refund, withKey(uuid.Nil)); w.Code != http.StatusAccepted {
			t.Fatalf("refund existing while disabled: status = %d body=%s", w.Code, w.Body.String())
		}
		if got := appItScalar(t, pool, `SELECT status || '/' || application_id FROM payments.payment_intents WHERE id = $1`, existing); got != "succeeded/"+key {
			t.Fatalf("existing intent = %q, want untouched", got)
		}
	})
}
