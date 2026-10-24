package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Trending sounds (2026-10-24): the uses of each sound, as media-service's
// ranking worker reads them through GET /v1/internal/sound-uses
// (service/sound_uses.go).
//
// A use is a post that links the sound (posts.audio_track_id) and plays to
// the whole internet: the same rows the signed-out playback rule admits
// (service anonymousMayAccessPost). The SQL below is the row-level half of
// that rule, so the page carries only candidates; the service then runs the
// rule itself on every row, with the author gate (private account, hidden
// author) it alone can resolve. The two halves cannot fork: a row the SQL
// let through is judged again by the one Go rule.
//
//   - a reel / flick or a long video (content_type), never a feed post
//   - live and effectively approved (ViewerEligibleSQL: not deleted, not
//     scheduled, no hold and no active restriction)
//   - visibility exactly 'public', not 18+, not members-only
//   - published inside [since, until). Publication is created_at: the
//     schedule worker moves created_at to the moment it publishes
//     (scheduled.go), so a scheduled post counts from its publication.
//   - never the sound's own source reel: neither the post the sound names
//     (audio_tracks.source_reel_id) nor a post that carries the sound's
//     source video (post_media.media_id = source_media_id).
//
// Paged by sound id, so a page holds every candidate row of the sounds it
// names and a sound's counts are never split across two pages.

// MaxSoundUsesPage bounds the sounds of one page.
const MaxSoundUsesPage = 1000

// soundUseKinds are the content types a use is counted on.
const soundUseKinds = `('flick', 'reel', 'long_video', 'video')`

// SoundUse is one candidate post of a sound: the fields the signed-out
// playback rule reads, and when it was published.
type SoundUse struct {
	SoundID                uuid.UUID
	PostID                 uuid.UUID
	AuthorID               uuid.UUID
	PublishedAt            time.Time
	Visibility             string
	ReviewStatus           string
	ActiveRestrictionCount int
	DeletedAt              *time.Time
	PublishAt              *time.Time
	AgeRestricted          bool
	TierRequiredID         *uuid.UUID
}

// AsPost is the row as the playback rule judges a post.
func (u SoundUse) AsPost() *Post {
	return &Post{
		ID:                     u.PostID,
		AuthorID:               u.AuthorID,
		Visibility:             u.Visibility,
		ReviewStatus:           u.ReviewStatus,
		ActiveRestrictionCount: u.ActiveRestrictionCount,
		DeletedAt:              u.DeletedAt,
		PublishAt:              u.PublishAt,
		AgeRestricted:          u.AgeRestricted,
		TierRequiredID:         u.TierRequiredID,
		AudioTrackID:           &u.SoundID,
		CreatedAt:              u.PublishedAt,
	}
}

// soundUseCandidateSQL is the row-level predicate on posts aliased p.
func soundUseCandidateSQL() string {
	return `p.audio_track_id IS NOT NULL
		  AND p.content_type IN ` + soundUseKinds + `
		  AND ` + ViewerEligibleSQL("p") + `
		  AND p.visibility = 'public'
		  AND COALESCE(p.age_restricted, FALSE) = FALSE
		  AND p.tier_required_id IS NULL
		  AND p.created_at >= $1 AND p.created_at < $2`
}

// SoundUseRows returns the candidate uses of the sounds after `after` (by
// id), at most `limit` sounds, ordered by sound then post. next is the last
// sound of a full page, nil when there are no more.
func (s *Store) SoundUseRows(ctx context.Context, since, until time.Time, after uuid.UUID, limit int) ([]SoundUse, *uuid.UUID, error) {
	if limit < 1 || limit > MaxSoundUsesPage {
		return nil, nil, errors.New("sound uses page limit out of range")
	}
	if !since.Before(until) {
		return nil, nil, errors.New("sound uses window is empty")
	}
	candidate := soundUseCandidateSQL()
	rows, err := s.db.Query(ctx, `
		WITH page AS (
			SELECT DISTINCT p.audio_track_id AS sound_id
			  FROM posts p
			 WHERE `+candidate+`
			   AND p.audio_track_id > $3
			 ORDER BY 1
			 LIMIT $4
		)
		SELECT p.audio_track_id, p.id, p.author_id, p.created_at,
		       COALESCE(p.visibility, ''), COALESCE(p.review_status, ''),
		       COALESCE(p.active_restriction_count, 0), p.deleted_at, p.publish_at,
		       COALESCE(p.age_restricted, FALSE), p.tier_required_id,
		       page.sound_id
		  FROM page
		  LEFT JOIN audio_tracks t ON t.id = page.sound_id
		  LEFT JOIN posts p ON p.audio_track_id = page.sound_id
		   AND `+candidate+`
		   AND (t.source_reel_id IS NULL OR p.id <> t.source_reel_id)
		   AND (t.source_media_id IS NULL OR NOT EXISTS (
		        SELECT 1 FROM post_media pm
		         WHERE pm.post_id = p.id AND pm.media_id = t.source_media_id))
		 ORDER BY page.sound_id, p.id
	`, since, until, after, limit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var out []SoundUse
	sounds := 0
	var last uuid.UUID
	for rows.Next() {
		var (
			u        SoundUse
			soundID  *uuid.UUID
			postID   *uuid.UUID
			authorID *uuid.UUID
			at       *time.Time
			pageID   uuid.UUID
		)
		if err := rows.Scan(&soundID, &postID, &authorID, &at,
			&u.Visibility, &u.ReviewStatus, &u.ActiveRestrictionCount, &u.DeletedAt, &u.PublishAt,
			&u.AgeRestricted, &u.TierRequiredID, &pageID); err != nil {
			return nil, nil, err
		}
		if pageID != last || sounds == 0 {
			sounds++
			last = pageID
		}
		// A sound of the page whose only candidates were its source reel:
		// counted as a page member (so paging moves past it), no use.
		if postID == nil || soundID == nil || authorID == nil || at == nil {
			continue
		}
		u.SoundID, u.PostID, u.AuthorID, u.PublishedAt = *soundID, *postID, *authorID, *at
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if sounds < limit {
		return out, nil, nil
	}
	next := last
	return out, &next, nil
}
