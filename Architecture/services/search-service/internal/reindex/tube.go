package reindex

// Rebuilding the Tube indices from post-service.
//
// tube_channels_v1 and tube_collections_v1 (store/search/tube.go) have no
// event feed: post-service publishes nothing when a channel is created or
// renamed, or when a playlist is made, edited, or flipped private. This
// walk over post-service's internal listing is therefore not the fallback
// the users and products reindexes are — it is the ONLY way these indices
// are populated, and running it is how they stay current until events
// exist. It is cheap (two keyset walks) and safe on a live index (upserts
// by id), so it can be run on a schedule.
//
// VISIBILITY IS ENFORCED HERE, NOT ASSUMED. post-service lists every
// playlist and says which are public. A public one is indexed; ANY other
// visibility is deleted from the index — unlisted and private alike, since
// "reachable with the link" is not "discoverable by search". A playlist
// that went private since the last run is removed by exactly the same
// rule, so the index cannot keep advertising a title its owner withdrew.

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/search-service/internal/postclient"
	"github.com/atpost/search-service/internal/store/search"
)

// tubePageSize is the keyset page size for both walks.
const tubePageSize = 200

// TubeResult summarizes a run. Fetched and Indexed are separate, and
// RemovedNonPublic is reported beside them, so an operator can see both
// that the walk completed and that the visibility rule did work.
type TubeResult struct {
	ChannelsFetched     int    `json:"channels_fetched"`
	ChannelsIndexed     int    `json:"channels_indexed"`
	PlaylistsFetched    int    `json:"playlists_fetched"`
	CollectionsIndexed  int    `json:"collections_indexed"`
	RemovedNonPublic    int    `json:"removed_non_public"`
	Pages               int    `json:"pages"`
	ChannelsIndexTotal  int64  `json:"channels_index_total"`
	CollectionsIndexTot int64  `json:"collections_index_total"`
	Duration            string `json:"duration"`
}

// ReindexTube walks both listings and brings the two indices in line.
func ReindexTube(ctx context.Context, client *postclient.Client, store *search.Store, log *slog.Logger) (TubeResult, error) {
	started := time.Now()
	var res TubeResult
	if client == nil || !client.Configured() {
		return res, fmt.Errorf("reindex: POST_SERVICE_URL not configured")
	}

	// Channels.
	after := ""
	for {
		page, err := client.ListTubeChannels(ctx, after, tubePageSize)
		if err != nil {
			return res, fmt.Errorf("reindex tube: channels page (after %q): %w", after, err)
		}
		res.Pages++
		if len(page.Items) == 0 {
			break
		}
		res.ChannelsFetched += len(page.Items)
		docs := make([]search.TubeChannelDoc, 0, len(page.Items))
		for _, ch := range page.Items {
			docs = append(docs, TubeChannelDocOf(ch))
		}
		n, err := store.BulkIndexTubeChannels(ctx, docs)
		if err != nil {
			log.Warn("reindex tube: bulk index channels failed", "after", after, "err", err)
		}
		res.ChannelsIndexed += n
		if !page.HasMore || page.NextAfter == "" {
			break
		}
		after = page.NextAfter
	}

	// Playlists → collections.
	after = ""
	for {
		page, err := client.ListTubePlaylists(ctx, after, tubePageSize)
		if err != nil {
			return res, fmt.Errorf("reindex tube: playlists page (after %q): %w", after, err)
		}
		res.Pages++
		if len(page.Items) == 0 {
			break
		}
		res.PlaylistsFetched += len(page.Items)
		docs := make([]search.TubeCollectionDoc, 0, len(page.Items))
		for _, p := range page.Items {
			if doc, public := TubeCollectionDocOf(p); public {
				docs = append(docs, doc)
				continue
			}
			// Not public: make sure no document survives for it.
			if err := store.DeleteTubeCollection(ctx, p.ID); err != nil {
				log.Warn("reindex tube: remove non-public playlist failed", "id", p.ID, "err", err)
				continue
			}
			res.RemovedNonPublic++
		}
		n, err := store.BulkIndexTubeCollections(ctx, docs)
		if err != nil {
			log.Warn("reindex tube: bulk index collections failed", "after", after, "err", err)
		}
		res.CollectionsIndexed += n
		if !page.HasMore || page.NextAfter == "" {
			break
		}
		after = page.NextAfter
	}

	for _, index := range []string{search.IndexTubeChannels, search.IndexTubeCollections} {
		if err := store.RefreshIndex(ctx, index); err != nil {
			log.Warn("reindex tube: refresh failed; documents will become searchable shortly", "index", index, "err", err)
		}
	}
	if total, err := store.CountIndexDocs(ctx, search.IndexTubeChannels); err == nil {
		res.ChannelsIndexTotal = total
	}
	if total, err := store.CountIndexDocs(ctx, search.IndexTubeCollections); err == nil {
		res.CollectionsIndexTot = total
	}

	res.Duration = time.Since(started).Round(time.Millisecond).String()
	log.Info("reindex: tube complete",
		"channels_fetched", res.ChannelsFetched, "channels_indexed", res.ChannelsIndexed,
		"playlists_fetched", res.PlaylistsFetched, "collections_indexed", res.CollectionsIndexed,
		"removed_non_public", res.RemovedNonPublic, "pages", res.Pages, "duration", res.Duration)
	return res, nil
}

// TubeChannelDocOf is the one conversion from the listing row to the
// document. Pure.
func TubeChannelDocOf(ch postclient.TubeChannel) search.TubeChannelDoc {
	doc := search.TubeChannelDoc{
		ChannelID:     ch.ID,
		OwnerID:       ch.OwnerID,
		Name:          ch.Name,
		Handle:        ch.Handle,
		About:         ch.About,
		FollowerCount: ch.FollowerCount,
		CreatedAt:     ch.CreatedAt,
		UpdatedAt:     ch.UpdatedAt,
	}
	if ch.AvatarMediaID != nil {
		doc.AvatarMediaID = *ch.AvatarMediaID
	}
	return doc
}

// TubeCollectionDocOf converts a listing row; public=false means "do not
// index, delete". Pure.
func TubeCollectionDocOf(p postclient.TubePlaylist) (search.TubeCollectionDoc, bool) {
	if p.Visibility != "public" || p.OwnerID == nil {
		return search.TubeCollectionDoc{}, false
	}
	doc := search.TubeCollectionDoc{
		PlaylistID:  p.ID,
		OwnerID:     *p.OwnerID,
		Title:       p.Title,
		Description: p.Description,
		Visibility:  p.Visibility,
		ItemCount:   p.ItemCount,
	}
	if p.CoverMediaID != nil {
		doc.CoverMediaID = *p.CoverMediaID
	}
	if p.CoverURL != nil {
		doc.CoverURL = *p.CoverURL
	}
	if p.CreatedAt != nil {
		doc.CreatedAt = *p.CreatedAt
	}
	if p.UpdatedAt != nil {
		doc.UpdatedAt = *p.UpdatedAt
	}
	return doc, true
}
