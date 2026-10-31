package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The 31 Oct 2026 founder decisions on the routes: a members-only video's
// end screens, cards and product tags are its members' and its author's;
// everyone else gets an empty list (200), never an error.

func TestMembersOnlyEndScreensAndCardsAreTheMembers(t *testing.T) {
	r := newESRig(t)
	r.seedScreens()
	u := "https://example.com"
	r.store.cards[r.post] = []postgres.VideoCard{
		{ID: uuid.New(), Type: "video", TargetID: &r.ownVideo, Title: "Next", AppearAtMs: 1000},
		{ID: uuid.New(), Type: "external_link", TargetURL: &u, Title: "Docs", AppearAtMs: 4000},
	}
	tier := uuid.New()
	r.tier = &tier
	member := uuid.New()
	r.members[member] = true

	for _, path := range []string{r.esPath(), r.cardPath()} {
		for name, caller := range map[string]*uuid.UUID{"non-member": &r.viewer, "signed out": nil} {
			w := r.do(t, http.MethodGet, path, caller, "")
			if w.Code != http.StatusOK {
				t.Fatalf("%s %s: %d %s; want 200 with an empty list", name, path, w.Code, w.Body)
			}
			if list := decodeList(t, w); len(list) != 0 {
				t.Fatalf("%s %s: %d elements of a members-only video", name, path, len(list))
			}
		}
		for name, caller := range map[string]*uuid.UUID{"member": &member, "author": &r.owner} {
			w := r.do(t, http.MethodGet, path, caller, "")
			if list := decodeList(t, w); w.Code != http.StatusOK || len(list) == 0 {
				t.Fatalf("%s %s: %d, %d elements; want the video's", name, path, w.Code, len(list))
			}
		}
	}
	// An open video's end screen is everyone's.
	r.tier = nil
	if list := decodeList(t, r.do(t, http.MethodGet, r.esPath(), &r.viewer, "")); len(list) == 0 {
		t.Fatal("an open video's end screen was withheld")
	}
}

func TestMembersOnlyProductTagsAreTheMembers(t *testing.T) {
	author, member, viewer, tier := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	post := uuid.New()
	var gate *uuid.UUID = &tier
	var gateErr error
	tags := []*postgres.PostProductTag{{ID: uuid.New(), PostID: post}}
	g := newGateRig(t, func(uuid.UUID, *uuid.UUID) error { return nil }, func(d *service.HandlerTestDeps) {
		d.PostGate = func(context.Context, uuid.UUID) (uuid.UUID, *uuid.UUID, error) { return author, gate, gateErr }
		d.OfflineEntitlement = func(_ context.Context, v uuid.UUID, _ *postgres.Post) (bool, error) { return v == member, nil }
		d.ProductTags = func(context.Context, uuid.UUID) ([]*postgres.PostProductTag, error) { return tags, nil }
	})
	path := "/v1/posts/" + post.String() + "/product-tags"
	count := func(caller *uuid.UUID) (int, int) {
		w := g.do(http.MethodGet, path, caller, "")
		var env struct {
			Data []json.RawMessage `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		if w.Code == http.StatusOK && env.Data == nil {
			t.Fatalf("%s: data is null; want a list", w.Body)
		}
		return w.Code, len(env.Data)
	}
	for name, caller := range map[string]*uuid.UUID{"non-member": &viewer, "signed out": nil} {
		if code, n := count(caller); code != http.StatusOK || n != 0 {
			t.Fatalf("%s: %d with %d tags; want 200 and none", name, code, n)
		}
	}
	for name, caller := range map[string]*uuid.UUID{"member": &member, "author": &author} {
		if code, n := count(caller); code != http.StatusOK || n != 1 {
			t.Fatalf("%s: %d with %d tags; want the video's", name, code, n)
		}
	}
	gate = nil
	if code, n := count(nil); code != http.StatusOK || n != 1 {
		t.Fatalf("an open video: %d with %d tags", code, n)
	}
	gateErr = errors.New("db down")
	if code, n := count(&member); code == http.StatusOK || n != 0 {
		t.Fatalf("a failed gate lookup: %d with %d tags; want an error and none", code, n)
	}
}

// A playlist shown to anyone but its owner does not list an unlisted video,
// except to that video's author; the owner's own playlist still does.
func TestPlaylistItemsRouteListsUnlistedOnlyToItsAuthor(t *testing.T) {
	r := newTubeRig(t)
	items := r.auth.items[r.playlist.ID]
	unlistedPost, videoAuthor := items[1].PostID, uuid.New()
	r.auth.unlisted = map[uuid.UUID]uuid.UUID{unlistedPost: videoAuthor}
	viewer := uuid.New()
	ids := func(caller *uuid.UUID) []string {
		w := r.do(http.MethodGet, "/v1/playlists/"+r.playlist.ID.String()+"/items?limit=10", caller, "")
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		var rows []struct {
			PostID string `json:"post_id"`
		}
		_ = json.Unmarshal(decodeTube(t, w.Body.Bytes()).Data, &rows)
		out := make([]string, len(rows))
		for i, row := range rows {
			out[i] = row.PostID
		}
		return out
	}
	for name, caller := range map[string]*uuid.UUID{"viewer": &viewer, "signed out": nil} {
		if got := ids(caller); len(got) != 1 || got[0] != items[0].PostID.String() {
			t.Fatalf("%s: items %v; want the unlisted video left out", name, got)
		}
	}
	for name, caller := range map[string]*uuid.UUID{"playlist owner": &r.owner, "video author": &videoAuthor} {
		if got := ids(caller); len(got) != 2 {
			t.Fatalf("%s: items %v; want both", name, got)
		}
	}
}
