package service

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/atpost/community-service/internal/mediaclaims"
	"github.com/atpost/community-service/internal/mediaclient"
	"github.com/google/uuid"
)

func TestCommunityMediaChanges(t *testing.T) {
	id, a, b, c := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	got := communityMediaChanges(id, &a, &b, &c, &b)
	if len(got) != 2 {
		t.Fatalf("changes %v", got)
	}
	avatar, banner := got[0], got[1]
	if avatar.Kind != mediaclient.KindCommunityAvatar || avatar.Ref != id ||
		!slices.Equal(avatar.Add, []uuid.UUID{c}) || !slices.Equal(avatar.Drop, []uuid.UUID{a}) {
		t.Fatalf("avatar change %+v", avatar)
	}
	if banner.Kind != mediaclient.KindCommunityBanner || banner.Ref != id || len(banner.Add)+len(banner.Drop) != 0 {
		t.Fatalf("an unchanged banner is a change: %+v", banner)
	}
	created := communityMediaChanges(id, nil, nil, &a, &b)
	if !slices.Equal(created[0].Add, []uuid.UUID{a}) || !slices.Equal(created[1].Add, []uuid.UUID{b}) {
		t.Fatalf("a created community's media is not claimed: %+v", created)
	}
}

// recordingClaims records releases.
type recordingClaims struct{ released []string }

func (r *recordingClaims) Claim(context.Context, string, uuid.UUID, []uuid.UUID) error { return nil }
func (r *recordingClaims) Release(_ context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) error {
	if len(ids) != 0 {
		panic("a deleted community must release everything it holds")
	}
	r.released = append(r.released, kind+"|"+ref.String())
	return nil
}

func TestDeletedCommunityReleasesBothColumns(t *testing.T) {
	rc := &recordingClaims{}
	s := (&Service{}).WithMedia(mediaclaims.New(nil, rc))
	id := uuid.New()
	s.releaseCommunityMedia(context.Background(), id)
	want := []string{mediaclient.KindCommunityAvatar + "|" + id.String(), mediaclient.KindCommunityBanner + "|" + id.String()}
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
