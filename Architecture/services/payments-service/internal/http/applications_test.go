package http

// Application scoping on the HTTP surface (migration 010), against the
// in-memory fake: which applications a caller may name, the one-release
// default, the refund comparison, the list filters, and the registry routes.

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/atpost/payments-service/internal/service"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

func (f *fakeService) GetApplication(_ context.Context, key string) (*postgres.Application, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.apps[key]
	if !ok {
		return nil, postgres.ErrApplicationNotFound
	}
	cp := *a
	return &cp, nil
}

func (f *fakeService) ListApplications(_ context.Context) ([]postgres.Application, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]postgres.Application, 0, len(f.apps))
	for _, a := range f.apps {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (f *fakeService) PutApplication(_ context.Context, in postgres.PutApplicationInput) (*postgres.ApplicationWrite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts = append(f.puts, in)
	_, existed := f.apps[in.Key]
	a := &postgres.Application{Key: in.Key, DisplayName: in.DisplayName, Status: in.Status,
		MerchantDisplayName: in.MerchantDisplayName, EnabledMethods: postgres.NormalizeMethods(in.EnabledMethods),
		Settings: in.Settings}
	f.apps[in.Key] = a
	return &postgres.ApplicationWrite{Application: *a, Created: !existed, Changed: true}, nil
}

func (f *fakeService) ListApplicationTransactions(_ context.Context, fl postgres.TransactionFilter) ([]postgres.Transaction, *postgres.RefundCursor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.txFilters = append(f.txFilters, fl)
	return nil, nil, nil
}

// tokenCaller is any registered service-token caller.
type tokenCaller struct {
	issuer string
	refs   []string
	signer *servicetoken.Signer
}

func newTokenCaller(t *testing.T, v *servicetoken.Verifier, issuer, kid string, ops, refs []string) tokenCaller {
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
	return tokenCaller{issuer: issuer, refs: refs, signer: signer}
}

func (c tokenCaller) header(t *testing.T, ops ...string) map[string]string {
	t.Helper()
	tok, err := c.signer.Mint(servicetoken.AudiencePayments, c.issuer, ops, c.refs, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{ServiceAuthHeader: "Bearer " + tok}
}

var moneyOps = []string{servicetoken.OpIntentCreate, servicetoken.OpIntentRead, servicetoken.OpRefundCreate}

func foodIntentBody(app, method, channel string) []byte {
	m := map[string]any{
		"payer_id": uuid.New(), "payee_id": uuid.New(), "reference_type": "food_order", "reference_id": uuid.New(),
		"amount_minor": 45000, "method": method, "idempotency_key": "food:" + uuid.NewString(),
	}
	if app != "" {
		m["application_id"] = app
	}
	if channel != "" {
		m["channel"] = channel
	}
	b, _ := json.Marshal(m)
	return b
}

type intentEnvelope struct {
	Data struct {
		ID            uuid.UUID         `json:"id"`
		ApplicationID string            `json:"application_id"`
		Channel       string            `json:"channel"`
		ClientSession map[string]string `json:"client_session"`
	} `json:"data"`
}

const internalIntents = "/v1/payments/internal/intents"

func TestCreateIntent_ApplicationScoping(t *testing.T) {
	fake := newFake()
	fake.apps["it_disabled"] = &postgres.Application{Key: "it_disabled", Status: "disabled", EnabledMethods: []string{"card", "upi"}}
	fake.apps["it_upi_only"] = &postgres.Application{Key: "it_upi_only", Status: "active", EnabledMethods: []string{"upi"}}
	v := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	food := newTokenCaller(t, v, "food-service", "f1", moneyOps, []string{servicetoken.RefFoodOrder})
	lab := newTokenCaller(t, v, "lab-service", "l1", moneyOps, []string{servicetoken.RefFoodOrder})
	multi := newTokenCaller(t, v, "multi-service", "m1", moneyOps, []string{servicetoken.RefFoodOrder})
	r := newRouter(t, fake, routerOpts{internalKey: testInternalKey, verifier: v, callerApps: map[string][]string{
		"food-service":  {"feast"},
		"lab-service":   {"feast", "it_disabled", "it_upi_only"},
		"multi-service": {"feast", "mstore"},
	}})
	create := food.header(t, servicetoken.OpIntentCreate)

	t.Run("the food caller creates a feast payment and it is echoed with the channel", func(t *testing.T) {
		w := do(r, http.MethodPost, internalIntents, foodIntentBody("feast", "upi", "feast_kitchen_android"), create)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		var env intentEnvelope
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if env.Data.ApplicationID != "feast" || env.Data.Channel != "feast_kitchen_android" {
			t.Fatalf("echo = %+v", env.Data)
		}
		if got := fake.initiations[len(fake.initiations)-1]; got.ApplicationID != "feast" || got.Channel != "feast_kitchen_android" {
			t.Fatalf("service input = %+v", got)
		}
	})

	refusals := []struct {
		name    string
		headers map[string]string
		body    []byte
		status  int
		code    string
	}{
		{"the food caller may not create an mstore payment", create, foodIntentBody("mstore", "upi", ""), 422, CodeApplicationNotAllowed},
		{"an unregistered application", create, foodIntentBody("vtube", "upi", ""), 422, CodeApplicationUnknown},
		{"a malformed application", create, foodIntentBody("Feast", "upi", ""), 422, CodeApplicationUnknown},
		{"a disabled application", lab.header(t, servicetoken.OpIntentCreate), foodIntentBody("it_disabled", "upi", ""), 422, CodeApplicationDisabled},
		{"a method the application does not accept", lab.header(t, servicetoken.OpIntentCreate), foodIntentBody("it_upi_only", "card", ""), 422, CodeMethodNotEnabled},
		{"no application from a caller with several", multi.header(t, servicetoken.OpIntentCreate), foodIntentBody("", "upi", ""), 422, CodeApplicationRequired},
		{"no application from the legacy key", withKey(uuid.Nil), foodIntentBody("", "upi", ""), 422, CodeApplicationRequired},
		{"a malformed channel", create, foodIntentBody("feast", "upi", "Android App"), 400, "INVALID_BODY"},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			before := len(fake.intents)
			w := do(r, http.MethodPost, internalIntents, tc.body, tc.headers)
			if w.Code != tc.status || errorCode(t, w.Body.Bytes()) != tc.code {
				t.Fatalf("status = %d body=%s, want %d %s", w.Code, w.Body.String(), tc.status, tc.code)
			}
			if len(fake.intents) != before {
				t.Fatal("a refused request created an intent")
			}
		})
	}

	t.Run("no application from a caller with exactly one uses it", func(t *testing.T) {
		w := do(r, http.MethodPost, internalIntents, foodIntentBody("", "upi", ""), create)
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		var env intentEnvelope
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		if env.Data.ApplicationID != "feast" {
			t.Fatalf("application_id = %q, want feast", env.Data.ApplicationID)
		}
	})
	t.Run("the legacy key may name any active application", func(t *testing.T) {
		if w := do(r, http.MethodPost, internalIntents, foodIntentBody("mstore", "upi", ""), withKey(uuid.Nil)); w.Code != http.StatusCreated {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
	})
}

func TestRefund_ApplicationScoping(t *testing.T) {
	fake := newFake()
	v := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	food := newTokenCaller(t, v, "food-service", "f1", moneyOps, []string{servicetoken.RefFoodOrder})
	r := newRouter(t, fake, routerOpts{verifier: v})
	intent, err := fake.InitiatePayment(context.Background(), service.InitiateInput{PayerID: uuid.New(), PayeeID: uuid.New(),
		ReferenceType: "food_order", ReferenceID: uuid.New(), AmountMinor: 45000, Method: "upi",
		OwnerDomain: "food-service", ApplicationID: "feast"})
	if err != nil {
		t.Fatal(err)
	}
	intent.Status = "succeeded"
	path := internalIntents + "/" + intent.ID.String() + "/refund"
	refund := func(app string) map[string]any {
		m := map[string]any{"amount_minor": 45000, "idempotency_key": "refund:" + uuid.NewString(), "reason": "cancelled"}
		if app != "" {
			m["application_id"] = app
		}
		return m
	}
	body := func(m map[string]any) []byte { b, _ := json.Marshal(m); return b }

	t.Run("a refund naming another application is refused", func(t *testing.T) {
		w := do(r, http.MethodPost, path, body(refund("mstore")), food.header(t, servicetoken.OpRefundCreate))
		if w.Code != http.StatusUnprocessableEntity || errorCode(t, w.Body.Bytes()) != CodeApplicationMismatch {
			t.Fatalf("status = %d body=%s, want 422 %s", w.Code, w.Body.String(), CodeApplicationMismatch)
		}
		if len(fake.refunds) != 0 {
			t.Fatal("a mismatched refund was accepted")
		}
	})
	t.Run("a refund naming the intent's application is accepted and echoed", func(t *testing.T) {
		w := do(r, http.MethodPost, path, body(refund("feast")), food.header(t, servicetoken.OpRefundCreate))
		if w.Code != http.StatusAccepted {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		var env struct {
			Data struct {
				ApplicationID string `json:"application_id"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		if env.Data.ApplicationID != "feast" || fake.refunds[len(fake.refunds)-1].ApplicationID != "feast" {
			t.Fatalf("echo = %q request = %+v", env.Data.ApplicationID, fake.refunds[len(fake.refunds)-1])
		}
	})
	t.Run("a refund naming none uses the caller's only application", func(t *testing.T) {
		w := do(r, http.MethodPost, path, body(refund("")), food.header(t, servicetoken.OpRefundCreate))
		if w.Code != http.StatusAccepted || fake.refunds[len(fake.refunds)-1].ApplicationID != "feast" {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
	})
}

func TestListFilters_ApplicationScoping(t *testing.T) {
	fake := newFake()
	v := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	food := newTokenCaller(t, v, "food-service", "f1", append([]string{OpRefundAdmin}, moneyOps...), []string{servicetoken.RefFoodOrder})
	r := newRouter(t, fake, routerOpts{internalKey: testInternalKey, verifier: v})
	ref := uuid.New()
	for _, app := range []string{"feast", "mstore"} {
		if _, err := fake.InitiatePayment(context.Background(), service.InitiateInput{PayerID: uuid.New(), PayeeID: uuid.New(),
			ReferenceType: "food_order", ReferenceID: ref, AmountMinor: 100, Method: "upi", OwnerDomain: "food-service", ApplicationID: app}); err != nil {
			t.Fatal(err)
		}
	}
	list := internalIntents + "?ref_type=food_order&ref_id=" + ref.String()

	t.Run("intents: a token may filter to its own application only", func(t *testing.T) {
		if w := do(r, http.MethodGet, list+"&application_id=mstore", nil, food.header(t, servicetoken.OpIntentRead)); w.Code != http.StatusForbidden ||
			errorCode(t, w.Body.Bytes()) != CodeApplicationNotAllowed {
			t.Fatalf("mstore: status = %d body=%s, want 403 %s", w.Code, w.Body.String(), CodeApplicationNotAllowed)
		}
		w := do(r, http.MethodGet, list+"&application_id=feast", nil, food.header(t, servicetoken.OpIntentRead))
		var env struct {
			Data []postgres.PaymentIntent `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		if w.Code != http.StatusOK || len(env.Data) != 1 || env.Data[0].ApplicationID != "feast" {
			t.Fatalf("feast: status = %d body=%s", w.Code, w.Body.String())
		}
		if w := do(r, http.MethodGet, list+"&application_id=Not-A-Key", nil, withKey(uuid.Nil)); w.Code != http.StatusBadRequest {
			t.Fatalf("malformed: status = %d, want 400", w.Code)
		}
	})
	t.Run("needs-attention: the filter reaches the store, and a token may only name its own", func(t *testing.T) {
		if w := do(r, http.MethodGet, refundListPath+"?application_id=feast", nil, withKey(uuid.Nil)); w.Code != http.StatusOK {
			t.Fatalf("legacy: status = %d body=%s", w.Code, w.Body.String())
		}
		if got := fake.listFilters[len(fake.listFilters)-1]; got.ApplicationID != "feast" {
			t.Fatalf("filter = %+v, want application feast", got)
		}
		n := len(fake.listFilters)
		if w := do(r, http.MethodGet, refundListPath+"?application_id=mstore", nil, food.header(t, OpRefundAdmin)); w.Code != http.StatusForbidden {
			t.Fatalf("token: status = %d body=%s, want 403", w.Code, w.Body.String())
		}
		if len(fake.listFilters) != n {
			t.Fatal("a refused filter reached the service")
		}
	})
}

func TestApplicationRegistryRoutes(t *testing.T) {
	const apps = "/v1/payments/internal/applications"
	fake := newFake()
	v := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	food := newTokenCaller(t, v, "food-service", "f1", moneyOps, []string{servicetoken.RefFoodOrder})
	ops := newTokenCaller(t, v, "ops-console", "o1", []string{servicetoken.OpIntentRead, OpApplicationAdmin}, []string{servicetoken.RefOrder})
	operator := map[string]string{"X-User-Id": uuid.NewString()}
	put := []byte(`{"display_name":"MopEdu","status":"active","merchant_display_name":"Momentum Merchant","enabled_methods":["upi","card"],"settings":{"support_email":"help@example.com"}}`)
	dev := newRouter(t, fake, routerOpts{internalKey: testInternalKey, verifier: v})
	prod := newRouter(t, fake, routerOpts{internalKey: testInternalKey, verifier: v, production: true})

	t.Run("a token lists and reads only its own applications", func(t *testing.T) {
		w := do(dev, http.MethodGet, apps, nil, food.header(t, servicetoken.OpIntentRead))
		var env struct {
			Data struct {
				Items []postgres.Application `json:"items"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		if w.Code != http.StatusOK || len(env.Data.Items) != 1 || env.Data.Items[0].Key != "feast" {
			t.Fatalf("list: status = %d body=%s", w.Code, w.Body.String())
		}
		if w := do(dev, http.MethodGet, apps+"/mstore", nil, food.header(t, servicetoken.OpIntentRead)); w.Code != http.StatusForbidden {
			t.Fatalf("mstore: status = %d, want 403", w.Code)
		}
		if w := do(dev, http.MethodGet, apps+"/feast", nil, food.header(t, servicetoken.OpIntentRead)); w.Code != http.StatusOK {
			t.Fatalf("feast: status = %d body=%s", w.Code, w.Body.String())
		}
		if w := do(dev, http.MethodGet, apps, nil, withKey(uuid.Nil)); w.Code != http.StatusOK {
			t.Fatalf("legacy list: status = %d", w.Code)
		}
	})

	t.Run("transactions: scoped to the token's application and domain", func(t *testing.T) {
		w := do(dev, http.MethodGet, apps+"/feast/transactions?type=refund&status=needs_attention&limit=20", nil, food.header(t, servicetoken.OpIntentRead))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		got := fake.txFilters[len(fake.txFilters)-1]
		if got.ApplicationID != "feast" || got.OwnerDomain != "food-service" || got.Type != "refund" || got.Status != "needs_attention" || got.Limit != 20 {
			t.Fatalf("filter = %+v", got)
		}
		n := len(fake.txFilters)
		if w := do(dev, http.MethodGet, apps+"/mstore/transactions", nil, food.header(t, servicetoken.OpIntentRead)); w.Code != http.StatusForbidden ||
			errorCode(t, w.Body.Bytes()) != CodeApplicationNotAllowed {
			t.Fatalf("mstore: status = %d body=%s, want 403", w.Code, w.Body.String())
		}
		for _, q := range []string{"?type=order", "?type=payment&status=needs_attention", "?limit=0", "?cursor=%21"} {
			if w := do(dev, http.MethodGet, apps+"/feast/transactions"+q, nil, withKey(uuid.Nil)); w.Code != http.StatusBadRequest {
				t.Errorf("%s: status = %d, want 400", q, w.Code)
			}
		}
		if w := do(dev, http.MethodGet, apps+"/dating/transactions", nil, withKey(uuid.Nil)); w.Code != http.StatusNotFound {
			t.Fatalf("unregistered: status = %d, want 404", w.Code)
		}
		if len(fake.txFilters) != n {
			t.Fatal("a refused transactions request reached the service")
		}
		if w := do(dev, http.MethodGet, apps+"/mstore/transactions", nil, withKey(uuid.Nil)); w.Code != http.StatusOK ||
			fake.txFilters[len(fake.txFilters)-1].OwnerDomain != "" {
			t.Fatalf("legacy: status = %d, want 200 across every domain", w.Code)
		}
	})

	t.Run("PUT in production refuses the internal key before anything is written", func(t *testing.T) {
		w := do(prod, http.MethodPut, apps+"/mopedu", put, with(withKey(uuid.Nil), operator))
		if w.Code != http.StatusForbidden || errorCode(t, w.Body.Bytes()) != CodeServiceTokenRequired || len(fake.puts) != 0 {
			t.Fatalf("status = %d body=%s puts=%d, want 403 %s", w.Code, w.Body.String(), len(fake.puts), CodeServiceTokenRequired)
		}
	})
	t.Run("PUT needs the admin operation", func(t *testing.T) {
		if w := do(prod, http.MethodPut, apps+"/mopedu", put, with(food.header(t, servicetoken.OpIntentRead), operator)); w.Code != http.StatusForbidden || len(fake.puts) != 0 {
			t.Fatalf("status = %d puts=%d, want 403", w.Code, len(fake.puts))
		}
	})
	t.Run("PUT validates before writing", func(t *testing.T) {
		admin := with(ops.header(t, OpApplicationAdmin), operator)
		for name, tc := range map[string]struct {
			path    string
			body    string
			headers map[string]string
		}{
			"no operator":      {apps + "/mopedu", string(put), ops.header(t, OpApplicationAdmin)},
			"malformed key":    {apps + "/MopEdu", string(put), admin},
			"bad status":       {apps + "/mopedu", `{"display_name":"M","status":"paused","merchant_display_name":"M","enabled_methods":["upi"]}`, admin},
			"cod":              {apps + "/mopedu", `{"display_name":"M","status":"active","merchant_display_name":"M","enabled_methods":["cod"]}`, admin},
			"no methods":       {apps + "/mopedu", `{"display_name":"M","status":"active","merchant_display_name":"M","enabled_methods":[]}`, admin},
			"no merchant name": {apps + "/mopedu", `{"display_name":"M","status":"active","enabled_methods":["upi"]}`, admin},
			"unknown field":    {apps + "/mopedu", `{"display_name":"M","status":"active","merchant_display_name":"M","enabled_methods":["upi"],"merchant":"x"}`, admin},
			"settings array":   {apps + "/mopedu", `{"display_name":"M","status":"active","merchant_display_name":"M","enabled_methods":["upi"],"settings":[1]}`, admin},
		} {
			if w := do(prod, http.MethodPut, tc.path, []byte(tc.body), tc.headers); w.Code != http.StatusBadRequest {
				t.Errorf("%s: status = %d body=%s, want 400", name, w.Code, w.Body.String())
			}
		}
		if len(fake.puts) != 0 {
			t.Fatalf("an invalid PUT reached the service %d time(s)", len(fake.puts))
		}
	})
	t.Run("PUT with the admin token creates, then updates, and records the credential", func(t *testing.T) {
		admin := with(ops.header(t, OpApplicationAdmin), operator)
		if w := do(prod, http.MethodPut, apps+"/mopedu", put, admin); w.Code != http.StatusCreated {
			t.Fatalf("create: status = %d body=%s", w.Code, w.Body.String())
		}
		got := fake.puts[len(fake.puts)-1]
		if got.Credential != "service_token:ops-console" || got.OperatorID != operator["X-User-Id"] || got.MerchantDisplayName != "Momentum Merchant" {
			t.Fatalf("put input = %+v", got)
		}
		if w := do(prod, http.MethodPut, apps+"/mopedu", put, admin); w.Code != http.StatusOK {
			t.Fatalf("second PUT: status = %d body=%s, want 200", w.Code, w.Body.String())
		}
	})
	t.Run("outside production the internal key writes, recorded as such", func(t *testing.T) {
		if w := do(dev, http.MethodPut, apps+"/mopedu", put, with(withKey(uuid.Nil), operator)); w.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		if got := fake.puts[len(fake.puts)-1]; got.Credential != "internal_key" {
			t.Fatalf("credential = %q", got.Credential)
		}
	})
}

// The checkout sheet's merchant name comes from the registry, in client_session.
func TestClientSessionCarriesTheApplicationsMerchantName(t *testing.T) {
	fake := newFake()
	fake.apps["feast"].MerchantDisplayName = "Feast by Momentum"
	r := newRouter(t, fake, routerOpts{webhookSecret: testWebhookSecret, internalKey: testInternalKey})
	w := do(r, http.MethodPost, internalIntents, foodIntentBody("feast", "upi", ""), withKey(uuid.Nil))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var env intentEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.ClientSession["merchant_display_name"] != "Feast by Momentum" || env.Data.ClientSession["key_id"] == "" {
		t.Fatalf("client_session = %v", env.Data.ClientSession)
	}
	if env.Data.ApplicationID != "feast" {
		t.Fatalf("application_id = %q", env.Data.ApplicationID)
	}
}
