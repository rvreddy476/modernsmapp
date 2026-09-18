package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// A private playlist leaked through the creator listing (2026-09-18).
//
// GET /v1/creators/:creatorId/playlists returned every row the creator
// owned, private ones included: title, description, cover and item_count
// went to anyone who knew a user id. Only the CONTENTS were protected —
// GET /v1/playlists/:playlistId and .../items refused a private playlist to
// a stranger, so the two reads openly disagreed about the same row.
//
// These tests pin the one rule both now go through: a private playlist is
// its creator's alone, in the listing and by id; public and unlisted are
// reachable by anyone, signed in or not. The store fake applies the same
// include-private filter the SQL does, so a regression in the decision
// (rather than in the SQL) is what fails here.

func playlistFixture(creator uuid.UUID, visibility string) postgres.Playlist {
	return postgres.Playlist{
		ID:         uuid.New(),
		CreatorID:  creator,
		Title:      visibility + " playlist",
		Visibility: visibility,
		ItemCount:  3,
	}
}

func TestListPlaylistsByCreatorShowsStrangersThePublicShelfOnly(t *testing.T) {
	owner := uuid.New()
	stranger := uuid.New()
	public := playlistFixture(owner, "public")
	unlisted := playlistFixture(owner, "unlisted")
	private := playlistFixture(owner, "private")

	store := &fakeAuthoringStore{listed: []postgres.Playlist{public, unlisted, private}}
	svc := newAuthoringService(store)

	cases := []struct {
		name   string
		caller *uuid.UUID
		want   []uuid.UUID
	}{
		{"owner sees the whole shelf", &owner, []uuid.UUID{public.ID, unlisted.ID, private.ID}},
		{"stranger sees only the public one", &stranger, []uuid.UUID{public.ID}},
		{"anonymous sees only the public one", nil, []uuid.UUID{public.ID}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := svc.ListPlaylistsByCreator(context.Background(), owner, tc.caller, 20, 0)
			if err != nil {
				t.Fatalf("ListPlaylistsByCreator: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d playlists, want %d (%+v)", len(got), len(tc.want), got)
			}
			for i, id := range tc.want {
				if got[i].ID != id {
					t.Errorf("playlist[%d] = %s, want %s", i, got[i].ID, id)
				}
			}
		})
	}
}

// The filter must be pushed to the store, not applied to the page after it
// comes back: a post-hoc filter hands back short pages and makes
// limit/offset lie about what is left.
func TestListPlaylistsByCreatorAsksTheStoreToFilter(t *testing.T) {
	owner := uuid.New()
	stranger := uuid.New()
	store := &fakeAuthoringStore{listed: []postgres.Playlist{playlistFixture(owner, "public")}}
	svc := newAuthoringService(store)

	if _, err := svc.ListPlaylistsByCreator(context.Background(), owner, &stranger, 20, 0); err != nil {
		t.Fatalf("ListPlaylistsByCreator: %v", err)
	}
	if store.lastList == nil || *store.lastList {
		t.Fatalf("a stranger's listing asked the store for the owner's shelf (ownerView=%v)", store.lastList)
	}
	if _, err := svc.ListPlaylistsByCreator(context.Background(), owner, &owner, 20, 0); err != nil {
		t.Fatalf("ListPlaylistsByCreator: %v", err)
	}
	if store.lastList == nil || !*store.lastList {
		t.Fatalf("the creator's own listing did not ask the store for their whole shelf (ownerView=%v)", store.lastList)
	}
}

// Fail closed, the way GetPlaylist already does: with no store there is no
// way to know which rows are private, so nothing is served.
func TestListPlaylistsByCreatorFailsClosedWithoutAStore(t *testing.T) {
	owner := uuid.New()
	svc := &Service{}
	if _, err := svc.ListPlaylistsByCreator(context.Background(), owner, &owner, 20, 0); err == nil {
		t.Fatal("a listing with no ownership store behind it must be refused, not answered")
	}
}

// The listing and the fetch by id differ on exactly one value, on purpose.
//
// 'unlisted' means reachable with the link but not discoverable in a list —
// the same thing it means for a video — so an unlisted playlist answers a
// direct fetch by id to anyone while staying out of the creator listing a
// stranger loads on the channel page. Leaving it in that listing would make
// the setting do nothing there. 'public' is everyone's and 'private' is its
// creator's in BOTH reads, and a fetch by id is never a way around the
// listing for a private playlist — which is the leak this all started from.
//
// The whole grid is asserted here so neither door can drift on its own.
func TestGetPlaylistAndListingDifferOnlyOnUnlisted(t *testing.T) {
	owner := uuid.New()
	stranger := uuid.New()

	want := map[string]map[string][2]bool{ // visibility → caller → {listed, byID}
		"public":   {"owner": {true, true}, "stranger": {true, true}, "anonymous": {true, true}},
		"unlisted": {"owner": {true, true}, "stranger": {false, true}, "anonymous": {false, true}},
		"private":  {"owner": {true, true}, "stranger": {false, false}, "anonymous": {false, false}},
	}

	for _, visibility := range []string{"public", "unlisted", "private"} {
		t.Run(visibility, func(t *testing.T) {
			p := playlistFixture(owner, visibility)
			for _, caller := range []struct {
				name string
				id   *uuid.UUID
			}{{"owner", &owner}, {"stranger", &stranger}, {"anonymous", nil}} {
				store := &fakeAuthoringStore{playlist: &p, listed: []postgres.Playlist{p}}
				svc := newAuthoringService(store)

				_, byIDErr := svc.GetPlaylist(context.Background(), p.ID, caller.id)
				listed, err := svc.ListPlaylistsByCreator(context.Background(), owner, caller.id, 20, 0)
				if err != nil {
					t.Fatalf("%s listing: %v", caller.name, err)
				}
				got := [2]bool{len(listed) == 1, byIDErr == nil}
				if exp := want[visibility][caller.name]; got != exp {
					t.Errorf("%s / %s playlist: {listed, byID} = %v, want %v (by-id err %v)",
						caller.name, visibility, got, exp, byIDErr)
				}
			}
		})
	}
}

// The predicates, stated once more on their own: the only cell where the two
// rules disagree is a non-owner looking at an unlisted playlist.
func TestPlaylistListingAndFetchPredicatesDifferOnlyOnUnlisted(t *testing.T) {
	owner := uuid.New()
	stranger := uuid.New()
	for _, visibility := range []string{"public", "unlisted", "private"} {
		p := playlistFixture(owner, visibility)
		readable := playlistReadableBy(&p, &stranger)
		listable := playlistListableBy(&p, &stranger)
		if (readable != listable) != (visibility == "unlisted") {
			t.Errorf("%s playlist, non-owner: readable=%v listable=%v — they may differ only for unlisted",
				visibility, readable, listable)
		}
		if !playlistReadableBy(&p, &owner) || !playlistListableBy(&p, &owner) {
			t.Errorf("%s playlist: its own creator must both read and list it", visibility)
		}
	}
}

// playlists.visibility is NOT posts.visibility: its CHECK admits
// public|unlisted|private. A typo must be a named refusal rather than a
// constraint violation surfacing as a 500.
func TestPlaylistVisibilityValuesMatchTheColumnCheck(t *testing.T) {
	for _, ok := range []string{"public", "unlisted", "private"} {
		if !validPlaylistVisibility(ok) {
			t.Errorf("%q is in the column's CHECK but rejected here", ok)
		}
	}
	for _, bad := range []string{"followers", "circle", "hidden", "PUBLIC", ""} {
		if validPlaylistVisibility(bad) {
			t.Errorf("%q is not in the column's CHECK but accepted here", bad)
		}
	}
}

func TestUpdatePlaylistRefusesBadVisibilityAndEmptyTitle(t *testing.T) {
	owner := uuid.New()
	p := playlistFixture(owner, "public")
	svc := newAuthoringService(&fakeAuthoringStore{playlist: &p})

	bad := "followers"
	if _, err := svc.UpdatePlaylist(context.Background(), owner, p.ID, postgres.PlaylistPatch{Visibility: &bad}); err != ErrInvalidPlaylistVisibility {
		t.Errorf("visibility %q: err=%v want ErrInvalidPlaylistVisibility", bad, err)
	}
	blank := "   "
	if _, err := svc.UpdatePlaylist(context.Background(), owner, p.ID, postgres.PlaylistPatch{Title: &blank}); err != ErrPlaylistTitleRequired {
		t.Errorf("blank title: err=%v want ErrPlaylistTitleRequired", err)
	}
}

// The edit and the reorder are owner-scoped, like every other playlist
// write. pgStore is nil on this Service, so a refusal that stopped refusing
// would panic rather than quietly pass.
func TestPlaylistEditsRefuseNonOwner(t *testing.T) {
	owner := uuid.New()
	attacker := uuid.New()
	p := playlistFixture(owner, "public")
	svc := newAuthoringService(&fakeAuthoringStore{playlist: &p})

	title := "pwned"
	if _, err := svc.UpdatePlaylist(context.Background(), attacker, p.ID, postgres.PlaylistPatch{Title: &title}); err != ErrNotPlaylistOwner {
		t.Errorf("UpdatePlaylist by a non-owner: err=%v want ErrNotPlaylistOwner", err)
	}
	if _, err := svc.MovePlaylistItem(context.Background(), attacker, p.ID, uuid.New(), 0); err != ErrNotPlaylistOwner {
		t.Errorf("MovePlaylistItem by a non-owner: err=%v want ErrNotPlaylistOwner", err)
	}
	if _, err := svc.MovePlaylistItem(context.Background(), owner, p.ID, uuid.New(), -1); err != ErrPlaylistPositionInvalid {
		t.Errorf("MovePlaylistItem to a negative position: err=%v want ErrPlaylistPositionInvalid", err)
	}
}

// GET /v1/playlists/:playlistId/items returned bare pointers, so drawing one
// row cost a second call to POST /v1/posts/batch. The rows are hydrated now,
// and the change is ADDITIVE: the four pointer fields stay at the top level,
// exactly as they were, with `post` beside them.
func TestPlaylistItemDetailKeepsThePointerFields(t *testing.T) {
	item := postgres.PlaylistItem{
		PlaylistID: uuid.New(),
		PostID:     uuid.New(),
		Position:   2,
		AddedAt:    time.Unix(1_700_000_000, 0).UTC(),
	}
	raw, err := json.Marshal(PlaylistItemDetail{PlaylistItem: item})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, field := range []string{"playlist_id", "post_id", "position", "added_at"} {
		if _, ok := got[field]; !ok {
			t.Errorf("%q is missing; the pointer form must stay readable (%s)", field, raw)
		}
	}
	if _, ok := got["post"]; !ok {
		t.Errorf("post is missing; the whole point is that a client no longer has to call /v1/posts/batch (%s)", raw)
	}
	if string(got["post"]) != "null" {
		t.Errorf("post for an unreadable row = %s, want null", got["post"])
	}
}
