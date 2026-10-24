package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/graph-service/internal/permission"
	"github.com/google/uuid"
)

func privacyServer(t *testing.T, data map[string]any) *Service {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	return &Service{userServiceURL: srv.URL, httpClient: srv.Client()}
}

// who_can_mention / who_can_tag are read from identity's settings answer.
func TestFetchPrivacyReadsMentionAndTag(t *testing.T) {
	s := privacyServer(t, map[string]any{"who_can_mention": "following", "who_can_tag": "no_one"})
	p, err := s.fetchPrivacyFromUserService(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if p.WhoCanMention != "following" || p.WhoCanTag != "no_one" {
		t.Fatalf("decoded mention=%q tag=%q", p.WhoCanMention, p.WhoCanTag)
	}
}

// A successful answer from an identity build that predates the settings
// carries no keys: the product default (everyone) applies.
func TestFetchPrivacyDefaultsMissingMentionAndTagToEveryone(t *testing.T) {
	s := privacyServer(t, map[string]any{"who_can_message": "no_one"})
	p, err := s.fetchPrivacyFromUserService(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if p.WhoCanMention != "everyone" || p.WhoCanTag != "everyone" {
		t.Fatalf("missing keys: mention=%q tag=%q, want everyone/everyone", p.WhoCanMention, p.WhoCanTag)
	}
}

// A FAILED fetch is not a pre-migration build: the strict snapshot leaves
// both unknown, and the matrix denies mention and tag on it.
func TestStrictDefaultsDenyMentionAndTag(t *testing.T) {
	p := strictPrivacyDefaults()
	for _, a := range []permission.Action{permission.ActionMention, permission.ActionTag} {
		if permission.Resolve(a, permission.Facts{TargetFollowsActor: true}, p).Allowed {
			t.Fatalf("%s allowed on the strict-default snapshot", a)
		}
	}
}

func TestCanBatchServesMentionAndTag(t *testing.T) {
	for _, a := range []permission.Action{permission.ActionViewPosts, permission.ActionComment, permission.ActionMention, permission.ActionTag} {
		if !IsCanBatchAction(a) {
			t.Errorf("%s must be served by the internal can endpoint", a)
		}
		var s *Service
		if _, err := s.CanBatch(context.Background(), uuid.New(), a, nil); err != nil {
			t.Errorf("CanBatch(%s, no targets): %v", a, err)
		}
	}
	for _, a := range []permission.Action{permission.ActionMessage, permission.ActionCall, permission.ActionFollow} {
		if IsCanBatchAction(a) {
			t.Errorf("%s must not be served by the internal can endpoint", a)
		}
	}
}
