package service

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/atpost/group-service/internal/mediaclaims"
	"github.com/atpost/group-service/internal/mediaclient"
	"github.com/google/uuid"
)

func TestGroupMediaChanges(t *testing.T) {
	id, a, b, c := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	got := groupMediaChanges(id, &a, &b, keepUnlessSet(&a, &c), keepUnlessSet(&b, nil))
	if len(got) != 2 {
		t.Fatalf("changes %v", got)
	}
	avatar, cover := got[0], got[1]
	if avatar.Kind != mediaclient.KindGroupAvatar || avatar.Ref != id ||
		!slices.Equal(avatar.Add, []uuid.UUID{c}) || !slices.Equal(avatar.Drop, []uuid.UUID{a}) {
		t.Fatalf("avatar change %+v", avatar)
	}
	if cover.Kind != mediaclient.KindGroupCover || cover.Ref != id || len(cover.Add)+len(cover.Drop) != 0 {
		t.Fatalf("a cover the save did not set is a change: %+v", cover)
	}
	swap := groupMediaChanges(id, &a, &b, &a, &c)
	if !slices.Equal(swap[1].Add, []uuid.UUID{c}) || !slices.Equal(swap[1].Drop, []uuid.UUID{b}) || len(swap[0].Add)+len(swap[0].Drop) != 0 {
		t.Fatalf("a replaced cover: %+v", swap)
	}
	first := groupMediaChanges(id, nil, nil, keepUnlessSet(nil, &a), keepUnlessSet(nil, nil))
	if !slices.Equal(first[0].Add, []uuid.UUID{a}) || len(first[1].Add) != 0 {
		t.Fatalf("a first avatar is not claimed: %+v", first)
	}
}

// recordingClaims records releases.
type recordingClaims struct{ released []string }

func (r *recordingClaims) Claim(context.Context, string, uuid.UUID, []uuid.UUID) error { return nil }
func (r *recordingClaims) Release(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	if len(ids) != 0 {
		panic("a deleted group must release everything it holds")
	}
	r.released = append(r.released, kind+"|"+ref.String())
	return nil
}

func TestDeletedGroupReleasesAvatarAndCover(t *testing.T) {
	rc := &recordingClaims{}
	s := (&Service{}).WithMedia(mediaclaims.New(nil, rc))
	id := uuid.New()
	s.releaseGroupMedia(context.Background(), id)
	want := []string{mediaclient.KindGroupAvatar + "|" + id.String(), mediaclient.KindGroupCover + "|" + id.String()}
	if !slices.Equal(rc.released, want) {
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
	return nil
}

func (f *fakeMedia) holds(kind string, ref, id uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.held[fakeKey(kind, ref)][id]
}

func readyAsset(owner uuid.UUID) *mediaclient.Asset {
	return &mediaclient.Asset{ID: uuid.New(), UploaderID: owner, ProcessingStatus: "ready", ModerationStatus: "passed"}
}
