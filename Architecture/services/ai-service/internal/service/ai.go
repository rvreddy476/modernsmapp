package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/ai-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const cachePrefix = "ai:"
const captionTTL = time.Hour
const hashtagTTL = time.Hour
const smartReplyTTL = 15 * time.Minute
const moderationTTL = time.Hour

// Service holds business logic for the ai-service.
type Service struct {
	store *postgres.Store
	rdb   *redis.Client
}

// New returns a new Service.
func New(store *postgres.Store, rdb *redis.Client) *Service {
	return &Service{store: store, rdb: rdb}
}

// EnqueueJob creates a job record with status=queued and returns it.
func (s *Service) EnqueueJob(ctx context.Context, jobType, refType string, refID, requesterID uuid.UUID) (*postgres.AIJob, error) {
	job := &postgres.AIJob{
		JobType:      jobType,
		InputRefType: refType,
		InputRefID:   refID,
	}
	if requesterID != uuid.Nil {
		job.RequesterID = &requesterID
	}
	if err := s.store.CreateJob(ctx, job); err != nil {
		return nil, fmt.Errorf("enqueue job: %w", err)
	}
	slog.Info("job enqueued", "job_id", job.ID, "job_type", jobType)
	return job, nil
}

// GetJob returns job status and result.
func (s *Service) GetJob(ctx context.Context, id uuid.UUID) (*postgres.AIJob, error) {
	job, err := s.store.GetJob(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get job: %w", err)
	}
	return job, nil
}

// GetCachedCaption checks Redis ai:caption:{draftID} → returns suggestions slice or nil.
func (s *Service) GetCachedCaption(ctx context.Context, draftID uuid.UUID) ([]string, error) {
	key := cachePrefix + "caption:" + draftID.String()
	val, err := s.rdb.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get cached caption: %w", err)
	}
	var suggestions []string
	if err := json.Unmarshal([]byte(val), &suggestions); err != nil {
		return nil, fmt.Errorf("unmarshal caption cache: %w", err)
	}
	return suggestions, nil
}

// CacheCaption stores AI caption suggestions in Redis with 1h TTL.
func (s *Service) CacheCaption(ctx context.Context, draftID uuid.UUID, suggestions []string) error {
	key := cachePrefix + "caption:" + draftID.String()
	data, err := json.Marshal(suggestions)
	if err != nil {
		return fmt.Errorf("marshal caption: %w", err)
	}
	return s.rdb.SetEx(ctx, key, string(data), captionTTL).Err()
}

// GetCachedHashtags checks Redis ai:hashtags:{draftID} → returns tags or nil.
func (s *Service) GetCachedHashtags(ctx context.Context, draftID uuid.UUID) ([]string, error) {
	key := cachePrefix + "hashtags:" + draftID.String()
	val, err := s.rdb.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get cached hashtags: %w", err)
	}
	var tags []string
	if err := json.Unmarshal([]byte(val), &tags); err != nil {
		return nil, fmt.Errorf("unmarshal hashtag cache: %w", err)
	}
	return tags, nil
}

// CacheHashtags stores AI hashtag suggestions in Redis with 1h TTL.
func (s *Service) CacheHashtags(ctx context.Context, draftID uuid.UUID, tags []string) error {
	key := cachePrefix + "hashtags:" + draftID.String()
	data, err := json.Marshal(tags)
	if err != nil {
		return fmt.Errorf("marshal hashtags: %w", err)
	}
	return s.rdb.SetEx(ctx, key, string(data), hashtagTTL).Err()
}

// GetCachedSmartReplies checks Redis ai:smart_reply:{convID}.
func (s *Service) GetCachedSmartReplies(ctx context.Context, convID uuid.UUID) ([]string, error) {
	key := cachePrefix + "smart_reply:" + convID.String()
	val, err := s.rdb.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get cached smart replies: %w", err)
	}
	var replies []string
	if err := json.Unmarshal([]byte(val), &replies); err != nil {
		return nil, fmt.Errorf("unmarshal smart reply cache: %w", err)
	}
	return replies, nil
}

// CacheSmartReplies stores smart reply suggestions with 15min TTL.
func (s *Service) CacheSmartReplies(ctx context.Context, convID uuid.UUID, replies []string) error {
	key := cachePrefix + "smart_reply:" + convID.String()
	data, err := json.Marshal(replies)
	if err != nil {
		return fmt.Errorf("marshal smart replies: %w", err)
	}
	return s.rdb.SetEx(ctx, key, string(data), smartReplyTTL).Err()
}

// CheckModeration looks up cached toxicity score in Redis ai:toxicity:{contentID}, falls back to DB.
func (s *Service) CheckModeration(ctx context.Context, contentType string, contentID uuid.UUID) (*postgres.ModerationResult, error) {
	key := cachePrefix + "toxicity:" + contentID.String()
	val, err := s.rdb.Get(ctx, key).Result()
	if err == nil {
		r := &postgres.ModerationResult{}
		if jsonErr := json.Unmarshal([]byte(val), r); jsonErr == nil {
			return r, nil
		}
	}

	result, err := s.store.GetModerationResult(ctx, contentType, contentID)
	if err != nil {
		return nil, fmt.Errorf("check moderation: %w", err)
	}
	return result, nil
}

// RecordModerationResult saves AI moderation result to DB and caches in Redis.
func (s *Service) RecordModerationResult(ctx context.Context, r *postgres.ModerationResult) error {
	if err := s.store.CreateModerationResult(ctx, r); err != nil {
		return fmt.Errorf("record moderation result: %w", err)
	}

	key := cachePrefix + "toxicity:" + r.ContentID.String()
	data, err := json.Marshal(r)
	if err == nil {
		if setErr := s.rdb.SetEx(ctx, key, string(data), moderationTTL).Err(); setErr != nil {
			slog.Warn("failed to cache moderation result", "error", setErr)
		}
	}
	return nil
}
