package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/moderationcap"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Handler-level proof for POST/GET /v1/posts/internal/restrictions with a
// fake store: the auth ladder (401 key, 403 capability, 403 source, 422
// claims), the store's refusals mapped to 404/409, and idempotent replay.
// The store itself is proven on post_it_test in
// store/postgres/post_restrictions_integration_test.go.

var (
	restrictionTestKey     = []byte("post-restriction-test-key-0123456789ab")
	postModerationTestKey  = []byte("post-moderation-test-key-0123456789abc")
	restrictionTestAuthor  = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	restrictionTestPost    = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	restrictionTestMissing = uuid.MustParse("99999999-9999-4999-8999-999999999999")
)

// fakeRestrictionStore records applied decisions by digest and replays.
type fakeRestrictionStore struct {
	mu       sync.Mutex
	applied  map[uuid.UUID][]byte
	outcomes map[uuid.UUID]*postgres.RestrictionOutcome
	calls    int
	fail     error
	listed   postgres.RestrictionListFilter
}

func newFakeRestrictionStore() *fakeRestrictionStore {
	return &fakeRestrictionStore{applied: map[uuid.UUID][]byte{}, outcomes: map[uuid.UUID]*postgres.RestrictionOutcome{}}
}

func (f *fakeRestrictionStore) ApplyPostRestriction(_ context.Context, in postgres.RestrictionCommand) (*postgres.RestrictionOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.fail != nil {
		return nil, f.fail
	}
	if digest, ok := f.applied[in.DecisionID]; ok {
		if string(digest) != string(in.ClaimsDigest) {
			return nil, postgres.ErrRestrictionDecisionConflict
		}
		out := *f.outcomes[in.DecisionID]
		out.Replayed = true
		return &out, nil
	}
	if in.PostID == restrictionTestMissing {
		return nil, postgres.ErrRestrictionSubjectNotFound
	}
	if in.SubjectAuthorID != restrictionTestAuthor {
		return nil, postgres.ErrRestrictionSubjectMismatch
	}
	state := "active"
	if in.Action == "release_hold" {
		state = "released"
	}
	out := &postgres.RestrictionOutcome{
		RestrictionID: uuid.MustParse("55555555-5555-4555-8555-555555555555"), PostID: in.PostID, CaseID: in.CaseID,
		Source: in.Source, State: state, CaseRevision: in.CaseRevision, ActiveRestrictionCount: 1,
		EffectiveReviewStatus: "restricted", Changed: true,
	}
	f.applied[in.DecisionID] = in.ClaimsDigest
	f.outcomes[in.DecisionID] = out
	return out, nil
}

func (f *fakeRestrictionStore) GetModerationSubject(_ context.Context, postID uuid.UUID) (*postgres.ModerationSubject, error) {
	if postID == restrictionTestMissing {
		return nil, errors.New("no rows")
	}
	last := uuid.MustParse("abababab-abab-4bab-8bab-abababababab")
	return &postgres.ModerationSubject{
		PostID: postID, AuthorID: restrictionTestAuthor, ReviewStatus: "approved", SearchRev: 4,
		BaseReviewStatus: "approved", EffectiveReviewStatus: "restricted", LatestBaseDecisionID: &last, LastDecisionID: &last,
		LastDecisionSource: postgres.LastDecisionSourceCopyright, LatestBaseDecisionSource: postgres.LastDecisionSourceCopyright,
		ActiveRestrictions: []postgres.PostRestriction{{RestrictionID: uuid.New(), PostID: postID, Source: "copyright", CaseID: uuid.New(), Scope: "global", State: "active", CaseRevision: 1}},
	}, nil
}

func (f *fakeRestrictionStore) ListPostRestrictions(_ context.Context, filter postgres.RestrictionListFilter) ([]postgres.PostRestriction, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listed = filter
	return []postgres.PostRestriction{{RestrictionID: uuid.New(), PostID: restrictionTestPost, Source: "copyright", CaseID: uuid.New(), Scope: "global", State: "active", CaseRevision: 1}}, "", nil
}

type restrictionRig struct {
	r     *gin.Engine
	store *fakeRestrictionStore
	ts    *servicetoken.Signer // trust-safety-service, registered for post:restrictions.read
	admin *servicetoken.Signer // admin-service, registered for the admin family only
}

func newRestrictionRig(t *testing.T) *restrictionRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	verifier, err := moderationcap.NewRestrictionVerifier(restrictionTestKey, nil, moderationcap.MaxRestrictionTTL)
	if err != nil {
		t.Fatal(err)
	}
	tsPub, tsPriv, _ := servicetoken.GenerateKeypair()
	aPub, aPriv, _ := servicetoken.GenerateKeypair()
	env := map[string]string{
		"SERVICE_CALLERS":                            "admin-service,trust-safety-service",
		"SERVICE_CALLER_ADMIN_SERVICE_KID":           "a1",
		"SERVICE_CALLER_ADMIN_SERVICE_PUBKEY":        aPub,
		"SERVICE_CALLER_ADMIN_SERVICE_OPS":           strings.Join(AdminPermissions, ",") + "," + OpRestrictionsRead, // over-granted on purpose
		"SERVICE_CALLER_TRUST_SAFETY_SERVICE_KID":    "t1",
		"SERVICE_CALLER_TRUST_SAFETY_SERVICE_PUBKEY": tsPub,
		"SERVICE_CALLER_TRUST_SAFETY_SERVICE_OPS":    OpRestrictionsRead,
	}
	sv, err := ServiceCallersFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	ts, _ := servicetoken.NewSignerFromBase64(IssuerTrustSafety, "t1", tsPriv)
	admin, _ := servicetoken.NewSignerFromBase64(IssuerAdminService, "a1", aPriv)
	store := newFakeRestrictionStore()
	r := gin.New()
	New(nil, nil).WithInternalKey(adminTestKey).WithRestrictionVerifier(verifier).WithRestrictionStore(store).WithServiceAuth(sv).RegisterRoutes(r)
	return &restrictionRig{r: r, store: store, ts: ts, admin: admin}
}

func restrictionClaims() moderationcap.RestrictionClaims {
	return moderationcap.RestrictionClaims{
		Action: "place_hold", Source: "copyright", CaseID: uuid.NewString(), CaseRevision: 3,
		SubjectID: restrictionTestPost.String(), SubjectAuthorID: restrictionTestAuthor.String(), ExpectedState: "absent",
		DecisionID: uuid.NewString(), PolicyVersion: "copyright-v1", ReasonCode: "removal_upheld", ActorID: uuid.NewString(),
	}
}

// signWith is the wire protocol (HMAC-SHA256 over the canonical claims,
// base64url), reproduced so a test can sign exactly what it wants: a
// stamped-but-invalid command, an expired one, or one under another key.
func signWith(key []byte, c moderationcap.RestrictionClaims) string {
	raw, _ := json.Marshal(c)
	mac := hmac.New(sha256.New, key)
	mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func stamp(c moderationcap.RestrictionClaims, ttl time.Duration) moderationcap.RestrictionClaims {
	now := time.Now().UTC()
	c.Version, c.Issuer, c.Purpose, c.Audience = 1, moderationcap.IssuerTrustSafety, moderationcap.PurposePostRestriction, moderationcap.AudiencePostService
	c.IssuedAtUnix, c.ExpiresAtUnix = now.Unix(), now.Add(ttl).Unix()
	return c
}

func signedCommand(t *testing.T, c moderationcap.RestrictionClaims) (moderationcap.RestrictionClaims, string) {
	t.Helper()
	signer, err := moderationcap.NewRestrictionSigner(restrictionTestKey, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claims, sig, err := signer.Sign(c)
	if err != nil {
		t.Fatal(err)
	}
	return claims, sig
}

func commandBody(t *testing.T, c moderationcap.RestrictionClaims, sig string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"claims": c, "capability": sig})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func withKey() map[string]string { return map[string]string{"X-Internal-Service-Key": adminTestKey} }

func (rig *restrictionRig) post(t *testing.T, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	return adminServe(rig.r, http.MethodPost, "/v1/posts/internal/restrictions", body, hdr)
}

func TestRestrictionCommand_401WithoutInternalKey(t *testing.T) {
	rig := newRestrictionRig(t)
	claims, sig := signedCommand(t, restrictionClaims())
	w := rig.post(t, commandBody(t, claims, sig), nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s, want 401", w.Code, w.Body.String())
	}
	if rig.store.calls != 0 {
		t.Fatal("the store was reached without the internal key")
	}
}

func TestRestrictionCommand_403Capability(t *testing.T) {
	rig := newRestrictionRig(t)
	good := stamp(restrictionClaims(), 5*time.Minute)
	cases := map[string]struct {
		claims moderationcap.RestrictionClaims
		sig    string
	}{
		"tampered case_id after signing": func() struct {
			claims moderationcap.RestrictionClaims
			sig    string
		} {
			sig := signWith(restrictionTestKey, good)
			c := good
			c.CaseID = uuid.NewString()
			return struct {
				claims moderationcap.RestrictionClaims
				sig    string
			}{c, sig}
		}(),
		"wrong purpose": func() struct {
			claims moderationcap.RestrictionClaims
			sig    string
		} {
			c := good
			c.Purpose = "post_moderation"
			return struct {
				claims moderationcap.RestrictionClaims
				sig    string
			}{c, signWith(restrictionTestKey, c)}
		}(),
		"wrong audience": func() struct {
			claims moderationcap.RestrictionClaims
			sig    string
		} {
			c := good
			c.Audience = "media-service"
			return struct {
				claims moderationcap.RestrictionClaims
				sig    string
			}{c, signWith(restrictionTestKey, c)}
		}(),
		"the post moderation key": {good, signWith(postModerationTestKey, good)},
		"expired": func() struct {
			claims moderationcap.RestrictionClaims
			sig    string
		} {
			c := good
			c.IssuedAtUnix = time.Now().Add(-20 * time.Minute).Unix()
			c.ExpiresAtUnix = time.Now().Add(-10 * time.Minute).Unix()
			return struct {
				claims moderationcap.RestrictionClaims
				sig    string
			}{c, signWith(restrictionTestKey, c)}
		}(),
		"ttl over 15 minutes": func() struct {
			claims moderationcap.RestrictionClaims
			sig    string
		} {
			c := stamp(restrictionClaims(), time.Hour)
			return struct {
				claims moderationcap.RestrictionClaims
				sig    string
			}{c, signWith(restrictionTestKey, c)}
		}(),
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			before := rig.store.calls
			w := rig.post(t, commandBody(t, tc.claims, tc.sig), withKey())
			if w.Code != http.StatusForbidden || adminErrorCode(t, w) != "INVALID_CAPABILITY" {
				t.Fatalf("status=%d body=%s, want 403 INVALID_CAPABILITY", w.Code, w.Body.String())
			}
			if rig.store.calls != before {
				t.Fatal("a refused capability reached the store")
			}
		})
	}
}

func TestRestrictionCommand_403WhenNoVerifierConfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(nil, nil).WithInternalKey(adminTestKey).RegisterRoutes(r)
	claims, sig := signedCommand(t, restrictionClaims())
	w := adminServe(r, http.MethodPost, "/v1/posts/internal/restrictions", commandBody(t, claims, sig), withKey())
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want 403", w.Code)
	}
}

func TestRestrictionCommand_403SafetySourceReserved(t *testing.T) {
	rig := newRestrictionRig(t)
	c := restrictionClaims()
	c.Source = "safety"
	claims, sig := signedCommand(t, c)
	w := rig.post(t, commandBody(t, claims, sig), withKey())
	if w.Code != http.StatusForbidden || adminErrorCode(t, w) != CodeSourceNotEnabled {
		t.Fatalf("status=%d body=%s, want 403 SOURCE_NOT_ENABLED", w.Code, w.Body.String())
	}
	if rig.store.calls != 0 {
		t.Fatal("safety reached the store")
	}
}

func TestRestrictionCommand_422SignedButInvalidClaims(t *testing.T) {
	rig := newRestrictionRig(t)
	bad := map[string]func(*moderationcap.RestrictionClaims){
		"release_hold with removal_upheld": func(c *moderationcap.RestrictionClaims) { c.Action = "release_hold" },
		"unknown reason":                   func(c *moderationcap.RestrictionClaims) { c.ReasonCode = "because" },
		"unknown expected_state":           func(c *moderationcap.RestrictionClaims) { c.ExpectedState = "pending" },
		"zero case_revision":               func(c *moderationcap.RestrictionClaims) { c.CaseRevision = 0 },
		"subject_id not a uuid":            func(c *moderationcap.RestrictionClaims) { c.SubjectID = "post-1" },
		"decision_id missing":              func(c *moderationcap.RestrictionClaims) { c.DecisionID = "" },
	}
	for name, mutate := range bad {
		t.Run(name, func(t *testing.T) {
			c := stamp(restrictionClaims(), 5*time.Minute)
			mutate(&c)
			w := rig.post(t, commandBody(t, c, signWith(restrictionTestKey, c)), withKey())
			if w.Code != http.StatusUnprocessableEntity || adminErrorCode(t, w) != CodeInvalidRestrictionClaims {
				t.Fatalf("status=%d body=%s, want 422 INVALID_CLAIMS", w.Code, w.Body.String())
			}
			if rig.store.calls != 0 {
				t.Fatal("invalid claims reached the store")
			}
		})
	}
}

func TestRestrictionCommand_400MalformedBody(t *testing.T) {
	rig := newRestrictionRig(t)
	for _, body := range []string{``, `{}`, `{"claims":{}}`, `not json`} {
		if w := rig.post(t, body, withKey()); w.Code != http.StatusBadRequest {
			t.Fatalf("body %q: status=%d, want 400", body, w.Code)
		}
	}
	claims := stamp(restrictionClaims(), 5*time.Minute)
	if w := rig.post(t, commandBody(t, claims, ""), withKey()); w.Code != http.StatusBadRequest {
		t.Fatalf("empty capability: status=%d, want 400", w.Code)
	}
	if rig.store.calls != 0 {
		t.Fatal("a malformed body reached the store")
	}
}

func TestRestrictionCommand_404And409FromTheStore(t *testing.T) {
	rig := newRestrictionRig(t)
	missing := restrictionClaims()
	missing.SubjectID = restrictionTestMissing.String()
	claims, sig := signedCommand(t, missing)
	if w := rig.post(t, commandBody(t, claims, sig), withKey()); w.Code != http.StatusNotFound || adminErrorCode(t, w) != CodeSubjectNotFound {
		t.Fatalf("missing post: status=%d body=%s, want 404 SUBJECT_NOT_FOUND", w.Code, w.Body.String())
	}
	mismatch := restrictionClaims()
	mismatch.SubjectAuthorID = uuid.NewString()
	claims, sig = signedCommand(t, mismatch)
	if w := rig.post(t, commandBody(t, claims, sig), withKey()); w.Code != http.StatusConflict || adminErrorCode(t, w) != CodeSubjectMismatch {
		t.Fatalf("other author: status=%d body=%s, want 409 SUBJECT_MISMATCH", w.Code, w.Body.String())
	}
	for code, err := range map[string]error{
		CodeStaleCaseRevision: postgres.ErrRestrictionStaleRevision,
		CodeStateMismatch:     postgres.ErrRestrictionStateMismatch,
		"DECISION_CONFLICT":   postgres.ErrRestrictionDecisionConflict,
	} {
		rig.store.fail = err
		claims, sig = signedCommand(t, restrictionClaims())
		if w := rig.post(t, commandBody(t, claims, sig), withKey()); w.Code != http.StatusConflict || adminErrorCode(t, w) != code {
			t.Fatalf("%v: status=%d body=%s, want 409 %s", err, w.Code, w.Body.String(), code)
		}
	}
	rig.store.fail = errors.New("connection reset")
	claims, sig = signedCommand(t, restrictionClaims())
	if w := rig.post(t, commandBody(t, claims, sig), withKey()); w.Code != http.StatusInternalServerError {
		t.Fatalf("store failure: status=%d, want 500", w.Code)
	}
}

func TestRestrictionCommand_IdempotentReplayAndConflict(t *testing.T) {
	rig := newRestrictionRig(t)
	base := restrictionClaims()
	claims, sig := signedCommand(t, base)
	w := rig.post(t, commandBody(t, claims, sig), withKey())
	if w.Code != http.StatusOK {
		t.Fatalf("first: status=%d body=%s", w.Code, w.Body.String())
	}
	var first struct {
		Data postgres.RestrictionOutcome `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.Data.Replayed || first.Data.State != "active" || first.Data.EffectiveReviewStatus != "restricted" {
		t.Fatalf("first outcome: %+v", first.Data)
	}

	// A retry re-signs the SAME decision with fresh times: replayed=true,
	// everything else identical.
	time.Sleep(1100 * time.Millisecond)
	retry, retrySig := signedCommand(t, base)
	if retry.IssuedAtUnix == claims.IssuedAtUnix {
		t.Fatal("test setup: the retry did not get fresh times")
	}
	w = rig.post(t, commandBody(t, retry, retrySig), withKey())
	if w.Code != http.StatusOK {
		t.Fatalf("replay: status=%d body=%s", w.Code, w.Body.String())
	}
	var second struct {
		Data postgres.RestrictionOutcome `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &second)
	if !second.Data.Replayed {
		t.Fatalf("replay not flagged: %+v", second.Data)
	}
	second.Data.Replayed = false
	if second.Data != first.Data {
		t.Fatalf("replay outcome differs:\n first=%+v\nsecond=%+v", first.Data, second.Data)
	}

	// The same decision_id with a different reason is a conflict.
	changed := base
	changed.ReasonCode = "reinstated_on_review"
	claims, sig = signedCommand(t, changed)
	if w := rig.post(t, commandBody(t, claims, sig), withKey()); w.Code != http.StatusConflict || adminErrorCode(t, w) != "DECISION_CONFLICT" {
		t.Fatalf("conflict: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestRestrictionRead_TokenLadder(t *testing.T) {
	rig := newRestrictionRig(t)
	path := "/v1/posts/internal/restrictions?case_ids=" + uuid.NewString()
	// No key at all: the global gate.
	if w := adminServe(rig.r, http.MethodGet, path, "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("no key: status=%d, want 401", w.Code)
	}
	// Key, no token.
	if w := adminServe(rig.r, http.MethodGet, path, "", withKey()); w.Code != http.StatusUnauthorized {
		t.Fatalf("key only: status=%d, want 401", w.Code)
	}
	hdr := func(tok string) map[string]string {
		h := withKey()
		h[ServiceAuthHeader] = "Bearer " + tok
		return h
	}
	// admin-service, even over-granted the op, is not trust-safety.
	adminTok, _ := rig.admin.Mint(AudiencePost, "admin-console", []string{OpRestrictionsRead}, nil, 60e9, servicetoken.WithActor(uuid.NewString()))
	if w := adminServe(rig.r, http.MethodGet, path, "", hdr(adminTok)); w.Code != http.StatusForbidden {
		t.Fatalf("admin issuer: status=%d body=%s, want 403", w.Code, w.Body.String())
	}
	// trust-safety without the op.
	noOp, _ := rig.ts.Mint(AudiencePost, "reconcile", []string{"trust_safety:standing.read"}, nil, 60e9)
	if w := adminServe(rig.r, http.MethodGet, path, "", hdr(noOp)); w.Code != http.StatusForbidden {
		t.Fatalf("wrong op: status=%d, want 403", w.Code)
	}
	// Wrong audience.
	wrongAud, _ := rig.ts.Mint("trust_safety", "reconcile", []string{OpRestrictionsRead}, nil, 60e9)
	if w := adminServe(rig.r, http.MethodGet, path, "", hdr(wrongAud)); w.Code != http.StatusForbidden {
		t.Fatalf("wrong audience: status=%d, want 403", w.Code)
	}
	// The real thing.
	tok, _ := rig.ts.Mint(AudiencePost, "reconcile", []string{OpRestrictionsRead}, nil, 60e9)
	w := adminServe(rig.r, http.MethodGet, path+"&post_id="+restrictionTestPost.String()+"&limit=50", "", hdr(tok))
	if w.Code != http.StatusOK {
		t.Fatalf("trust-safety: status=%d body=%s", w.Code, w.Body.String())
	}
	if rig.store.listed.Source != "copyright" || len(rig.store.listed.CaseIDs) != 1 || rig.store.listed.PostID == nil || rig.store.listed.Limit != 50 {
		t.Fatalf("filter not passed through: %+v", rig.store.listed)
	}
	var body struct {
		Data restrictionListResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body.Data.Items) != 1 {
		t.Fatalf("body: %s (%v)", w.Body.String(), err)
	}
	// Bad query values are 400, never a wider read.
	for _, q := range []string{"?case_ids=nope", "?post_id=nope", "?updated_after=yesterday", "?limit=0", "?limit=501"} {
		if w := adminServe(rig.r, http.MethodGet, "/v1/posts/internal/restrictions"+q, "", hdr(tok)); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d, want 400", q, w.Code)
		}
	}
}

// The extended moderation subject: the appeal client reads last_decision_id
// and last_decision_source, the plan's fields ride alongside, and the
// legacy fields keep their keys.
func TestModerationSubject_ExposesLastBaseDecisionAndSource(t *testing.T) {
	rig := newRestrictionRig(t)
	w := adminServe(rig.r, http.MethodGet, "/v1/posts/internal/moderation-subject/"+restrictionTestPost.String(), "", withKey())
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"post_id", "author_id", "review_status", "content_revision", "deleted",
		"base_review_status", "effective_review_status", "latest_base_decision_id", "active_restrictions",
		"last_decision_id", "last_decision_source", "latest_base_decision_source"} {
		if _, ok := body.Data[key]; !ok {
			t.Errorf("moderation subject lacks %q: %s", key, w.Body.String())
		}
	}
	if string(body.Data["last_decision_id"]) != `"abababab-abab-4bab-8bab-abababababab"` || string(body.Data["last_decision_source"]) != `"copyright"` || string(body.Data["latest_base_decision_source"]) != `"copyright"` {
		t.Fatalf("last decision fields: %s", w.Body.String())
	}
	if string(body.Data["review_status"]) != `"approved"` || string(body.Data["content_revision"]) != `4` {
		t.Fatalf("legacy fields changed: %s", w.Body.String())
	}
	if w := adminServe(rig.r, http.MethodGet, "/v1/posts/internal/moderation-subject/"+restrictionTestMissing.String(), "", withKey()); w.Code != http.StatusNotFound {
		t.Fatalf("missing: status=%d, want 404", w.Code)
	}
	if w := adminServe(rig.r, http.MethodGet, "/v1/posts/internal/moderation-subject/"+restrictionTestPost.String(), "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("no key: status=%d, want 401", w.Code)
	}
}
