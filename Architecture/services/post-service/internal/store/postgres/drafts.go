package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// prefixedDraftCols renders draftCols with a table alias for
// UPDATE ... FROM ... RETURNING queries.
func prefixedDraftCols(alias string) string {
	parts := strings.Split(draftCols, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

// ReelDraft represents a reel draft row in the reel_drafts table.
type ReelDraft struct {
	ID                uuid.UUID  `json:"id"`
	AuthorID          uuid.UUID  `json:"author_id"`
	MediaID           *uuid.UUID `json:"media_id,omitempty"`
	Title             string     `json:"title"`
	Caption           string     `json:"caption"`
	Hashtags          []string   `json:"hashtags"`
	Tags              []string   `json:"tags"`
	Visibility        string     `json:"visibility"`
	TopicID           *int       `json:"topic_id,omitempty"`
	Category          string     `json:"category"`
	Language          string     `json:"language"`
	SEOTitle          string     `json:"seo_title"`
	CrossPostPostbook bool       `json:"cross_post_postbook"`
	CrossPostPosttube bool       `json:"cross_post_posttube"`
	PublishToFeed     bool       `json:"publish_to_feed"`
	IsMadeForKids     bool       `json:"is_made_for_kids"`
	PaidPromotion     bool       `json:"paid_promotion"`
	AlteredContent    bool       `json:"altered_content"`
	AutoChapters      bool       `json:"auto_chapters"`
	FeaturedPlaces    bool       `json:"featured_places"`
	AutoConcepts      bool       `json:"auto_concepts"`
	License           string     `json:"license"`
	AllowEmbedding    bool       `json:"allow_embedding"`
	RemixSetting      string     `json:"remix_setting"`
	LikesEnabled      bool       `json:"likes_enabled"`
	CommentsEnabled   bool       `json:"comments_enabled"`
	CommentModeration string     `json:"comment_moderation"`
	CommentAccess     string     `json:"comment_access"`
	RecordingDate     *time.Time `json:"recording_date,omitempty"`
	RecordingLocation string     `json:"recording_location"`
	AudioTrackID      *string    `json:"audio_track_id,omitempty"`
	AudioStartMs      int        `json:"audio_start_ms"`
	OriginalAudioVol  float32    `json:"original_audio_volume"`
	OverlayAudioVol   float32    `json:"overlay_audio_volume"`
	CoverMediaID      *uuid.UUID `json:"cover_media_id,omitempty"`
	ScheduleAt        *time.Time `json:"schedule_at,omitempty"`
	Status            string     `json:"status"`
	ModerationStatus  string     `json:"moderation_status"`
	PublishedPostID   *uuid.UUID `json:"published_post_id,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	// BlockedReason is set with status 'blocked' when the schedule worker
	// parked the draft (migration 055): author_suspended…, or
	// standing_unavailable after 24 h without an answer from trust-safety.
	BlockedReason *string `json:"blocked_reason,omitempty"`
	// Upload studio (migration 068): copied onto the post at publish.
	AIGenerated     bool    `json:"ai_generated"`
	LocationName    *string `json:"location_name,omitempty"`
	LocationPlaceID *string `json:"location_place_id,omitempty"`
	// ContentType is the kind the studio measured, 'flick' or 'long_video'
	// (migration 069). nil: never said; publish classifies it.
	ContentType *string `json:"content_type,omitempty"`
	// Video credits (migration 070), as the Credits step saved them and as
	// the publish hands them to CreatePost (which links, checks and
	// announces the tags only then). Omitted when unset.
	Credits         []CreditInput `json:"credits,omitempty"`
	Genres          []string      `json:"genres,omitempty"`
	ReleaseYear     *int          `json:"release_year,omitempty"`
	ProductionHouse *string       `json:"production_house,omitempty"`
	// TierRequiredID is the membership tier the post will be gated on from
	// birth (migration 071); the publish hands it to CreatePost, which checks
	// it against the author's active tiers and writes it on the row. nil =
	// open.
	TierRequiredID *uuid.UUID `json:"tier_required_id,omitempty"`
	// Action is the Action button the post gets at publication (migration
	// 072): label and URL as saved (the URL rules were checked then);
	// eligibility and the safety check run when it publishes. null = none.
	Action *DraftAction `json:"action"`
}

// DraftAction is a saved, not yet published, Action button.
type DraftAction struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

func (a *DraftAction) labelArg() *string {
	if a == nil {
		return nil
	}
	return &a.Label
}

func (a *DraftAction) urlArg() *string {
	if a == nil {
		return nil
	}
	return &a.URL
}

// decodeDraftCredits reads reel_drafts.credits (NULL = none).
func (d *ReelDraft) decodeDraftCredits(raw []byte) error {
	d.Credits = nil
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, &d.Credits)
}

const draftCols = `id, author_id, media_id,
	title, caption, hashtags, tags,
	visibility, topic_id, category, language, seo_title,
	cross_post_postbook, cross_post_posttube, publish_to_feed,
	is_made_for_kids, paid_promotion, altered_content,
	auto_chapters, featured_places, auto_concepts,
	license, allow_embedding, remix_setting,
	likes_enabled, comments_enabled, comment_moderation, comment_access,
	recording_date, recording_location,
	audio_track_id, audio_start_ms, original_audio_volume, overlay_audio_volume,
	cover_media_id, schedule_at,
	status, moderation_status, published_post_id,
	created_at, updated_at, blocked_reason,
	ai_generated, location_name, location_place_id,
	content_type,
	credits, genres, release_year, production_house,
	tier_required_id,
	action_label, action_url`

// draftScan holds the columns a draft row is decoded from beside the
// struct's own fields (credits JSON, the two action columns).
type draftScan struct {
	credits     []byte
	actionLabel *string
	actionURL   *string
}

// draftScanDestinations is the single source of truth for draftCols scan
// order, shared by the one-row and the many-row scans (the many-row scan
// had drifted: it never read tier_required_id, so ListDrafts failed on
// every row once migration 071 added the column).
func draftScanDestinations(d *ReelDraft, x *draftScan) []any {
	return []any{
		&d.ID, &d.AuthorID, &d.MediaID,
		&d.Title, &d.Caption, &d.Hashtags, &d.Tags,
		&d.Visibility, &d.TopicID, &d.Category, &d.Language, &d.SEOTitle,
		&d.CrossPostPostbook, &d.CrossPostPosttube, &d.PublishToFeed,
		&d.IsMadeForKids, &d.PaidPromotion, &d.AlteredContent,
		&d.AutoChapters, &d.FeaturedPlaces, &d.AutoConcepts,
		&d.License, &d.AllowEmbedding, &d.RemixSetting,
		&d.LikesEnabled, &d.CommentsEnabled, &d.CommentModeration, &d.CommentAccess,
		&d.RecordingDate, &d.RecordingLocation,
		&d.AudioTrackID, &d.AudioStartMs, &d.OriginalAudioVol, &d.OverlayAudioVol,
		&d.CoverMediaID, &d.ScheduleAt,
		&d.Status, &d.ModerationStatus, &d.PublishedPostID,
		&d.CreatedAt, &d.UpdatedAt, &d.BlockedReason,
		&d.AIGenerated, &d.LocationName, &d.LocationPlaceID,
		&d.ContentType,
		&x.credits, &d.Genres, &d.ReleaseYear, &d.ProductionHouse,
		&d.TierRequiredID,
		&x.actionLabel, &x.actionURL,
	}
}

func (d *ReelDraft) finishScan(x *draftScan) error {
	if err := d.decodeDraftCredits(x.credits); err != nil {
		return err
	}
	d.Action = nil
	if x.actionLabel != nil && x.actionURL != nil && *x.actionLabel != "" && *x.actionURL != "" {
		d.Action = &DraftAction{Label: *x.actionLabel, URL: *x.actionURL}
	}
	return nil
}

func scanDraft(row pgx.Row) (*ReelDraft, error) {
	var d ReelDraft
	var x draftScan
	if err := row.Scan(draftScanDestinations(&d, &x)...); err != nil {
		return nil, err
	}
	if err := d.finishScan(&x); err != nil {
		return nil, err
	}
	return &d, nil
}

func scanDraftRows(rows pgx.Rows) ([]ReelDraft, error) {
	var drafts []ReelDraft
	for rows.Next() {
		var d ReelDraft
		var x draftScan
		if err := rows.Scan(draftScanDestinations(&d, &x)...); err != nil {
			return nil, err
		}
		if err := d.finishScan(&x); err != nil {
			return nil, err
		}
		drafts = append(drafts, d)
	}
	return drafts, rows.Err()
}

// CreateDraft inserts a new reel draft.
func (s *Store) CreateDraft(ctx context.Context, d *ReelDraft) error {
	now := time.Now().UTC()
	d.CreatedAt = now
	d.UpdatedAt = now
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	if d.Status == "" {
		d.Status = "draft"
	}
	if d.ModerationStatus == "" {
		d.ModerationStatus = "pending"
	}
	creditsRaw, err := marshalDraftCredits(d.Credits)
	if err != nil {
		return err
	}
	genres := d.Genres
	if genres == nil {
		genres = []string{}
	}

	_, err = s.db.Exec(ctx, `
		INSERT INTO reel_drafts (`+draftCols+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$39,$40,$41,$42,$43,$44,$45,$46,$47,$48,$49,$50,$51,$52,$53)`,
		d.ID, d.AuthorID, d.MediaID,
		d.Title, d.Caption, d.Hashtags, d.Tags,
		d.Visibility, d.TopicID, d.Category, d.Language, d.SEOTitle,
		d.CrossPostPostbook, d.CrossPostPosttube, d.PublishToFeed,
		d.IsMadeForKids, d.PaidPromotion, d.AlteredContent,
		d.AutoChapters, d.FeaturedPlaces, d.AutoConcepts,
		d.License, d.AllowEmbedding, d.RemixSetting,
		d.LikesEnabled, d.CommentsEnabled, d.CommentModeration, d.CommentAccess,
		d.RecordingDate, d.RecordingLocation,
		d.AudioTrackID, d.AudioStartMs, d.OriginalAudioVol, d.OverlayAudioVol,
		d.CoverMediaID, d.ScheduleAt,
		d.Status, d.ModerationStatus, d.PublishedPostID,
		d.CreatedAt, d.UpdatedAt, d.BlockedReason,
		d.AIGenerated, d.LocationName, d.LocationPlaceID,
		d.ContentType,
		creditsRaw, genres, d.ReleaseYear, d.ProductionHouse,
		d.TierRequiredID,
		d.Action.labelArg(), d.Action.urlArg(),
	)
	return err
}

// GetDraft fetches a single draft by ID.
func (s *Store) GetDraft(ctx context.Context, draftID uuid.UUID) (*ReelDraft, error) {
	row := s.db.QueryRow(ctx, `SELECT `+draftCols+` FROM reel_drafts WHERE id = $1`, draftID)
	return scanDraft(row)
}

// UpdateDraft applies partial updates to a draft. Only non-nil fields in the map are updated.
//
// An edit revives a parked draft (LOW 11, 2026-10-30): a 'blocked' row
// goes back to 'draft' and loses its blocked_reason, as MarkReelDraftBlocked
// promises. A draft is only ever parked after its schedule_at came due, so a
// revived draft whose schedule_at has passed loses it too unless the same
// patch sets a new one: editing never publishes a reel on the spot (a studio
// that saves field by field would otherwise publish it half-edited). The
// author schedules it again or publishes it now; every reason a draft is
// parked for is checked again then.
//
// A patch that names the status itself (PublishDraft's reschedule) sets it
// as given. Setting schedule_at starts the publish backoff afresh
// (migration 079): a new time is at least five minutes ahead, so it cannot
// jump the claim queue; any other edit leaves the backoff as it is.
func (s *Store) UpdateDraft(ctx context.Context, draftID uuid.UUID, authorID uuid.UUID, updates map[string]interface{}) error {
	query, args := reelDraftUpdateSQL(draftID, authorID, updates, time.Now().UTC())
	_, err := s.db.Exec(ctx, query, args...)
	return err
}

// reelDraftUpdateSQL builds UpdateDraft's statement. Columns are written in
// name order so the statement is the same for the same patch.
func reelDraftUpdateSQL(draftID, authorID uuid.UUID, updates map[string]interface{}, now time.Time) (string, []interface{}) {
	cols := make([]string, 0, len(updates))
	for col := range updates {
		if col == "updated_at" {
			continue
		}
		cols = append(cols, col)
	}
	sort.Strings(cols)
	sets := make([]string, 0, len(cols)+6)
	args := make([]interface{}, 0, len(cols)+3)
	for _, col := range cols {
		args = append(args, updates[col])
		sets = append(sets, col+" = $"+itoa(len(args)))
	}
	args = append(args, now)
	sets = append(sets, "updated_at = $"+itoa(len(args)))
	_, setsStatus := updates["status"]
	_, setsSchedule := updates["schedule_at"]
	if !setsStatus {
		// Revive a parked draft. Every CASE reads the row as it was.
		sets = append(sets,
			"status = CASE WHEN status = 'blocked' THEN 'draft' ELSE status END",
			"blocked_reason = CASE WHEN status = 'blocked' THEN NULL ELSE blocked_reason END")
		if !setsSchedule {
			sets = append(sets,
				"schedule_at = CASE WHEN status = 'blocked' AND schedule_at <= NOW() THEN NULL ELSE schedule_at END")
		}
	}
	if setsSchedule {
		sets = append(sets, "publish_attempts = 0", "next_attempt_at = NULL")
	}
	args = append(args, draftID, authorID)
	query := `UPDATE reel_drafts SET ` + strings.Join(sets, ", ") +
		` WHERE id = $` + itoa(len(args)-1) + ` AND author_id = $` + itoa(len(args))
	return query, args
}

// ListDrafts returns drafts for a given author, newest first.
func (s *Store) ListDrafts(ctx context.Context, authorID uuid.UUID, limit int, cursor *time.Time) ([]ReelDraft, *time.Time, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	var rows pgx.Rows
	var err error
	if cursor != nil {
		rows, err = s.db.Query(ctx,
			`SELECT `+draftCols+` FROM reel_drafts
			 WHERE author_id = $1 AND status != 'deleted' AND created_at < $2
			 ORDER BY created_at DESC LIMIT $3`,
			authorID, *cursor, limit+1,
		)
	} else {
		rows, err = s.db.Query(ctx,
			`SELECT `+draftCols+` FROM reel_drafts
			 WHERE author_id = $1 AND status != 'deleted'
			 ORDER BY created_at DESC LIMIT $2`,
			authorID, limit+1,
		)
	}
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	drafts, err := scanDraftRows(rows)
	if err != nil {
		return nil, nil, err
	}

	var nextCursor *time.Time
	if len(drafts) > limit {
		t := drafts[limit].CreatedAt
		nextCursor = &t
		drafts = drafts[:limit]
	}
	return drafts, nextCursor, nil
}

// DeleteDraft soft-deletes a draft (sets status = 'deleted').
func (s *Store) DeleteDraft(ctx context.Context, draftID uuid.UUID, authorID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE reel_drafts SET status = 'deleted', updated_at = $1 WHERE id = $2 AND author_id = $3`,
		time.Now().UTC(), draftID, authorID,
	)
	return err
}

// GetScheduledDrafts returns drafts whose schedule_at has passed and are still in 'draft' status.
func (s *Store) GetScheduledDrafts(ctx context.Context, now time.Time, limit int) ([]ReelDraft, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+draftCols+` FROM reel_drafts
		 WHERE schedule_at IS NOT NULL AND schedule_at <= $1 AND status = 'draft'
		 ORDER BY schedule_at ASC LIMIT $2`,
		now, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDraftRows(rows)
}

// ClaimDueReelDrafts atomically claims due reel drafts (status draft →
// publishing_pending) under FOR UPDATE SKIP LOCKED so concurrent worker
// replicas never publish the same draft twice (Module 1 P0-5; Codex risk
// #10: the previous list-then-publish loop was unsafe across replicas).
// Drafts stuck in publishing_pending longer than staleAfter are reclaimed
// (dead worker); the draft-id-as-post-id idempotency makes retries safe.
//
// A draft whose last attempt failed transiently waits for next_attempt_at
// (migration 079) and is ordered by it, so drafts that keep failing never
// hold the front of the queue.
func (s *Store) ClaimDueReelDrafts(ctx context.Context, now time.Time, staleAfter time.Duration, limit int) ([]ReelDraft, error) {
	rows, err := s.db.Query(ctx, `
		WITH due AS (
			SELECT id FROM reel_drafts
			WHERE (status = 'draft' AND schedule_at IS NOT NULL AND schedule_at <= $1
			       AND (next_attempt_at IS NULL OR next_attempt_at <= $1))
			   OR (status = 'publishing_pending' AND updated_at < $2)
			ORDER BY COALESCE(next_attempt_at, schedule_at) ASC
			LIMIT $3
			FOR UPDATE SKIP LOCKED
		)
		UPDATE reel_drafts d
		SET status = 'publishing_pending', updated_at = NOW()
		FROM due WHERE d.id = due.id
		RETURNING `+prefixedDraftCols("d"),
		now, now.Add(-staleAfter), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDraftRows(rows)
}

// ReelDraftRetryPlan decides what happens to a claimed draft whose
// publication failed transiently, given the number of failed attempts
// including this one: try again at retryAt, or park it with blockReason
// (non-empty) when it has failed too often.
type ReelDraftRetryPlan func(attempts int) (retryAt time.Time, blockReason string)

// DeferReelDraftPublish returns a claimed reel draft after a transient
// publish failure: counted, and either back to 'draft' with its next
// attempt time or, when plan says so, parked as 'blocked'. Conditional on
// the claim, like MarkReelDraftBlocked: a draft already parked, published
// or edited back is left alone (parked=false, nil).
func (s *Store) DeferReelDraftPublish(ctx context.Context, draftID uuid.UUID, plan ReelDraftRetryPlan) (parked bool, err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var attempts int
	err = tx.QueryRow(ctx,
		`SELECT publish_attempts FROM reel_drafts
		 WHERE id = $1 AND status = 'publishing_pending' FOR UPDATE`, draftID).Scan(&attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	attempts++
	retryAt, reason := plan(attempts)
	if reason != "" {
		_, err = tx.Exec(ctx,
			`UPDATE reel_drafts SET status = 'blocked', blocked_reason = $2, publish_attempts = $3,
			        next_attempt_at = NULL, updated_at = NOW()
			 WHERE id = $1`, draftID, reason, attempts)
	} else {
		_, err = tx.Exec(ctx,
			`UPDATE reel_drafts SET status = 'draft', publish_attempts = $2, next_attempt_at = $3, updated_at = NOW()
			 WHERE id = $1`, draftID, attempts, retryAt)
	}
	if err != nil {
		return false, err
	}
	return reason != "", tx.Commit(ctx)
}

// MarkReelDraftBlocked parks a claimed reel draft (migration 055): the
// schedule worker could not publish it because trust-safety refused the
// author or stayed unknown for 24 h, or the draft itself was refused
// (service.reelDraftBlockReason). Conditional on the claim so a stale
// worker cannot block a draft the author has since edited back to 'draft'.
// The author re-arms it by editing: UpdateDraft sets it back to draft (and
// drops a schedule_at that has passed, so it is scheduled again on purpose).
func (s *Store) MarkReelDraftBlocked(ctx context.Context, draftID uuid.UUID, reason string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE reel_drafts SET status = 'blocked', blocked_reason = $2, updated_at = NOW()
		 WHERE id = $1 AND status = 'publishing_pending'`, draftID, reason)
	return err
}

// MarkDraftPublished updates a draft's status to 'published' and links it to the post.
func (s *Store) MarkDraftPublished(ctx context.Context, draftID uuid.UUID, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE reel_drafts SET status = 'published', published_post_id = $1, updated_at = $2 WHERE id = $3`,
		postID, time.Now().UTC(), draftID,
	)
	return err
}

// itoa converts an int to string without importing strconv.
func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}
