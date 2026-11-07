package service

import (
	"testing"

	"github.com/atpost/user-service/internal/store"
)

// Pins the audience table the handler's ViewerAccess feeds (see the http
// package's about_visibility.go): the owner sees every item including
// private ones; friends see friends + followers + public; followers see
// followers + public; everyone else public only.
func TestAboutAudienceTable(t *testing.T) {
	items := []store.AboutItem{
		{Visibility: "public"},
		{Visibility: "followers"},
		{Visibility: "friends"},
		{Visibility: "private"},
	}
	cases := []struct {
		name   string
		access ViewerAccess
		want   []string
	}{
		{"owner", ViewerAccess{IsSelf: true}, []string{"public", "followers", "friends", "private"}},
		{"friend", ViewerAccess{IsFollower: true, IsFriend: true}, []string{"public", "followers", "friends"}},
		{"follower", ViewerAccess{IsFollower: true}, []string{"public", "followers"}},
		{"stranger", ViewerAccess{}, []string{"public"}},
	}
	for _, tc := range cases {
		got := filterAboutItems(items, tc.access)
		if len(got) != len(tc.want) {
			t.Fatalf("%s: got %d items, want %v", tc.name, len(got), tc.want)
		}
		for i, it := range got {
			if it.Visibility != tc.want[i] {
				t.Fatalf("%s: item %d is %q, want %q", tc.name, i, it.Visibility, tc.want[i])
			}
		}
	}
}

// US-L2: a visibility the table does not know reads as private to everyone
// but the owner. It used to read as public.
func TestAboutUnknownVisibilityIsPrivate(t *testing.T) {
	items := []store.AboutItem{{Visibility: "Friends"}, {Visibility: "only-me"}, {Visibility: ""}}
	for _, access := range []ViewerAccess{{}, {IsFollower: true}, {IsFollower: true, IsFriend: true}} {
		if got := filterAboutItems(items, access); len(got) != 0 {
			t.Fatalf("viewer %+v saw %d items with unknown visibilities, want none", access, len(got))
		}
	}
	if got := filterAboutItems(items, ViewerAccess{IsSelf: true}); len(got) != len(items) {
		t.Fatalf("the owner saw %d of their own %d items", len(got), len(items))
	}
}
