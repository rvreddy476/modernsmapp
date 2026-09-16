package store

import (
	"context"
	"fmt"
	"time"
)

// AdminStats are the counts the Dating admin dashboard shows (admin console
// Wave 2). Every figure uses the same predicate as the queue it summarises,
// so a number on the dashboard matches the list behind it.
type AdminStats struct {
	// Work queues.
	ReportsPending      int `json:"reports_pending"`
	PanicOpen           int `json:"panic_open"`
	PhotosPendingReview int `json:"photos_pending_review"`
	SelfiesInReview     int `json:"selfies_in_review"`

	// Population.
	ProfilesActive     int `json:"profiles_active"`
	ProfilesRestricted int `json:"profiles_restricted"`
	ProfilesSuspended  int `json:"profiles_suspended"`
	ProfilesNewToday   int `json:"profiles_new_today"`
	ProfilesNew7d      int `json:"profiles_new_7d"`

	// Volume.
	MatchesToday int `json:"matches_today"`
	Matches7d    int `json:"matches_7d"`

	// DayStartsAt is the start of "today" the *_today counts use: midnight in
	// India (Asia/Kolkata), where the product runs.
	DayStartsAt time.Time `json:"day_starts_at"`
	GeneratedAt time.Time `json:"generated_at"`
}

// ReportPendingStatuses are the report states still waiting on a moderator.
var ReportPendingStatuses = []string{"submitted", "under_review", "investigating"}

// AdminStats reads the dashboard counts in one round trip. Each is a plain
// COUNT over an indexed status or timestamp column; nothing here scans a
// message table or joins across users.
func (s *Store) AdminStats(ctx context.Context) (*AdminStats, error) {
	out := &AdminStats{}
	err := s.db.QueryRow(ctx, `
        WITH bounds AS (
            SELECT (date_trunc('day', now() AT TIME ZONE 'Asia/Kolkata') AT TIME ZONE 'Asia/Kolkata') AS day_start,
                   now() - interval '7 days' AS week_start,
                   now() AS generated_at
        )
        SELECT
            (SELECT COUNT(*) FROM dating_reports WHERE status = ANY($1))::int,
            (SELECT COUNT(*) FROM dating_panic_incidents WHERE status = 'open')::int,
            (SELECT COUNT(*) FROM dating_photos WHERE moderation_status IN ('pending', 'pending_review'))::int,
            (SELECT COUNT(*) FROM dating_verifications WHERE selfie_status = 'pending_review')::int,
            (SELECT COUNT(*) FROM dating_profiles WHERE profile_status = 'active')::int,
            (SELECT COUNT(*) FROM dating_profiles WHERE profile_status = 'restricted')::int,
            (SELECT COUNT(*) FROM dating_profiles WHERE profile_status = 'suspended')::int,
            (SELECT COUNT(*) FROM dating_profiles, bounds WHERE created_at >= bounds.day_start)::int,
            (SELECT COUNT(*) FROM dating_profiles, bounds WHERE created_at >= bounds.week_start)::int,
            (SELECT COUNT(*) FROM dating_matches, bounds WHERE matched_at >= bounds.day_start)::int,
            (SELECT COUNT(*) FROM dating_matches, bounds WHERE matched_at >= bounds.week_start)::int,
            bounds.day_start, bounds.generated_at
        FROM bounds`, ReportPendingStatuses).Scan(
		&out.ReportsPending, &out.PanicOpen, &out.PhotosPendingReview, &out.SelfiesInReview,
		&out.ProfilesActive, &out.ProfilesRestricted, &out.ProfilesSuspended,
		&out.ProfilesNewToday, &out.ProfilesNew7d, &out.MatchesToday, &out.Matches7d,
		&out.DayStartsAt, &out.GeneratedAt)
	if err != nil {
		return nil, fmt.Errorf("admin stats: %w", err)
	}
	return out, nil
}
