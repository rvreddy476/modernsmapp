package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Module 3 M3-P0-1 / SR-3 — reconciling the legacy shadow graph.
//
// profile-service kept its own `profile.blocks` table. Nothing enforced it:
// feed, search, chat and notifications all read graph-service's `blocks`. So
// every row in `profile.blocks` is a block a real user asked for and never
// received.
//
// Retiring the routes stops NEW divergence. It does not help the people who
// already pressed Block and were quietly unprotected. This reconciler moves
// that intent into the canonical graph.
//
// THE MERGE RULE IS ANY-BLOCK-WINS, AND ONLY THAT.
//
// A block present in either store becomes a block in the canonical store. The
// reconciler never removes a canonical block because the legacy table lacks
// it, and never treats an absence as an unblock. The asymmetry is deliberate:
//
//   - A false positive — carrying over a block the user later lifted in a
//     surface that wrote only to the legacy table — costs the user one
//     re-unblock, and they are the one who chose to unblock.
//   - A false negative — dropping a block because the legacy table has since
//     been edited, or because a row was missed — silently exposes someone to
//     an account they blocked. That is the exact harm this whole item exists
//     to close.
//
// There is no symmetric "latest timestamp wins" reading of this. The two
// stores have no shared clock discipline, and safety intent is not a value to
// be averaged.
//
// The reconciler is IDEMPOTENT and runs continuously, not once: a deployment
// that still has an old client writing to the legacy table keeps producing
// rows, and those rows still represent someone asking for protection.

// LegacyBlockSource reads blocks from the retired store. It is an interface so
// the reconciler can be driven against a live legacy database in production
// and against a seeded fixture in the live test — without the test having to
// reimplement the merge rule it is checking.
type LegacyBlockSource interface {
	// LegacyBlocks returns (blocker, blocked) pairs from the shadow table,
	// starting after the given offset, up to limit rows.
	LegacyBlocks(ctx context.Context, offset, limit int) ([]LegacyBlockPair, error)
}

// LegacyBlockPair is one block recorded in the retired store.
type LegacyBlockPair struct {
	BlockerID uuid.UUID
	BlockedID uuid.UUID
}

// LegacyBlockReconcileResult reports what a pass did, so the operational
// signal is "how many people were unprotected" rather than "job ran".
type LegacyBlockReconcileResult struct {
	Scanned  int
	Imported int // blocks that did not exist canonically and now do
	Skipped  int // already canonical (or self-block); nothing to do
}

// ReconcileLegacyBlocks imports every legacy block that is missing from the
// canonical graph, running each import through BlockAtomic.
//
// Using BlockAtomic rather than a bulk INSERT is the point: an imported block
// must sever the relationships that exist across it and must emit the durable
// safety event, exactly as a fresh block would. A plain INSERT would leave a
// block row alongside a live follow edge — the same inconsistent state
// M3-P0-6 exists to prevent — and would tell no downstream service anything.
func (s *Store) ReconcileLegacyBlocks(ctx context.Context, src LegacyBlockSource, batchSize int) (LegacyBlockReconcileResult, error) {
	var res LegacyBlockReconcileResult
	if batchSize <= 0 {
		batchSize = 500
	}

	for offset := 0; ; offset += batchSize {
		batch, err := src.LegacyBlocks(ctx, offset, batchSize)
		if err != nil {
			return res, fmt.Errorf("reconcile legacy blocks: read: %w", err)
		}
		if len(batch) == 0 {
			return res, nil
		}

		for _, pair := range batch {
			res.Scanned++
			if pair.BlockerID == pair.BlockedID {
				res.Skipped++
				continue
			}

			// Only the EXACT direction is checked. A block by B of A does not
			// satisfy a block by A of B: the canonical store records who
			// asked, and both directions may be independently true.
			var exists bool
			if err := s.db.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM blocks WHERE blocker_id = $1 AND blocked_id = $2)`,
				pair.BlockerID, pair.BlockedID).Scan(&exists); err != nil {
				return res, fmt.Errorf("reconcile legacy blocks: check %s→%s: %w",
					pair.BlockerID, pair.BlockedID, err)
			}
			if exists {
				res.Skipped++
				continue
			}

			if _, err := s.BlockAtomic(ctx, pair.BlockerID, pair.BlockedID); err != nil {
				return res, fmt.Errorf("reconcile legacy blocks: import %s→%s: %w",
					pair.BlockerID, pair.BlockedID, err)
			}
			res.Imported++
		}

		if len(batch) < batchSize {
			return res, nil
		}
	}
}

// PgLegacyBlockSource reads `profile.blocks` from a database that still has
// it. Returns no rows when the table is absent, so a deployment that never had
// the shadow table needs no special configuration.
type PgLegacyBlockSource struct{ s *Store }

// NewPgLegacyBlockSource reads the legacy table through the given store's pool.
// In production the legacy table lives in profile-service's database, so this
// store is constructed with that pool, not graph-service's.
func NewPgLegacyBlockSource(s *Store) *PgLegacyBlockSource { return &PgLegacyBlockSource{s: s} }

func (p *PgLegacyBlockSource) LegacyBlocks(ctx context.Context, offset, limit int) ([]LegacyBlockPair, error) {
	var reg *string
	if err := p.s.db.QueryRow(ctx, `SELECT to_regclass('profile.blocks')::text`).Scan(&reg); err != nil {
		return nil, err
	}
	if reg == nil {
		return nil, nil // no shadow table in this deployment
	}

	rows, err := p.s.db.Query(ctx, `
		SELECT blocker_id, blocked_id FROM profile.blocks
		ORDER BY blocker_id, blocked_id
		OFFSET $1 LIMIT $2`, offset, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LegacyBlockPair
	for rows.Next() {
		var pair LegacyBlockPair
		if err := rows.Scan(&pair.BlockerID, &pair.BlockedID); err != nil {
			return nil, err
		}
		out = append(out, pair)
	}
	return out, rows.Err()
}
