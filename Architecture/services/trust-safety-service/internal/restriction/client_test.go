// The restriction command client without a database (Copyright Match plan
// sections 6.4, 9.2): the claims a command signs, the digest that stays
// the same across retries, the outcome table, the wire shape of a send and
// of the reconciliation read.
package restriction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/shared/moderationcap"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

const testHMACKey = "0123456789abcdef0123456789abcdef-test-key"

func testSigner(t *testing.T) *moderationcap.RestrictionSigner {
	t.Helper()
	s, err := moderationcap.NewRestrictionSigner([]byte(testHMACKey), CapabilityTTL)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func testVerifier(t *testing.T) *moderationcap.RestrictionVerifier {
	t.Helper()
	v, err := moderationcap.NewRestrictionVerifier([]byte(testHMACKey), nil, moderationcap.MaxRestrictionTTL)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func sampleCommand() Command {
	return Command{
		DecisionID: uuid.New(), CaseID: uuid.New(), CaseRevision: 7,
		Action: moderationcap.RestrictionActionPlaceHold, Source: moderationcap.RestrictionSourceCopyright,
		SubjectPostID: uuid.New(), SubjectAuthorID: uuid.New(),
		ExpectedState: moderationcap.RestrictionStateAbsent, ReasonCode: "removal_upheld",
		PolicyVersion: "copyright-v1", ActorID: uuid.New(),
	}
}

func TestClaims_ContentAndDigestStableAcrossRetries(t *testing.T) {
	cmd := sampleCommand()
	signer := testSigner(t)

	first, sig1, err := signer.Sign(Claims(cmd))
	if err != nil {
		t.Fatal(err)
	}
	// A retry: the same command signed again, later.
	time.Sleep(1100 * time.Millisecond)
	second, sig2, err := signer.Sign(Claims(cmd))
	if err != nil {
		t.Fatal(err)
	}

	// The authority claims are the signer's; every other claim is the row's.
	if first.Version != moderationcap.RestrictionClaimsVersion || first.Issuer != moderationcap.IssuerTrustSafety ||
		first.Purpose != moderationcap.PurposePostRestriction || first.Audience != moderationcap.AudiencePostService {
		t.Fatalf("authority claims: %+v", first)
	}
	if first.Action != cmd.Action || first.Source != cmd.Source || first.CaseID != cmd.CaseID.String() ||
		first.CaseRevision != cmd.CaseRevision || first.SubjectID != cmd.SubjectPostID.String() ||
		first.SubjectAuthorID != cmd.SubjectAuthorID.String() || first.ExpectedState != cmd.ExpectedState ||
		first.DecisionID != cmd.DecisionID.String() || first.PolicyVersion != cmd.PolicyVersion ||
		first.ReasonCode != cmd.ReasonCode || first.ActorID != cmd.ActorID.String() {
		t.Fatalf("claims do not carry the command: %+v vs %+v", first, cmd)
	}
	if first.ExpiresAtUnix-first.IssuedAtUnix != int64(CapabilityTTL.Seconds()) || first.ExpiresAtUnix-first.IssuedAtUnix > int64(moderationcap.MaxRestrictionTTL.Seconds()) {
		t.Fatalf("ttl: issued=%d expires=%d", first.IssuedAtUnix, first.ExpiresAtUnix)
	}
	// Both verify under post-service's verifier.
	v := testVerifier(t)
	if err := v.Verify(first, sig1); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	if err := v.Verify(second, sig2); err != nil {
		t.Fatalf("second verify: %v", err)
	}
	// The retry differs only in time and signature; the digest is the same
	// as the one the command row stores.
	if second.IssuedAtUnix <= first.IssuedAtUnix || sig1 == sig2 {
		t.Fatalf("retry must re-sign with fresh time bounds: %d/%d %s/%s", first.IssuedAtUnix, second.IssuedAtUnix, sig1, sig2)
	}
	if !bytes.Equal(first.Digest(), second.Digest()) || !bytes.Equal(first.Digest(), Digest(cmd)) || len(Digest(cmd)) != 32 {
		t.Fatalf("digest must be stable across retries")
	}
	// A different reason, revision, expected state or decision is a
	// different digest (post-service answers DECISION_CONFLICT).
	for name, mut := range map[string]func(*Command){
		"reason":   func(c *Command) { c.ReasonCode = "reinstated_on_review" },
		"revision": func(c *Command) { c.CaseRevision++ },
		"expected": func(c *Command) { c.ExpectedState = moderationcap.RestrictionStateReleased },
		"decision": func(c *Command) { c.DecisionID = uuid.New() },
		"subject":  func(c *Command) { c.SubjectPostID = uuid.New() },
	} {
		other := cmd
		mut(&other)
		if bytes.Equal(Digest(other), Digest(cmd)) {
			t.Fatalf("%s: digest must change", name)
		}
	}
}

func TestClassify_OutcomeTable(t *testing.T) {
	cases := []struct {
		status int
		code   string
		want   Disposition
	}{
		{200, "", DispositionAcked},
		{201, "", DispositionAcked},
		{0, "", DispositionRetry},
		{500, "INTERNAL_ERROR", DispositionRetry},
		{502, "", DispositionRetry},
		{503, "", DispositionRetry},
		{504, "", DispositionRetry},
		{429, "", DispositionRetry},
		{408, "", DispositionRetry},
		{409, CodeStaleCaseRevision, DispositionSuperseded},
		{409, CodeDecisionConflict, DispositionParked},
		{409, CodeSubjectMismatch, DispositionParked},
		{409, CodeStateMismatch, DispositionParked},
		{409, "", DispositionParked},
		{403, CodeInvalidCapability, DispositionParked},
		{403, CodeSourceNotEnabled, DispositionParked},
		{404, CodeSubjectNotFound, DispositionParked},
		{422, CodeInvalidClaims, DispositionParked},
		{400, "INVALID_REQUEST", DispositionParked},
		{401, "", DispositionParked},
		{301, "", DispositionParked},
	}
	for _, tc := range cases {
		if got := Classify(tc.status, tc.code); got != tc.want {
			t.Errorf("Classify(%d, %q) = %s, want %s", tc.status, tc.code, got, tc.want)
		}
	}
}

// postStub is a post-service double for the command route: it verifies the
// capability like post-service does and answers whatever the script says.
type postStub struct {
	t        *testing.T
	v        *moderationcap.RestrictionVerifier
	key      string
	mu       sync.Mutex
	seen     []moderationcap.RestrictionClaims
	answers  []stubAnswer
	listBody func(r *http.Request) (int, string)
}

type stubAnswer struct {
	status int
	body   string
}

func (s *postStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != CommandPath {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get(HeaderInternalServiceKey) != s.key {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"UNAUTHORIZED"}}`))
		return
	}
	if r.Method == http.MethodGet {
		status, body := s.listBody(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
		return
	}
	var req struct {
		Claims     moderationcap.RestrictionClaims `json:"claims"`
		Capability string                          `json:"capability"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.t.Errorf("stub: decode: %v", err)
		w.WriteHeader(400)
		return
	}
	if err := s.v.Verify(req.Claims, req.Capability); err != nil {
		s.t.Errorf("stub: capability refused: %v", err)
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":{"code":"INVALID_CAPABILITY"}}`))
		return
	}
	s.mu.Lock()
	s.seen = append(s.seen, req.Claims)
	var a stubAnswer
	if len(s.answers) > 0 {
		a, s.answers = s.answers[0], s.answers[1:]
	} else {
		a = stubAnswer{200, `{"data":{"state":"active","changed":true,"replayed":false}}`}
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(a.status)
	_, _ = w.Write([]byte(a.body))
}

func newStub(t *testing.T, answers ...stubAnswer) (*postStub, *httptest.Server) {
	t.Helper()
	s := &postStub{t: t, v: testVerifier(t), key: "k", answers: answers}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return s, srv
}

func TestClient_Send_OutcomesAndRetryKeepTheDecision(t *testing.T) {
	stub, srv := newStub(t,
		stubAnswer{503, `{"error":{"code":"UNAVAILABLE"}}`},
		stubAnswer{200, `{"data":{"restriction_id":"x","state":"active","changed":true,"replayed":true}}`},
		stubAnswer{409, `{"error":{"code":"STALE_CASE_REVISION","message":"stale"}}`},
		stubAnswer{409, `{"error":{"code":"DECISION_CONFLICT"}}`},
		stubAnswer{409, `{"error":{"code":"SUBJECT_MISMATCH"}}`},
		stubAnswer{409, `{"error":{"code":"STATE_MISMATCH"}}`},
		stubAnswer{403, `{"error":{"code":"SOURCE_NOT_ENABLED"}}`},
		stubAnswer{404, `{"error":{"code":"SUBJECT_NOT_FOUND"}}`},
		stubAnswer{422, `{"error":{"code":"INVALID_CLAIMS"}}`},
		stubAnswer{500, `not json`},
	)
	c := New(Config{BaseURL: srv.URL, InternalKey: "k", Signer: testSigner(t)})
	cmd := sampleCommand()
	ctx := context.Background()

	want := []struct {
		disp     Disposition
		code     string
		replayed bool
	}{
		{DispositionRetry, "UNAVAILABLE", false},
		{DispositionAcked, "", true},
		{DispositionSuperseded, CodeStaleCaseRevision, false},
		{DispositionParked, CodeDecisionConflict, false},
		{DispositionParked, CodeSubjectMismatch, false},
		{DispositionParked, CodeStateMismatch, false},
		{DispositionParked, CodeSourceNotEnabled, false},
		{DispositionParked, CodeSubjectNotFound, false},
		{DispositionParked, CodeInvalidClaims, false},
		{DispositionRetry, "", false},
	}
	for i, w := range want {
		out := c.Send(ctx, cmd)
		if out.Disposition != w.disp || out.ErrorCode != w.code || out.Replayed != w.replayed {
			t.Fatalf("send %d: got %s %q replayed=%v err=%v, want %s %q replayed=%v", i, out.Disposition, out.ErrorCode, out.Replayed, out.Err, w.disp, w.code, w.replayed)
		}
		if w.disp == DispositionAcked {
			if !strings.Contains(string(out.Body), `"state":"active"`) || out.Err != nil {
				t.Fatalf("ack body=%s err=%v", out.Body, out.Err)
			}
		} else if out.Err == nil {
			t.Fatalf("send %d: a refusal carries its error", i)
		}
	}
	// Every send carried the SAME decision id and digest (the retry is the
	// same command re-signed), and all ten reached the stub.
	if len(stub.seen) != len(want) {
		t.Fatalf("stub saw %d commands, want %d", len(stub.seen), len(want))
	}
	for i, cl := range stub.seen {
		if cl.DecisionID != cmd.DecisionID.String() || !bytes.Equal(cl.Digest(), Digest(cmd)) {
			t.Fatalf("send %d changed the decision: %+v", i, cl)
		}
	}
}

func TestClient_Send_TransportFailureIsRetry(t *testing.T) {
	_, srv := newStub(t)
	url := srv.URL
	srv.Close()
	c := New(Config{BaseURL: url, InternalKey: "k", Signer: testSigner(t), HTTPClient: &http.Client{Timeout: time.Second}})
	if out := c.Send(context.Background(), sampleCommand()); out.Disposition != DispositionRetry || out.StatusCode != 0 || out.Err == nil {
		t.Fatalf("closed server: %+v", out)
	}
}

func TestClient_Send_SemanticallyInvalidCommandIsParkedWithoutARequest(t *testing.T) {
	stub, srv := newStub(t)
	c := New(Config{BaseURL: srv.URL, InternalKey: "k", Signer: testSigner(t)})
	bad := sampleCommand()
	bad.ReasonCode = "claim_withdrawn" // a release reason on a place
	out := c.Send(context.Background(), bad)
	if out.Disposition != DispositionParked || out.ErrorCode != "SIGNER_REFUSED" || !errors.Is(out.Err, moderationcap.ErrInvalidRestrictionClaims) {
		t.Fatalf("bad reason: %+v", out)
	}
	if len(stub.seen) != 0 {
		t.Fatalf("an invalid command must never be sent")
	}
}

func TestClient_Send_NotConfiguredIsRetryWithoutARequest(t *testing.T) {
	stub, srv := newStub(t)
	c := New(Config{BaseURL: srv.URL, InternalKey: "k"})
	if out := c.Send(context.Background(), sampleCommand()); out.Disposition != DispositionRetry || !errors.Is(out.Err, ErrNotConfigured) {
		t.Fatalf("unconfigured: %+v", out)
	}
	if len(stub.seen) != 0 {
		t.Fatalf("nothing may be sent without a signer")
	}
}

func TestClient_Send_WrongKeyIsParked(t *testing.T) {
	_, srv := newStub(t)
	c := New(Config{BaseURL: srv.URL, InternalKey: "wrong", Signer: testSigner(t)})
	if out := c.Send(context.Background(), sampleCommand()); out.Disposition != DispositionParked || out.StatusCode != 401 {
		t.Fatalf("wrong key: %+v", out)
	}
}

func TestClient_ListByCase_TokenQueryAndPaging(t *testing.T) {
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := servicetoken.NewSignerFromBase64(Issuer, "t1", priv)
	if err != nil {
		t.Fatal(err)
	}
	verifier := servicetoken.NewVerifier(Audience)
	if err := verifier.RegisterBase64(Issuer, "t1", pub, []string{OpRestrictionsRead}, nil); err != nil {
		t.Fatal(err)
	}
	caseA, caseB := uuid.New(), uuid.New()
	var queries []string
	stub, srv := newStub(t)
	stub.listBody = func(r *http.Request) (int, string) {
		raw := strings.TrimPrefix(r.Header.Get(HeaderServiceAuthorization), "Bearer ")
		v, err := verifier.Verify(raw, OpRestrictionsRead, "")
		if err != nil || v.Issuer != Issuer {
			t.Errorf("read token refused: %v", err)
			return 403, `{"error":{"code":"SERVICE_TOKEN_REJECTED"}}`
		}
		q := r.URL.Query()
		queries = append(queries, q.Encode())
		if q.Get("source") != "copyright" || q.Get("case_ids") != caseA.String()+","+caseB.String() {
			t.Errorf("query: %s", q.Encode())
		}
		if q.Get("cursor") == "" {
			return 200, `{"data":{"items":[{"case_id":"` + caseA.String() + `","post_id":"` + uuid.NewString() + `","state":"active","case_revision":1}],"next_cursor":"c2"}}`
		}
		return 200, `{"data":{"items":[{"case_id":"` + caseB.String() + `","post_id":"` + uuid.NewString() + `","state":"released","case_revision":2}]}}`
	}
	c := New(Config{BaseURL: srv.URL, InternalKey: "k", Tokens: tokens})
	rows, err := c.ListByCase(context.Background(), []uuid.UUID{caseA, caseB})
	if err != nil || len(rows) != 2 || rows[0].CaseID != caseA || rows[0].State != "active" || rows[1].CaseID != caseB || rows[1].CaseRevision != 2 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if len(queries) != 2 || !strings.Contains(queries[1], "cursor=c2") {
		t.Fatalf("paging: %v", queries)
	}

	// Refused: the error names the status, not the body.
	stub.listBody = func(*http.Request) (int, string) {
		return 403, `{"error":{"code":"SERVICE_TOKEN_REJECTED","message":"secret detail"}}`
	}
	if _, err := c.ListByCase(context.Background(), []uuid.UUID{caseA}); !errors.Is(err, ErrReadRefused) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("refused: %v", err)
	}
	// Too many ids, none, unconfigured.
	many := make([]uuid.UUID, MaxCaseIDsPerRead+1)
	for i := range many {
		many[i] = uuid.New()
	}
	if _, err := c.ListByCase(context.Background(), many); err == nil {
		t.Fatal("101 ids must be refused client-side")
	}
	if rows, err := c.ListByCase(context.Background(), nil); err != nil || rows != nil {
		t.Fatalf("no ids: %v %v", rows, err)
	}
	if _, err := New(Config{BaseURL: srv.URL}).ListByCase(context.Background(), []uuid.UUID{caseA}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("unconfigured read: %v", err)
	}
}

func TestFromEnv(t *testing.T) {
	_, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	c, err := FromEnv(get(map[string]string{}), "k")
	if err != nil || c.CanSend() || c.CanRead() {
		t.Fatalf("blank: err=%v send=%v read=%v", err, c.CanSend(), c.CanRead())
	}
	if _, err := FromEnv(get(map[string]string{EnvRestrictionKey: "short"}), "k"); err == nil {
		t.Fatal("a short HMAC key must be refused")
	}
	if _, err := FromEnv(get(map[string]string{EnvTokenKey: priv}), "k"); err == nil {
		t.Fatal("a token key without a kid must be refused")
	}
	if _, err := FromEnv(get(map[string]string{EnvTokenKey: "nope", EnvTokenKID: "t1"}), "k"); err == nil {
		t.Fatal("an unusable token key must be refused")
	}
	c, err = FromEnv(get(map[string]string{EnvRestrictionKey: testHMACKey, EnvTokenKey: priv, EnvTokenKID: "t1", EnvPostServiceURL: "http://post:1/"}), "k")
	if err != nil || !c.CanSend() || !c.CanRead() || c.baseURL != "http://post:1" {
		t.Fatalf("full: err=%v send=%v read=%v base=%q", err, c.CanSend(), c.CanRead(), c.baseURL)
	}
}
