package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/graph-service/internal/permission"
	"github.com/google/uuid"
)

type stubAcceptedChatProvider struct {
	accepted bool
	err      error
	calls    int
}

func (s *stubAcceptedChatProvider) HasAcceptedDirectChat(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	s.calls++
	return s.accepted, s.err
}

func TestAcceptedChatFactFailsClosed(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	ctx := context.Background()

	if (&Service{}).WithAcceptedChatCalls(true, &stubAcceptedChatProvider{err: errors.New("down"), accepted: true}).acceptedChatFact(ctx, a, b) {
		t.Fatal("a provider error granted the accepted-chat fact")
	}
	off := &stubAcceptedChatProvider{accepted: true}
	if (&Service{}).WithAcceptedChatCalls(false, off).acceptedChatFact(ctx, a, b) || off.calls != 0 {
		t.Fatal("flag off must never fetch or grant")
	}
	if (&Service{}).WithAcceptedChatCalls(true, nil).acceptedChatFact(ctx, a, b) {
		t.Fatal("nil provider granted the fact")
	}
	self := &stubAcceptedChatProvider{accepted: true}
	if (&Service{}).WithAcceptedChatCalls(true, self).acceptedChatFact(ctx, a, a) || self.calls != 0 {
		t.Fatal("a self pair must never fetch or grant")
	}
	if !(&Service{}).WithAcceptedChatCalls(true, &stubAcceptedChatProvider{accepted: true}).acceptedChatFact(ctx, a, b) {
		t.Fatal("an accepted chat did not grant the fact")
	}
}

func TestHTTPAcceptedChatProvider(t *testing.T) {
	if _, err := NewHTTPAcceptedChatProvider("", "key", nil); err == nil {
		t.Fatal("empty MESSAGE_SERVICE_URL accepted")
	}
	if _, err := NewHTTPAcceptedChatProvider("http://chat:8080", " ", nil); err == nil {
		t.Fatal("empty INTERNAL_SERVICE_KEY accepted")
	}

	var gotPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey = r.URL.Path, r.Header.Get("X-Internal-Service-Key")
		if r.URL.RawQuery != "" {
			t.Errorf("user ids leaked into the query string: %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"data":{"accepted":true}}`))
	}))
	defer srv.Close()
	p, err := NewHTTPAcceptedChatProvider(srv.URL, "k", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	ok, err := p.HasAcceptedDirectChat(context.Background(), uuid.New(), uuid.New())
	if err != nil || !ok {
		t.Fatalf("accepted=%v err=%v; want true,nil", ok, err)
	}
	if gotPath != "/internal/v1/chat/direct-chat/state" || gotKey != "k" {
		t.Fatalf("path=%q key=%q", gotPath, gotKey)
	}

	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"not deployed yet", http.StatusNotFound, `{}`},
		{"server error", http.StatusInternalServerError, `{}`},
		{"undecodable", http.StatusOK, `nope`},
		{"200 without the field is unknown", http.StatusOK, `{"data":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			p, _ := NewHTTPAcceptedChatProvider(srv.URL, "k", srv.Client())
			ok, err := p.HasAcceptedDirectChat(context.Background(), uuid.New(), uuid.New())
			if err == nil || ok {
				t.Fatalf("accepted=%v err=%v; want false + error", ok, err)
			}
		})
	}
}

// Unreadable settings on either side answer reason "unavailable", never the
// fail-safe no_one presented as the user's own choice.
func TestStrictDefaultsReportUnavailableForMessage(t *testing.T) {
	p := strictPrivacyDefaults()
	for _, f := range []permission.Facts{{}, {IsConnection: true, TargetFollowsActor: true}} {
		d := permission.Resolve(permission.ActionMessage, f, p)
		if d.Allowed || d.Fallback != "" || d.Reason != permission.ReasonUnavailable {
			t.Fatalf("strict defaults message decision = %+v, want deny unavailable", d)
		}
	}
	if !p.Unavailable {
		t.Fatal("strict defaults must be marked unavailable")
	}
}
