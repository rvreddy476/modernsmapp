package http

import (
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Tube catalogue listing for search (MTube, 2026-09-27;
// channels_internal_list.go). The routes are read from the REAL router,
// like the video-series routes, so a handler nobody wired cannot pass; and
// a non-public playlist discloses its id and visibility and nothing else.

func TestTubeCatalogueInternalRoutesAreRegistered(t *testing.T) {
	routes := registeredRoutesWithPrefix(t, "/internal/tube")
	for _, want := range []string{
		"GET /internal/tube/channels",
		"GET /internal/tube/playlists",
	} {
		if !routes[want] {
			t.Errorf("%s is not registered. Registered routes under /internal/tube: %v", want, keysOf(routes))
		}
	}
}

func TestTubePlaylistListRow_NonPublicDisclosesOnlyIDAndVisibility(t *testing.T) {
	cover := "https://cdn.example/cover.jpg"
	for _, vis := range []string{"private", "unlisted"} {
		p := &postgres.Playlist{ID: uuid.New(), CreatorID: uuid.New(), Title: "secret", Description: "mine", CoverURL: &cover, Visibility: vis, ItemCount: 9}
		row := tubePlaylistListRowOf(p)
		if row.ID != p.ID || row.Visibility != vis {
			t.Fatalf("%s: id/visibility = %v/%s", vis, row.ID, row.Visibility)
		}
		if row.OwnerID != nil || row.Title != "" || row.Description != "" || row.ItemCount != 0 || row.CoverURL != nil || row.CoverMediaID != nil || row.CreatedAt != nil {
			t.Fatalf("%s playlist leaked fields through the listing: %+v", vis, row)
		}
	}
}

func TestTubePlaylistListRow_PublicCarriesTheCoverAsIdWhenItIsOne(t *testing.T) {
	mediaID := uuid.New()
	asID := mediaID.String()
	p := &postgres.Playlist{ID: uuid.New(), CreatorID: uuid.New(), Title: "mixes", Visibility: "public", ItemCount: 3, CoverURL: &asID}
	row := tubePlaylistListRowOf(p)
	if row.OwnerID == nil || *row.OwnerID != p.CreatorID || row.Title != "mixes" || row.ItemCount != 3 {
		t.Fatalf("public row = %+v", row)
	}
	if row.CoverMediaID == nil || *row.CoverMediaID != mediaID || row.CoverURL == nil || *row.CoverURL != asID {
		t.Fatalf("a bare media id as cover must be surfaced as cover_media_id and kept as cover_url: %+v", row)
	}
	url := "https://cdn.example/cover.jpg"
	p.CoverURL = &url
	if row := tubePlaylistListRowOf(p); row.CoverMediaID != nil || row.CoverURL == nil || *row.CoverURL != url {
		t.Fatalf("a URL cover is not a media id: %+v", row)
	}
}
