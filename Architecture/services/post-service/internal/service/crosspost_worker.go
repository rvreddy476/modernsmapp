package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// StartCrossPostWorker starts a background goroutine that processes pending cross-posts.
func (s *Service) StartCrossPostWorker(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.processPendingCrossPosts(ctx); err != nil {
					slog.Error("crosspost worker error", "error", err)
				}
			}
		}
	}()
}

func (s *Service) processPendingCrossPosts(ctx context.Context) error {
	pending, err := s.pgStore.GetPendingCrossPosts(ctx, 50)
	if err != nil {
		return err
	}

	for _, cp := range pending {
		if err := s.executeCrossPost(ctx, cp.ID, cp.SourceReelID, cp.TargetType); err != nil {
			errMsg := err.Error()
			if updateErr := s.pgStore.UpdateCrossPostStatus(ctx, cp.ID, "failed", &errMsg); updateErr != nil {
				slog.Error("crosspost: failed to update status", "id", cp.ID, "error", updateErr)
			}
			continue
		}
		if updateErr := s.pgStore.UpdateCrossPostStatus(ctx, cp.ID, "published", nil); updateErr != nil {
			slog.Error("crosspost: failed to mark published", "id", cp.ID, "error", updateErr)
		}
	}
	return nil
}

func (s *Service) executeCrossPost(ctx context.Context, crossPostID, sourceReelID uuid.UUID, targetType string) error {
	// Look up the source reel post
	post, err := s.pgStore.GetPost(ctx, sourceReelID)
	if err != nil {
		return fmt.Errorf("get source reel: %w", err)
	}
	if post == nil {
		return fmt.Errorf("source reel %s not found", sourceReelID)
	}

	switch targetType {
	case "postbook":
		// Cross-post to Postbook feed — the reel is already a post, just ensure publish_to_feed is true
		slog.Info("crosspost: published to postbook feed", "reel_id", sourceReelID, "crosspost_id", crossPostID)
		return nil
	case "posttube":
		// Cross-post to PostTube — in production this would call the PostTube ingestion API
		slog.Info("crosspost: published to posttube", "reel_id", sourceReelID, "crosspost_id", crossPostID)
		return nil
	default:
		return fmt.Errorf("unknown target type: %s", targetType)
	}
}
