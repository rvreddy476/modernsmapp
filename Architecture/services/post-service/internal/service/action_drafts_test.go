package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/atpost/post-service/internal/actionlinks"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Both kinds of draft carry the Action button to publication: the label
// and URL rules when saved, eligibility and safety in CreatePost.

func TestReelDraftActionPatch(t *testing.T) {
	for _, tc := range []struct {
		raw        string
		label, url any
		wantKeys   bool
		wantErr    error
	}{
		{``, nil, nil, false, nil},
		{`null`, nil, nil, true, nil},
		{`{"label":"sign_up","url":"HTTPS://Example.com/Join"}`, "sign_up", "https://example.com/Join", true, nil},
		{`{"label":"nope","url":"https://example.com"}`, nil, nil, false, ErrInvalidActionLabel},
		{`{"label":"sign_up","url":"https://tinyurl.com/x"}`, nil, nil, false, actionlinks.ErrShortener},
		{`{"label":"sign_up","url":"https://t.me/+abc"}`, nil, nil, false, actionlinks.ErrGroupInvite},
		{`"sign_up"`, nil, nil, false, ErrInvalidActionShape},
	} {
		updates := map[string]interface{}{}
		err := draftActionUpdates(&UpdateDraftInput{Action: json.RawMessage(tc.raw)}, updates)
		if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
			t.Errorf("%s: err = %v, want %v", tc.raw, err, tc.wantErr)
			continue
		}
		_, hasLabel := updates["action_label"]
		_, hasURL := updates["action_url"]
		if hasLabel != tc.wantKeys || hasURL != tc.wantKeys {
			t.Errorf("%s: updates = %v", tc.raw, updates)
			continue
		}
		if tc.wantKeys && (updates["action_label"] != tc.label || updates["action_url"] != tc.url) {
			t.Errorf("%s: updates = %v, want %v / %v", tc.raw, updates, tc.label, tc.url)
		}
	}
}

// A saved reel-draft action reaches the post's create input; none stays none.
func TestReelDraftActionReachesCreate(t *testing.T) {
	d := typicalDraft()
	d.Action = &postgres.DraftAction{Label: "learn_more", URL: "https://example.com/more"}
	in := reelDraftPostInput(&d, uuid.New(), "long_video")
	if in.Action == nil || in.Action.Label != "learn_more" || in.Action.URL != "https://example.com/more" {
		t.Fatalf("create input action = %+v", in.Action)
	}
	d.Action = nil
	if in := reelDraftPostInput(&d, uuid.New(), "long_video"); in.Action != nil {
		t.Fatalf("no draft action, create got %+v", in.Action)
	}
}

// Composer drafts: the payload accepts "action" (and null), checks it when
// saved, and hands it to the create.
func TestPostDraftPayloadAction(t *testing.T) {
	p, err := parseDraftPayload(json.RawMessage(`{"text":"hiring","action":{"label":"apply_now","url":"https://example.com/jobs"}}`))
	if err != nil || p.Action == nil || p.Action.Label != "apply_now" {
		t.Fatalf("parse: %+v %v", p, err)
	}
	if err := validateDraft("post", p); err != nil {
		t.Fatalf("valid action refused: %v", err)
	}
	in := buildDraftCreateInput(&postgres.PostDraft{ID: uuid.New(), AuthorID: uuid.New(), PostType: "post"}, p)
	if in.Action == nil || in.Action.URL != "https://example.com/jobs" {
		t.Fatalf("create input action = %+v", in.Action)
	}
	if p, err := parseDraftPayload(json.RawMessage(`{"text":"hiring","action":null}`)); err != nil || p.Action != nil {
		t.Fatalf("null action: %+v %v", p, err)
	}
	bad, err := parseDraftPayload(json.RawMessage(`{"text":"hiring","action":{"label":"apply_now","url":"https://s.id/x"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDraft("post", bad); !errors.Is(err, ErrInvalidDraft) || !errors.Is(err, actionlinks.ErrShortener) {
		t.Fatalf("shortener in a draft: %v", err)
	}
}

// PATCH /v1/drafts/:id refuses a bad action before the store is touched
// (the Service has none: reaching it would panic).
func TestUpdateDraftRefusesABadActionBeforeWriting(t *testing.T) {
	s := &Service{}
	_, err := s.UpdateDraft(context.Background(), uuid.New(), uuid.New(), &UpdateDraftInput{
		Action: json.RawMessage(`{"label":"apply_now","url":"https://chat.whatsapp.com/abc"}`),
	})
	if !errors.Is(err, actionlinks.ErrGroupInvite) {
		t.Fatalf("err = %v, want ErrGroupInvite before any write", err)
	}
}
