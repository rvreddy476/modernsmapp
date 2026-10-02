package postgres

// Going-live eligibility (2 Oct 2026): what the new-streamer viewer cap
// reads. No migration: both answers come from columns and tables that
// already exist (live_streams, live_stream_presence).

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ReasonAdminStopped is the ended_reason of a stream an admin stopped.
const ReasonAdminStopped = "admin_stopped"

// CompletedStream reports whether st counts as a completed stream of its
// creator: it ended (not failed), it was on air for at least minLive
// (started_at to ended_at), and it was not stopped by an admin. The same
// "ended after N minutes on air" the founding creator badge uses, minus the
// streams an admin had to stop.
func CompletedStream(st *LiveStream, minLive time.Duration) bool {
	if st == nil || st.Status != StatusEnded || st.StartedAt == nil || st.EndedAt == nil {
		return false
	}
	if st.EndedReason != nil && *st.EndedReason == ReasonAdminStopped {
		return false
	}
	return st.EndedAt.Sub(*st.StartedAt) >= minLive
}

// CountCompletedStreams counts the creator's completed streams
// (CompletedStream).
func (s *Store) CountCompletedStreams(ctx context.Context, creatorID uuid.UUID, minLive time.Duration) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*)::int
		FROM live_streams
		WHERE creator_user_id = $1
		  AND status = 'ended'
		  AND started_at IS NOT NULL AND ended_at IS NOT NULL
		  AND ended_at - started_at >= make_interval(secs => $2)
		  AND ended_reason IS DISTINCT FROM 'admin_stopped'`,
		creatorID, minLive.Seconds()).Scan(&n)
	return n, err
}

// IsViewerPresent reports whether the user is in the stream's room right
// now, by the presence rows the LiveKit webhooks keep.
func (s *Store) IsViewerPresent(ctx context.Context, streamID, userID uuid.UUID) (bool, error) {
	var present bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (
		    SELECT 1 FROM live_stream_presence
		    WHERE stream_id = $1 AND user_id = $2 AND present
		)`, streamID, userID).Scan(&present)
	return present, err
}
