package service

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/post-service/internal/store/postgres"
)

// The scheduled reel-draft queue (2026-10-30, audit HIGH 1).
//
// The schedule worker claims the 50 due drafts with the earliest time. A
// draft that failed used to be released with its schedule_at unchanged, so
// it was first in line on every tick; 50 drafts that could never publish
// held the whole window and nobody else's scheduled reel was claimed again.
//
//   - A draft is held to CreatePost's rules when it is written (UpdateDraft,
//     CreateDraft) and when it is scheduled, so it cannot be saved in a
//     shape the publish would refuse.
//   - When the publish refuses the draft itself (reelDraftBlockReason), the
//     draft is parked as 'blocked' with the reason; the author sees it and
//     edits it, which revives it (store UpdateDraft).
//   - Anything else is transient: the draft goes back with a next attempt
//     time that doubles up to an hour (reelDraftRetryDelay), and is parked
//     as publish_failed after maxReelDraftPublishAttempts (about a day). A
//     draft waiting on its next attempt is not claimed, so it holds no
//     place in the window.

// ErrDraftNeedsVideo: a reel draft without a video cannot be scheduled or
// published; a reel is its video.
var ErrDraftNeedsVideo = errors.New("a reel draft needs a video before it can be published")

const (
	// BlockReasonPublishFailed parks a draft whose publication kept
	// failing for a reason nobody named (maxReelDraftPublishAttempts).
	BlockReasonPublishFailed = "publish_failed"

	// maxReelDraftPublishAttempts transient failures park the draft. With
	// reelDraftRetryDelay that is a little over a day of trying.
	maxReelDraftPublishAttempts = 30
	// reelDraftRetryMax caps the wait between two attempts.
	reelDraftRetryMax = time.Hour
)

// reelDraftRetryDelay is the wait after the attempts-th failed attempt:
// one minute, doubling, at most an hour.
func reelDraftRetryDelay(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := time.Minute
	for i := 1; i < attempts; i++ {
		d *= 2
		if d >= reelDraftRetryMax {
			return reelDraftRetryMax
		}
	}
	return d
}

// reelDraftRetryPlan is the store's plan for a transient failure seen at
// now: retry after reelDraftRetryDelay, or park once the draft has failed
// maxReelDraftPublishAttempts times.
func reelDraftRetryPlan(now time.Time) postgres.ReelDraftRetryPlan {
	return func(attempts int) (time.Time, string) {
		if attempts >= maxReelDraftPublishAttempts {
			return time.Time{}, BlockReasonPublishFailed
		}
		return now.Add(reelDraftRetryDelay(attempts)), ""
	}
}

// reelDraftRefusals are the publish refusals that are about the draft and
// will be the same on every retry, with the reason the author is shown.
// Media that is still processing, an unreachable authority and every
// unknown error are transient and absent here.
var reelDraftRefusals = []struct {
	errs   []error
	reason string
}{
	{[]error{ErrDraftNeedsVideo}, "no_video"},
	{[]error{ErrEmptyPost}, "empty_post"},
	{[]error{ErrTextTooLong}, "caption_too_long"},
	{[]error{ErrInvalidVisibility}, "invalid_visibility"},
	{[]error{ErrInvalidRemixSetting}, "invalid_remix_setting"},
	{[]error{ErrInvalidCommentAccess}, "invalid_comment_access"},
	{[]error{ErrMediaNotFound, ErrMediaNotOwned, ErrMediaTypeMismatch}, "media_unavailable"},
	{[]error{ErrInvalidCover}, "invalid_cover"},
	{[]error{ErrTitleRequired, ErrTitleTooLong}, "invalid_title"},
	{[]error{ErrInvalidContentType}, "invalid_content_type"},
	{[]error{ErrChannelRequired}, "channel_required"},
	{[]error{ErrInvalidCategory}, "invalid_category"},
	{[]error{ErrInvalidLocationPlaceID}, "invalid_location"},
	{[]error{ErrInvalidHashtag, ErrTooManyHashtags}, "invalid_hashtags"},
	{[]error{ErrCreditsNotAllowed, ErrTooManyCredits, ErrInvalidCredit, ErrInvalidGenre,
		ErrInvalidReleaseYear, ErrInvalidProductionHouse}, "invalid_credits"},
	{[]error{ErrInvalidDistribution, ErrUnsupportedDistribution}, "invalid_distribution"},
	{[]error{ErrInvalidTier}, BlockReasonInvalidTier},
}

// reelDraftBlockReason reports whether a failed publication of a claimed
// draft is terminal, and the reason it is parked with.
func reelDraftBlockReason(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	var suspended *AuthorSuspendedError
	if errors.As(err, &suspended) {
		return suspended.BlockReason(), true
	}
	if errors.Is(err, ErrAuthorSuspended) {
		return BlockReasonAuthorSuspended, true
	}
	// An action the author may not add (not eligible, unsafe, a rule the
	// saved URL no longer passes); the safety check being down is not.
	if IsTerminalActionError(err) {
		return BlockReasonActionRefused, true
	}
	// Long-video uploads paused, or a members-only tier on a video the
	// copyright title review would hold.
	if IsTerminalCopyrightError(err) {
		return copyrightBlockReason(err), true
	}
	for _, r := range reelDraftRefusals {
		for _, e := range r.errs {
			if errors.Is(err, e) {
				return r.reason, true
			}
		}
	}
	return "", false
}

// validateReelDraftForPublish holds a stored draft to the rules CreatePost
// applies to the values it carries over, before CreatePost is asked: a
// draft saved before these rules existed is refused (and, on the worker,
// parked) for the right reason instead of failing somewhere deeper.
func validateReelDraftForPublish(d *postgres.ReelDraft) error {
	if d.MediaID == nil {
		return ErrDraftNeedsVideo
	}
	if !createVisibilityAllowed(d.Visibility) {
		return ErrInvalidVisibility
	}
	if _, err := normalizeCreateEnum(d.RemixSetting, PostRemixSettings, "allow", ErrInvalidRemixSetting); err != nil {
		return err
	}
	if _, err := normalizeCreateEnum(d.CommentAccess, PostCommentAccesses, "everyone", ErrInvalidCommentAccess); err != nil {
		return err
	}
	return ValidatePostContent(d.Caption, 1)
}

// validateDraftCaption is the create ceiling on a caption; an empty caption
// is fine on a reel, whose video is its content.
func validateDraftCaption(caption string) error {
	if n := utf8.RuneCountInString(caption); n > MaxPostTextRunes {
		return fmt.Errorf("%w: %d code points, limit %d", ErrTextTooLong, n, MaxPostTextRunes)
	}
	return nil
}

// parseDraftSchedule reads a schedule_at a draft write sends: an RFC 3339
// time inside the create route's window (ValidatePublishAt), or
// ErrInvalidPublishAt. A time in 1970 used to be stored and claimed at
// once, ahead of everyone else's drafts.
func parseDraftSchedule(raw string, now time.Time) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, ErrInvalidPublishAt
	}
	t = t.UTC()
	if err := ValidatePublishAt(&t, now); err != nil {
		return time.Time{}, err
	}
	return t, nil
}

// draftFieldUpdates resolves the patch fields CreatePost would refuse,
// under its rules, before anything is written (HIGH 1):
//
//   - visibility is one of PostVisibilities (stored lower-case);
//   - remix_setting and comment_access are in their enums ("" keeps the
//     create default);
//   - the caption is within MaxPostTextRunes;
//   - schedule_at is inside the scheduling window. "" leaves it as it is.
//
// It reports whether the patch schedules the draft, so the caller can
// check the draft has a video.
func draftFieldUpdates(input *UpdateDraftInput, now time.Time, updates map[string]interface{}) (schedules bool, err error) {
	if input.Visibility != nil {
		v := strings.ToLower(strings.TrimSpace(*input.Visibility))
		if !createVisibilityAllowed(v) {
			return false, ErrInvalidVisibility
		}
		updates["visibility"] = v
	}
	if input.RemixSetting != nil {
		v, err := normalizeCreateEnum(*input.RemixSetting, PostRemixSettings, "allow", ErrInvalidRemixSetting)
		if err != nil {
			return false, err
		}
		updates["remix_setting"] = v
	}
	if input.CommentAccess != nil {
		v, err := normalizeCreateEnum(*input.CommentAccess, PostCommentAccesses, "everyone", ErrInvalidCommentAccess)
		if err != nil {
			return false, err
		}
		updates["comment_access"] = v
	}
	if input.Caption != nil {
		if err := validateDraftCaption(*input.Caption); err != nil {
			return false, err
		}
		updates["caption"] = *input.Caption
	}
	if input.ScheduleAt != nil && strings.TrimSpace(*input.ScheduleAt) != "" {
		t, err := parseDraftSchedule(*input.ScheduleAt, now)
		if err != nil {
			return false, err
		}
		updates["schedule_at"] = t
		schedules = true
	}
	return schedules, nil
}
