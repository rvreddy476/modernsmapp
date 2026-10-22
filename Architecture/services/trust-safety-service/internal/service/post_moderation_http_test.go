// The post-service client against an httptest post-service: the subject
// read as post-service answers it today, the optional decision fields once
// section 6.2 lands, and the command's 409 codes mapped to the errors the
// appeal state machine acts on.
package service

import (
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
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

type fakePostService struct {
	mu          sync.Mutex
	subjectBody string
	subjectCode int
	commandCode int
	commandErr  string
	lastClaims  moderationcap.Claims
	lastKey     string
	// tokenV verifies the command token like post-service does (sweep #11);
	// tokenOK records whether the last command carried a valid one.
	tokenV   *servicetoken.Verifier
	tokenOK  bool
	commands int
}

func (f *fakePostService) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/posts/internal/moderation-subject/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.lastKey = r.Header.Get("X-Internal-Service-Key")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.subjectCode)
		_, _ = w.Write([]byte(f.subjectBody))
	})
	mux.HandleFunc("/v1/posts/internal/moderation", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.lastKey = r.Header.Get("X-Internal-Service-Key")
		f.commands++
		v, err := f.tokenV.Verify(strings.TrimPrefix(r.Header.Get("X-Service-Authorization"), "Bearer "), postModerationOp, "")
		f.tokenOK = err == nil && v.Issuer == "trust-safety-service"
		var body struct {
			Claims     moderationcap.Claims `json:"claims"`
			Capability string               `json:"capability"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.lastClaims = body.Claims
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.commandCode)
		if f.commandErr != "" {
			_, _ = w.Write([]byte(`{"error":{"code":"` + f.commandErr + `","message":"x"}}`))
		} else {
			_, _ = w.Write([]byte(`{"data":{"changed":true}}`))
		}
	})
	return mux
}

func newClientRig(t *testing.T) (*HTTPPostModerationClient, *fakePostService) {
	t.Helper()
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := servicetoken.NewSignerFromBase64("trust-safety-service", "t1", priv)
	if err != nil {
		t.Fatal(err)
	}
	tokenV := servicetoken.NewVerifier("post")
	if err := tokenV.RegisterBase64("trust-safety-service", "t1", pub, []string{postModerationOp}, nil); err != nil {
		t.Fatal(err)
	}
	fake := &fakePostService{subjectCode: http.StatusOK, commandCode: http.StatusOK, tokenV: tokenV}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	signer, err := moderationcap.NewSigner([]byte("0123456789abcdef0123456789abcdef"), "trust-safety-service", "post_moderation", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return NewHTTPPostModerationClient(srv.URL, "test-key", signer, tokens, srv.Client()), fake
}

func TestPostModerationClient_SubjectAsExposedToday(t *testing.T) {
	client, fake := newClientRig(t)
	post, author := uuid.New(), uuid.New()
	fake.subjectBody = `{"data":{"post_id":"` + post.String() + `","author_id":"` + author.String() + `","review_status":"rejected","content_revision":7,"deleted":false}}`
	got, err := client.GetSubject(context.Background(), post)
	if err != nil {
		t.Fatal(err)
	}
	if got.PostID != post || got.AuthorID != author || got.ReviewStatus != "rejected" || got.ContentRevision != 7 || got.Deleted ||
		got.LastDecisionID != nil || got.LastDecisionSource != "" {
		t.Fatalf("subject=%+v", got)
	}
	if fake.lastKey != "test-key" {
		t.Fatalf("internal key=%q", fake.lastKey)
	}
}

func TestPostModerationClient_SubjectDecisionFieldsWhenPresent(t *testing.T) {
	client, fake := newClientRig(t)
	post, author, decision := uuid.New(), uuid.New(), uuid.New()
	base := `"post_id":"` + post.String() + `","author_id":"` + author.String() + `","content_revision":7`
	cases := []struct {
		name       string
		body       string
		wantStatus string
		wantID     *uuid.UUID
		wantSource string
	}{
		{"last_decision fields", `{"data":{` + base + `,"review_status":"rejected","last_decision_id":"` + decision.String() + `","last_decision_source":"copyright"}}`,
			"rejected", ptrUUID(decision), "copyright"},
		{"plan 6.2 spellings", `{"data":{` + base + `,"review_status":"restricted","base_review_status":"rejected","latest_base_decision_id":"` + decision.String() + `","latest_base_decision_source":"admin"}}`,
			"rejected", ptrUUID(decision), "admin"},
		{"nil uuid is no decision", `{"data":{` + base + `,"review_status":"rejected","last_decision_id":"00000000-0000-0000-0000-000000000000"}}`,
			"rejected", nil, ""},
		{"null decision", `{"data":{` + base + `,"review_status":"rejected","last_decision_id":null,"last_decision_source":null}}`,
			"rejected", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake.mu.Lock()
			fake.subjectBody = tc.body
			fake.mu.Unlock()
			got, err := client.GetSubject(context.Background(), post)
			if err != nil {
				t.Fatal(err)
			}
			if got.ReviewStatus != tc.wantStatus || got.LastDecisionSource != tc.wantSource ||
				(got.LastDecisionID == nil) != (tc.wantID == nil) || (tc.wantID != nil && *got.LastDecisionID != *tc.wantID) {
				t.Fatalf("subject=%+v", got)
			}
		})
	}
}

func TestPostModerationClient_SubjectErrors(t *testing.T) {
	client, fake := newClientRig(t)
	post := uuid.New()
	fake.subjectCode, fake.subjectBody = http.StatusNotFound, `{"error":{"code":"NOT_FOUND"}}`
	if _, err := client.GetSubject(context.Background(), post); !errors.Is(err, ErrPostSubjectNotFound) {
		t.Fatalf("404: %v", err)
	}
	fake.subjectCode = http.StatusInternalServerError
	if _, err := client.GetSubject(context.Background(), post); err == nil || errors.Is(err, ErrPostSubjectNotFound) {
		t.Fatalf("500: %v", err)
	}
	// A body for another post, or without an author or revision, is refused.
	fake.subjectCode = http.StatusOK
	for _, body := range []string{
		`{"data":{"post_id":"` + uuid.NewString() + `","author_id":"` + uuid.NewString() + `","review_status":"rejected","content_revision":7}}`,
		`{"data":{"post_id":"` + post.String() + `","review_status":"rejected","content_revision":7}}`,
		`{"data":{"post_id":"` + post.String() + `","author_id":"` + uuid.NewString() + `","review_status":"rejected"}}`,
	} {
		fake.subjectBody = body
		if _, err := client.GetSubject(context.Background(), post); err == nil {
			t.Fatalf("incomplete subject accepted: %s", body)
		}
	}
}

func TestPostModerationClient_OverturnClaimsAndRefusals(t *testing.T) {
	client, fake := newClientRig(t)
	appeal := &postgres.ContentAppeal{ID: uuid.New(), ContentID: uuid.New()}
	reviewer := uuid.New()
	if err := client.OverturnAppeal(context.Background(), appeal, reviewer, 7, "Appeal overturned: ok"); err != nil {
		t.Fatal(err)
	}
	// Security sweep #11: the command carries trust-safety's
	// post:moderation.write token.
	if !fake.tokenOK {
		t.Fatal("the overturn command did not carry a valid post:moderation.write token")
	}
	c := fake.lastClaims
	if c.DecisionID != appeal.ID.String() || c.SubjectID != appeal.ContentID.String() || c.ContentRevision != 7 ||
		c.Decision != "approve" || c.ActorID != reviewer.String() || c.Reason != "Appeal overturned: ok" ||
		c.PolicyVersion != "appeal-v1" || c.Issuer != "trust-safety-service" || c.Purpose != "post_moderation" {
		t.Fatalf("claims=%+v", c)
	}
	cases := []struct {
		code int
		body string
		want error
	}{
		{http.StatusConflict, "STALE_MODERATION_SUBJECT", ErrPostDecisionSuperseded},
		{http.StatusConflict, "INVALID_TRANSITION", ErrPostDecisionSuperseded},
		{http.StatusConflict, "SUPERSEDED", ErrPostDecisionSuperseded},
		{http.StatusConflict, "DECISION_CONFLICT", ErrPostDecisionConflict},
	}
	for _, tc := range cases {
		fake.commandCode, fake.commandErr = tc.code, tc.body
		if err := client.OverturnAppeal(context.Background(), appeal, reviewer, 7, "r"); !errors.Is(err, tc.want) {
			t.Fatalf("%d %s: got %v, want %v", tc.code, tc.body, err, tc.want)
		}
	}
	// Anything else is transient: neither refusal, so the appeal stays in
	// flight and is replayed.
	for _, tc := range []struct {
		code int
		body string
	}{{http.StatusConflict, "SOMETHING_ELSE"}, {http.StatusInternalServerError, ""}, {http.StatusForbidden, "INVALID_CAPABILITY"}} {
		fake.commandCode, fake.commandErr = tc.code, tc.body
		err := client.OverturnAppeal(context.Background(), appeal, reviewer, 7, "r")
		if err == nil || errors.Is(err, ErrPostDecisionSuperseded) || errors.Is(err, ErrPostDecisionConflict) {
			t.Fatalf("%d %s: got %v, want a plain error", tc.code, tc.body, err)
		}
	}
}

// Without a service-token signer no command is sent: post-service would
// answer 401, so the client refuses first and the appeal stays in flight.
func TestPostModerationClient_OverturnNeedsATokenSigner(t *testing.T) {
	client, fake := newClientRig(t)
	client.tokens = nil
	appeal := &postgres.ContentAppeal{ID: uuid.New(), ContentID: uuid.New()}
	if err := client.OverturnAppeal(context.Background(), appeal, uuid.New(), 7, "r"); !errors.Is(err, ErrPostModerationNotConfigured) {
		t.Fatalf("no token signer: %v, want ErrPostModerationNotConfigured", err)
	}
	if fake.commands != 0 {
		t.Fatalf("%d command(s) reached post-service without a token", fake.commands)
	}
}
