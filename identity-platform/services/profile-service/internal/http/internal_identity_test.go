package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Internal identity read (dating lane D2): a sibling service reads a user's
// registration first name and date of birth. These tests pin who may call it,
// exactly what it returns, and what it must never return.

const identityTestKey = "identity-test-internal-key"

type identityBasicsService struct {
	stubProfileService
	rows  map[uuid.UUID]*store.IdentityBasics
	err   error
	calls int
}

func (s *identityBasicsService) GetIdentityBasics(_ context.Context, id uuid.UUID) (*store.IdentityBasics, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.rows[id], nil
}

func registeredIdentity() (uuid.UUID, *store.IdentityBasics) {
	id := uuid.MustParse("6f1c2b1e-4a4f-4d7e-9a53-0d7c1f2e3b4a")
	dob := time.Date(1990, 3, 17, 0, 0, 0, 0, time.UTC)
	return id, &store.IdentityBasics{
		UserID:    id,
		FirstName: "Asha",
		DoB:       &dob,
		DoBSource: store.DoBSourceRegistration,
	}
}

func identityRouter(t *testing.T, svc ProfileService, key string, logger *slog.Logger) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(svc, logger)
	if key != "" {
		h.WithInternalKey(key)
	}
	h.RegisterRoutes(r, func(c *gin.Context) { c.Next() }, func(c *gin.Context) { c.Next() })
	return r
}

func identityPath(id string) string {
	return strings.Replace(InternalIdentityPath, ":userId", id, 1)
}

func doIdentityRequest(r http.Handler, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	return resp
}

func decodeIdentityData(t *testing.T, resp *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, resp.Body.String())
	}
	if env.Data == nil {
		t.Fatalf("response has no data object: %s", resp.Body.String())
	}
	return env.Data
}

func decodeErrorCode(t *testing.T, resp *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil || env.Error == nil {
		t.Fatalf("expected an error envelope, got %s", resp.Body.String())
	}
	return env.Error.Code
}

func stringField(t *testing.T, data map[string]json.RawMessage, key string) string {
	t.Helper()
	raw, ok := data[key]
	if !ok {
		t.Fatalf("field %q missing", key)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("field %q is not a string: %s", key, raw)
	}
	return s
}

func TestInternalIdentity_ServiceCallerGetsExactFields(t *testing.T) {
	id, row := registeredIdentity()
	svc := &identityBasicsService{rows: map[uuid.UUID]*store.IdentityBasics{id: row}}
	r := identityRouter(t, svc, identityTestKey, nil)

	resp := doIdentityRequest(r, identityPath(id.String()), map[string]string{
		"X-Internal-Service-Key": identityTestKey,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 (body %s)", resp.Code, resp.Body.String())
	}

	data := decodeIdentityData(t, resp)
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{"dob", "dob_source", "first_name", "user_id"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("fields = %v, want exactly %v", keys, want)
	}
	if got := stringField(t, data, "user_id"); got != id.String() {
		t.Errorf("user_id = %q, want %q", got, id)
	}
	if got := stringField(t, data, "first_name"); got != "Asha" {
		t.Errorf("first_name = %q, want Asha", got)
	}
	if got := stringField(t, data, "dob"); got != "1990-03-17" {
		t.Errorf("dob = %q, want 1990-03-17", got)
	}
	if got := stringField(t, data, "dob_source"); got != "registration" {
		t.Errorf("dob_source = %q, want registration", got)
	}
}

func TestInternalIdentity_ResponseCarriesNoContactOrLastName(t *testing.T) {
	id, row := registeredIdentity()
	svc := &identityBasicsService{rows: map[uuid.UUID]*store.IdentityBasics{id: row}}
	r := identityRouter(t, svc, identityTestKey, nil)

	resp := doIdentityRequest(r, identityPath(id.String()), map[string]string{
		"X-Internal-Service-Key": identityTestKey,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.Code)
	}
	body := strings.ToLower(resp.Body.String())
	for _, forbidden := range []string{"email", "phone", "last_name", "lastname", "surname", "username", "gender"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("response contains %q: %s", forbidden, resp.Body.String())
		}
	}
}

func TestInternalIdentity_RefusesRequestsCarryingGatewayUserIdentity(t *testing.T) {
	id, row := registeredIdentity()
	for _, header := range []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "X-Admin-Role"} {
		t.Run(header, func(t *testing.T) {
			svc := &identityBasicsService{rows: map[uuid.UUID]*store.IdentityBasics{id: row}}
			r := identityRouter(t, svc, identityTestKey, nil)

			value := id.String()
			if header == "X-Scopes" {
				value = "admin"
			} else if header == "X-Admin-Role" {
				value = "superadmin"
			}
			// The gateway injects the real key on every proxied request, so the
			// key being correct must not rescue a request that carries a user.
			resp := doIdentityRequest(r, identityPath(id.String()), map[string]string{
				"X-Internal-Service-Key": identityTestKey,
				header:                   value,
			})
			if resp.Code != http.StatusForbidden {
				t.Fatalf("status %d, want 403 (body %s)", resp.Code, resp.Body.String())
			}
			if code := decodeErrorCode(t, resp); code != CodeUserCallerRefused {
				t.Errorf("error code %q, want %s", code, CodeUserCallerRefused)
			}
			if svc.calls != 0 {
				t.Errorf("store was consulted %d times for a refused caller", svc.calls)
			}
			if strings.Contains(resp.Body.String(), "1990") {
				t.Errorf("refusal leaked the date of birth: %s", resp.Body.String())
			}
		})
	}
}

func TestInternalIdentityGuard_RefusesMissingOrWrongKey(t *testing.T) {
	cases := []struct {
		name       string
		configured string
		sent       *string
		want       int
	}{
		{name: "missing key", configured: identityTestKey, sent: nil, want: http.StatusUnauthorized},
		{name: "blank key", configured: identityTestKey, sent: ptr(""), want: http.StatusUnauthorized},
		{name: "wrong key", configured: identityTestKey, sent: ptr("not-the-key"), want: http.StatusUnauthorized},
		{name: "key prefix", configured: identityTestKey, sent: ptr(identityTestKey[:5]), want: http.StatusUnauthorized},
		{name: "unconfigured service admits nobody", configured: "", sent: ptr(""), want: http.StatusUnauthorized},
		{name: "correct key", configured: identityTestKey, sent: ptr(identityTestKey), want: http.StatusNoContent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.GET("/guarded", requireInternalServiceCaller(tc.configured), func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})
			headers := map[string]string{}
			if tc.sent != nil {
				headers["X-Internal-Service-Key"] = *tc.sent
			}
			resp := doIdentityRequest(r, "/guarded", headers)
			if resp.Code != tc.want {
				t.Fatalf("status %d, want %d (body %s)", resp.Code, tc.want, resp.Body.String())
			}
		})
	}
}

func TestInternalIdentity_RouteRefusesMissingOrWrongKey(t *testing.T) {
	id, row := registeredIdentity()
	for name, headers := range map[string]map[string]string{
		"missing": {},
		"wrong":   {"X-Internal-Service-Key": "not-the-key"},
	} {
		t.Run(name, func(t *testing.T) {
			svc := &identityBasicsService{rows: map[uuid.UUID]*store.IdentityBasics{id: row}}
			r := identityRouter(t, svc, identityTestKey, nil)
			resp := doIdentityRequest(r, identityPath(id.String()), headers)
			if resp.Code != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401", resp.Code)
			}
			if svc.calls != 0 {
				t.Errorf("store consulted without a credential")
			}
		})
	}
}

func TestInternalIdentity_NotRegisteredWithoutAKey(t *testing.T) {
	id, row := registeredIdentity()
	svc := &identityBasicsService{rows: map[uuid.UUID]*store.IdentityBasics{id: row}}
	r := identityRouter(t, svc, "", nil)
	resp := doIdentityRequest(r, identityPath(id.String()), nil)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 when no internal key is configured", resp.Code)
	}
	if svc.calls != 0 {
		t.Errorf("store consulted on an unconfigured service")
	}
}

func TestInternalIdentity_UnknownUserIs404(t *testing.T) {
	svc := &identityBasicsService{rows: map[uuid.UUID]*store.IdentityBasics{}}
	r := identityRouter(t, svc, identityTestKey, nil)
	resp := doIdentityRequest(r, identityPath(uuid.NewString()), map[string]string{
		"X-Internal-Service-Key": identityTestKey,
	})
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 (body %s)", resp.Code, resp.Body.String())
	}
	if code := decodeErrorCode(t, resp); code != "NOT_FOUND" {
		t.Errorf("error code %q, want NOT_FOUND", code)
	}
}

func TestInternalIdentity_InvalidIDIs400(t *testing.T) {
	for _, raw := range []string{"not-a-uuid", "123", "00000000-0000-0000-0000-000000000000"} {
		t.Run(raw, func(t *testing.T) {
			svc := &identityBasicsService{rows: map[uuid.UUID]*store.IdentityBasics{}}
			r := identityRouter(t, svc, identityTestKey, nil)
			resp := doIdentityRequest(r, identityPath(raw), map[string]string{
				"X-Internal-Service-Key": identityTestKey,
			})
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400 (body %s)", resp.Code, resp.Body.String())
			}
			if code := decodeErrorCode(t, resp); code != "INVALID_ID" {
				t.Errorf("error code %q, want INVALID_ID", code)
			}
			if svc.calls != 0 {
				t.Errorf("store consulted for an invalid id")
			}
		})
	}
}

func TestInternalIdentity_StoreFailureIs500WithoutData(t *testing.T) {
	svc := &identityBasicsService{err: errors.New("db down")}
	r := identityRouter(t, svc, identityTestKey, nil)
	resp := doIdentityRequest(r, identityPath(uuid.NewString()), map[string]string{
		"X-Internal-Service-Key": identityTestKey,
	})
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", resp.Code)
	}
	if strings.Contains(resp.Body.String(), "db down") {
		t.Errorf("internal error text leaked: %s", resp.Body.String())
	}
}

func TestInternalIdentity_UnknownDOBIsNull(t *testing.T) {
	id := uuid.New()
	svc := &identityBasicsService{rows: map[uuid.UUID]*store.IdentityBasics{
		id: {UserID: id, FirstName: "", DoB: nil, DoBSource: store.DoBSourceNone},
	}}
	r := identityRouter(t, svc, identityTestKey, nil)
	resp := doIdentityRequest(r, identityPath(id.String()), map[string]string{
		"X-Internal-Service-Key": identityTestKey,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.Code)
	}
	data := decodeIdentityData(t, resp)
	if string(data["dob"]) != "null" {
		t.Errorf("dob = %s, want null", data["dob"])
	}
	if got := stringField(t, data, "dob_source"); got != "none" {
		t.Errorf("dob_source = %q, want none", got)
	}
}

func TestInternalIdentity_AuditLogNamesCallerAndUserButNeverDOB(t *testing.T) {
	id, row := registeredIdentity()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	svc := &identityBasicsService{rows: map[uuid.UUID]*store.IdentityBasics{id: row}}
	r := identityRouter(t, svc, identityTestKey, logger)

	resp := doIdentityRequest(r, identityPath(id.String()), map[string]string{
		"X-Internal-Service-Key": identityTestKey,
		"X-Caller-Service":       "dating-service",
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.Code)
	}
	out := buf.String()
	if !strings.Contains(out, "internal identity read") {
		t.Fatalf("no audit line: %s", out)
	}
	if !strings.Contains(out, "caller=dating-service") || !strings.Contains(out, id.String()) {
		t.Errorf("audit line must name caller and user id: %s", out)
	}
	for _, secret := range []string{"1990", "Asha"} {
		if strings.Contains(out, secret) {
			t.Errorf("audit log contains %q: %s", secret, out)
		}
	}
}

func TestInternalIdentity_UnsafeCallerLabelIsNotLoggedVerbatim(t *testing.T) {
	cases := map[string]string{
		"":                       "unlabelled",
		"dating-service":         "dating-service",
		"Dating Service":         "unlabelled",
		"evil\nlevel=ERROR":      "unlabelled",
		strings.Repeat("a", 65):  "unlabelled",
		"notification-service-2": "notification-service-2",
	}
	for in, want := range cases {
		if got := callerLabel(in); got != want {
			t.Errorf("callerLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInternalIdentityPath_IsOutsideGatewayRoutedPrefixes(t *testing.T) {
	// The api-gateway proxies /v1/profiles/* to this service and injects the
	// internal key on the way. A path under /v1/ could be routed there; this
	// one must not be. "/internal/" keeps the gateway's admin gate in play if a
	// future catch-all ever forwards it.
	if strings.HasPrefix(InternalIdentityPath, "/v1/") {
		t.Fatalf("%s sits under /v1/, which the gateway routes", InternalIdentityPath)
	}
	if !strings.Contains(InternalIdentityPath, "/internal/") {
		t.Fatalf("%s must contain /internal/", InternalIdentityPath)
	}
}

func TestResolveInternalKey(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		wantKey string
		wantErr bool
		warn    bool
	}{
		{name: "key set, no env", env: map[string]string{"INTERNAL_SERVICE_KEY": "k"}, wantKey: "k"},
		{name: "key set, prod", env: map[string]string{"INTERNAL_SERVICE_KEY": "k", "ENV": "prod"}, wantKey: "k"},
		{name: "missing, no env fails closed", env: map[string]string{}, wantErr: true},
		{name: "blank, prod", env: map[string]string{"INTERNAL_SERVICE_KEY": "   ", "ENV": "prod"}, wantErr: true},
		{name: "missing, staging", env: map[string]string{"ENV": "staging"}, wantErr: true},
		{name: "missing, local", env: map[string]string{"ENV": "local"}, warn: true},
		{name: "missing, DEV", env: map[string]string{"ENV": "DEV"}, warn: true},
		{name: "missing, APP_ENV development", env: map[string]string{"APP_ENV": "development"}, warn: true},
		{name: "APP_ENV production beats stale ENV dev", env: map[string]string{"APP_ENV": "production", "ENV": "dev"}, wantErr: true},
		{name: "APP_ENV development beats stale ENV prod", env: map[string]string{"APP_ENV": "development", "ENV": "prod"}, warn: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, warning, err := ResolveInternalKey(func(k string) string { return tc.env[k] })
			if tc.wantErr {
				if !errors.Is(err, ErrInternalKeyRequired) {
					t.Fatalf("err = %v, want ErrInternalKeyRequired", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if key != tc.wantKey {
				t.Errorf("key = %q, want %q", key, tc.wantKey)
			}
			if tc.warn != (warning != "") {
				t.Errorf("warning = %q, want warning=%v", warning, tc.warn)
			}
		})
	}
}

func ptr(s string) *string { return &s }
