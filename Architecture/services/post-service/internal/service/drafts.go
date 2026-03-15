package service

import (
	"context"
	"fmt"
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
	Title              *string   `json:"title"`
	Caption            *string   `json:"caption"`
	Hashtags           []string  `json:"hashtags"`
	Tags               []string  `json:"tags"`
	Visibility         *string   `json:"visibility"`
	TopicID            *int      `json:"topic_id"`
	Category           *string   `json:"category"`
	Language           *string   `json:"language"`
	SEOTitle           *string   `json:"seo_title"`
	CrossPostPostbook  *bool     `json:"cross_post_postbook"`
	CrossPostPosttube  *bool     `json:"cross_post_posttube"`
	PublishToFeed      *bool     `json:"publish_to_feed"`
	IsMadeForKids      *bool     `json:"is_made_for_kids"`
	PaidPromotion      *bool     `json:"paid_promotion"`
	AlteredContent     *bool     `json:"altered_content"`
	AutoChapters       *bool     `json:"auto_chapters"`
	FeaturedPlaces     *bool     `json:"featured_places"`
	AutoConcepts       *bool     `json:"auto_concepts"`
	License            *string   `json:"license"`
	AllowEmbedding     *bool     `json:"allow_embedding"`
	RemixSetting       *string   `json:"remix_setting"`
	LikesEnabled       *bool     `json:"likes_enabled"`
	CommentsEnabled    *bool     `json:"comments_enabled"`
	CommentModeration  *string   `json:"comment_moderation"`
	CommentAccess      *string   `json:"comment_access"`
	RecordingDate      *string   `json:"recording_date"`
	RecordingLocation  *string   `json:"recording_location"`
	AudioTrackID       *string   `json:"audio_track_id"`
	AudioStartMs       *int      `json:"audio_start_ms"`
	OriginalAudioVol   *float32  `json:"original_audio_volume"`
	OverlayAudioVol    *float32  `json:"overlay_audio_volume"`
	CoverMediaID       *string   `json:"cover_media_id"`
	ScheduleAt         *string   `json:"schedule_at"`
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

	if input.Title != nil {
		updates["title"] = *input.Title
	}
	if input.Caption != nil {
		updates["caption"] = *input.Caption
	}
	if input.Hashtags != nil {
		updates["hashtags"] = input.Hashtags
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
		updates["category"] = *input.Category
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
	if input.AudioTrackID != nil {
		updates["audio_track_id"] = *input.AudioTrackID
	}
	if input.AudioStartMs != nil {
		updates["audio_start_ms"] = *input.AudioStartMs
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

	// Build CreatePostInput from draft
	var mediaIDs []uuid.UUID
	if draft.MediaID != nil {
		mediaIDs = []uuid.UUID{*draft.MediaID}
	}

	input := &CreatePostInput{
		AuthorID:          authorID,
		Text:              draft.Caption,
		Visibility:        draft.Visibility,
		ContentType:       "flick", // classified at upload time; legacy drafts default to flick
		MediaIDs:          mediaIDs,
		CoverMediaID:      draft.CoverMediaID,
		NoComments:        !draft.CommentsEnabled,
		NoLikes:           !draft.LikesEnabled,
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

	post, err := s.CreatePost(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("publish draft as post: %w", err)
	}

	// Mark draft as published
	if err := s.pgStore.MarkDraftPublished(ctx, draftID, post.ID); err != nil {
		// Non-fatal: the post was already created
	}

	return post, nil
}

// PublishScheduledDrafts finds drafts past their schedule_at and publishes them.
func (s *Service) PublishScheduledDrafts(ctx context.Context) (int, error) {
	drafts, err := s.pgStore.GetScheduledDrafts(ctx, time.Now().UTC(), 50)
	if err != nil {
		return 0, fmt.Errorf("get scheduled drafts: %w", err)
	}

	published := 0
	for _, draft := range drafts {
		_, pubErr := s.PublishDraft(ctx, draft.ID, draft.AuthorID, nil)
		if pubErr != nil {
			continue
		}
		published++
	}
	return published, nil
}
