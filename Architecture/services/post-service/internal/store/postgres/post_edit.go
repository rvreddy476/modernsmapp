package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

/*
	Edit after publish (MTube, 2026-09-27; migration 051 part F).

	PATCH /v1/posts/:id lets the owner change the descriptive fields of a
	post — never its media. Every write here:

	  * is a single UPDATE on the row, guarded by author_id, so the store
	    re-checks ownership even though the service already did;
	  * writes one append-only post_edit_audit row in the same transaction
	    with {field: {from, to}} for every scalar that changed (text as
	    {"changed": true} only — no body in the audit);
	  * bumps the search revision and emits PostSearchEligibilityChanged in
	    the same transaction when visibility changed (the same choke point
	    PublishPost and the moderation writes use), so search and every
	    feed projection learn about a public/unlisted/private flip.
*/

// PostEditPatch is a partial edit: a nil pointer is "leave it alone". The
// service has already validated every value (taxonomy, visibility enum,
// title / text ceilings, hashtag alphabet, media ownership).
type PostEditPatch struct {
	Title         *string
	Text          *string
	Tags          *[]string
	Hashtags      *[]string
	Category      *string
	Visibility    *string
	CoverMediaID  *uuid.UUID
	AllowDownload *bool
	NoComments    *bool
	MadeForKids   *bool
	Language      *string

	// Mentions and tagged people (2026-10-24): the lists AFTER the
	// who_can_mention / who_can_tag drop. MentionUsernames replaces
	// posts.mention_usernames and, in this transaction, the post's
	// post_mentions rows; TaggedUserIDs replaces posts.tagged_user_ids.
	MentionUsernames *[]string
	TaggedUserIDs    *[]uuid.UUID

	// Creator Hub (2026-09-28): the create route's columns (006/038) and
	// the migration 052 settings. ClearRecordingDate / ClearRelatedPost
	// write NULL ("" on the wire); they win over the value pointers.
	PaidPromotion      *bool
	AlteredContent     *bool
	License            *string
	AllowEmbedding     *bool
	RecordingDate      *time.Time
	ClearRecordingDate bool
	RecordingLocation  *string
	RemixSetting       *string
	CommentModeration  *string
	CommentAccess      *string
	AgeRestricted      *bool
	HideLikeCount      *bool
	DefaultCommentSort *string
	RelatedPostID      *uuid.UUID
	ClearRelatedPost   bool

	// Post screens parity (migration 066 + the 007 is_branded column).
	// ClearLinkURL writes NULL ("" on the wire) and wins over LinkURL.
	IsBranded    *bool
	AIGenerated  *bool
	LinkURL      *string
	ClearLinkURL bool

	// Video credits (migration 070). Genres replaces the list ([] clears);
	// ClearReleaseYear / ClearProductionHouse write NULL and win over the
	// value pointers. Credits, when set, replaces the credit list in this
	// transaction (credits.go) and its Result is filled in.
	Genres               *[]string
	ReleaseYear          *int
	ClearReleaseYear     bool
	ProductionHouse      *string
	ClearProductionHouse bool
	Credits              *CreditsWrite

	// Members-only gate (2026-10-23): TierRequiredID sets tier_required_id
	// (the service checked it is one of the author's active tiers);
	// ClearTierRequired writes NULL and wins. A change bumps search_rev and
	// emits PostSearchEligibilityChanged (carrying the gate) in this
	// transaction, as PUT /membership does.
	TierRequiredID    *uuid.UUID
	ClearTierRequired bool

	// Distribution replaces posts.distribution (the service built it from
	// the stored policy plus notify_subscribers). When set, the same
	// transaction bumps distribution_rev and writes DistributionEvent(rev)
	// to the outbox, exactly as PATCH /distribution does.
	// DistributionChange is the audit entry for it.
	Distribution       json.RawMessage
	DistributionEvent  func(rev int64) (eventType string, payload interface{})
	DistributionChange *PostEditChange

	// AuditAction names the post_edit_audit row: "post.edit" (the default)
	// or "post.bulk_edit".
	AuditAction string
}

// PostEditChange is one field's before/after in the audit row.
type PostEditChange struct {
	From any `json:"from,omitempty"`
	To   any `json:"to,omitempty"`
	// Changed is set instead of From/To for the post body.
	Changed bool `json:"changed,omitempty"`
}

// ErrPostEditNotOwned: the row exists but author_id is not the caller
// (or the post is deleted). The service maps it to 403 after it has
// already established existence, so the two never disagree.
var ErrPostEditNotOwned = errors.New("post is not owned by the caller")

// UpdatePostFields applies patch to the caller's post and returns the row
// as it now stands (media attached). Returns pgx.ErrNoRows for a post that
// does not exist or is deleted, ErrPostEditNotOwned for someone else's.
func (s *Store) UpdatePostFields(ctx context.Context, postID, actorID uuid.UUID, patch PostEditPatch) (*Post, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	before, err := scanPost(tx.QueryRow(ctx,
		`SELECT `+postCols+` FROM posts WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, postID))
	if err != nil {
		return nil, err // pgx.ErrNoRows for a missing / deleted post
	}
	if before.AuthorID != actorID {
		return nil, ErrPostEditNotOwned
	}
	// A post under an active hold (copyright case or title review,
	// migrations 056 / 073) may neither gain nor keep a members-only tier:
	// judged under the row lock, so a hold placed concurrently is seen.
	if before.ActiveRestrictionCount > 0 && editResultTier(before, patch) != nil {
		return nil, ErrPostCopyrightHeld
	}

	changes := postEditChanges(before, patch)
	if len(changes) == 0 && patch.Credits == nil {
		// Nothing to write; still answer the current row.
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return s.GetPost(ctx, postID)
	}

	after, err := scanPost(tx.QueryRow(ctx, `
		UPDATE posts SET
			title            = COALESCE($3, title),
			text             = COALESCE($4, text),
			tags             = COALESCE($5, tags),
			hashtags         = COALESCE($6, hashtags),
			category         = COALESCE($7, category),
			visibility       = COALESCE($8, visibility),
			cover_media_id   = COALESCE($9, cover_media_id),
			allow_download   = COALESCE($10, allow_download),
			no_comments      = COALESCE($11, no_comments),
			is_made_for_kids = COALESCE($12, is_made_for_kids),
			language         = COALESCE($13, language),
			paid_promotion       = COALESCE($14, paid_promotion),
			altered_content      = COALESCE($15, altered_content),
			license              = COALESCE($16, license),
			allow_embedding      = COALESCE($17, allow_embedding),
			recording_date       = CASE WHEN $19::boolean THEN NULL ELSE COALESCE($18::date, recording_date) END,
			recording_location   = COALESCE($20, recording_location),
			remix_setting        = COALESCE($21, remix_setting),
			comment_moderation   = COALESCE($22, comment_moderation),
			comment_access       = COALESCE($23, comment_access),
			age_restricted       = COALESCE($24, age_restricted),
			hide_like_count      = COALESCE($25, hide_like_count),
			default_comment_sort = COALESCE($26, default_comment_sort),
			related_post_id      = CASE WHEN $28::boolean THEN NULL ELSE COALESCE($27::uuid, related_post_id) END,
			distribution         = COALESCE($29::jsonb, distribution),
			distribution_rev     = distribution_rev + CASE WHEN $29::jsonb IS NULL THEN 0 ELSE 1 END,
			is_branded           = COALESCE($30, is_branded),
			ai_generated         = COALESCE($31, ai_generated),
			link_url             = CASE WHEN $33::boolean THEN NULL ELSE COALESCE($32::text, link_url) END,
			genres               = COALESCE($34::text[], genres),
			release_year         = CASE WHEN $36::boolean THEN NULL ELSE COALESCE($35::int, release_year) END,
			production_house     = CASE WHEN $38::boolean THEN NULL ELSE COALESCE($37::text, production_house) END,
			tier_required_id     = CASE WHEN $40::boolean THEN NULL ELSE COALESCE($39::uuid, tier_required_id) END,
			mention_usernames    = COALESCE($41::text[], mention_usernames),
			tagged_user_ids      = COALESCE($42::uuid[], tagged_user_ids),
			updated_at       = NOW()
		WHERE id = $1 AND author_id = $2 AND deleted_at IS NULL
		RETURNING `+postCols,
		postID, actorID,
		patch.Title, patch.Text, textArrayArg(patch.Tags), textArrayArg(patch.Hashtags),
		patch.Category, patch.Visibility, patch.CoverMediaID,
		patch.AllowDownload, patch.NoComments, patch.MadeForKids, patch.Language,
		patch.PaidPromotion, patch.AlteredContent, patch.License, patch.AllowEmbedding,
		patch.RecordingDate, patch.ClearRecordingDate, patch.RecordingLocation,
		patch.RemixSetting, patch.CommentModeration, patch.CommentAccess,
		patch.AgeRestricted, patch.HideLikeCount, patch.DefaultCommentSort,
		patch.RelatedPostID, patch.ClearRelatedPost, jsonArg(patch.Distribution),
		patch.IsBranded, patch.AIGenerated, patch.LinkURL, patch.ClearLinkURL,
		textArrayArg(patch.Genres), patch.ReleaseYear, patch.ClearReleaseYear,
		patch.ProductionHouse, patch.ClearProductionHouse,
		patch.TierRequiredID, patch.ClearTierRequired,
		textArrayArg(patch.MentionUsernames), uuidArrayArg(patch.TaggedUserIDs)))
	if err != nil {
		return nil, fmt.Errorf("update post fields: %w", err)
	}
	// post_mentions follows the stored list (the mentions inbox reads it):
	// a mention the edit removed, or dropped for who_can_mention, goes.
	if _, changed := changes["mentions"]; changed {
		if err := syncPostMentionsTx(ctx, tx, postID, after.ContentType, *patch.MentionUsernames); err != nil {
			return nil, err
		}
	}

	// Video credits (credits.go): replaced in this transaction, before the
	// search bump below so the eligibility event carries the new list.
	if patch.Credits != nil {
		patch.Credits.PostTitle = after.Title
		if err := replaceCreditsTx(ctx, tx, postID, actorID, patch.Credits); err != nil {
			return nil, err
		}
		if patch.Credits.Result.Changed {
			changes["credits"] = PostEditChange{Changed: true}
		}
	}

	action := patch.AuditAction
	if action == "" {
		action = "post.edit"
	}
	if len(changes) > 0 {
		if err := insertPostEditAudit(ctx, tx, postID, actorID, action, changes); err != nil {
			return nil, err
		}
	}
	if patch.Distribution != nil && patch.DistributionEvent != nil {
		eventType, payload := patch.DistributionEvent(after.DistributionRev)
		if err := InsertOutboxEventTx(ctx, tx, eventType, "post", postID, payload); err != nil {
			return nil, fmt.Errorf("emit distribution update on edit: %w", err)
		}
	}
	// Search re-reads the post when its visibility or anything search
	// indexes about a long video's credits changed (migration 070): the
	// document is replaced whole by the eligibility event.
	if creditSearchChanged(changes) || tierChanged(changes) {
		if _, visChanged := changes["visibility"]; !visChanged {
			if err := BumpSearchRevAndEmitTx(ctx, tx, postID); err != nil {
				return nil, fmt.Errorf("emit search eligibility on credits edit: %w", err)
			}
		}
	}
	if vis, changed := changes["visibility"]; changed {
		if err := BumpSearchRevAndEmitTx(ctx, tx, postID); err != nil {
			return nil, fmt.Errorf("emit search eligibility on edit: %w", err)
		}
		// Feed: the post's timeline rows were written for the old
		// visibility (an unlisted post must leave followers' feeds, a
		// public one must reach them). Not for a scheduled post: it has
		// not been fanned out, and the schedule worker's PostCreated will
		// carry the visibility it publishes with.
		if after.PublishAt == nil {
			old, _ := vis.From.(string)
			if err := InsertOutboxEventTx(ctx, tx, events.EventPostVisibilityChanged, "post", postID, PostVisibilityChangedEvent{
				PostVisibilityChangedPayload: events.PostVisibilityChangedPayload{
					PostID:        postID.String(),
					AuthorID:      after.AuthorID.String(),
					ContentType:   after.ContentType,
					OldVisibility: old,
					NewVisibility: after.Visibility,
					CreatedAt:     after.CreatedAt,
					ChangedAt:     time.Now().UTC(),
					ChannelID:     channelIDForAuthorTx(ctx, tx, after.AuthorID),
				},
				Reason: VisibilityReasonAudience,
			}); err != nil {
				return nil, fmt.Errorf("emit visibility change on edit: %w", err)
			}
		}
	}
	// A members-only gate added or changed by the edit: every non-member's
	// cached copy goes (visibility_events.go). The helper skips a scheduled
	// post.
	if tierChanged(changes) && gateNarrows(before.TierRequiredID, after.TierRequiredID) {
		if err := emitVisibilityNarrowedTx(ctx, tx, postID, VisibilityReasonMembershipGate); err != nil {
			return nil, err
		}
	}
	// Offline copies (2026-10-02, offline_copies.go), in this transaction:
	// turning downloads off revokes every viewer's copy, and making the post
	// private revokes the copies of everyone it is not shared with. The
	// owner's own copies stay either way.
	if _, changed := changes["allow_download"]; changed && !after.AllowDownload {
		if err := RevokePostOfflineCopiesTx(ctx, tx, []uuid.UUID{postID}, OfflineRevokeNotAllowed, actorID, false); err != nil {
			return nil, err
		}
	}
	if _, changed := changes["visibility"]; changed && after.Visibility == "private" {
		if err := RevokePostOfflineCopiesTx(ctx, tx, []uuid.UUID{postID}, OfflineRevokePrivate, actorID, true); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	media, err := s.loadPostMediaForPost(ctx, postID)
	if err != nil {
		return nil, err
	}
	after.Media = media
	return after, nil
}

// textArrayArg turns an optional slice into a nullable TEXT[] argument: nil
// pointer -> SQL NULL (COALESCE keeps the column), empty slice -> '{}'.
func textArrayArg(v *[]string) interface{} {
	if v == nil {
		return nil
	}
	if *v == nil {
		return []string{}
	}
	return *v
}

// uuidArrayArg is textArrayArg for UUID[]: nil pointer -> SQL NULL, empty
// slice -> '{}'.
func uuidArrayArg(v *[]uuid.UUID) interface{} {
	if v == nil {
		return nil
	}
	if *v == nil {
		return []uuid.UUID{}
	}
	return *v
}

// syncPostMentionsTx makes the post's post_mentions rows exactly usernames
// (post_mentions.mentioned_user_id holds the username; see mentions.go).
func syncPostMentionsTx(ctx context.Context, tx pgx.Tx, postID uuid.UUID, postType string, usernames []string) error {
	if usernames == nil {
		usernames = []string{}
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM post_mentions WHERE post_id = $1 AND NOT (mentioned_user_id = ANY($2::text[]))`,
		postID, usernames); err != nil {
		return fmt.Errorf("sync post mentions (delete): %w", err)
	}
	for _, u := range usernames {
		if _, err := tx.Exec(ctx, `
			INSERT INTO post_mentions (post_id, post_type, mentioned_user_id)
			VALUES ($1, $2, $3)
			ON CONFLICT (post_id, mentioned_user_id) DO NOTHING`, postID, postType, u); err != nil {
			return fmt.Errorf("sync post mentions (insert): %w", err)
		}
	}
	return nil
}

// postEditChanges diffs the patch against the row so the audit records only
// what actually changed, and so a no-op PATCH writes nothing.
func postEditChanges(before *Post, patch PostEditPatch) map[string]PostEditChange {
	changes := map[string]PostEditChange{}
	str := func(field string, cur string, next *string) {
		if next != nil && *next != cur {
			changes[field] = PostEditChange{From: cur, To: *next}
		}
	}
	boolean := func(field string, cur bool, next *bool) {
		if next != nil && *next != cur {
			changes[field] = PostEditChange{From: cur, To: *next}
		}
	}
	str("title", before.Title, patch.Title)
	if patch.Text != nil && *patch.Text != before.Text {
		changes["text"] = PostEditChange{Changed: true}
	}
	if patch.Tags != nil && !sameStrings(before.Tags, *patch.Tags) {
		changes["tags"] = PostEditChange{From: nonNil(before.Tags), To: nonNil(*patch.Tags)}
	}
	if patch.Hashtags != nil && !sameStrings(before.Hashtags, *patch.Hashtags) {
		changes["hashtags"] = PostEditChange{From: nonNil(before.Hashtags), To: nonNil(*patch.Hashtags)}
	}
	if patch.MentionUsernames != nil && !sameStrings(before.MentionUsernames, *patch.MentionUsernames) {
		changes["mentions"] = PostEditChange{From: nonNil(before.MentionUsernames), To: nonNil(*patch.MentionUsernames)}
	}
	if patch.TaggedUserIDs != nil && !sameUUIDs(before.TaggedUserIDs, *patch.TaggedUserIDs) {
		changes["tagged_user_ids"] = PostEditChange{From: uuidStrings(before.TaggedUserIDs), To: uuidStrings(*patch.TaggedUserIDs)}
	}
	str("category", before.Category, patch.Category)
	str("visibility", before.Visibility, patch.Visibility)
	if patch.CoverMediaID != nil && (before.CoverMediaID == nil || *before.CoverMediaID != *patch.CoverMediaID) {
		var from any
		if before.CoverMediaID != nil {
			from = before.CoverMediaID.String()
		}
		changes["cover_media_id"] = PostEditChange{From: from, To: patch.CoverMediaID.String()}
	}
	boolean("allow_download", before.AllowDownload, patch.AllowDownload)
	boolean("no_comments", before.NoComments, patch.NoComments)
	boolean("is_made_for_kids", before.IsMadeForKids, patch.MadeForKids)
	str("language", before.Language, patch.Language)

	boolean("paid_promotion", before.PaidPromotion, patch.PaidPromotion)
	boolean("altered_content", before.AlteredContent, patch.AlteredContent)
	str("license", before.License, patch.License)
	boolean("allow_embedding", before.AllowEmbedding, patch.AllowEmbedding)
	if from, to := dateString(before.RecordingDate), nextRecordingDate(before.RecordingDate, patch); from != to {
		changes["recording_date"] = PostEditChange{From: from, To: to}
	}
	str("recording_location", before.RecordingLocation, patch.RecordingLocation)
	str("remix_setting", before.RemixSetting, patch.RemixSetting)
	str("comment_moderation", before.CommentModeration, patch.CommentModeration)
	str("comment_access", before.CommentAccess, patch.CommentAccess)
	boolean("age_restricted", before.AgeRestricted, patch.AgeRestricted)
	boolean("hide_like_count", before.HideLikeCount, patch.HideLikeCount)
	str("default_comment_sort", before.DefaultCommentSort, patch.DefaultCommentSort)
	if from, to := uuidString(before.RelatedPostID), nextRelatedPost(before.RelatedPostID, patch); from != to {
		changes["related_post_id"] = PostEditChange{From: from, To: to}
	}
	if patch.Distribution != nil && patch.DistributionChange != nil {
		changes["notify_subscribers"] = *patch.DistributionChange
	}
	boolean("is_branded", before.IsBranded, patch.IsBranded)
	boolean("ai_generated", before.AIGenerated, patch.AIGenerated)
	if from, to := optString(before.LinkURL), nextLinkURL(before.LinkURL, patch); from != to {
		changes["link_url"] = PostEditChange{From: from, To: to}
	}
	if patch.Genres != nil && !sameStrings(before.Genres, *patch.Genres) {
		changes["genres"] = PostEditChange{From: nonNil(before.Genres), To: nonNil(*patch.Genres)}
	}
	if from, to := optInt(before.ReleaseYear), nextReleaseYear(before.ReleaseYear, patch); from != to {
		changes["release_year"] = PostEditChange{From: from, To: to}
	}
	if from, to := optString(before.ProductionHouse), nextProductionHouse(before.ProductionHouse, patch); from != to {
		changes["production_house"] = PostEditChange{From: from, To: to}
	}
	if from, to := uuidString(before.TierRequiredID), nextTierRequired(before.TierRequiredID, patch); from != to {
		changes["tier_required_id"] = PostEditChange{From: from, To: to}
	}
	return changes
}

// tierChanged reports an edit to the members-only gate: search must drop a
// newly gated post and re-admit one whose gate was cleared.
func tierChanged(changes map[string]PostEditChange) bool {
	_, ok := changes["tier_required_id"]
	return ok
}

func nextTierRequired(cur *uuid.UUID, patch PostEditPatch) string {
	switch {
	case patch.ClearTierRequired:
		return ""
	case patch.TierRequiredID != nil:
		return patch.TierRequiredID.String()
	}
	return uuidString(cur)
}

// creditSearchChanged reports an edit to what search indexes about a long
// video's credits (credit names and roles, genres, year, production house).
func creditSearchChanged(changes map[string]PostEditChange) bool {
	for _, k := range []string{"credits", "genres", "release_year", "production_house"} {
		if _, ok := changes[k]; ok {
			return true
		}
	}
	return false
}

// optInt renders a nullable INT column for the diff (0 for NULL; 0 is not a
// year any row may hold).
func optInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func nextReleaseYear(cur *int, patch PostEditPatch) int {
	switch {
	case patch.ClearReleaseYear:
		return 0
	case patch.ReleaseYear != nil:
		return *patch.ReleaseYear
	}
	return optInt(cur)
}

func nextProductionHouse(cur *string, patch PostEditPatch) string {
	switch {
	case patch.ClearProductionHouse:
		return ""
	case patch.ProductionHouse != nil:
		return *patch.ProductionHouse
	}
	return optString(cur)
}

// dateString renders a DATE column value the way the wire carries it
// ("YYYY-MM-DD", "" for NULL) so the diff compares days, not instants.
func dateString(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

func nextRecordingDate(cur *time.Time, patch PostEditPatch) string {
	switch {
	case patch.ClearRecordingDate:
		return ""
	case patch.RecordingDate != nil:
		return dateString(patch.RecordingDate)
	}
	return dateString(cur)
}

func uuidString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

func nextRelatedPost(cur *uuid.UUID, patch PostEditPatch) string {
	switch {
	case patch.ClearRelatedPost:
		return ""
	case patch.RelatedPostID != nil:
		return patch.RelatedPostID.String()
	}
	return uuidString(cur)
}

// jsonArg is a nullable JSONB argument: empty -> SQL NULL (COALESCE keeps
// the column).
func jsonArg(raw json.RawMessage) interface{} {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func sameUUIDs(a, b []uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func insertPostEditAudit(ctx context.Context, tx pgx.Tx, postID, actorID uuid.UUID, action string, changes map[string]PostEditChange) error {
	body, err := json.Marshal(changes)
	if err != nil {
		return fmt.Errorf("marshal edit audit: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO post_edit_audit (post_id, actor_user_id, action, changes)
		VALUES ($1, $2, $3, $4)`, postID, actorID, action, body); err != nil {
		return fmt.Errorf("insert edit audit: %w", err)
	}
	return nil
}

// BulkVisibilityOutcome is one id's result from the bulk routes
// (POST /v1/uploads/bulk and /bulk-delete): ok, or the error code the single
// route would have answered for that post. The name predates the widening.
type BulkVisibilityOutcome struct {
	ID    uuid.UUID `json:"id"`
	OK    bool      `json:"ok"`
	Error string    `json:"error,omitempty"`
}

// PostAuthorsByIDs returns author_id for each live post id (missing and
// deleted ids are absent), for the bulk route's owner-of-all check.
func (s *Store) PostAuthorsByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
	out := make(map[uuid.UUID]uuid.UUID, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `SELECT id, author_id FROM posts WHERE id = ANY($1) AND deleted_at IS NULL`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, author uuid.UUID
		if err := rows.Scan(&id, &author); err != nil {
			return nil, err
		}
		out[id] = author
	}
	return out, rows.Err()
}

// CreatorCounts is GET /v1/posts/me/summary's content half.
type CreatorCounts struct {
	Videos      int64
	Shorts      int64
	Live        int64
	Collections int64
}

// CountCreatorContent counts the author's live (not deleted) posts by kind:
// long videos, shorts, and posts that came from a live stream (source =
// 'live'), plus their user playlists. Scheduled posts count: the Creator
// Hub shows them.
func (s *Store) CountCreatorContent(ctx context.Context, authorID uuid.UUID) (CreatorCounts, error) {
	var c CreatorCounts
	err := s.db.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN content_type IN ('video', 'long_video') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN content_type IN ('flick', 'reel') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN source = 'live' THEN 1 ELSE 0 END), 0),
			(SELECT COUNT(*) FROM playlists WHERE creator_id = $1 AND kind = 'user')
		FROM posts
		WHERE author_id = $1 AND deleted_at IS NULL`, authorID,
	).Scan(&c.Videos, &c.Shorts, &c.Live, &c.Collections)
	return c, err
}

// UpdatePostCategory sets posts.category (already validated against the
// taxonomy by the service) on a live post.
func (s *Store) UpdatePostCategory(ctx context.Context, postID uuid.UUID, category string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE posts SET category = $2, updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`,
		postID, category)
	return err
}

// optString renders a nullable TEXT column the way the diff compares it
// ("" for NULL).
func optString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func nextLinkURL(cur *string, patch PostEditPatch) string {
	switch {
	case patch.ClearLinkURL:
		return ""
	case patch.LinkURL != nil:
		return *patch.LinkURL
	}
	return optString(cur)
}
