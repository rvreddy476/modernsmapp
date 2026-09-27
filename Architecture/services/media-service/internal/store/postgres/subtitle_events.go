package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Caption state for search (2026-09-27).
//
// search-service indexes `has_subtitles` on post documents (the `cc`
// feature filter) but nothing live ever set it: post-service does not know
// caption state. Every write that can change which of an asset's tracks are
// PUBLISHED now records a MediaSubtitlesChanged snapshot in
// media_event_outbox IN THE SAME TRANSACTION as the write — the outbox's own
// rule (migration 013): a state transition and the event describing it are
// one commit. The relay (service/media_outbox.go) publishes it to
// `media.events`.
//
// The writes that do this:
//
//	CreateSubtitle              manual upload (POST /v1/subtitles/:id) and
//	                            the auto-caption write; a new row is
//	                            published by default (migration 020)
//	UpdateSubtitleContent       owner correction (PATCH /v1/subtitles/:id);
//	                            may create a published manual track
//	SetSubtitlePublished        the owner's publish/unpublish toggle
//	MarkGeneratedSubtitleDraft  the caption job turning its track into a draft
//
// ONE ROW PER ASSET. media_event_outbox is UNIQUE (media_asset_id,
// event_type), so a later change REPLACES the pending snapshot: a new
// event_id, the new payload, published_at cleared. A new event_id (not the
// old one re-armed) is what makes the relay's publish-then-mark safe: a relay
// that read the old row marks the OLD id, which no longer exists, and the
// new row stays pending. Nothing is lost; at worst the relay logs one "not
// found" and picks the new row up on its next tick.
//
// ORDERING. The asset row is locked (FOR NO KEY UPDATE — compatible with the
// KEY SHARE lock the subtitle write's foreign key takes) BEFORE the write, so
// two writers to one asset run one after the other, the second computes its
// snapshot from the first's committed rows, and occurred_at is
// clock_timestamp() taken under that lock — a later snapshot always has a
// later occurred_at. Consumers use it to drop a stale snapshot, because
// media.events is not partitioned by asset. Locking the asset first is also
// the order DeleteAssetForReferrer takes (asset, then subtitle rows), so the
// two cannot deadlock.

// SubtitleStatePayload is the MediaSubtitlesChanged payload for an asset
// whose published track languages are `languages`. Pure, for the tests.
// Languages come back sorted and never nil, so the wire shape is `[]`, not
// `null`, when nothing is published.
func SubtitleStatePayload(mediaID uuid.UUID, languages []string) ([]byte, error) {
	langs := append([]string{}, languages...)
	sort.Strings(langs)
	return json.Marshal(sharedevents.MediaSubtitlesChangedPayload{
		MediaID:               mediaID.String(),
		HasPublishedSubtitles: len(langs) > 0,
		Languages:             langs,
	})
}

// withSubtitleStateEvent runs one subtitle write and its outbox snapshot in
// one transaction. write reports whether it changed anything; a write that
// was suppressed (an owner-edited track the job may not overwrite) records
// no event. An asset row that does not exist records no event either — there
// is nothing to announce, and the write itself decides whether that is an
// error.
func (s *MediaAssetStore) withSubtitleStateEvent(ctx context.Context, mediaID uuid.UUID, write func(tx pgx.Tx) (bool, error)) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("subtitle write: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var uploaderID uuid.UUID
	assetFound := true
	err = tx.QueryRow(ctx,
		`SELECT uploader_id FROM media_assets WHERE id = $1 FOR NO KEY UPDATE`, mediaID).
		Scan(&uploaderID)
	if errors.Is(err, pgx.ErrNoRows) {
		assetFound = false
	} else if err != nil {
		return fmt.Errorf("subtitle write: lock media: %w", err)
	}

	changed, err := write(tx)
	if err != nil {
		return err
	}
	if changed && assetFound {
		if err := enqueueSubtitleStateTx(ctx, tx, mediaID, uploaderID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// enqueueSubtitleStateTx records the asset's current published-caption
// state in the outbox, replacing any snapshot not yet superseded. Must run
// inside the write's transaction, after the write and under the asset lock.
func enqueueSubtitleStateTx(ctx context.Context, tx pgx.Tx, mediaID, uploaderID uuid.UUID) error {
	rows, err := tx.Query(ctx, `
		SELECT language FROM media_subtitles
		 WHERE media_asset_id = $1 AND published
		 ORDER BY language`, mediaID)
	if err != nil {
		return fmt.Errorf("subtitle state: read published tracks: %w", err)
	}
	var langs []string
	for rows.Next() {
		var lang string
		if err := rows.Scan(&lang); err != nil {
			rows.Close()
			return fmt.Errorf("subtitle state: scan: %w", err)
		}
		langs = append(langs, lang)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("subtitle state: read published tracks: %w", err)
	}

	payload, err := SubtitleStatePayload(mediaID, langs)
	if err != nil {
		return fmt.Errorf("subtitle state payload: %w", err)
	}
	var actor any
	if uploaderID != uuid.Nil {
		actor = uploaderID
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO media_event_outbox
		       (event_id, media_asset_id, event_type, actor_user_id, payload, occurred_at, created_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, clock_timestamp(), clock_timestamp())
		ON CONFLICT (media_asset_id, event_type) DO UPDATE
		   SET event_id      = EXCLUDED.event_id,
		       actor_user_id = EXCLUDED.actor_user_id,
		       payload       = EXCLUDED.payload,
		       occurred_at   = EXCLUDED.occurred_at,
		       created_at    = EXCLUDED.created_at,
		       published_at  = NULL,
		       attempts      = 0,
		       last_error    = NULL
	`, uuid.NewString(), mediaID, sharedevents.MediaSubtitlesChanged, actor, payload); err != nil {
		return fmt.Errorf("subtitle state: outbox: %w", err)
	}
	return nil
}
