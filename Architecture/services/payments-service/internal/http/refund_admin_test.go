package http

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

func (f *fakeService) ListRefundsNeedingAttention(_ context.Context, fl postgres.NeedsAttentionFilter) ([]postgres.NeedsAttentionRefund, *postgres.RefundCursor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listFilters = append(f.listFilters, fl)
	return f.parked, nil, nil
}

func (f *fakeService) ResolveRefundCommand(_ context.Context, in postgres.ResolveRefundInput) (*postgres.RefundResolution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolveCalls = append(f.resolveCalls, in)
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	return &postgres.RefundResolution{
		CommandID: in.CommandID, Status: postgres.RefundStatusResolved, Resolution: in.Resolution,
		Note: in.Note, ResolvedBy: in.OperatorID, ResolvedAt: time.Now().UTC(),
	}, nil
}

// newRefundAdminCaller is commerce-service holding the refund admin operation
// (and refund.create, which is NOT enough on its own).
func newRefundAdminCaller(t *testing.T) commerceCaller {
	t.Helper()
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := servicetoken.NewSignerFromBase64("commerce-service", "c1", priv)
	if err != nil {
		t.Fatal(err)
	}
	v := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	if err := v.RegisterBase64("commerce-service", "c1", pub,
		[]string{servicetoken.OpRefundCreate, OpRefundAdmin}, []string{servicetoken.RefOrder}); err != nil {
		t.Fatal(err)
	}
	return commerceCaller{signer: signer, verifier: v}
}

func with(h map[string]string, extra map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

const refundListPath = "/v1/payments/internal/refunds/needs-attention"

func resolvePath(id uuid.UUID) string {
	return "/v1/payments/internal/refunds/" + id.String() + "/resolve"
}

var testDataBody = []byte(`{"resolution":"test_data","note":"dev seed: simulated payment"}`)

// Neither operator route is reachable without the /internal credential, and a
// token must carry the refund admin operation. Nothing reaches the service.
func TestRefundAdminRoutes_RefuseWithoutCredential(t *testing.T) {
	fake := newFake()
	caller := newRefundAdminCaller(t)
	r := newRouter(t, fake, routerOpts{internalKey: testInternalKey, caller: &caller})
	operator := map[string]string{"X-User-Id": uuid.NewString()}

	cases := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"no credential", operator, http.StatusUnauthorized},
		{"a wrong internal key", with(operator, map[string]string{"X-Internal-Service-Key": "not-the-key"}), http.StatusUnauthorized},
		{"a token without the refund admin operation", with(operator, caller.header(t, servicetoken.OpRefundCreate)), http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if w := do(r, http.MethodGet, refundListPath, nil, tc.headers); w.Code != tc.want {
				t.Errorf("list: status = %d, want %d; body=%s", w.Code, tc.want, w.Body.String())
			}
			if w := do(r, http.MethodPost, resolvePath(uuid.New()), testDataBody, tc.headers); w.Code != tc.want {
				t.Errorf("resolve: status = %d, want %d; body=%s", w.Code, tc.want, w.Body.String())
			}
		})
	}
	if len(fake.listFilters) != 0 || len(fake.resolveCalls) != 0 {
		t.Fatalf("an unauthenticated request reached the service: lists=%d resolves=%d",
			len(fake.listFilters), len(fake.resolveCalls))
	}
}

// With no internal key configured the key is not a credential on these routes.
func TestRefundAdminRoutes_KeyIsNotACredentialWithoutTheFallback(t *testing.T) {
	fake := newFake()
	caller := newRefundAdminCaller(t)
	r := newRouter(t, fake, routerOpts{caller: &caller})
	if w := do(r, http.MethodPost, resolvePath(uuid.New()), testDataBody, withKey(uuid.New())); w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if len(fake.resolveCalls) != 0 {
		t.Fatal("the resolve reached the service")
	}
}

func TestRefundAdminRoutes_List(t *testing.T) {
	fake := newFake()
	caller := newRefundAdminCaller(t)
	r := newRouter(t, fake, routerOpts{internalKey: testInternalKey, caller: &caller})
	fake.parked = []postgres.NeedsAttentionRefund{{
		ID: uuid.New(), IntentID: uuid.New(), ReferenceType: "food_order", ReferenceID: uuid.NewString(),
		AmountMinor: 60712, Currency: "INR", ReasonCode: "provider_rejected", Attempts: 1,
	}}

	t.Run("legacy internal key sees every domain", func(t *testing.T) {
		w := do(r, http.MethodGet, refundListPath+"?ref_type=food_order&limit=10", nil, withKey(uuid.Nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		got := fake.listFilters[len(fake.listFilters)-1]
		if got.Limit != 10 || got.ReferenceType != "food_order" || got.OwnerDomain != "" || got.After != nil {
			t.Fatalf("filter = %+v", got)
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
		if len(env.Data.Items) != 1 || env.Data.Items[0].ID != fake.parked[0].ID || env.Data.NextCursor != nil {
			t.Fatalf("response = %s", w.Body.String())
		}
	})
	t.Run("a token caller is scoped to its own domain", func(t *testing.T) {
		w := do(r, http.MethodGet, refundListPath, nil, caller.header(t, OpRefundAdmin))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		if got := fake.listFilters[len(fake.listFilters)-1]; got.OwnerDomain != "commerce-service" {
			t.Fatalf("filter = %+v, want owner commerce-service", got)
		}
	})
	t.Run("a token caller cannot name a reference type outside its allowlist", func(t *testing.T) {
		n := len(fake.listFilters)
		w := do(r, http.MethodGet, refundListPath+"?ref_type=food_order", nil, caller.header(t, OpRefundAdmin))
		if w.Code != http.StatusForbidden || len(fake.listFilters) != n {
			t.Fatalf("status = %d lists=%d", w.Code, len(fake.listFilters)-n)
		}
	})
	t.Run("bad paging parameters are refused", func(t *testing.T) {
		for _, q := range []string{"?limit=0", "?limit=201", "?limit=x", "?cursor=%21%21"} {
			if w := do(r, http.MethodGet, refundListPath+q, nil, withKey(uuid.Nil)); w.Code != http.StatusBadRequest {
				t.Errorf("%s: status = %d, want 400", q, w.Code)
			}
		}
	})
	t.Run("the cursor round-trips", func(t *testing.T) {
		want := postgres.RefundCursor{CreatedAt: time.Date(2026, 9, 14, 10, 11, 12, 345678000, time.UTC), ID: uuid.New()}
		got, err := decodeRefundCursor(encodeRefundCursor(want))
		if err != nil || !got.CreatedAt.Equal(want.CreatedAt) || got.ID != want.ID {
			t.Fatalf("cursor = %+v, %v; want %+v", got, err, want)
		}
	})
}

func TestRefundAdminRoutes_Resolve(t *testing.T) {
	fake := newFake()
	caller := newRefundAdminCaller(t)
	r := newRouter(t, fake, routerOpts{internalKey: testInternalKey, caller: &caller})
	cmd := uuid.New()
	operator := uuid.New()

	t.Run("the operator must be named", func(t *testing.T) {
		w := do(r, http.MethodPost, resolvePath(cmd), testDataBody, withKey(uuid.Nil))
		if w.Code != http.StatusBadRequest || len(fake.resolveCalls) != 0 {
			t.Fatalf("status = %d calls=%d", w.Code, len(fake.resolveCalls))
		}
	})
	t.Run("the body is validated before the service is called", func(t *testing.T) {
		for _, b := range []string{
			`{"resolution":"refunded","note":"x"}`,
			`{"resolution":"written_off","note":"   "}`,
			`{"resolution":"test_data"}`,
			`not json`,
		} {
			if w := do(r, http.MethodPost, resolvePath(cmd), []byte(b), withKey(operator)); w.Code != http.StatusBadRequest {
				t.Errorf("%s: status = %d, want 400", b, w.Code)
			}
		}
		if w := do(r, http.MethodPost, "/v1/payments/internal/refunds/not-a-uuid/resolve", testDataBody, withKey(operator)); w.Code != http.StatusBadRequest {
			t.Errorf("bad id: status = %d, want 400", w.Code)
		}
		if len(fake.resolveCalls) != 0 {
			t.Fatalf("an invalid resolve reached the service %d time(s)", len(fake.resolveCalls))
		}
	})
	t.Run("the internal key resolves on every domain and records the operator", func(t *testing.T) {
		w := do(r, http.MethodPost, resolvePath(cmd), testDataBody, withKey(operator))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		got := fake.resolveCalls[len(fake.resolveCalls)-1]
		if got.CommandID != cmd || got.Resolution != "test_data" || got.Note != "dev seed: simulated payment" ||
			got.OperatorID != operator.String() || got.Credential != "internal_key" || got.OwnerDomain != "" {
			t.Fatalf("resolve input = %+v", got)
		}
	})
	t.Run("a token resolves only within its own domain", func(t *testing.T) {
		h := with(caller.header(t, OpRefundAdmin), map[string]string{"X-User-Id": operator.String()})
		w := do(r, http.MethodPost, resolvePath(cmd), testDataBody, h)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		got := fake.resolveCalls[len(fake.resolveCalls)-1]
		if got.OwnerDomain != "commerce-service" || got.Credential != "service_token:commerce-service" {
			t.Fatalf("resolve input = %+v", got)
		}
	})
	t.Run("store refusals map to 404 and 409", func(t *testing.T) {
		for err, want := range map[error]int{
			postgres.ErrRefundCommandNotFound:  http.StatusNotFound,
			postgres.ErrRefundCommandNotParked: http.StatusConflict,
			postgres.ErrManualRefundRefused:    http.StatusConflict,
		} {
			fake.resolveErr = err
			if w := do(r, http.MethodPost, resolvePath(cmd), testDataBody, withKey(operator)); w.Code != want {
				t.Errorf("%v: status = %d, want %d", err, w.Code, want)
			}
		}
		fake.resolveErr = nil
	})
}
