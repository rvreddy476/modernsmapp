package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// CreateDraftInput holds the fields for creating a new reel draft.
type CreateDraftInput struct {
	AuthorID   uuid.UUID
	MediaID    *uuid.UUID
	Visibility string
	Caption    string
}

// UpdateDraftInput holds the fields that can be patched on a draft.
type UpdateDraftInput struct {
	Title             *string  `json:"title"`
	Caption           *string  `json:"caption"`
	Hashtags          []string `json:"hashtags"`
	Tags              []string `json:"tags"`
	Visibility        *string  `json:"visibility"`
	TopicID           *int     `json:"topic_id"`
	Category          *string  `json:"category"`
	Language          *string  `json:"language"`
	SEOTitle          *string  `json:"seo_title"`
	CrossPostPostbook *bool    `json:"cross_post_postbook"`
	CrossPostPosttube *bool    `json:"cross_post_posttube"`
	PublishToFeed     *bool    `json:"publish_to_feed"`
	IsMadeForKids     *bool    `json:"is_made_for_kids"`
	PaidPromotion     *bool    `json:"paid_promotion"`
	AlteredContent    *bool    `json:"altered_content"`
	AutoChapters      *bool    `json:"auto_chapters"`
	FeaturedPlaces    *bool    `json:"featured_places"`
	AutoConcepts      *bool    `json:"auto_concepts"`
	License           *string  `json:"license"`
	AllowEmbedding    *bool    `json:"allow_embedding"`
	RemixSetting      *string  `json:"remix_setting"`
	LikesEnabled      *bool    `json:"likes_enabled"`
	CommentsEnabled   *bool    `json:"comments_enabled"`
	CommentModeration *string  `json:"comment_moderation"`
	CommentAccess     *string  `json:"comment_access"`
	RecordingDate     *string  `json:"recording_date"`
	RecordingLocation *string  `json:"recording_location"`
	// Upload studio (migration 068). "" clears location_name / place id.
	AIGenerated     *bool   `json:"ai_generated"`
	LocationName    *string `json:"location_name"`
	LocationPlaceID *string `json:"location_place_id"`
	// ContentType is the kind the studio measured: flick or long_video
	// (migration 069). Anything else is refused.
	ContentType      *string  `json:"content_type"`
	AudioTrackID     *string  `json:"audio_track_id"`
	AudioStartMs     *int     `json:"audio_start_ms"`
	OriginalAudioVol *float32 `json:"original_audio_volume"`
	OverlayAudioVol  *float32 `json:"overlay_audio_volume"`
	CoverMediaID     *string  `json:"cover_media_id"`
	ScheduleAt       *string  `json:"schedule_at"`
	// Video credits (migration 070, credits.go): the Credits step, under the
	// create rules. credits and genres replace ([] clears); release_year and
	// production_house take null to clear ("" too for the house). Tags are
	// only checked, linked and announced when the draft publishes.
	Credits         *[]postgres.CreditInput `json:"credits"`
	Genres          *[]string               `json:"genres"`
	ReleaseYear     json.RawMessage         `json:"release_year"`
	ProductionHouse json.RawMessage         `json:"production_house"`
	// TierRequiredID is the members-only gate the post gets at publication
	// (migration 071, members_tiers.go): a tier UUID string; null or ""
	// clears it. Raw so null is told apart from absent. Anything else is
	// ErrInvalidDraftTier. Whether it is one of the author's active tiers is
	// asked at publication, by CreatePost.
	TierRequiredID json.RawMessage `json:"tier_required_id"`
}

// ErrInvalidDraftTier: tier_required_id on a draft patch is neither a UUID
// string nor null / "" (which clear it). It matches ErrInvalidTier, so the
// routes answer 400 INVALID_TIER.
var ErrInvalidDraftTier = fmt.Errorf("%w: tier_required_id must be a tier id, or null to clear it", ErrInvalidTier)

// draftTierUpdates resolves a patch's tier_required_id into the column
// write, before anything is written: absent leaves it, null / "" clears it,
// a UUID sets it.
func draftTierUpdates(input *UpdateDraftInput, updates map[string]interface{}) error {
	raw := input.TierRequiredID
	if len(raw) == 0 {
		return nil
	}
	if rawNull(raw) {
		updates["tier_required_id"] = nil
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ErrInvalidDraftTier
	}
	s = strings.TrimSpace(s)
	if s == "" {
		updates["tier_required_id"] = nil
		return nil
	}
	id, err := uuid.Parse(s)
	if err != nil || id == uuid.Nil {
		return ErrInvalidDraftTier
	}
	updates["tier_required_id"] = id
	return nil
}

// draftCreditUpdates resolves the credit fields of a reel-draft patch into
// column writes under the create route's rules, before anything is
// written. storedKind is the draft's saved content_type ("" = never said);
// a draft that is, or is being made, a flick refuses credit data with
// ErrCreditsNotAllowed.
func draftCreditUpdates(input *UpdateDraftInput, storedKind string, now time.Time, updates map[string]interface{}) error {
	sets := false
	if input.Credits != nil {
		credits, err := ValidateCredits(*input.Credits)
		if err != nil {
			return err
		}
		if len(credits) == 0 {
			updates["credits"] = nil
		} else {
			raw, err := json.Marshal(credits)
			if err != nil {
				return err
			}
			updates["credits"] = raw
			sets = true
		}
	}
	if input.Genres != nil {
		genres, err := NormalizeGenres(*input.Genres)
		if err != nil {
			return err
		}
		updates["genres"] = genres
		sets = sets || len(genres) > 0
	}
	if len(input.ReleaseYear) > 0 {
		if rawNull(input.ReleaseYear) {
			updates["release_year"] = nil
		} else {
			var y int
			if err := json.Unmarshal(input.ReleaseYear, &y); err != nil {
				return ErrInvalidReleaseYear
			}
			if err := ValidateReleaseYear(y, now); err != nil {
				return err
			}
			updates["release_year"] = y
			sets = true
		}
	}
	if len(input.ProductionHouse) > 0 {
		house := ""
		if !rawNull(input.ProductionHouse) {
			if err := json.Unmarshal(input.ProductionHouse, &house); err != nil {
				return ErrInvalidProductionHouse
			}
		}
		h, err := NormalizeProductionHouse(house)
		if err != nil {
			return err
		}
		if h == nil {
			updates["production_house"] = nil
		} else {
			updates["production_house"] = *h
			sets = true
		}
	}
	kind := storedKind
	if input.ContentType != nil {
		kind = *input.ContentType
	}
	if sets && kind == "flick" {
		return ErrCreditsNotAllowed
	}
	return nil
}

func rawNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

// draftSetsCredits reports a patch that touches any credit field.
func draftSetsCredits(input *UpdateDraftInput) bool {
	return input.Credits != nil || input.Genres != nil || len(input.ReleaseYear) > 0 || len(input.ProductionHouse) > 0
}

// ErrInvalidDraftSound: audio_track_id on a draft patch is neither a sound
// id nor the empty string that removes the sound.
var ErrInvalidDraftSound = errors.New("audio_track_id must be a sound id, or empty to remove the sound")

// draftSoundUpdates resolves the sound of a draft patch into column writes
// (2026-09-29).
//
//   - audio_track_id absent: the sound is left as it is, and audio_start_ms,
//     when sent, moves the start.
//   - audio_track_id "": the sound is REMOVED. audio_track_id becomes NULL
//     and audio_start_ms 0, whatever start came with it. The draft used to
//     keep the sound its author had taken off, and publish it.
//   - anything else must be a sound id, or the patch is refused
//     (ErrInvalidDraftSound) before a single column is written.
//
// A negative start is 0; the upper bound is the sound's, applied when the
// link is made (clampSoundStart).
func draftSoundUpdates(input *UpdateDraftInput, updates map[string]interface{}) error {
	if input.AudioTrackID != nil {
		raw := strings.TrimSpace(*input.AudioTrackID)
		if raw == "" {
			updates["audio_track_id"] = nil
			updates["audio_start_ms"] = 0
			return nil
		}
		id, err := uuid.Parse(raw)
		if err != nil {
			return ErrInvalidDraftSound
		}
		updates["audio_track_id"] = id.String()
	}
	if input.AudioStartMs != nil {
		start := *input.AudioStartMs
		if start < 0 {
			start = 0
		}
		updates["audio_start_ms"] = start
	}
	return nil
}

// ErrInvalidDraftContentType: content_type on a draft patch is neither
// flick nor long_video.
var ErrInvalidDraftContentType = errors.New("content_type must be flick or long_video")

// draftPublishUpdates resolves the patch fields a publish copies onto the
// post under the post's own rules (2026-10-22), so a draft can never hold
// a value the publish would refuse:
//
//   - content_type is flick or long_video, or the patch is refused
//     (ErrInvalidDraftContentType) before a single column is written;
//   - hashtags pass NormalizeExplicitHashtags, the create route's rule, and
//     are stored as it canonicalises them. [] clears them.
func draftPublishUpdates(input *UpdateDraftInput, updates map[string]interface{}) error {
	if input.ContentType != nil {
		switch kind := *input.ContentType; kind {
		case "flick", "long_video":
			updates["content_type"] = kind
		default:
			return ErrInvalidDraftContentType
		}
	}
	if input.Hashtags != nil {
		tags, err := NormalizeExplicitHashtags(input.Hashtags)
		if err != nil {
			return err
		}
		if tags == nil {
			tags = []string{}
		}
		updates["hashtags"] = tags
	}
	return nil
}

// CreateDraft creates a new reel draft.
func (s *Service) CreateDraft(ctx context.Context, input *CreateDraftInput) (*postgres.ReelDraft, error) {
	vis := input.Visibility
	if vis == "" {
		vis = "public"
	}

	draft := &postgres.ReelDraft{
		ID:                uuid.New(),
		AuthorID:          input.AuthorID,
		MediaID:           input.MediaID,
		Caption:           input.Caption,
		Visibility:        vis,
		Status:            "draft",
		ModerationStatus:  "pending",
		CrossPostPostbook: false,
		PublishToFeed:     true,
		AutoChapters:      true,
		FeaturedPlaces:    true,
		AutoConcepts:      true,
		License:           "standard",
		AllowEmbedding:    true,
		RemixSetting:      "allow",
		LikesEnabled:      true,
		CommentsEnabled:   true,
		CommentModeration: "basic",
		CommentAccess:     "everyone",
		Language:          "en",
		OriginalAudioVol:  1.0,
		OverlayAudioVol:   1.0,
	}

	if err := s.pgStore.CreateDraft(ctx, draft); err != nil {
		return nil, fmt.Errorf("create draft: %w", err)
	}
	return draft, nil
}

// GetDraft fetches a draft and verifies ownership.
func (s *Service) GetDraft(ctx context.Context, draftID uuid.UUID, authorID uuid.UUID) (*postgres.ReelDraft, error) {
	draft, err := s.pgStore.GetDraft(ctx, draftID)
	if err != nil {
		return nil, fmt.Errorf("get draft: %w", err)
	}
	if draft.AuthorID != authorID {
		return nil, fmt.Errorf("not found")
	}
	return draft, nil
}

// UpdateDraft applies partial updates to a draft.
func (s *Service) UpdateDraft(ctx context.Context, draftID uuid.UUID, authorID uuid.UUID, input *UpdateDraftInput) (*postgres.ReelDraft, error) {
	updates := make(map[string]interface{})

	// First, so a patch that is refused writes nothing.
	if err := draftSoundUpdates(input, updates); err != nil {
		return nil, err
	}
	if err := draftPublishUpdates(input, updates); err != nil {
		return nil, err
	}
	if err := draftTierUpdates(input, updates); err != nil {
		return nil, err
	}
	if draftSetsCredits(input) {
		storedKind := ""
		if input.ContentType == nil {
			// The kind the draft already said decides; one read, only for a
			// patch that touches credits without naming the kind.
			if d, err := s.pgStore.GetDraft(ctx, draftID); err == nil && d != nil && d.AuthorID == authorID && d.ContentType != nil {
				storedKind = *d.ContentType
			}
		}
		if err := draftCreditUpdates(input, storedKind, s.clock(), updates); err != nil {
			return nil, err
		}
	}
	if input.Title != nil {
		updates["title"] = *input.Title
	}
	if input.Caption != nil {
		updates["caption"] = *input.Caption
	}
	if input.Tags != nil {
		updates["tags"] = input.Tags
	}
	if input.Visibility != nil {
		updates["visibility"] = *input.Visibility
	}
	if input.TopicID != nil {
		updates["topic_id"] = *input.TopicID
	}
	if input.Category != nil {
		// Slug form on the draft too, so the composer reads back exactly
		// what publication will store (publish goes through CreatePost,
		// which normalises again; this keeps the two in step).
		updates["category"] = NormalizeCategory(*input.Category)
	}
	if input.Language != nil {
		updates["language"] = *input.Language
	}
	if input.SEOTitle != nil {
		updates["seo_title"] = *input.SEOTitle
	}
	if input.CrossPostPostbook != nil {
		updates["cross_post_postbook"] = *input.CrossPostPostbook
	}
	if input.CrossPostPosttube != nil {
		updates["cross_post_posttube"] = *input.CrossPostPosttube
	}
	if input.PublishToFeed != nil {
		updates["publish_to_feed"] = *input.PublishToFeed
	}
	if input.IsMadeForKids != nil {
		updates["is_made_for_kids"] = *input.IsMadeForKids
	}
	if input.PaidPromotion != nil {
		updates["paid_promotion"] = *input.PaidPromotion
	}
	if input.AlteredContent != nil {
		updates["altered_content"] = *input.AlteredContent
	}
	if input.AutoChapters != nil {
		updates["auto_chapters"] = *input.AutoChapters
	}
	if input.FeaturedPlaces != nil {
		updates["featured_places"] = *input.FeaturedPlaces
	}
	if input.AutoConcepts != nil {
		updates["auto_concepts"] = *input.AutoConcepts
	}
	if input.License != nil {
		updates["license"] = *input.License
	}
	if input.AllowEmbedding != nil {
		updates["allow_embedding"] = *input.AllowEmbedding
	}
	if input.RemixSetting != nil {
		updates["remix_setting"] = *input.RemixSetting
	}
	if input.LikesEnabled != nil {
		updates["likes_enabled"] = *input.LikesEnabled
	}
	if input.CommentsEnabled != nil {
		updates["comments_enabled"] = *input.CommentsEnabled
	}
	if input.CommentModeration != nil {
		updates["comment_moderation"] = *input.CommentModeration
	}
	if input.CommentAccess != nil {
		updates["comment_access"] = *input.CommentAccess
	}
	if input.RecordingDate != nil {
		if *input.RecordingDate != "" {
			t, err := time.Parse("2006-01-02", *input.RecordingDate)
			if err == nil {
				updates["recording_date"] = t
			}
		}
	}
	if input.RecordingLocation != nil {
		updates["recording_location"] = *input.RecordingLocation
	}
	if input.AIGenerated != nil {
		updates["ai_generated"] = *input.AIGenerated
	}
	if input.LocationName != nil {
		if name := strings.TrimSpace(*input.LocationName); name == "" {
			updates["location_name"] = nil
		} else {
			updates["location_name"] = name
		}
	}
	if input.LocationPlaceID != nil {
		// The post's own rule, so a draft can never hold an id the
		// publish would refuse. "" clears it.
		placeID, err := NormalizeLocationPlaceID(input.LocationPlaceID)
		if err != nil {
			return nil, err
		}
		if placeID == nil {
			updates["location_place_id"] = nil
		} else {
			updates["location_place_id"] = *placeID
		}
	}
	if input.OriginalAudioVol != nil {
		updates["original_audio_volume"] = *input.OriginalAudioVol
	}
	if input.OverlayAudioVol != nil {
		updates["overlay_audio_volume"] = *input.OverlayAudioVol
	}
	if input.CoverMediaID != nil {
		if id, err := uuid.Parse(*input.CoverMediaID); err == nil {
			updates["cover_media_id"] = id
		}
	}
	if input.ScheduleAt != nil {
		if *input.ScheduleAt != "" {
			t, err := time.Parse(time.RFC3339, *input.ScheduleAt)
			if err == nil {
				updates["schedule_at"] = t
			}
		}
	}

	if len(updates) == 0 {
		return s.GetDraft(ctx, draftID, authorID)
	}

	if err := s.pgStore.UpdateDraft(ctx, draftID, authorID, updates); err != nil {
		return nil, fmt.Errorf("update draft: %w", err)
	}
	return s.pgStore.GetDraft(ctx, draftID)
}

// ListDrafts returns the author's drafts with cursor pagination.
func (s *Service) ListDrafts(ctx context.Context, authorID uuid.UUID, limit int, cursor *time.Time) ([]postgres.ReelDraft, *time.Time, error) {
	return s.pgStore.ListDrafts(ctx, authorID, limit, cursor)
}

// DeleteDraft soft-deletes a draft.
func (s *Service) DeleteDraft(ctx context.Context, draftID uuid.UUID, authorID uuid.UUID) error {
	return s.pgStore.DeleteDraft(ctx, draftID, authorID)
}

// PublishDraft converts a draft into a real post and marks it published.
func (s *Service) PublishDraft(ctx context.Context, draftID uuid.UUID, authorID uuid.UUID, scheduleAt *string) (*postgres.Post, error) {
	draft, err := s.GetDraft(ctx, draftID, authorID)
	if err != nil {
		return nil, err
	}
	if draft.Status == "published" {
		return nil, fmt.Errorf("draft already published")
	}
	if draft.Status == "deleted" {
		return nil, fmt.Errorf("draft has been deleted")
	}

	// If scheduling, just update the schedule_at and return
	if scheduleAt != nil && *scheduleAt != "" {
		t, err := time.Parse(time.RFC3339, *scheduleAt)
		if err != nil {
			return nil, fmt.Errorf("invalid schedule_at: %w", err)
		}
		updates := map[string]interface{}{
			"schedule_at": t,
			"status":      "draft",
		}
		if err := s.pgStore.UpdateDraft(ctx, draftID, authorID, updates); err != nil {
			return nil, fmt.Errorf("schedule draft: %w", err)
		}
		// Return a placeholder post with the draft ID
		return &postgres.Post{ID: draftID, AuthorID: authorID, Text: draft.Caption}, nil
	}

	// Author standing before anything is built (publish_standing.go):
	// 403 AUTHOR_SUSPENDED or 503 STANDING_UNAVAILABLE, never a post.
	if err := s.requirePublishStanding(ctx, authorID); err != nil {
		return nil, err
	}

	// P0-5 idempotency: the draft id becomes the post id, so a crash-retry
	// (or a concurrent worker that stole a stale claim) hits the posts PK
	// instead of creating a second post.
	publishPostID := draft.ID
	input := reelDraftPostInput(draft, authorID, s.unsaidDraftKind(ctx, draft))

	post, err := s.CreatePost(ctx, input)
	if err != nil {
		if postgres.IsUniqueViolation(err) {
			// Already published by a previous attempt — resolve to it.
			if existing, gerr := s.pgStore.GetPost(ctx, publishPostID); gerr == nil && existing != nil {
				s.keepDraftSound(ctx, existing, draft)
				s.pgStore.MarkDraftPublished(ctx, draftID, existing.ID) //nolint:errcheck
				return existing, nil
			}
		}
		return nil, fmt.Errorf("publish draft as post: %w", err)
	}
	s.keepDraftSound(ctx, post, draft)

	// Mark draft as published
	if err := s.pgStore.MarkDraftPublished(ctx, draftID, post.ID); err != nil {
		// Non-fatal: the post was already created
	}

	return post, nil
}

// reelDraftPostInput is the post a reel draft publishes as. Both publish
// paths — the person's and the schedule worker's — build it here, so they
// cannot drift apart again. unsaidKind is the kind to use when the draft
// never said one (unsaidDraftKind). Every UpdateDraftInput field reaches
// this input or is listed draft-only in reel_draft_fields_test.go.
func reelDraftPostInput(draft *postgres.ReelDraft, authorID uuid.UUID, unsaidKind string) *CreatePostInput {
	var mediaIDs []uuid.UUID
	if draft.MediaID != nil {
		mediaIDs = []uuid.UUID{*draft.MediaID}
	}
	postID := draft.ID
	input := &CreatePostInput{
		PostID:       &postID,
		AuthorID:     authorID,
		Text:         draft.Caption,
		Visibility:   draft.Visibility,
		ContentType:  unsaidKind,
		MediaIDs:     mediaIDs,
		CoverMediaID: draft.CoverMediaID,
		NoComments:   !draft.CommentsEnabled,
		NoLikes:      !draft.LikesEnabled,
		// Reel drafts have no download switch; the column default applies.
		AllowDownload:     true,
		PostType:          "video",
		AppOrigin:         "posttube",
		PublishToFeed:     draft.PublishToFeed,
		ShareToPostbook:   draft.CrossPostPostbook,
		Title:             draft.Title,
		Tags:              draft.Tags,
		Category:          draft.Category,
		Language:          draft.Language,
		PaidPromotion:     draft.PaidPromotion,
		AlteredContent:    draft.AlteredContent,
		IsMadeForKids:     draft.IsMadeForKids,
		License:           draft.License,
		AllowEmbedding:    draft.AllowEmbedding,
		RemixSetting:      draft.RemixSetting,
		CommentModeration: draft.CommentModeration,
		CommentAccess:     draft.CommentAccess,
		OriginalAudioVol:  draft.OriginalAudioVol,
		OverlayAudioVol:   draft.OverlayAudioVol,
	}
	applyDraftScreenFields(input, draft)
	return input
}

// applyDraftScreenFields copies what the upload studio saves beyond the
// original draft columns onto the post being created, so none of it is
// lost to a saved draft:
//
//   - the AI label and the tagged place (migration 068);
//   - the kind the studio measured (migration 069) — a long video saved as
//     a draft used to publish as a flick;
//   - the studio's hashtags, the SEO title, and the recording date and
//     place, which the draft always stored and the publish dropped;
//   - publish_to_feed=false as the explicit legacy opt-out, the way the
//     create handler forwards it, so it reaches the stored distribution
//     policy and not just the column (Codex P1-1).
func applyDraftScreenFields(input *CreatePostInput, draft *postgres.ReelDraft) {
	input.AIGenerated = draft.AIGenerated
	if draft.LocationName != nil && strings.TrimSpace(*draft.LocationName) != "" {
		input.LocationName = draft.LocationName
		input.LocationPlaceID = draft.LocationPlaceID
	}
	if draft.ContentType != nil && *draft.ContentType != "" {
		input.ContentType = *draft.ContentType
	}
	input.Hashtags = draft.Hashtags
	// Members-only from birth (migration 071): CreatePost checks the tier
	// and writes it on the row, so a draft meant for members never
	// publishes open.
	input.TierRequiredID = copyTierID(draft.TierRequiredID)
	input.SEOTitle = draft.SEOTitle
	input.RecordingDate = draft.RecordingDate
	input.RecordingLocation = draft.RecordingLocation
	if !draft.PublishToFeed {
		off := false
		input.LegacyDistribution.PublishToFeed = &off
	}
	// Video credits (migration 070): only a draft that publishes as a long
	// video carries them. A draft whose kind was never said and measured as
	// a flick publishes without them rather than failing its publication
	// (a scheduled draft has nobody watching to fix it).
	if isLongVideoContentType(normalizeLegacyContentType(input.ContentType)) {
		input.VideoCredits = VideoCreditFields{Credits: draft.Credits, Genres: draft.Genres, ReleaseYear: draft.ReleaseYear}
		if draft.ProductionHouse != nil {
			input.VideoCredits.ProductionHouse = *draft.ProductionHouse
		}
	}
}

// unsaidDraftKind is the kind a draft publishes as when its client never
// said one (content_type NULL: every draft saved before migration 069, and
// any client that does not send it). It is classified from the measured
// video the way the direct create path classifies one (ClassifyVideo), and
// stays a flick — what every legacy draft published as — while transcode
// has not measured the video yet. A draft that said its kind skips the
// lookups; applyDraftScreenFields uses the kind it said.
func (s *Service) unsaidDraftKind(ctx context.Context, draft *postgres.ReelDraft) string {
	if draft.ContentType != nil && *draft.ContentType != "" {
		return *draft.ContentType
	}
	if draft.MediaID == nil {
		return "flick"
	}
	dur := s.pgStore.ResolveMediaDuration(ctx, *draft.MediaID)
	w, h, _ := s.pgStore.ResolveMediaDimensions(ctx, *draft.MediaID)
	return classifyUnsaidDraft(dur, w, h)
}

// classifyUnsaidDraft: the measurement decides, and an unmeasured video
// (duration 0) is a flick.
func classifyUnsaidDraft(durationSeconds, width, height int) string {
	if durationSeconds <= 0 {
		return "flick"
	}
	kind, _ := ClassifyVideo(float64(durationSeconds), width, height)
	return kind
}

// PublishScheduledDrafts claims due reel drafts (SKIP LOCKED — safe with
// multiple worker replicas) and publishes each. Transient failures release
// the claim for the next tick; the draft-id-as-post-id idempotency makes
// crash retries produce exactly one post.
func (s *Service) PublishScheduledDrafts(ctx context.Context) (int, error) {
	drafts, err := s.pgStore.ClaimDueReelDrafts(ctx, time.Now().UTC(), staleClaimAfter, 50)
	if err != nil {
		return 0, fmt.Errorf("claim scheduled reel drafts: %w", err)
	}

	published := 0
	for _, draft := range drafts {
		_, pubErr := s.publishClaimedReelDraft(ctx, draft.ID, draft.AuthorID)
		if pubErr != nil {
			s.pgStore.ReleaseReelDraftClaim(ctx, draft.ID) //nolint:errcheck
			continue
		}
		published++
	}
	return published, nil
}

// publishClaimedReelDraft publishes a draft this worker has already
// claimed (status publishing_pending — PublishDraft's editable-status
// guard doesn't apply to a claimed row).
func (s *Service) publishClaimedReelDraft(ctx context.Context, draftID, authorID uuid.UUID) (*postgres.Post, error) {
	draft, err := s.GetDraft(ctx, draftID, authorID)
	if err != nil {
		return nil, err
	}
	if draft.Status == "published" || draft.Status == "deleted" {
		return nil, fmt.Errorf("draft no longer publishable (status %s)", draft.Status)
	}
	// Author standing (publish_standing.go), background policy: a refusal
	// parks the draft as 'blocked' with the reason; an unknown is retried
	// with per-item backoff (the caller releases the claim) and parked as
	// standing_unavailable after 24 h of it.
	switch verdict, reason, serr := s.backgroundPublishStanding(ctx, standingKeyReelDraft(draftID), authorID); verdict {
	case standingProceed:
	case standingBlock:
		s.pgStore.MarkReelDraftBlocked(ctx, draftID, reason) //nolint:errcheck
		return nil, serr
	default:
		return nil, serr
	}
	publishPostID := draft.ID
	input := reelDraftPostInput(draft, authorID, s.unsaidDraftKind(ctx, draft))
	post, err := s.CreatePost(ctx, input)
	if err != nil {
		if postgres.IsUniqueViolation(err) {
			if existing, gerr := s.pgStore.GetPost(ctx, publishPostID); gerr == nil && existing != nil {
				s.keepDraftSound(ctx, existing, draft)
				s.pgStore.MarkDraftPublished(ctx, draftID, existing.ID) //nolint:errcheck
				return existing, nil
			}
		}
		// A tier that is no longer the author's is terminal: park the draft
		// (the author re-arms it by editing) rather than retry it forever.
		// An unreachable monetization-service is transient: the caller
		// releases the claim. Neither publishes the post open.
		if errors.Is(err, ErrInvalidTier) {
			s.pgStore.MarkReelDraftBlocked(ctx, draftID, BlockReasonInvalidTier) //nolint:errcheck
		}
		return nil, fmt.Errorf("publish claimed draft: %w", err)
	}
	s.keepDraftSound(ctx, post, draft)
	if err := s.pgStore.MarkDraftPublished(ctx, draftID, post.ID); err != nil {
		// Non-fatal: idempotency resolves this on the stale-claim retry.
	}
	return post, nil
}

// keepDraftSound carries the sound a reel draft chose (audio_track_id,
// audio_start_ms) onto the post published from it; both publish paths used
// to drop it. It runs on the crash-retry path too: the link is idempotent,
// and a first attempt may have died between the create and the link.
// Best-effort, as on the create route: the post exists, so a sound its
// author may not use is logged and the reel goes out with its own audio.
func (s *Service) keepDraftSound(ctx context.Context, post *postgres.Post, draft *postgres.ReelDraft) {
	if post == nil || draft == nil {
		return
	}
	link, err := s.attachDraftSound(ctx, post.AuthorID, post.ID, draft.AudioTrackID, draft.AudioStartMs)
	if err != nil {
		slog.Warn("reel draft publish: attach audio failed",
			"post_id", post.ID, "draft_id", draft.ID, "err", err)
		return
	}
	ApplySoundLink(post, link)
}
