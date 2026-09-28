package service

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

/*
	Creator Hub settings (2026-09-28): the values PATCH /v1/posts/:postId and
	POST /v1/uploads/bulk accept beyond the 2026-09-27 set.

	The create route (POST /v1/posts) stores license, remix_setting,
	comment_moderation and comment_access as sent, defaulting only the empty
	string; nothing in post-service validated them before this file. The
	lists below are the ones the reel composer's draft table has always
	enforced (migration 006's reel_drafts CHECKs) and the web and Android
	composers send, and they are the ONE place the edit routes read them
	from.

	  422 INVALID_LICENSE             license outside PostLicenses
	  422 INVALID_REMIX_SETTING       remix_setting outside PostRemixSettings
	  422 INVALID_COMMENT_MODERATION  comment_moderation outside PostCommentModerations
	  422 INVALID_COMMENT_ACCESS      comment_access outside PostCommentAccesses
	  422 INVALID_COMMENT_SORT        default_comment_sort outside PostCommentSorts
	  422 INVALID_RECORDING_DATE      not YYYY-MM-DD, or a day after today
	  422 INVALID_RECORDING_LOCATION  more than 100 runes
	  422 RELATED_NOT_FOUND           related_post_id missing, deleted or not the owner's
	  422 RELATED_SELF                related_post_id is the post itself
*/

var (
	PostLicenses           = []string{"standard", "creative_commons"}
	PostRemixSettings      = []string{"allow", "allow_audio_only", "disallow"}
	PostCommentModerations = []string{"none", "basic", "strict", "hold_all"}
	PostCommentAccesses    = []string{"everyone", "followers", "nobody"}
	// PostCommentSorts is migration 052's CHECK; the comments read takes
	// the same two words as ?sort=.
	PostCommentSorts = []string{"top", "newest"}
)

// MaxRecordingLocationRunes caps recording_location.
const MaxRecordingLocationRunes = 100

// recordingDateSlack lets "today" in the easternmost time zone (UTC+14)
// through: the rule is "not in the future" for the person setting it, and
// the server only knows UTC.
const recordingDateSlack = 14 * time.Hour

var (
	ErrInvalidLicense           = fmt.Errorf("license must be one of %s", strings.Join(PostLicenses, ", "))
	ErrInvalidRemixSetting      = fmt.Errorf("remix_setting must be one of %s", strings.Join(PostRemixSettings, ", "))
	ErrInvalidCommentModeration = fmt.Errorf("comment_moderation must be one of %s", strings.Join(PostCommentModerations, ", "))
	ErrInvalidCommentAccess     = fmt.Errorf("comment_access must be one of %s", strings.Join(PostCommentAccesses, ", "))
	ErrInvalidCommentSort       = fmt.Errorf("default_comment_sort must be one of %s", strings.Join(PostCommentSorts, ", "))
	ErrInvalidRecordingDate     = errors.New("recording_date must be YYYY-MM-DD and not in the future")
	ErrInvalidRecordingLocation = fmt.Errorf("recording_location may be at most %d characters", MaxRecordingLocationRunes)
	ErrRelatedNotFound          = errors.New("related_post_id must be one of your own posts")
	ErrRelatedSelf              = errors.New("a post cannot be its own related post")
)

// normalizeEnum lowercases and trims v and reports whether it is in allowed.
func normalizeEnum(v string, allowed []string) (string, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	for _, a := range allowed {
		if v == a {
			return v, true
		}
	}
	return v, false
}

// parseRecordingDate reads "YYYY-MM-DD" (the empty string is the caller's
// "clear") and refuses a day after today.
func parseRecordingDate(raw string, now time.Time) (time.Time, error) {
	d, err := time.Parse("2006-01-02", strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, ErrInvalidRecordingDate
	}
	latest := now.UTC().Add(recordingDateSlack)
	today := time.Date(latest.Year(), latest.Month(), latest.Day(), 0, 0, 0, 0, time.UTC)
	if d.After(today) {
		return time.Time{}, ErrInvalidRecordingDate
	}
	return d, nil
}

// normalizeRecordingLocation trims and caps recording_location.
func normalizeRecordingLocation(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if utf8.RuneCountInString(v) > MaxRecordingLocationRunes {
		return "", ErrInvalidRecordingLocation
	}
	return v, nil
}
