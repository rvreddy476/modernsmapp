package service

import (
	"context"
	"log/slog"
	"time"
)

// Lane D6 — the media recheck sweeper (photo_safety.go explains why a
// sweeper rather than a consumer). Every replica may run it: a duplicate
// result is a no-op write of the recheck cursor.

// photoRecheckTick is how often one batch is checked.
const photoRecheckTick = 5 * time.Minute

// StartPhotoRecheck runs RecheckPhotoMedia every photoRecheckTick until ctx
// ends.
func (s *Service) StartPhotoRecheck(ctx context.Context) {
	ticker := time.NewTicker(photoRecheckTick)
	defer ticker.Stop()
	for {
		n, err := s.RecheckPhotoMedia(ctx, time.Now())
		if err != nil {
			slog.Warn("dating photo recheck pass incomplete", "checked", n, "error", err)
		} else if n > 0 {
			slog.Info("dating photo recheck pass", "checked", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
