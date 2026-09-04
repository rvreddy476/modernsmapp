package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Module 1 P0-5 — server-side drafts + scheduling for the unified
// composer. Reel/long-video drafts keep their legacy endpoints; this
// covers text / photo / carousel / poll / article.

// ErrDraftNotFound mirrors ErrPostNotFound for drafts.
var ErrDraftNotFound = errors.New("draft not found")

// ErrDraftNotEditable is returned when the draft exists but is already
// published, deleted, or mid-publish.
var ErrDraftNotEditable = errors.New("draft is not editable")

// ErrInvalidDraft marks a payload that fails validation (400).
var ErrInvalidDraft = errors.New("invalid draft")

// staleClaimAfter is how long a draft may sit in 'publishing' before
// another worker assumes the claim holder died and re-claims it.
const staleClaimAfter = 10 * time.Minute

// PostDraftPayload is the stored composer state. It mirrors the
// client-settable slice of CreatePostInput; publish maps it 1:1. Unknown
// fields are rejected on save so a future client can't silently lose
// state to an old server.
type PostDraftPayload struct {
	Text           string            `json:"text"`
	Visibility     string            `json:"visibility,omitempty"`
	ContentType    string            `json:"content_type,omitempty"`
	MediaIDs       []uuid.UUID       `json:"media_ids,omitempty"`
	AltTexts       map[string]string `json:"alt_texts,omitempty"` // media_id → alt text (P0-7)
	Poll           *DraftPoll        `json:"poll,omitempty"`
	RichText       json.RawMessage   `json:"rich_text,omitempty"`
	NoComments     bool              `json:"no_comments,omitempty"`
	NoLikes        bool              `json:"no_likes,omitempty"`
	LocationName   *string           `json:"location_name,omitempty"`
	LocationLat    *float64          `json:"location_lat,omitempty"`
	LocationLng    *float64          `json:"location_lng,omitempty"`
	Feeling        *string           `json:"feeling,omitempty"`
	Activity       *string           `json:"activity,omitempty"`
	ActivityDetail *string           `json:"activity_detail,omitempty"`
	Language       string            `json:"language,omitempty"`
	Title          string            `json:"title,omitempty"`
	Distribution   json.RawMessage   `json:"distribution,omitempty"`

	// ── Reel/flick fields (Codex P1-5) ────────────────────────────────
	// The mobile reel composer schedules by posting its full create body
	// as a draft payload. Those keys were absent here and
	// DisallowUnknownFields rejected the whole request, so every
	// scheduled reel failed with INVALID_DRAFT. They are typed (not
	// swallowed) so each one is mapped back at publication.
	CoverFrameMs *int    `json:"cover_frame_ms,omitempty"`
	Filter       string  `json:"filter,omitempty"`
	AudioTrackID *string `json:"audio_track_id,omitempty"`
	CoverMediaID *string `json:"cover_media_id,omitempty"`
	// Reel disclosure/rights fields the composer may include.
	Tags           []string `json:"tags,omitempty"`
	Category       string   `json:"category,omitempty"`
	PaidPromotion  bool     `json:"paid_promotion,omitempty"`
	IsMadeForKids  bool     `json:"is_made_for_kids,omitempty"`
	AlteredContent bool     `json:"altered_content,omitempty"`
	// Per-reel controls and tagged people (2026-09-04), for the same
	// reason as the block above: scheduling must not lose what the
	// composer set. AllowDownload is a pointer so an omitted key keeps
	// the column default at publication, exactly as on the create route.
	HideShare     bool     `json:"hide_share,omitempty"`
	AllowDownload *bool    `json:"allow_download,omitempty"`
	TaggedUserIDs []string `json:"tagged_user_ids,omitempty"`
}

// DraftPoll matches CreatePollInput but with JSON tags for storage.
type DraftPoll struct {
	Question       string   `json:"question"`
	Options        []string `json:"options"`
	AllowsMultiple bool     `json:"allows_multiple,omitempty"`
	DurationHours  *int     `json:"duration_hours,omitempty"`
}

// Reel/flick drafts are accepted here too (P1-5) so the mobile composer
// has one scheduling contract; the payload's content_type decides what is
// published.
var validDraftPostTypes = map[string]bool{
	"post": true, "poll": true, "article": true, "reel": true, "video": true,
}

// validateDraft checks a draft on save (Codex: validate on save AND at
// publish). Publish re-runs this plus the full CreatePost gates.
func validateDraft(postType string, payload *PostDraftPayload) error {
	if !validDraftPostTypes[postType] {
		return fmt.Errorf("%w: post_type must be post, poll, or article", ErrInvalidDraft)
	}
	if len(payload.MediaIDs) > 10 {
		return fmt.Errorf("%w: maximum 10 media attachments", ErrInvalidDraft)
	}
	if postType == "poll" {
		if payload.Poll == nil {
			return fmt.Errorf("%w: poll draft requires a poll block", ErrInvalidDraft)
		}
		if strings.TrimSpace(payload.Poll.Question) == "" {
			return fmt.Errorf("%w: poll question is required", ErrInvalidDraft)
		}
		if len(payload.Poll.Options) < 2 || len(payload.Poll.Options) > 5 {
			return fmt.Errorf("%w: poll needs 2-5 options", ErrInvalidDraft)
		}
	} else if strings.TrimSpace(payload.Text) == "" && len(payload.MediaIDs) == 0 {
		return fmt.Errorf("%w: draft needs text or media", ErrInvalidDraft)
	}
	// The distribution policy is validated on save so the author learns
	// about an unsupported policy immediately, not at 2am publish time.
	if _, err := ParseDistributionPolicy(payload.Distribution); err != nil {
		return err
	}
	return nil
}

func parseDraftPayload(raw json.RawMessage) (*PostDraftPayload, error) {
	var p PostDraftPayload
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDraft, err)
	}
	return &p, nil
}

// CreatePostDraft validates and stores a new draft.
func (s *Service) CreatePostDraft(ctx context.Context, authorID uuid.UUID, postType string,
	rawPayload json.RawMessage, scheduleAt *time.Time) (*postgres.PostDraft, error) {
	payload, err := parseDraftPayload(rawPayload)
	if err != nil {
		return nil, err
	}
	if postType == "" {
		postType = "post"
		if payload.Poll != nil {
			postType = "poll"
		}
	}
	if err := validateDraft(postType, payload); err != nil {
		return nil, err
	}
	if scheduleAt != nil && scheduleAt.Before(time.Now().Add(-time.Minute)) {
		return nil, fmt.Errorf("%w: schedule_at is in the past", ErrInvalidDraft)
	}
	d := &postgres.PostDraft{
		AuthorID:   authorID,
		PostType:   postType,
		Payload:    rawPayload,
		ScheduleAt: scheduleAt,
	}
	// P1-4/P1-5: the draft row and its media reference set are written in
	// ONE transaction — a successful response can never leave the
	// reference table incomplete.
	if err := s.pgStore.CreatePostDraft(ctx, d, payload.MediaIDs); err != nil {
		return nil, err
	}
	return d, nil
}

// UpdatePostDraft revalidates and replaces a draft's contents.
func (s *Service) UpdatePostDraft(ctx context.Context, id, authorID uuid.UUID, postType string,
	rawPayload json.RawMessage, scheduleAt *time.Time) (*postgres.PostDraft, error) {
	payload, err := parseDraftPayload(rawPayload)
	if err != nil {
		return nil, err
	}
	if postType == "" {
		postType = "post"
		if payload.Poll != nil {
			postType = "poll"
		}
	}
	if err := validateDraft(postType, payload); err != nil {
		return nil, err
	}
	ok, err := s.pgStore.UpdatePostDraft(ctx, id, authorID, postType, rawPayload, scheduleAt, payload.MediaIDs)
	if err != nil {
		return nil, err
	}
	if !ok {
		// Distinguish "missing" from "not editable" for the client.
		existing, gerr := s.pgStore.GetPostDraft(ctx, id, authorID)
		if gerr == nil && existing != nil {
			return nil, ErrDraftNotEditable
		}
		return nil, ErrDraftNotFound
	}
	// The reference set was rewritten inside UpdatePostDraft's transaction.
	return s.pgStore.GetPostDraft(ctx, id, authorID)
}

func (s *Service) GetPostDraft(ctx context.Context, id, authorID uuid.UUID) (*postgres.PostDraft, error) {
	d, err := s.pgStore.GetPostDraft(ctx, id, authorID)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, ErrDraftNotFound
	}
	return d, nil
}

func (s *Service) ListPostDrafts(ctx context.Context, authorID uuid.UUID, limit int) ([]postgres.PostDraft, error) {
	return s.pgStore.ListPostDrafts(ctx, authorID, limit)
}

func (s *Service) DeletePostDraft(ctx context.Context, id, authorID uuid.UUID) error {
	ok, err := s.pgStore.DeletePostDraft(ctx, id, authorID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrDraftNotFound
	}
	return nil
}

// PublishPostDraft publishes now (scheduleAt nil) or schedules. Immediate
// publish claims the draft first so a concurrent scheduler tick can't
// double-publish it.
func (s *Service) PublishPostDraft(ctx context.Context, id, authorID uuid.UUID, scheduleAt *time.Time) (*postgres.Post, *postgres.PostDraft, error) {
	d, err := s.GetPostDraft(ctx, id, authorID)
	if err != nil {
		return nil, nil, err
	}
	switch d.Status {
	case "published":
		return nil, nil, ErrDraftNotEditable
	case "publishing":
		return nil, nil, ErrDraftNotEditable
	}

	if scheduleAt != nil {
		if scheduleAt.Before(time.Now()) {
			return nil, nil, fmt.Errorf("%w: schedule_at is in the past", ErrInvalidDraft)
		}
		// Scheduling only changes the time; re-send the existing media
		// set so the atomic rewrite is a no-op rather than a wipe.
		schedPayload, perr := parseDraftPayload(d.Payload)
		var schedMedia []uuid.UUID
		if perr == nil {
			schedMedia = schedPayload.MediaIDs
		}
		ok, err := s.pgStore.UpdatePostDraft(ctx, id, authorID, d.PostType, d.Payload, scheduleAt, schedMedia)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, nil, ErrDraftNotEditable
		}
		d, err = s.GetPostDraft(ctx, id, authorID)
		return nil, d, err
	}

	// Codex P1-3: take the SAME atomic claim the scheduled worker uses.
	// Reading the row and publishing was not linearizable — a concurrent
	// delete or edit could interleave. The compare-and-set makes
	// publish-vs-delete and publish-vs-edit resolve deterministically.
	claimed, err := s.pgStore.ClaimPostDraftForPublish(ctx, id, authorID)
	if err != nil {
		if errors.Is(err, postgres.ErrDraftClaimLost) {
			return nil, nil, ErrDraftNotEditable
		}
		return nil, nil, err
	}

	post, err := s.publishDraftRow(ctx, claimed)
	if err != nil {
		return nil, nil, err
	}
	return post, nil, nil
}

// publishDraftRow converts one claimed/owned draft row into a post with
// the draft id as the post id (idempotency: a retry after a crash hits
// the posts PK and resolves to the already-created post — exactly one
// post, exactly one PostCreated outbox event).
func (s *Service) publishDraftRow(ctx context.Context, d *postgres.PostDraft) (*postgres.Post, error) {
	payload, err := parseDraftPayload(d.Payload)
	if err != nil {
		s.pgStore.MarkPostDraftBlocked(ctx, d.ID, "payload no longer parses: "+err.Error(), d.ClaimToken) //nolint:errcheck
		return nil, err
	}
	// Publish-time revalidation (Codex P0-5): schema validation, author
	// standing, then the full CreatePost gates (spam, blocked hashtags,
	// media classification, video review gate, after-hours protection).
	if err := validateDraft(d.PostType, payload); err != nil {
		s.pgStore.MarkPostDraftBlocked(ctx, d.ID, err.Error(), d.ClaimToken) //nolint:errcheck
		return nil, err
	}
	reason, standingErr := s.authorStandingOK(ctx, d.AuthorID)
	switch {
	case errors.Is(standingErr, ErrStandingUnknown):
		// P2-1: never publish through uncertainty. Release the claim so a
		// later tick retries once trust-safety is reachable again.
		s.pgStore.ReleasePostDraftClaim(ctx, d.ID, d.ClaimToken) //nolint:errcheck
		return nil, fmt.Errorf("author standing unavailable; will retry: %w", standingErr)
	case standingErr != nil:
		s.pgStore.ReleasePostDraftClaim(ctx, d.ID, d.ClaimToken) //nolint:errcheck
		return nil, standingErr
	case reason != "":
		s.pgStore.MarkPostDraftBlocked(ctx, d.ID, reason, d.ClaimToken) //nolint:errcheck
		return nil, fmt.Errorf("author standing check failed: %s", reason)
	}

	contentType := payload.ContentType
	if d.PostType == "poll" {
		contentType = "poll"
	}
	// P1-5: a reel draft publishes as a flick, not a text post.
	postType := "text"
	if d.PostType == "reel" || d.PostType == "video" ||
		contentType == "flick" || contentType == "long_video" {
		postType = "video"
	}
	postID := d.ID
	input := &CreatePostInput{
		PostID:         &postID,
		AuthorID:       d.AuthorID,
		Text:           payload.Text,
		Visibility:     payload.Visibility,
		ContentType:    contentType,
		MediaIDs:       payload.MediaIDs,
		RichText:       payload.RichText,
		NoComments:     payload.NoComments,
		NoLikes:        payload.NoLikes,
		LocationName:   payload.LocationName,
		LocationLat:    payload.LocationLat,
		LocationLng:    payload.LocationLng,
		Feeling:        payload.Feeling,
		Activity:       payload.Activity,
		ActivityDetail: payload.ActivityDetail,
		Language:       payload.Language,
		Title:          payload.Title,
		PostType:       postType,
		Distribution:   payload.Distribution,
		// P1-5: every reel-specific field is carried through, so nothing
		// the composer set is lost by scheduling instead of posting now.
		Tags:           payload.Tags,
		Category:       payload.Category,
		PaidPromotion:  payload.PaidPromotion,
		IsMadeForKids:  payload.IsMadeForKids,
		AlteredContent: payload.AlteredContent,
		HideShare:      payload.HideShare,
		AllowDownload:  payload.AllowDownload == nil || *payload.AllowDownload,
	}
	// Unparseable ids were accepted at save time before this field existed
	// on the payload; dropping one is better than failing a scheduled post
	// the author is no longer looking at.
	for _, raw := range payload.TaggedUserIDs {
		if id, err := uuid.Parse(raw); err == nil {
			input.TaggedUserIDs = append(input.TaggedUserIDs, id)
		}
	}
	if payload.CoverMediaID != nil {
		if id, err := uuid.Parse(*payload.CoverMediaID); err == nil {
			input.CoverMediaID = &id
		}
	}
	if payload.Poll != nil {
		// Poll duration starts at actual publication time: CreatePost
		// computes ends_at = now + duration at this moment, not at the
		// time the draft was written (Codex P0-5 acceptance).
		input.Poll = &CreatePollInput{
			Question:       payload.Poll.Question,
			Options:        payload.Poll.Options,
			AllowsMultiple: payload.Poll.AllowsMultiple,
			DurationHours:  payload.Poll.DurationHours,
		}
	}

	post, err := s.CreatePost(ctx, input)
	if err != nil {
		if postgres.IsUniqueViolation(err) {
			// Crash-retry path: the post already exists from a previous
			// attempt. Resolve to it and finalize the draft.
			existing, gerr := s.pgStore.GetPost(ctx, postID)
			if gerr == nil && existing != nil {
				s.pgStore.MarkPostDraftPublished(ctx, d.ID, existing.ID, d.ClaimToken) //nolint:errcheck
				return existing, nil
			}
		}
		// Content rejection (spam et al) is terminal → blocked with
		// reason; infra errors are transient → release for retry.
		if strings.Contains(err.Error(), "content rejected") {
			s.pgStore.MarkPostDraftBlocked(ctx, d.ID, err.Error(), d.ClaimToken) //nolint:errcheck
		} else {
			s.pgStore.ReleasePostDraftClaim(ctx, d.ID, d.ClaimToken) //nolint:errcheck
		}
		return nil, err
	}

	// P1-5: background audio chosen in the reel composer must survive
	// scheduling, exactly as it does on the immediate-publish path.
	if payload.AudioTrackID != nil && *payload.AudioTrackID != "" {
		if audioID, err := uuid.Parse(*payload.AudioTrackID); err == nil {
			if err := s.AttachAudioToPost(ctx, d.AuthorID, post.ID, audioID); err != nil {
				slog.Warn("draft publish: attach audio failed",
					"post_id", post.ID, "audio_id", audioID, "err", err)
			}
		}
	}

	if err := s.pgStore.MarkPostDraftPublished(ctx, d.ID, post.ID, d.ClaimToken); err != nil {
		// Post exists; the stale-claim reclaim will retry the finalize and
		// hit the PK-conflict fast path above. Log, don't fail the publish.
		slog.Error("draft publish: finalize failed", "draft_id", d.ID, "err", err)
	}
	return post, nil
}

// PublishScheduledPostDrafts is the worker tick: claim due drafts
// (SKIP LOCKED, multi-replica safe) and publish each.
func (s *Service) PublishScheduledPostDrafts(ctx context.Context) (int, error) {
	claimed, err := s.pgStore.ClaimDuePostDrafts(ctx, time.Now().UTC(), staleClaimAfter, 50)
	if err != nil {
		return 0, fmt.Errorf("claim due drafts: %w", err)
	}
	published := 0
	for i := range claimed {
		if _, err := s.publishDraftRow(ctx, &claimed[i]); err != nil {
			slog.Warn("scheduled draft publish failed", "draft_id", claimed[i].ID, "err", err)
			continue
		}
		published++
	}
	return published, nil
}

// ErrStandingUnknown means the authoritative standing data could not be
// read. Publication must NOT proceed through that uncertainty
// (Codex P2-1) — the caller releases the claim and retries later.
var ErrStandingUnknown = errors.New("author standing unavailable")

// authorStandingOK checks trust-safety for an active suspension-grade
// strike at publish time.
//
// Failure policy (Codex P2-1): the v1 implementation treated missing
// config, transport errors, non-200 responses and decode failures as
// "allowed", so a trust-safety outage published everything unchecked.
// Now every one of those is ErrStandingUnknown → retryable, not a pass.
// Only a genuine "no active severe strike" answer permits publication.
//
// The single deliberate exception is an unconfigured URL in dev, which is
// gated by DRAFT_REQUIRE_STANDING_CHECK (default: required in production).
func (s *Service) authorStandingOK(ctx context.Context, authorID uuid.UUID) (string, error) {
	if s.trustSafetyURL == "" {
		if s.requireStandingCheck {
			return "", ErrStandingUnknown
		}
		return "", nil // dev: explicitly opted out
	}
	url := fmt.Sprintf("%s/v1/strikes/%s", s.trustSafetyURL, authorID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", ErrStandingUnknown
	}
	req.Header.Set("X-User-Id", authorID.String())
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", ErrStandingUnknown
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", ErrStandingUnknown
	}
	var body struct {
		Data []struct {
			Severity  string     `json:"severity"`
			ExpiresAt *time.Time `json:"expires_at"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", ErrStandingUnknown
	}
	now := time.Now()
	for _, strike := range body.Data {
		if strike.ExpiresAt != nil && strike.ExpiresAt.Before(now) {
			continue
		}
		switch strings.ToLower(strike.Severity) {
		case "ban", "suspend", "suspension", "severe":
			return "account has an active " + strike.Severity + " strike", nil
		}
	}
	return "", nil
}

// SetRequireStandingCheck controls whether an unreachable/unconfigured
// trust-safety service blocks scheduled publication.
func (s *Service) SetRequireStandingCheck(v bool) { s.requireStandingCheck = v }

// CleanupOrphanDraftMedia deletes media referenced only by drafts that
// were soft-deleted beyond the retention window and that no live post or
// surviving draft still uses (Codex P1-4). Returns the number deleted.
func (s *Service) CleanupOrphanDraftMedia(ctx context.Context, retention time.Duration, limit int) (int, error) {
	ids, err := s.pgStore.OrphanedDraftMedia(ctx, retention, limit)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, mediaID := range ids {
		if err := s.deleteMediaAsset(ctx, mediaID); err != nil {
			slog.Warn("draft media cleanup: delete failed", "media_id", mediaID, "err", err)
			continue
		}
		deleted++
	}
	return deleted, nil
}

// deleteMediaAsset asks media-service to drop an asset (internal call).
func (s *Service) deleteMediaAsset(ctx context.Context, mediaID uuid.UUID) error {
	base := os.Getenv("MEDIA_SERVICE_URL")
	if base == "" {
		base = "http://media-service:8087"
	}
	// fixes-v2 / Codex P1-5: the user-auth DELETE /v1/media/:id requires
	// an X-User-Id this sweeper does not have, so the old call could never
	// delete anything. Use the internal orphan contract, which
	// re-validates "unreferenced and old enough" inside media-service.
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		strings.TrimRight(base, "/")+"/v1/media/internal/orphan/"+mediaID.String(), nil)
	if err != nil {
		return err
	}
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil // already gone
	case resp.StatusCode == http.StatusConflict:
		// media-service re-checked and refused: the asset is still
		// referenced or too new. Not an error — skip it this sweep.
		slog.Info("draft media cleanup: media-service refused as not orphaned", "media_id", mediaID)
		return nil
	case resp.StatusCode >= 300:
		return fmt.Errorf("media-service returned %d", resp.StatusCode)
	}
	return nil
}

// SetTrustSafetyURL configures the standing-check endpoint.
func (s *Service) SetTrustSafetyURL(url string) { s.trustSafetyURL = url }
