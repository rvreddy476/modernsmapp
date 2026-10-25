package service

import (
	"context"
	"slices"
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
