package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/qa-service/internal/mediaclaims"
	"github.com/atpost/qa-service/internal/mediaclient"
	"github.com/atpost/qa-service/internal/store"
	"github.com/google/uuid"
)

func TestParseQuestionMedia(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	got, err := parseQuestionMedia([]string{a.String(), b.String(), a.String()})
	if err != nil || !slices.Equal(got, []uuid.UUID{a, b}) {
		t.Fatalf("parsed %v, %v", got, err)
	}
	if _, err := parseQuestionMedia([]string{a.String(), "not-a-uuid"}); err == nil || !strings.HasPrefix(err.Error(), "invalid: ") {
		t.Fatalf("a malformed media id = %v; it must be refused, not skipped", err)
	}
	ch := questionMediaChanges(a, []uuid.UUID{b, b})
	if len(ch) != 1 || ch[0].Kind != mediaclient.KindQuestionMedia || ch[0].Ref != a || !slices.Equal(ch[0].Add, []uuid.UUID{b}) {
		t.Fatalf("changes %+v", ch)
	}
}

// The gate runs before the store is touched: a refused question never
// reaches CreateQuestion's write (this Service has no store, so a write would
// panic).
func TestCreateQuestionRefusesMediaBeforeTheWrite(t *testing.T) {
	author := uuid.New()
	mine, theirs := readyAsset(author), readyAsset(uuid.New())
	pending := readyAsset(author)
	pending.ProcessingStatus = "pending_upload"
	f := newFakeMedia(mine, theirs, pending)
	base := store.CreateQuestionParams{Title: "why", Topics: []string{"go"}}
	for name, tc := range map[string]struct {
		gate  *mediaclaims.Gate
		media []string
		want  error
	}{
		"someone else's": {mediaclaims.New(f, f), []string{mine.ID.String(), theirs.ID.String()}, mediaclaims.ErrMediaNotOwned},
		"unconfirmed":    {mediaclaims.New(f, f), []string{pending.ID.String()}, mediaclaims.ErrMediaNotReady},
		"missing":        {mediaclaims.New(f, f), []string{uuid.NewString()}, mediaclaims.ErrMediaNotFound},
		"no client":      {nil, []string{mine.ID.String()}, mediaclaims.ErrMediaUnavailable},
	} {
		p := base
		p.MediaIDs = tc.media
		s := (&Service{}).WithMedia(tc.gate)
		if _, err := s.CreateQuestion(context.Background(), author, p); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}
	if len(f.held) != 0 {
		t.Fatalf("a refused question left claims: %v", f.held)
	}
	p := base
	p.MediaIDs = []string{"nope"}
	if _, err := (&Service{}).CreateQuestion(context.Background(), author, p); err == nil || !strings.HasPrefix(err.Error(), "invalid: ") {
		t.Fatalf("a malformed id = %v", err)
	}
}

// recordingClaims records releases.
type recordingClaims struct{ released []string }

func (r *recordingClaims) Claim(context.Context, string, uuid.UUID, []uuid.UUID) error { return nil }
func (r *recordingClaims) Release(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	if len(ids) != 0 {
		panic("a deleted question must release everything it holds")
	}
	r.released = append(r.released, kind+"|"+ref.String())
	return nil
}

func TestDeletedQuestionReleasesItsMedia(t *testing.T) {
	rc := &recordingClaims{}
	s := (&Service{}).WithMedia(mediaclaims.New(nil, rc))
	id := uuid.New()
	s.releaseQuestionMedia(context.Background(), id)
	if want := []string{mediaclient.KindQuestionMedia + "|" + id.String()}; !slices.Equal(rc.released, want) {
		t.Fatalf("released %v, want %v", rc.released, want)
	}
}

// fakeMedia is media-service for the service tests: an asset table and the
// claims it keeps (Lookup, Claims and claimsync.Claims).
type fakeMedia struct {
	mu     sync.Mutex
	assets map[uuid.UUID]*mediaclient.Asset
	held   map[string]map[uuid.UUID]bool
}

func newFakeMedia(assets ...*mediaclient.Asset) *fakeMedia {
	f := &fakeMedia{assets: map[uuid.UUID]*mediaclient.Asset{}, held: map[string]map[uuid.UUID]bool{}}
	for _, a := range assets {
		f.assets[a.ID] = a
	}
	return f
}

func fakeKey(kind string, ref uuid.UUID) string { return kind + "|" + ref.String() }

func (f *fakeMedia) Get(_ context.Context, id uuid.UUID) (*mediaclient.Asset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.assets[id]; ok {
		return a, nil
	}
	return nil, mediaclient.ErrNotFound
}

func (f *fakeMedia) Claim(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range ids {
		if _, ok := f.assets[id]; !ok {
			return mediaclient.ErrNotFound
		}
	}
	k := fakeKey(kind, ref)
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
	if len(ids) == 0 {
		delete(f.held, fakeKey(kind, ref))
	}
	for _, id := range ids {
		delete(f.held[fakeKey(kind, ref)], id)
	}
	f.pruneLocked(kind, ref)
	return nil
}

func (f *fakeMedia) ReleaseExcept(_ context.Context, kind string, ref uuid.UUID, keep []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id := range f.held[fakeKey(kind, ref)] {
		if !slices.Contains(keep, id) {
			delete(f.held[fakeKey(kind, ref)], id)
		}
	}
	f.pruneLocked(kind, ref)
	return nil
}

// pruneLocked drops a holder that no longer holds anything, as media-service
// does: a ref with no claim rows has no claims, so f.held lists only holders
// with at least one claim and len(f.held) == 0 means nothing is claimed.
func (f *fakeMedia) pruneLocked(kind string, ref uuid.UUID) {
	if k := fakeKey(kind, ref); len(f.held[k]) == 0 {
		delete(f.held, k)
	}
}

func (f *fakeMedia) holds(kind string, ref, id uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.held[fakeKey(kind, ref)][id]
}

func readyAsset(owner uuid.UUID) *mediaclient.Asset {
	return &mediaclient.Asset{ID: uuid.New(), UploaderID: owner, ProcessingStatus: "ready", ModerationStatus: "passed"}
}
