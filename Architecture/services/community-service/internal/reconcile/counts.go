package reconcile

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MemberCountReconciler periodically corrects the denormalized
// communities.member_count column. IncrementMemberCount / decrement
// calls can be missed by network blips, Kafka consumer crashes, or
// service redeploys mid-write — this sweeper makes every count
// eventually consistent with the row count in community_members.
type MemberCountReconciler struct {
	db *pgxpool.Pool
}

func NewMemberCountReconciler(db *pgxpool.Pool) *MemberCountReconciler {
	return &MemberCountReconciler{db: db}
}

func (r *MemberCountReconciler) Start(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.reconcile(ctx)
		}
	}
}

func (r *MemberCountReconciler) reconcile(ctx context.Context) {
	_, err := r.db.Exec(ctx, `
		UPDATE communities c
		SET member_count = actual.cnt,
		    updated_at = NOW()
		FROM (
		    SELECT community_id, COUNT(*) AS cnt
		    FROM community_members
		    WHERE role NOT IN ('banned','pending')
		    GROUP BY community_id
		) actual
		WHERE c.id = actual.community_id
		  AND c.member_count != actual.cnt
	`)
	if err != nil {
		slog.Error("reconcile: community member count update failed", "error", err)
		return
	}
	slog.Info("reconcile: community member counts reconciliation complete")
}
