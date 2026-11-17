package scylla

import (
	"context"
	"fmt"

	"github.com/atpost/analytics-service/internal/model"
	"github.com/google/uuid"
)

// Account erasure (reels-8) for the Scylla social_analytics tables this
// service writes or owns:
//
//   - watch_sessions ((content_id), session_id): viewer_id is a regular
//     column, so a viewer's rows are found by the keys PostgreSQL holds for
//     them (postgres.Store.WatchSessionKeys) and deleted one by one.
//   - viewer_history ((viewer_id), content_id): one partition per viewer.
//   - reel_views, content_engagement: no code writes them (RecordReelView and
//     RecordEngagement have no caller; purge_writers_test.go keeps it so), and
//     neither can be found by viewer without a full scan. Should a writer
//     come back, it must come with an erase here.

const (
	deleteWatchSessionCQL  = `DELETE FROM social_analytics.watch_sessions WHERE content_id = ? AND session_id = ?`
	deleteViewerHistoryCQL = `DELETE FROM social_analytics.viewer_history WHERE viewer_id = ?`
)

// DeleteWatchSessions removes the given watch_sessions rows. Deleting an
// absent row is a no-op, so a retry is safe.
func (s *WatchStore) DeleteWatchSessions(ctx context.Context, keys []model.WatchSessionKey) error {
	for _, k := range keys {
		if err := s.session.Query(deleteWatchSessionCQL, cql(k.ContentID), k.SessionID).WithContext(ctx).Exec(); err != nil {
			return fmt.Errorf("purge: delete watch session %s/%s: %w", k.ContentID, k.SessionID, err)
		}
	}
	return nil
}

// DeleteViewerHistory removes the viewer's viewer_history partition.
func (s *WatchStore) DeleteViewerHistory(ctx context.Context, viewerID uuid.UUID) error {
	if err := s.session.Query(deleteViewerHistoryCQL, cql(viewerID)).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("purge: delete viewer history: %w", err)
	}
	return nil
}
