// Command backfill-connections is a one-time data-migration tool.
//
// The platform historically ran two parallel friend systems: profile-service's
// `profile.friendships` table (identity database) and graph-service's
// `connections` / `connection_requests` tables (app database). The friendships
// table is being retired. This tool copies the existing friendship data into
// graph-service's tables so no one loses their friends.
//
// It is NOT wired into any service startup — it is run manually, once, by an
// operator:
//
//	IDENTITY_POSTGRES_DSN=postgres://... \
//	POSTGRES_DSN=postgres://... \
//	go run ./cmd/backfill-connections
//
// The tool is idempotent: every write uses ON CONFLICT ... DO NOTHING, so it is
// safe to re-run (e.g. after a partial run or to pick up newly created rows).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// stats is the summary of a single backfill run.
type stats struct {
	scanned         int // friendship rows read from the source
	connections     int // rows inserted into connections (accepted friendships)
	pendingRequests int // rows inserted into connection_requests (pending friendships)
	skipped         int // declined/blocked rows that were intentionally ignored
}

func main() {
	ctx := context.Background()

	identityDSN := os.Getenv("IDENTITY_POSTGRES_DSN")
	if identityDSN == "" {
		slog.Error("IDENTITY_POSTGRES_DSN is not set — this must point at the identity database (source)")
		os.Exit(1)
	}
	appDSN := os.Getenv("POSTGRES_DSN")
	if appDSN == "" {
		slog.Error("POSTGRES_DSN is not set — this must point at the app database (destination)")
		os.Exit(1)
	}

	identityPool, err := pgxpool.New(ctx, identityDSN)
	if err != nil {
		slog.Error("failed to connect to identity database", "error", err)
		os.Exit(1)
	}
	defer identityPool.Close()

	if err := identityPool.Ping(ctx); err != nil {
		slog.Error("identity database ping failed", "error", err)
		os.Exit(1)
	}

	appPool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		slog.Error("failed to connect to app database", "error", err)
		os.Exit(1)
	}
	defer appPool.Close()

	if err := appPool.Ping(ctx); err != nil {
		slog.Error("app database ping failed", "error", err)
		os.Exit(1)
	}

	slog.Info("connected to both databases — starting backfill")

	result, err := run(ctx, identityPool, appPool)
	if err != nil {
		slog.Error("backfill failed", "error", err,
			"friendships_scanned", result.scanned,
			"connections_inserted", result.connections,
			"pending_requests_inserted", result.pendingRequests,
			"skipped", result.skipped)
		os.Exit(1)
	}

	slog.Info("backfill complete",
		"friendships_scanned", result.scanned,
		"connections_inserted", result.connections,
		"pending_requests_inserted", result.pendingRequests,
		"skipped_declined_or_blocked", result.skipped)
}

// run performs the migration: it reads every row from profile.friendships in
// the identity database and writes the relevant ones into the connections /
// connection_requests tables of the app database.
//
// Splitting this out from main() keeps the logic testable with two pools.
func run(ctx context.Context, identityPool, appPool *pgxpool.Pool) (stats, error) {
	var st stats

	rows, err := identityPool.Query(ctx, `
		SELECT id, requester_id, addressee_id, status, created_at, updated_at
		FROM profile.friendships`)
	if err != nil {
		return st, fmt.Errorf("query profile.friendships: %w", err)
	}
	defer rows.Close()

	const insertConnection = `
		INSERT INTO connections (user_a, user_b, source_request_id, created_at)
		VALUES ($1, $2, NULL, $3)
		ON CONFLICT (user_a, user_b) DO NOTHING`

	const insertRequest = `
		INSERT INTO connection_requests (
			sender_id, receiver_id, status, source, message, risk_score,
			created_at, updated_at, responded_at, expires_at)
		VALUES ($1, $2, 'pending', 'profile', NULL, 0, $3, $4, NULL, $5)
		ON CONFLICT (sender_id, receiver_id) DO NOTHING`

	for rows.Next() {
		var (
			id          string
			requesterID string
			addresseeID string
			status      string
			createdAt   time.Time
			updatedAt   time.Time
		)
		if err := rows.Scan(&id, &requesterID, &addresseeID, &status, &createdAt, &updatedAt); err != nil {
			return st, fmt.Errorf("scan friendship row: %w", err)
		}
		st.scanned++

		switch status {
		case "accepted":
			// Normalize the pair so user_a < user_b (string compare of the
			// UUID strings) to match the canonical layout of connections.
			userA, userB := requesterID, addresseeID
			if userA > userB {
				userA, userB = userB, userA
			}
			tag, err := appPool.Exec(ctx, insertConnection, userA, userB, createdAt)
			if err != nil {
				return st, fmt.Errorf("insert connection for friendship %s: %w", id, err)
			}
			st.connections += int(tag.RowsAffected())

		case "pending":
			expiresAt := createdAt.Add(30 * 24 * time.Hour)
			tag, err := appPool.Exec(ctx, insertRequest,
				requesterID, addresseeID, createdAt, updatedAt, expiresAt)
			if err != nil {
				return st, fmt.Errorf("insert connection_request for friendship %s: %w", id, err)
			}
			st.pendingRequests += int(tag.RowsAffected())

		case "declined", "blocked":
			// Intentionally not migrated — graph-service models blocks
			// separately and declined requests carry no useful state.
			st.skipped++

		default:
			slog.Warn("unknown friendship status — skipping", "friendship_id", id, "status", status)
			st.skipped++
		}
	}
	if err := rows.Err(); err != nil {
		return st, fmt.Errorf("iterate profile.friendships: %w", err)
	}

	return st, nil
}
