package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type fakeClaims struct {
	claimCalls, releaseCalls, exceptCalls int
	claim                                 postgres.MediaClaim
	ids                                   []uuid.UUID
	err                                   error
	released                              int64
}

func (f *fakeClaims) ClaimMedia(_ context.Context, c postgres.MediaClaim, ids []uuid.UUID) error {
	f.claimCalls++
	f.claim, f.ids = c, ids
	return f.err
}

func (f *fakeClaims) ReleaseMediaClaimsExcept(_ context.Context, c postgres.MediaClaim, keep []uuid.UUID) (int64, error) {
	f.exceptCalls++
	f.claim, f.ids = c, keep
	return f.released, f.err
}

func (f *fakeClaims) ReleaseMediaClaims(_ context.Context, c postgres.MediaClaim, ids []uuid.UUID) (int64, error) {
	f.releaseCalls++
	f.claim, f.ids = c, ids
	return f.released, f.err
}

const claimsKey = "claims-internal-key"

func claimsRig(t *testing.T, svc mediaClaimsService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(nil).WithInternalKey(claimsKey)
	h.claims = svc
	noop := func(c *gin.Context) { c.Next() }
	h.RegisterRoutes(r, noop, noop)
	return r
}

func claimsCall(r *gin.Engine, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", claimsKey)
	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func claimBody(ids ...uuid.UUID) string {
	b, _ := json.Marshal(map[string]any{
		"claimant": "channel-service", "kind": "channel_update", "ref": "u-1", "media_ids": ids,
	})
	return string(b)
}

func TestClaimRouteForwardsTheClaim(t *testing.T) {
	fake := &fakeClaims{}
	r := claimsRig(t, fake)
	a, b := uuid.New(), uuid.New()
	w := claimsCall(r, "/v1/media/internal/claims", claimBody(a, b), nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"claimed":2`) {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.claimCalls != 1 || fake.claim != (postgres.MediaClaim{Claimant: "channel-service", Kind: "channel_update", Ref: "u-1"}) ||
		len(fake.ids) != 2 || fake.ids[0] != a || fake.ids[1] != b {
		t.Fatalf("forwarded %+v", fake)
	}
}

// Only a keyed service caller may claim or release: a claim blocks the
// owner's delete, and a release unblocks it.
func TestClaimRoutesAreServiceOnly(t *testing.T) {
	for _, path := range []string{"/v1/media/internal/claims", "/v1/media/internal/claims/release"} {
		for name, tc := range map[string]struct {
			headers map[string]string
			want    int
		}{
			"no key":        {map[string]string{"X-Internal-Service-Key": ""}, http.StatusUnauthorized},
			"wrong key":     {map[string]string{"X-Internal-Service-Key": "nope"}, http.StatusUnauthorized},
			"end-user call": {map[string]string{"X-User-Id": uuid.NewString()}, http.StatusForbidden},
		} {
			fake := &fakeClaims{}
			w := claimsCall(claimsRig(t, fake), path, claimBody(uuid.New()), tc.headers)
			if w.Code != tc.want {
				t.Errorf("%s %s: status %d, want %d", path, name, w.Code, tc.want)
			}
			if fake.claimCalls+fake.releaseCalls+fake.exceptCalls != 0 {
				t.Errorf("%s %s: reached the service", path, name)
			}
		}
	}
}

// The 404 carries MEDIA_NOT_FOUND so a claimant can tell "no such asset" from
// "no such route"; a failure is a 500, never a verdict.
func TestClaimRouteRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want int
		code string
	}{
		"missing media":  {fmt.Errorf("%w: x", service.ErrClaimMediaNotFound), http.StatusNotFound, `"MEDIA_NOT_FOUND"`},
		"invalid claim":  {fmt.Errorf("%w: unknown kind", service.ErrClaimInvalid), http.StatusBadRequest, `"CLAIM_INVALID"`},
		"database error": {errors.New("connection reset"), http.StatusInternalServerError, `"INTERNAL_ERROR"`},
	} {
		w := claimsCall(claimsRig(t, &fakeClaims{err: tc.err}), "/v1/media/internal/claims", claimBody(uuid.New()), nil)
		if w.Code != tc.want || !strings.Contains(w.Body.String(), tc.code) {
			t.Errorf("%s: %d %s, want %d %s", name, w.Code, w.Body.String(), tc.want, tc.code)
		}
	}
	w := claimsCall(claimsRig(t, &fakeClaims{}), "/v1/media/internal/claims", `not json`, nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad body: %d", w.Code)
	}
}

func TestReleaseRoute(t *testing.T) {
	fake := &fakeClaims{released: 3}
	w := claimsCall(claimsRig(t, fake), "/v1/media/internal/claims/release",
		`{"claimant":"channel-service","kind":"channel_update","ref":"u-1"}`, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"released":3`) {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if fake.releaseCalls != 1 || fake.claimCalls != 0 || len(fake.ids) != 0 {
		t.Fatalf("forwarded %+v", fake)
	}
	w = claimsCall(claimsRig(t, &fakeClaims{err: fmt.Errorf("%w: x", service.ErrClaimInvalid)}),
		"/v1/media/internal/claims/release", `{"claimant":"x","kind":"y","ref":"z"}`, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid release: %d", w.Code)
	}
	w = claimsCall(claimsRig(t, &fakeClaims{err: errors.New("boom")}),
		"/v1/media/internal/claims/release", `{"claimant":"channel-service","kind":"channel_update","ref":"z"}`, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("failed release must not read as released: %d", w.Code)
	}
}

// What the owner sees when a delete is refused.
func TestOwnerDeleteRefusalIsA409NamingWhere(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/v1/media/x", nil)
	err := &postgres.MediaInUseError{Holders: []postgres.ClaimHolder{{Claimant: "channel-service", Kind: "channel_update", Count: 2}}}
	if !writeDeleteMediaError(c, err) {
		t.Fatal("the in-use refusal was not handled")
	}
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409", w.Code)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details struct {
				InUseBy []service.InUseDetail `json:"in_use_by"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %s: %v", w.Body.String(), err)
	}
	if env.Error.Code != "MEDIA_IN_USE" || !strings.Contains(env.Error.Message, "2 channel updates") {
		t.Fatalf("body %s", w.Body.String())
	}
	if len(env.Error.Details.InUseBy) != 1 || env.Error.Details.InUseBy[0].Count != 2 {
		t.Fatalf("details %s", w.Body.String())
	}

	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/v1/media/x", nil)
	if !writeDeleteMediaError(c, fmt.Errorf("%w: fk", service.ErrMediaReferenced)) || w.Code != http.StatusConflict {
		t.Fatalf("a foreign-key refusal must be a 409, got %d", w.Code)
	}

	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/v1/media/x", nil)
	if writeDeleteMediaError(c, errors.New("media not found")) {
		t.Fatal("an unrelated error was claimed as an in-use refusal")
	}
}

func TestReleaseExceptRoute(t *testing.T) {
	keep := uuid.New()
	fake := &fakeClaims{released: 1}
	body := `{"claimant":"channel-service","kind":"channel_update","ref":"u-1","except_media_ids":["` + keep.String() + `"]}`
	w := claimsCall(claimsRig(t, fake), "/v1/media/internal/claims/release", body, nil)
	if w.Code != http.StatusOK || fake.exceptCalls != 1 || fake.releaseCalls != 0 || len(fake.ids) != 1 || fake.ids[0] != keep {
		t.Fatalf("status %d %s, forwarded %+v", w.Code, w.Body.String(), fake)
	}
	both := `{"claimant":"channel-service","kind":"channel_update","ref":"u-1","media_ids":["` + uuid.NewString() +
		`"],"except_media_ids":["` + keep.String() + `"]}`
	fake = &fakeClaims{}
	w = claimsCall(claimsRig(t, fake), "/v1/media/internal/claims/release", both, nil)
	if w.Code != http.StatusBadRequest || fake.exceptCalls+fake.releaseCalls != 0 {
		t.Fatalf("ids and except together must be refused: %d %+v", w.Code, fake)
	}
}

// The owner-delete handler needs the concrete service (and PostgreSQL), so
// its wiring is pinned on the source: the in-use refusal is answered before
// the message switch can turn it into a 500.
func TestOwnerDeleteHandlerAnswersInUseFirst(t *testing.T) {
	raw, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	i := strings.Index(src, "func (h *Handler) DeleteMedia(")
	if i < 0 {
		t.Fatal("DeleteMedia not found")
	}
	body := src[i:]
	if j := strings.Index(body[1:], "\nfunc "); j >= 0 {
		body = body[:j+1]
	}
	w, m := strings.Index(body, "if writeDeleteMediaError(c, err) {\n\t\t\treturn\n\t\t}"), strings.Index(body, "err.Error() ==")
	if w < 0 || m < 0 || w > m {
		t.Fatalf("DeleteMedia must call writeDeleteMediaError before matching messages (at %d, %d)", w, m)
	}
}
