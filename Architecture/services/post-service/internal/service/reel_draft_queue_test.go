package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// HIGH 1 (2026-10-30): a scheduled reel draft that can never publish is
// parked with its reason; a transient failure backs off; and a draft
// cannot be saved in a shape CreatePost refuses.

func TestReelDraftRetryDelayDoublesUpToAnHour(t *testing.T) {
	for _, c := range []struct {
		attempts int
		want     time.Duration
	}{
		{0, time.Minute}, {1, time.Minute}, {2, 2 * time.Minute}, {3, 4 * time.Minute},
		{6, 32 * time.Minute}, {7, time.Hour}, {8, time.Hour}, {29, time.Hour},
	} {
		if got := reelDraftRetryDelay(c.attempts); got != c.want {
			t.Errorf("attempt %d: delay %v, want %v", c.attempts, got, c.want)
		}
	}
}

func TestReelDraftRetryPlanBacksOffThenParks(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	plan := reelDraftRetryPlan(now)
	at, reason := plan(1)
	if reason != "" || !at.Equal(now.Add(time.Minute)) {
		t.Fatalf("first failure: retry %v reason %q, want a minute later and no reason", at, reason)
	}
	at, reason = plan(maxReelDraftPublishAttempts - 1)
	if reason != "" || !at.Equal(now.Add(time.Hour)) {
		t.Fatalf("attempt %d: retry %v reason %q", maxReelDraftPublishAttempts-1, at, reason)
	}
	if _, reason = plan(maxReelDraftPublishAttempts); reason != BlockReasonPublishFailed {
		t.Fatalf("attempt %d: reason %q, want %q", maxReelDraftPublishAttempts, reason, BlockReasonPublishFailed)
	}
	// Retried for about a day before it gives up, never sooner.
	total := time.Duration(0)
	for n := 1; n < maxReelDraftPublishAttempts; n++ {
		total += reelDraftRetryDelay(n)
	}
	if total < 24*time.Hour {
		t.Fatalf("a draft is parked after only %v of retries", total)
	}
}

func TestReelDraftBlockReasonParksRefusalsOfTheDraft(t *testing.T) {
	wrap := func(err error) error { return fmt.Errorf("publish claimed draft: %w", err) }
	for _, c := range []struct {
		err  error
		want string
	}{
		{ErrDraftNeedsVideo, "no_video"},
		{ErrEmptyPost, "empty_post"},
		{fmt.Errorf("%w: 6000 code points", ErrTextTooLong), "caption_too_long"},
		{ErrInvalidVisibility, "invalid_visibility"},
		{ErrInvalidRemixSetting, "invalid_remix_setting"},
		{ErrInvalidCommentAccess, "invalid_comment_access"},
		{ErrMediaNotFound, "media_unavailable"},
		{ErrMediaNotOwned, "media_unavailable"},
		{ErrMediaTypeMismatch, "media_unavailable"},
		{ErrInvalidCover, "invalid_cover"},
		{ErrTitleRequired, "invalid_title"},
		{ErrInvalidContentType, "invalid_content_type"},
		{ErrChannelRequired, "channel_required"},
		{ErrInvalidCategory, "invalid_category"},
		{ErrInvalidHashtag, "invalid_hashtags"},
		{ErrInvalidGenre, "invalid_credits"},
		{ErrInvalidDistribution, "invalid_distribution"},
		{ErrInvalidTier, BlockReasonInvalidTier},
		{ErrActionNotEligible, BlockReasonActionRefused},
		{ErrAuthorSuspended, BlockReasonAuthorSuspended},
	} {
		got, terminal := reelDraftBlockReason(wrap(c.err))
		if !terminal || got != c.want {
			t.Errorf("%v: (%q, %v), want (%q, true)", c.err, got, terminal, c.want)
		}
	}
}

// A suspension parks the draft with its end date, as the standing check
// itself parks it.
func TestReelDraftBlockReasonCarriesTheSuspensionEnd(t *testing.T) {
	until := time.Date(2026, 12, 27, 9, 30, 0, 0, time.UTC)
	err := fmt.Errorf("publish claimed draft: %w", &AuthorSuspendedError{Standing: "suspended", SuspendedUntil: &until})
	got, terminal := reelDraftBlockReason(err)
	if !terminal || got != BlockReasonAuthorSuspended+" until 2026-12-27T09:30:00Z" {
		t.Fatalf("(%q, %v)", got, terminal)
	}
}

func TestReelDraftBlockReasonRetriesTransientFailures(t *testing.T) {
	for _, err := range []error{
		nil,
		ErrMediaNotReady,
		ErrStandingUnknown,
		ErrTierLookupUnavailable,
		ErrMediaClaimsUnavailable,
		errors.New("connection reset by peer"),
	} {
		if reason, terminal := reelDraftBlockReason(fmt.Errorf("x: %w", err)); terminal && err != nil {
			t.Errorf("%v is transient, parked as %q", err, reason)
		}
		if err == nil {
			if _, terminal := reelDraftBlockReason(nil); terminal {
				t.Error("no error is not a refusal")
			}
		}
	}
}

func validReelDraft() *postgres.ReelDraft {
	media := uuid.New()
	return &postgres.ReelDraft{ID: uuid.New(), AuthorID: uuid.New(), MediaID: &media, Visibility: "public",
		RemixSetting: "allow", CommentAccess: "everyone"}
}

func TestValidateReelDraftForPublish(t *testing.T) {
	if err := validateReelDraftForPublish(validReelDraft()); err != nil {
		t.Fatalf("a valid draft with an empty caption: %v", err)
	}
	for _, c := range []struct {
		name   string
		change func(d *postgres.ReelDraft)
		want   error
	}{
		{"no video", func(d *postgres.ReelDraft) { d.MediaID = nil }, ErrDraftNeedsVideo},
		{"visibility", func(d *postgres.ReelDraft) { d.Visibility = "everyone" }, ErrInvalidVisibility},
		{"empty visibility", func(d *postgres.ReelDraft) { d.Visibility = "" }, ErrInvalidVisibility},
		{"remix_setting", func(d *postgres.ReelDraft) { d.RemixSetting = "x" }, ErrInvalidRemixSetting},
		{"comment_access", func(d *postgres.ReelDraft) { d.CommentAccess = "x" }, ErrInvalidCommentAccess},
		{"caption", func(d *postgres.ReelDraft) { d.Caption = strings.Repeat("a", MaxPostTextRunes+1) }, ErrTextTooLong},
	} {
		d := validReelDraft()
		c.change(d)
		if err := validateReelDraftForPublish(d); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
}

func strp(v string) *string { return &v }

func TestDraftFieldUpdatesHoldsTheCreateRules(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name  string
		input UpdateDraftInput
		want  error
	}{
		{"visibility", UpdateDraftInput{Visibility: strp("world")}, ErrInvalidVisibility},
		{"remix_setting", UpdateDraftInput{RemixSetting: strp("x")}, ErrInvalidRemixSetting},
		{"comment_access", UpdateDraftInput{CommentAccess: strp("strangers")}, ErrInvalidCommentAccess},
		{"caption", UpdateDraftInput{Caption: strp(strings.Repeat("क", MaxPostTextRunes+1))}, ErrTextTooLong},
		{"schedule in 1970", UpdateDraftInput{ScheduleAt: strp("1970-01-01T00:00:00Z")}, ErrInvalidPublishAt},
		{"schedule a minute out", UpdateDraftInput{ScheduleAt: strp(now.Add(time.Minute).Format(time.RFC3339))}, ErrInvalidPublishAt},
		{"schedule past 30 days", UpdateDraftInput{ScheduleAt: strp(now.Add(31 * 24 * time.Hour).Format(time.RFC3339))}, ErrInvalidPublishAt},
		{"schedule unparseable", UpdateDraftInput{ScheduleAt: strp("tomorrow")}, ErrInvalidPublishAt},
	} {
		updates := map[string]interface{}{}
		if _, err := draftFieldUpdates(&c.input, now, updates); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}

	at := now.Add(time.Hour)
	updates := map[string]interface{}{}
	schedules, err := draftFieldUpdates(&UpdateDraftInput{
		Visibility: strp(" Friends "), RemixSetting: strp(""), CommentAccess: strp("Followers"),
		Caption: strp(strings.Repeat("a", MaxPostTextRunes)), ScheduleAt: strp(at.Format(time.RFC3339)),
	}, now, updates)
	if err != nil || !schedules {
		t.Fatalf("a valid patch: schedules=%v err=%v", schedules, err)
	}
	if updates["visibility"] != "friends" || updates["remix_setting"] != "allow" || updates["comment_access"] != "followers" {
		t.Fatalf("normalised values: %+v", updates)
	}
	if got, ok := updates["schedule_at"].(time.Time); !ok || !got.Equal(at) {
		t.Fatalf("schedule_at = %v", updates["schedule_at"])
	}

	// An empty schedule_at leaves the schedule as it is.
	updates = map[string]interface{}{}
	if schedules, err := draftFieldUpdates(&UpdateDraftInput{ScheduleAt: strp("")}, now, updates); err != nil || schedules || len(updates) != 0 {
		t.Fatalf("empty schedule_at: schedules=%v err=%v updates=%v", schedules, err, updates)
	}
}

// A refused patch is refused before any store is touched (the zero
// Service has none).
func TestUpdateDraftRefusesBeforeWriting(t *testing.T) {
	for _, input := range []UpdateDraftInput{
		{RemixSetting: strp("x"), Caption: strp("never stored")},
		{Visibility: strp("world")},
		{ScheduleAt: strp("1970-01-01T00:00:00Z")},
	} {
		if _, err := (&Service{}).UpdateDraft(context.Background(), uuid.New(), uuid.New(), &input); err == nil {
			t.Fatalf("%+v was accepted", input)
		}
	}
}

func TestCreateDraftRefusesWhatCreatePostRefuses(t *testing.T) {
	for _, in := range []CreateDraftInput{
		{AuthorID: uuid.New(), Visibility: "everyone"},
		{AuthorID: uuid.New(), Caption: strings.Repeat("a", MaxPostTextRunes+1)},
	} {
		if _, err := (&Service{}).CreateDraft(context.Background(), &in); !errors.Is(err, ErrInvalidVisibility) && !errors.Is(err, ErrTextTooLong) {
			t.Fatalf("%+v: got %v", in, err)
		}
	}
}
