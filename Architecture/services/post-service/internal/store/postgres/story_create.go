package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Module 4 M4-P0-4 — atomic story creation.
//
// The old path inserted the story (immediately publishable) and then fired a
// best-effort goroutine to publish StoryCreated. Two independent failures
// followed from that: a story could exist with no moderation request ever
// emitted, and a story was visible before anyone had looked at it.
//
// One transaction now writes the pending story AND its moderation request. If
// the outbox insert fails, the story does not exist. There is no ordering in
// which a story becomes visible without a request having been durably recorded.

// ErrStoryMediaInvalid means the referenced media cannot back a story: it does
// not exist, belongs to another user, is deleted, or is the wrong kind.
//
// Callers must return ONE non-enumerating response for all of these. Telling
// the difference apart would let a caller probe which media ids exist and who
// owns them.
var ErrStoryMediaInvalid = errors.New("story media invalid")

// StoryModerationRequest is the outbox payload trust-safety consumes.
type StoryModerationRequest struct {
	StoryID         uuid.UUID `json:"story_id"`
	AuthorID        uuid.UUID `json:"author_id"`
	MediaID         uuid.UUID `json:"media_id"`
	MediaType       string    `json:"media_type"`
	Caption         string    `json:"caption"`
	ContentRevision int64     `json:"content_revision"`
}

// CreateStoryPending inserts a pending story plus its moderation request in one
// transaction, after verifying the author owns the referenced media.
func (s *Store) CreateStoryPending(ctx context.Context, story *Story, idempotencyKey string) (*Story, error) {
	if story.MediaID == nil {
		return nil, fmt.Errorf("%w: no media_id", ErrStoryMediaInvalid)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin story create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Idempotency: the same key returns the same story rather than creating a
	// second one. A retried upload must not produce two pending stories and
	// two moderation requests.
	if idempotencyKey != "" {
		var existing uuid.UUID
		err := tx.QueryRow(ctx,
			`SELECT id FROM stories WHERE author_id = $1 AND idempotency_key = $2 AND deleted_at IS NULL`,
			story.AuthorID, idempotencyKey).Scan(&existing)
		if err == nil {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return nil, commitErr
			}
			return s.GetStory(ctx, existing)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("story idempotency lookup: %w", err)
		}
	}

	// Ownership and lifecycle of the canonical asset, inside the transaction so
	// the asset cannot be deleted between the check and the insert. The FK
	// added in migration 032 is what actually serialises against the orphan
	// reclaimer; this query produces the non-enumerating error message.
	// Column names are the REAL ones: media_assets uses `uploader_id`, not
	// `user_id`, and has no `deleted_at` — deletion is a hard delete, which is
	// why the FK added in migration 032 is ON DELETE RESTRICT rather than a
	// soft-delete predicate. An earlier version of this query guessed both and
	// would have failed at runtime with 42703 on every story create.
	var ownerID uuid.UUID
	var fileType, processingStatus, moderationStatus string
	err = tx.QueryRow(ctx, `
		SELECT uploader_id, file_type, processing_status, moderation_status
		FROM media_assets WHERE id = $1
	`, *story.MediaID).Scan(&ownerID, &fileType, &processingStatus, &moderationStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: no such media", ErrStoryMediaInvalid)
	}
	if err != nil {
		return nil, fmt.Errorf("story media lookup: %w", err)
	}
	if ownerID != story.AuthorID {
		return nil, fmt.Errorf("%w: media belongs to another user", ErrStoryMediaInvalid)
	}
	// Lifecycle: only a finished asset can back a story. Referencing one that
	// is still uploading or processing would create a story whose bytes may
	// never arrive, and whose safety scan has not run.
	if processingStatus != "ready" {
		return nil, fmt.Errorf("%w: media is %q, not ready", ErrStoryMediaInvalid, processingStatus)
	}
	// Media-level safety. This is necessary but NOT sufficient: the story
	// itself still enters `pending` and needs its own decision, because the
	// caption and the story context are not covered by a media scan.
	if moderationStatus != "passed" && moderationStatus != "approved" {
		return nil, fmt.Errorf("%w: media moderation is %q", ErrStoryMediaInvalid, moderationStatus)
	}
	if !mediaTypeMatches(fileType, story.MediaType) {
		return nil, fmt.Errorf("%w: declared %q, asset is %q", ErrStoryMediaInvalid, story.MediaType, fileType)
	}

	story.ModerationState = "pending"
	story.ContentRevision = 1

	if _, err := tx.Exec(ctx, `
		INSERT INTO stories (id, author_id, media_id, media_type, caption, visibility,
			view_count, expires_at, is_highlight, highlight_group, created_at,
			moderation_state, content_revision, idempotency_key)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'pending',1,$12)
	`, story.ID, story.AuthorID, story.MediaID, story.MediaType, story.Caption,
		story.Visibility, 0, story.ExpiresAt, story.IsHighlight,
		story.HighlightGroup, story.CreatedAt, nullIfEmpty(idempotencyKey)); err != nil {
		return nil, fmt.Errorf("insert pending story: %w", err)
	}

	payload, err := json.Marshal(StoryModerationRequest{
		StoryID:         story.ID,
		AuthorID:        story.AuthorID,
		MediaID:         *story.MediaID,
		MediaType:       story.MediaType,
		Caption:         story.Caption,
		ContentRevision: 1,
	})
	if err != nil {
		return nil, fmt.Errorf("encode moderation request: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO post_outbox_events (event_type, aggregate_type, aggregate_id, payload, created_at)
		VALUES ('StoryModerationRequested', 'story', $1, $2, NOW())
	`, story.ID, payload); err != nil {
		return nil, fmt.Errorf("insert moderation request: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit story create: %w", err)
	}
	return story, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// mediaTypeMatches checks the declared story media type against the asset's
// stored file type. A mismatch is rejected rather than corrected: silently
// trusting the asset would let a client label a video as an image and bypass
// whichever pipeline is keyed off the declared type.
func mediaTypeMatches(fileType, declared string) bool {
	switch declared {
	case "image":
		return fileType == "image" || fileType == "photo"
	case "video":
		return fileType == "video"
	default:
		return false
	}
}

// ApplyStoryModerationDecision applies a terminal decision, but only when the
// revision matches the story's current revision.
//
// The revision guard is what makes a stale decision harmless: if the content
// changed after evaluation, the decision describes content that no longer
// exists and must not approve what is there now. Returns whether it applied.
func (s *Store) ApplyStoryModerationDecision(ctx context.Context, storyID uuid.UUID, revision int64, state, decisionID, reason, policyVersion string) (bool, error) {
	switch state {
	case "approved", "rejected", "manual_review":
	default:
		return false, fmt.Errorf("invalid terminal moderation state %q", state)
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE stories
		   SET moderation_state = $3,
		       moderated_at = NOW(),
		       moderation_decision_id = $4,
		       moderation_reason = $5,
		       moderation_policy_version = $6
		 WHERE id = $1
		   AND content_revision = $2
		   AND moderation_state IN ('pending','manual_review')
	`, storyID, revision, state, decisionID, reason, policyVersion)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// MediaUploader returns the owner of a media asset, or uuid.Nil when the asset
// does not exist. A missing asset is not an error: the caller is deciding
// access, and "no such asset" and "not yours" both resolve to denied.
func (s *Store) MediaUploader(ctx context.Context, mediaID uuid.UUID) (uuid.UUID, error) {
	var owner uuid.UUID
	err := s.db.QueryRow(ctx,
		`SELECT uploader_id FROM media_assets WHERE id = $1`, mediaID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, err
	}
	return owner, nil
}

// MediaAccessFacts are the media-owned gates that must pass before content
// visibility is considered. In particular, an owner may preview processing
// media, but no other viewer may receive bytes before both ready and passed.
type MediaAccessFacts struct {
	UploaderID       uuid.UUID
	ProcessingStatus string
	ModerationStatus string
}

func (s *Store) GetMediaAccessFacts(ctx context.Context, mediaID uuid.UUID) (*MediaAccessFacts, error) {
	var f MediaAccessFacts
	err := s.db.QueryRow(ctx, `
		SELECT uploader_id, processing_status, moderation_status
		  FROM media_assets
		 WHERE id = $1
	`, mediaID).Scan(&f.UploaderID, &f.ProcessingStatus, &f.ModerationStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// GetMediaAccessFactsBatch loads lifecycle/ownership gates for a media page in
// one query. Missing assets are absent from the result and therefore denied.
func (s *Store) GetMediaAccessFactsBatch(ctx context.Context, mediaIDs []uuid.UUID) (map[uuid.UUID]MediaAccessFacts, error) {
	result := make(map[uuid.UUID]MediaAccessFacts, len(mediaIDs))
	if len(mediaIDs) == 0 {
		return result, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, uploader_id, processing_status, moderation_status
		FROM media_assets WHERE id = ANY($1)
	`, mediaIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var facts MediaAccessFacts
		if err := rows.Scan(&id, &facts.UploaderID, &facts.ProcessingStatus, &facts.ModerationStatus); err != nil {
			return nil, err
		}
		result[id] = facts
	}
	return result, rows.Err()
}

// StoryForMedia returns the live story referencing this asset, if any.
//
// Soft-deleted stories are excluded: a deleted story must stop authorizing its
// media immediately, which is half of what makes takedown effective. The other
// half is the signed-URL TTL, which bounds how long an already-issued link
// keeps working.
func (s *Store) StoryForMedia(ctx context.Context, mediaID uuid.UUID) (*Story, error) {
	story, err := scanStory(s.db.QueryRow(ctx, `
		SELECT `+storyCols+` FROM stories
		WHERE media_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC LIMIT 1
	`, mediaID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return story, nil
}

// StoriesForMediaBatch returns the newest live story for each media asset.
func (s *Store) StoriesForMediaBatch(ctx context.Context, mediaIDs []uuid.UUID) (map[uuid.UUID]*Story, error) {
	result := make(map[uuid.UUID]*Story, len(mediaIDs))
	if len(mediaIDs) == 0 {
		return result, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+storyCols+` FROM stories
		WHERE media_id = ANY($1) AND deleted_at IS NULL
		ORDER BY created_at DESC
	`, mediaIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var story Story
		if err := scanStoryInto(&story, rows.Scan); err != nil {
			return nil, err
		}
		if story.MediaID != nil {
			if _, exists := result[*story.MediaID]; !exists {
				copy := story
				result[*story.MediaID] = &copy
			}
		}
	}
	return result, rows.Err()
}
