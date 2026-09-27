package events

// MediaSubtitlesChanged (2026-09-27) — media-service announces the caption
// state of one media asset whenever a write could have changed which of its
// subtitle tracks are PUBLISHED: a manual track uploaded or corrected (both
// are published on write), a generated track marked draft by the caption
// job, or the owner's publish/unpublish toggle.
//
// Published on media-service's `media.events` topic through its
// transactional outbox (media_event_outbox), in the same commit as the
// subtitle write. The payload is a STATE SNAPSHOT, not a delta: consumers
// set, they never increment, so a duplicate is harmless.
//
// Ordering: the outbox keeps one row per asset and replaces it on every
// change, and envelope.OccurredAt is taken from the database clock while
// the asset row is locked, so for one asset a later state always carries a
// later OccurredAt. media.events is not partitioned by asset, so consumers
// must drop a snapshot whose OccurredAt is older than the one they applied.
const MediaSubtitlesChanged = "MediaSubtitlesChanged" // payload: MediaSubtitlesChangedPayload

// MediaSubtitlesChangedPayload is the published-caption state of one asset.
//
// Drafts (media_subtitles.published = false) are never counted and their
// languages never listed: a draft does not exist for anyone but its owner.
type MediaSubtitlesChangedPayload struct {
	MediaID string `json:"media_id"`
	// HasPublishedSubtitles is true when at least one track is published.
	HasPublishedSubtitles bool `json:"has_published_subtitles"`
	// Languages are the published tracks' language tags, sorted; empty
	// (never null) when there are none.
	Languages []string `json:"languages"`
}
