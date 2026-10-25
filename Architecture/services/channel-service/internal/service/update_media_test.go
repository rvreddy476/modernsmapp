package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/channel-service/internal/mediaclient"
	"github.com/google/uuid"
)

// fakeMedia answers Get from a map; an id not in it is ErrNotFound. It also
// takes claims the way media-service does (media_claims.go): all or nothing,
// refused when an id is not in the map, and remembers what is held.
type fakeMedia struct {
	assets map[uuid.UUID]*mediaclient.Asset
	err    error
	calls  []uuid.UUID
	// held is the claim table: kind/ref -> media ids.
	held map[string]map[uuid.UUID]bool
	mu   sync.Mutex // concurrent-edit tests call in from several goroutines
}

func claimKey(kind string, ref uuid.UUID) string { return kind + "/" + ref.String() }

func (f *fakeMedia) Claim(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	for _, id := range ids {
		if _, ok := f.assets[id]; !ok {
			return mediaclient.ErrNotFound
		}
	}
	if f.held == nil {
		f.held = map[string]map[uuid.UUID]bool{}
	}
	k := claimKey(kind, ref)
	if f.held[k] == nil {
		f.held[k] = map[uuid.UUID]bool{}
	}
	for _, id := range ids {
		f.held[k][id] = true
	}
	return nil
}

func (f *fakeMedia) Release(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	k := claimKey(kind, ref)
	if len(ids) == 0 {
		delete(f.held, k)
		return nil
	}
	for _, id := range ids {
		delete(f.held[k], id)
	}
	return nil
}

// holds reports whether kind/ref holds a claim on id.
func (f *fakeMedia) holds(kind string, ref, id uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.held[claimKey(kind, ref)][id]
}

func (f *fakeMedia) Get(_ context.Context, id uuid.UUID) (*mediaclient.Asset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, id)
	if f.err != nil {
		return nil, f.err
	}
	a, ok := f.assets[id]
	if !ok {
		return nil, mediaclient.ErrNotFound
	}
	return a, nil
}

func asset(owner uuid.UUID, processing, moderation string) *mediaclient.Asset {
	return &mediaclient.Asset{ID: uuid.New(), UploaderID: owner, FileType: "image", ProcessingStatus: processing, ModerationStatus: moderation}
}

func svcWith(assets ...*mediaclient.Asset) (*Service, *fakeMedia) {
	f := &fakeMedia{assets: map[uuid.UUID]*mediaclient.Asset{}}
	for _, a := range assets {
		f.assets[a.ID] = a
	}
	return (&Service{}).WithMedia(f), f
}

func TestUpdateMediaAcceptsTheActorsConfirmedUploads(t *testing.T) {
	actor := uuid.New()
	for _, status := range []string{"uploaded", "processing", "ready"} {
		for _, mod := range []string{"pending", "manual_review", "passed"} {
			a := asset(actor, status, mod)
			s, _ := svcWith(a)
			if err := s.verifyUpdateMedia(context.Background(), actor, []uuid.UUID{a.ID}, nil); err != nil {
				t.Errorf("%s/%s refused: %v", status, mod, err)
			}
		}
	}
}

func TestUpdateMediaRefusesAnotherUsersMedia(t *testing.T) {
	actor := uuid.New()
	a := asset(uuid.New(), "ready", "passed")
	s, _ := svcWith(a)
	err := s.verifyUpdateMedia(context.Background(), actor, []uuid.UUID{a.ID}, nil)
	if !errors.Is(err, ErrMediaNotOwned) {
		t.Fatalf("another user's ready media = %v, want ErrMediaNotOwned", err)
	}
	if strings.Contains(err.Error(), a.UploaderID.String()) {
		t.Fatal("the refusal names the real uploader")
	}
}

// pending_upload is the reclaim case: media-service's orphan sweep deletes it
// and nothing on this side would stop the update pointing at nothing.
func TestUpdateMediaRefusesUnfinishedUploads(t *testing.T) {
	actor := uuid.New()
	for _, status := range []string{"pending_upload", "failed", "rejected", "deleted", "", "something_new"} {
		a := asset(actor, status, "passed")
		s, _ := svcWith(a)
		if err := s.verifyUpdateMedia(context.Background(), actor, []uuid.UUID{a.ID}, nil); !errors.Is(err, ErrMediaNotReady) {
			t.Errorf("processing_status %q = %v, want ErrMediaNotReady", status, err)
		}
	}
}

func TestUpdateMediaRefusesModerationRejected(t *testing.T) {
	actor := uuid.New()
	for _, mod := range []string{"rejected", ""} {
		a := asset(actor, "ready", mod)
		s, _ := svcWith(a)
		if err := s.verifyUpdateMedia(context.Background(), actor, []uuid.UUID{a.ID}, nil); !errors.Is(err, ErrMediaNotReady) {
			t.Errorf("moderation_status %q = %v, want ErrMediaNotReady", mod, err)
		}
	}
}

func TestUpdateMediaRefusesMissingMedia(t *testing.T) {
	actor := uuid.New()
	s, _ := svcWith()
	if err := s.verifyUpdateMedia(context.Background(), actor, []uuid.UUID{uuid.New()}, nil); !errors.Is(err, ErrMediaNotFound) {
		t.Fatalf("unknown id = %v, want ErrMediaNotFound", err)
	}
	if err := s.verifyUpdateMedia(context.Background(), actor, []uuid.UUID{uuid.Nil}, nil); !errors.Is(err, ErrMediaNotFound) {
		t.Fatalf("nil id = %v, want ErrMediaNotFound", err)
	}
}

// One bad id anywhere in the list refuses the whole update.
func TestUpdateMediaRefusesABadIdAfterGoodOnes(t *testing.T) {
	actor := uuid.New()
	good, foreign := asset(actor, "ready", "passed"), asset(uuid.New(), "ready", "passed")
	s, _ := svcWith(good, foreign)
	if err := s.verifyUpdateMedia(context.Background(), actor, []uuid.UUID{good.ID, foreign.ID}, nil); !errors.Is(err, ErrMediaNotOwned) {
		t.Fatalf("good+foreign = %v, want ErrMediaNotOwned", err)
	}
}

func TestUpdateMediaFailsClosed(t *testing.T) {
	actor := uuid.New()
	id := uuid.New()

	noClient := &Service{}
	if err := noClient.verifyUpdateMedia(context.Background(), actor, []uuid.UUID{id}, nil); !errors.Is(err, ErrMediaUnavailable) {
		t.Fatalf("no client = %v, want ErrMediaUnavailable", err)
	}

	s, f := svcWith()
	f.err = mediaclient.ErrUnavailable
	if err := s.verifyUpdateMedia(context.Background(), actor, []uuid.UUID{id}, nil); !errors.Is(err, ErrMediaUnavailable) {
		t.Fatalf("media-service down = %v, want ErrMediaUnavailable", err)
	}
}

func TestUpdateMediaWithoutMediaNeverCallsOut(t *testing.T) {
	noClient := &Service{}
	if err := noClient.verifyUpdateMedia(context.Background(), uuid.New(), nil, nil); err != nil {
		t.Fatalf("no media, no client = %v; a text update must not depend on media-service", err)
	}
}

// On an edit, ids already on the update (perhaps another admin's upload) are
// kept without a lookup; every id the edit ADDS is checked.
func TestEditMediaChecksOnlyWhatItAdds(t *testing.T) {
	actor := uuid.New()
	kept := asset(uuid.New(), "ready", "passed") // the original author's
	s, f := svcWith(kept)
	if err := s.verifyUpdateMedia(context.Background(), actor, []uuid.UUID{kept.ID}, []uuid.UUID{kept.ID}); err != nil {
		t.Fatalf("re-sending the update's own media = %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("already-attached media was looked up: %v", f.calls)
	}

	foreign := asset(uuid.New(), "ready", "passed")
	f.assets[foreign.ID] = foreign
	if err := s.verifyUpdateMedia(context.Background(), actor, []uuid.UUID{kept.ID, foreign.ID}, []uuid.UUID{kept.ID}); !errors.Is(err, ErrMediaNotOwned) {
		t.Fatalf("an edit adding another user's media = %v, want ErrMediaNotOwned", err)
	}
	pending := asset(actor, "pending_upload", "pending")
	f.assets[pending.ID] = pending
	if err := s.verifyUpdateMedia(context.Background(), actor, []uuid.UUID{pending.ID}, []uuid.UUID{kept.ID}); !errors.Is(err, ErrMediaNotReady) {
		t.Fatalf("an edit adding a pending upload = %v, want ErrMediaNotReady", err)
	}
}

func TestUpdateMediaIsBounded(t *testing.T) {
	actor := uuid.New()
	var assets []*mediaclient.Asset
	var ids []uuid.UUID
	for i := 0; i < maxUpdateMedia+1; i++ {
		a := asset(actor, "ready", "passed")
		assets = append(assets, a)
		ids = append(ids, a.ID)
	}
	s, f := svcWith(assets...)
	err := s.verifyUpdateMedia(context.Background(), actor, ids, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("%d media = %v, want an invalid refusal", len(ids), err)
	}
	if len(f.calls) != 0 {
		t.Fatal("the bound must be checked before any lookup")
	}
	if err := s.verifyUpdateMedia(context.Background(), actor, ids[:maxUpdateMedia], nil); err != nil {
		t.Fatalf("%d media = %v", maxUpdateMedia, err)
	}
}

// CreateUpdate and EditUpdate need a database, so the wiring is pinned on the
// source (update_media_integration_test.go proves the behaviour in CI): the
// gate runs, with the right actor, before the row is written.
func TestUpdateWritersRunTheMediaGateBeforeWriting(t *testing.T) {
	raw, err := os.ReadFile("channel.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, tc := range []struct{ sig, gate, write string }{
		{"func (s *Service) CreateUpdate(", "if err := s.verifyUpdateMedia(ctx, authorID, params.MediaIDs, nil); err != nil {\n\t\treturn nil, err\n\t}", "s.store.CreateUpdate("},
		{"func (s *Service) EditUpdate(", "if params.MediaIDs != nil {\n\t\tif err := s.verifyUpdateMedia(ctx, actorID, params.MediaIDs, u.MediaIDs); err != nil {\n\t\t\treturn nil, err\n\t\t}", "s.store.UpdateUpdate("},
	} {
		body, ok := funcBody(src, tc.sig)
		if !ok {
			t.Fatalf("%s not found", tc.sig)
		}
		g, w := strings.Index(body, tc.gate), strings.Index(body, tc.write)
		if g < 0 {
			t.Errorf("%s does not call %s", tc.sig, tc.gate)
			continue
		}
		if w < 0 || g > w {
			t.Errorf("%s writes the update before the media gate", tc.sig)
		}
	}
}
