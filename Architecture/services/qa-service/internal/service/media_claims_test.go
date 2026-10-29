package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/qa-service/internal/claimsync"
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
	id := uuid.New()
	hs := newFakeHolders()
	hs.dead(mediaclient.KindQuestionMedia, id)
	s := (&Service{}).WithMedia(mediaclaims.New(nil, rc)).WithMediaHolders(hs)
	s.ReleaseRemovedMedia(context.Background(), []uuid.UUID{id}, nil)
	if want := []string{mediaclient.KindQuestionMedia + "|" + id.String()}; !slices.Equal(rc.released, want) {
		t.Fatalf("released %v, want %v", rc.released, want)
	}
}

// fakeHolders is the store side of ReleaseRemovedMedia: which removed rows
// hold media, whether each is live, and the lock (which must be held around
// the re-read and the release).
type fakeHolders struct {
	mu       sync.Mutex
	rows     map[string]claimsync.Row
	order    []store.MediaHolder
	locked   map[uuid.UUID]bool
	amongErr error
	asked    [][2]int // len(questionIDs), len(answerIDs) per MediaHoldersAmong call
	unlocked bool     // a re-read ran without the row's lock
}

func newFakeHolders() *fakeHolders {
	return &fakeHolders{rows: map[string]claimsync.Row{}, locked: map[uuid.UUID]bool{}}
}

func (f *fakeHolders) add(kind string, ref uuid.UUID, live bool) {
	f.rows[fakeKey(kind, ref)] = claimsync.Row{Kind: kind, Ref: ref, MediaIDs: []uuid.UUID{uuid.New()}, Live: live}
	f.order = append(f.order, store.MediaHolder{Kind: kind, Ref: ref})
}
func (f *fakeHolders) dead(kind string, ref uuid.UUID) { f.add(kind, ref, false) }

func (f *fakeHolders) MediaHoldersAmong(_ context.Context, q, a []uuid.UUID) ([]store.MediaHolder, error) {
	f.asked = append(f.asked, [2]int{len(q), len(a)})
	if f.amongErr != nil {
		return nil, f.amongErr
	}
	return f.order, nil
}

func (f *fakeHolders) LockMediaHolder(_ context.Context, ref uuid.UUID) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.locked[ref] = true
	return func() { f.mu.Lock(); delete(f.locked, ref); f.mu.Unlock() }, nil
}

func (f *fakeHolders) MediaClaimRowFor(_ context.Context, kind string, ref uuid.UUID) (claimsync.Row, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.locked[ref] {
		f.unlocked = true
	}
	return f.rows[fakeKey(kind, ref)], nil
}

// claimAll gives fakeMedia a claim on every media id h's row names.
func (f *fakeHolders) claimAll(t *testing.T, m *fakeMedia) {
	t.Helper()
	for _, h := range f.order {
		r := f.rows[fakeKey(h.Kind, h.Ref)]
		for _, id := range r.MediaIDs {
			m.assets[id] = &mediaclient.Asset{ID: id}
		}
		if err := m.Claim(context.Background(), h.Kind, h.Ref, r.MediaIDs); err != nil {
			t.Fatal(err)
		}
	}
}

// A hide, an erasure and an author's delete release what every dead row
// held, questions and answers alike, and never what a still-live row holds.
func TestReleaseRemovedMediaReleasesDeadRowsOnly(t *testing.T) {
	q, a, under, live := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	hs := newFakeHolders()
	hs.dead(mediaclient.KindQuestionMedia, q)
	hs.dead(mediaclient.KindAnswerMedia, a)
	hs.dead(mediaclient.KindAnswerMedia, under) // an answer under q
	hs.add(mediaclient.KindQuestionMedia, live, true)
	m := newFakeMedia()
	hs.claimAll(t, m)
	s := (&Service{}).WithMedia(mediaclaims.New(m, m)).WithMediaHolders(hs)

	s.ReleaseRemovedMedia(context.Background(), []uuid.UUID{q, live}, []uuid.UUID{a})

	for _, ref := range []uuid.UUID{q, a, under} {
		for k := range m.held {
			if strings.HasSuffix(k, ref.String()) {
				t.Errorf("dead row %s still holds media", k)
			}
		}
	}
	if len(m.held[fakeKey(mediaclient.KindQuestionMedia, live)]) == 0 {
		t.Fatal("a row that is still live lost its claim: the owner could delete media it shows")
	}
	if hs.unlocked {
		t.Fatal("a row was re-read and released without the writers' lock")
	}
}

// A removal is never failed or held up by media-service: a failed release
// stops the pass (one timeout, not one per row) and leaves the rest to the
// reconcile tool.
func TestReleaseRemovedMediaStopsAtTheFirstFailure(t *testing.T) {
	hs := newFakeHolders()
	for i := 0; i < 3; i++ {
		hs.dead(mediaclient.KindQuestionMedia, uuid.New())
	}
	fc := &failingClaims{}
	s := (&Service{}).WithMedia(mediaclaims.New(nil, fc)).WithMediaHolders(hs)
	s.ReleaseRemovedMedia(context.Background(), []uuid.UUID{uuid.New()}, nil)
	if fc.calls != 1 {
		t.Fatalf("media-service was asked %d times after it failed; want 1", fc.calls)
	}

	// No client at all: nothing panics, nothing is released.
	(&Service{}).WithMediaHolders(hs).ReleaseRemovedMedia(context.Background(), []uuid.UUID{uuid.New()}, nil)

	// The store cannot say which rows hold media: nothing is sent.
	hs.amongErr = errors.New("db down")
	fc.calls = 0
	s.ReleaseRemovedMedia(context.Background(), []uuid.UUID{uuid.New()}, nil)
	if fc.calls != 0 {
		t.Fatalf("released %d holders without knowing which", fc.calls)
	}
}

// Nothing removed: the store is not asked.
func TestReleaseRemovedMediaNothingRemoved(t *testing.T) {
	hs := newFakeHolders()
	(&Service{}).WithMediaHolders(hs).ReleaseRemovedMedia(context.Background(), nil, nil)
	if len(hs.asked) != 0 {
		t.Fatalf("asked the store %v for an empty removal", hs.asked)
	}
}

// A moderation hide of a question or an answer releases it; a comment holds
// no media.
func TestReleaseHiddenMediaByTarget(t *testing.T) {
	for target, want := range map[string][2]int{"question": {1, 0}, "answer": {0, 1}, "comment": {}} {
		hs := newFakeHolders()
		(&Service{}).WithMediaHolders(hs).ReleaseHiddenMedia(context.Background(), target, uuid.New())
		var got [2]int
		if len(hs.asked) == 1 {
			got = hs.asked[0]
		}
		if got != want || len(hs.asked) > 1 {
			t.Errorf("%s: asked %v, want %v", target, hs.asked, want)
		}
	}
}

type failingClaims struct{ calls int }

func (f *failingClaims) Claim(context.Context, string, uuid.UUID, []uuid.UUID) error { return nil }
func (f *failingClaims) Release(context.Context, string, uuid.UUID, []uuid.UUID) error {
	f.calls++
	return errors.New("media-service down")
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
