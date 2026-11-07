package reindex

// Rebuilding the Tube indices from post-service.
//
// tube_channels_v1 and tube_collections_v1 (store/search/tube.go) have no
// event feed that WRITES documents (account erasure and deactivation aside:
// purge_adapter.go): search reads nothing when a channel is created or
// renamed, or when a playlist is made or edited. Since 2026-11-07 a deleted
// playlist, or one made unlisted or private, is REMOVED by its event
// (tube.playlist.deleted / visibility_changed, consumer.go); this walk is
// still what adds and updates, and its sweep is the backstop for
// removals. This walk over post-service's internal listing is therefore
// not the fallback the users and products reindexes are — it is the ONLY
// way these indices are populated, and running it is how they stay current
// until events exist. It is cheap (two keyset walks) and safe on a live index (upserts
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
	ChannelsFetched    int `json:"channels_fetched"`
	ChannelsIndexed    int `json:"channels_indexed"`
	PlaylistsFetched   int `json:"playlists_fetched"`
	CollectionsIndexed int `json:"collections_indexed"`
	RemovedNonPublic   int `json:"removed_non_public"`
	// ChannelsSwept / CollectionsSwept count documents deleted because the
	// complete walk no longer listed them (hard-deleted rows). Zero, and
	// SweepSkipped set, when the walk was not complete.
	ChannelsSwept       int64  `json:"channels_swept"`
	CollectionsSwept    int64  `json:"collections_swept"`
	SweepSkipped        string `json:"sweep_skipped,omitempty"`
	Pages               int    `json:"pages"`
	ChannelsIndexTotal  int64  `json:"channels_index_total"`
	CollectionsIndexTot int64  `json:"collections_index_total"`
	Duration            string `json:"duration"`
}

// ReindexTube walks both listings and brings the two indices in line.
func ReindexTube(ctx context.Context, client *postclient.Client, store *search.Store, log *slog.Logger) (TubeResult, error) {
	started := time.Now()
	// cutoff is the sweep line: every document this walk writes is stamped
	// with its write time, which is after cutoff, so after a complete walk
	// anything stamped earlier (or never) is a row post-service stopped
	// listing. Write time, not one run stamp, so a second reindex running
	// alongside this one cannot have its fresh documents swept.
	cutoff := started.UTC()
	channelsComplete, collectionsComplete := true, true
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
		stamp := time.Now().UTC()
		for _, ch := range page.Items {
			doc := TubeChannelDocOf(ch)
			doc.ReindexedAt = &stamp
			docs = append(docs, doc)
		}
		n, err := store.BulkIndexTubeChannels(ctx, docs)
		if err != nil {
			log.Warn("reindex tube: bulk index channels failed", "after", after, "err", err)
		}
		if err != nil || n < len(docs) {
			channelsComplete = false
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
		stamp := time.Now().UTC()
		docs := make([]search.TubeCollectionDoc, 0, len(page.Items))
		for _, p := range page.Items {
			if doc, public := TubeCollectionDocOf(p); public {
				doc.ReindexedAt = &stamp
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
		if err != nil || n < len(docs) {
			collectionsComplete = false
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

	// Mark and sweep (S-M3): a hard-deleted playlist is simply never listed
	// again. tube.playlist.deleted removes it as it happens; the sweep is the
	// backstop for an event that was lost or predates the event. Only a walk
	// that listed and wrote every row may sweep, and an empty listing never
	// does: emptying an index because a listing came back blank is the
	// outage-shaped failure this must not have.
	sweep := func(index string, fetched int, complete bool) int64 {
		switch {
		case !complete:
			res.SweepSkipped = "a bulk write failed; nothing swept"
			return 0
		case fetched == 0:
			res.SweepSkipped = "an empty listing; nothing swept"
			return 0
		}
		n, err := store.SweepTubeIndex(ctx, index, cutoff)
		if err != nil {
			log.Warn("reindex tube: sweep failed; unlisted documents remain until the next run", "index", index, "err", err)
			res.SweepSkipped = "sweep failed: " + err.Error()
			return 0
		}
		return n
	}
	res.ChannelsSwept = sweep(search.IndexTubeChannels, res.ChannelsFetched, channelsComplete)
	res.CollectionsSwept = sweep(search.IndexTubeCollections, res.PlaylistsFetched, collectionsComplete)

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
		"removed_non_public", res.RemovedNonPublic, "channels_swept", res.ChannelsSwept,
		"collections_swept", res.CollectionsSwept, "sweep_skipped", res.SweepSkipped,
		"pages", res.Pages, "duration", res.Duration)
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
