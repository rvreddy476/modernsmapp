package scoring

import (
	"context"
	"log"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/gocql/gocql"
)

// TrustFactorWorker periodically scans for suspicious sessions and computes
// TrustFactor scores.
type TrustFactorWorker struct {
	rdb    *redis.Client
	scylla *gocql.Session
}

func NewTrustFactorWorker(rdb *redis.Client, scylla *gocql.Session) *TrustFactorWorker {
	return &TrustFactorWorker{rdb: rdb, scylla: scylla}
}

// Start runs the TrustFactor computation loop every 10 minutes.
func (w *TrustFactorWorker) Start(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	log.Println("[TrustFactorWorker] started (10 min interval)")

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.processSuspiciousSessions(ctx)
		}
	}
}

func (w *TrustFactorWorker) processSuspiciousSessions(ctx context.Context) {
	// Scan Redis for sess:anomaly:* keys
	var cursor uint64
	processed := 0

	for {
		keys, nextCursor, err := w.rdb.Scan(ctx, cursor, "sess:anomaly:*", 100).Result()
		if err != nil {
			log.Printf("[TrustFactorWorker] scan error: %v", err)
			return
		}

		for _, key := range keys {
			sessionID := key[len("sess:anomaly:"):]
			tf := w.computeSessionTrustFactor(ctx, sessionID)
			w.applyTrustFactor(ctx, sessionID, tf)
			processed++
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	if processed > 0 {
		log.Printf("[TrustFactorWorker] processed %d suspicious sessions", processed)
	}
}

func (w *TrustFactorWorker) computeSessionTrustFactor(ctx context.Context, sessionID string) float64 {
	anomalies, err := w.rdb.HGetAll(ctx, "sess:anomaly:"+sessionID).Result()
	if err != nil {
		return 1.0
	}

	repeatedMilestones, _ := strconv.ParseInt(anomalies["repeated_milestones"], 10, 64)
	excessiveLoops, _ := strconv.ParseInt(anomalies["excessive_loops"], 10, 64)

	// Scoring logic per PRD Section 9:
	// 1.0 = normal, 0.7 = mildly suspicious, 0.3 = highly suspicious, 0.0 = confirmed fraud

	tf := 1.0

	// Repeated milestones: many milestone events suggest seek abuse
	if repeatedMilestones > 50 {
		tf = 0.0 // confirmed fraud
	} else if repeatedMilestones > 30 {
		tf = min(tf, 0.3)
	} else if repeatedMilestones > 15 {
		tf = min(tf, 0.7)
	}

	// Excessive loops: too many replays in a short time
	if excessiveLoops > 20 {
		tf = 0.0
	} else if excessiveLoops > 10 {
		tf = min(tf, 0.3)
	} else if excessiveLoops > 5 {
		tf = min(tf, 0.7)
	}

	return tf
}

func (w *TrustFactorWorker) applyTrustFactor(ctx context.Context, sessionID string, tf float64) {
	// Cache in Redis for inline VQS computation (10 min TTL)
	w.rdb.Set(ctx, "trust:sess:"+sessionID, tf, 10*time.Minute)

	// If ScyllaDB is available, try to update the watch session
	if w.scylla != nil && tf < 1.0 {
		// We can't easily find all content_ids for a session without a secondary index.
		// The TrustFactor is primarily used via Redis cache during VQS computation.
		// The hourly aggregation job will pick up the cached value when computing VQS.
		log.Printf("[TrustFactorWorker] session %s trust_factor=%.2f", sessionID, tf)
	}
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// GetTrustFactor retrieves the cached TrustFactor for a session.
// Returns 1.0 (normal) if not found.
func GetTrustFactor(ctx context.Context, rdb *redis.Client, sessionID string) float64 {
	val, err := rdb.Get(ctx, "trust:sess:"+sessionID).Float64()
	if err != nil {
		return 1.0
	}
	return val
}

// GetViewerTrustFactor retrieves a viewer-level TrustFactor (aggregated across sessions).
// Returns 1.0 (normal) if not found.
func GetViewerTrustFactor(ctx context.Context, rdb *redis.Client, viewerID uuid.UUID) float64 {
	val, err := rdb.Get(ctx, "trust:"+viewerID.String()).Float64()
	if err != nil {
		return 1.0
	}
	return val
}
