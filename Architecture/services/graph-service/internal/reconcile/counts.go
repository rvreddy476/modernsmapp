package reconcile

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CountReconciler periodically corrects denormalized follower/following/friend counts.
type CountReconciler struct {
	db *pgxpool.Pool
}

func NewCountReconciler(db *pgxpool.Pool) *CountReconciler {
	return &CountReconciler{db: db}
}

// Start runs the reconciler on an interval (call in a goroutine).
func (r *CountReconciler) Start(ctx context.Context) {
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

func (r *CountReconciler) reconcile(ctx context.Context) {
	// Fix follower counts
	_, err := r.db.Exec(ctx, `
		UPDATE counts c
		SET follower_count = actual.cnt,
		    updated_at = NOW()
		FROM (
		    SELECT followee_id AS user_id, COUNT(*) AS cnt
		    FROM follows
		    GROUP BY followee_id
		) actual
		WHERE c.user_id = actual.user_id
		  AND c.follower_count != actual.cnt
	`)
	if err != nil {
		slog.Error("reconcile: follower count update failed", "error", err)
		return
	}

	// Fix following counts
	_, err = r.db.Exec(ctx, `
		UPDATE counts c
		SET following_count = actual.cnt,
		    updated_at = NOW()
		FROM (
		    SELECT follower_id AS user_id, COUNT(*) AS cnt
		    FROM follows
		    GROUP BY follower_id
		) actual
		WHERE c.user_id = actual.user_id
		  AND c.following_count != actual.cnt
	`)
	if err != nil {
		slog.Error("reconcile: following count update failed", "error", err)
	}

	slog.Info("reconcile: graph counts reconciliation complete")
}
