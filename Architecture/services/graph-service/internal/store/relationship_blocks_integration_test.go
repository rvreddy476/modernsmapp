//go:build integration

package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// Pins the SQL behind the block-direction fields against live PostgreSQL.
// The handler unit tests (internal/http/relationship_blocks_handler_test.go)
// pin the mapping onto the wire; this pins which blocks row each raw fact
// reads, which is where the two routes' "blocked" keys diverge.
//
//	POSTGRES_DSN=postgres://... go test -tags integration ./internal/store/ -run BlockDirections -v
func TestRelationshipBlockDirections(t *testing.T) {
	pool := testPool(t)
	s := New(pool)
	ctx := context.Background()

	for _, tc := range []struct {
		name                   string
		viewerBlocks, targetBl bool
	}{
		{"viewer blocked target", true, false},
		{"target blocked viewer", false, true},
		{"mutual block", true, true},
		{"no block", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viewer, target := uuid.New(), uuid.New()
			if tc.viewerBlocks {
				seedBlock(t, pool, viewer, target)
			}
			if tc.targetBl {
				seedBlock(t, pool, target, viewer)
			}

			// Single-pair facts: Blocked = target blocked actor,
			// BlockedBy = actor blocked target.
			full, err := s.GetRelationshipFull(ctx, viewer, target)
			if err != nil {
				t.Fatalf("GetRelationshipFull: %v", err)
			}
			if full.Blocked != tc.targetBl || full.BlockedBy != tc.viewerBlocks {
				t.Errorf("single: Blocked=%v BlockedBy=%v, want Blocked=%v (target->viewer) BlockedBy=%v (viewer->target)",
					full.Blocked, full.BlockedBy, tc.targetBl, tc.viewerBlocks)
			}

			// Batch: Blocked = viewer blocked target, BlockedBy = target
			// blocked viewer; the named fields follow the same rows.
			batch, err := s.GetRelationshipBatch(ctx, viewer, []uuid.UUID{target})
			if err != nil {
				t.Fatalf("GetRelationshipBatch: %v", err)
			}
			r := batch[target]
			if r.Blocked != tc.viewerBlocks || r.BlockedBy != tc.targetBl {
				t.Errorf("batch legacy: Blocked=%v BlockedBy=%v, want %v / %v", r.Blocked, r.BlockedBy, tc.viewerBlocks, tc.targetBl)
			}
			if r.ViewerBlockedTarget != tc.viewerBlocks || r.TargetBlockedViewer != tc.targetBl {
				t.Errorf("batch named: viewer_blocked_target=%v target_blocked_viewer=%v, want %v / %v",
					r.ViewerBlockedTarget, r.TargetBlockedViewer, tc.viewerBlocks, tc.targetBl)
			}
		})
	}
}
