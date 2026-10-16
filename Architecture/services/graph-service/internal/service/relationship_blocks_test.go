package service

import (
	"encoding/json"
	"testing"

	"github.com/atpost/graph-service/internal/store"
)

// RelationshipFromFull must keep the legacy single-route keys exactly as they
// were (blocked = target blocked actor, blocked_by = actor blocked target) and
// derive the named fields from them.
func TestRelationshipFromFullBlockDirections(t *testing.T) {
	for _, tc := range []struct {
		name               string
		targetBlockedActor bool // RelationshipFull.Blocked
		actorBlockedTarget bool // RelationshipFull.BlockedBy
	}{
		{"viewer blocked target", false, true},
		{"target blocked viewer", true, false},
		{"mutual block", true, true},
		{"no block", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rel := RelationshipFromFull(&store.RelationshipFull{
				Blocked:   tc.targetBlockedActor,
				BlockedBy: tc.actorBlockedTarget,
			})
			if rel.Blocked != tc.targetBlockedActor || rel.BlockedBy != tc.actorBlockedTarget {
				t.Errorf("legacy keys changed: blocked=%v blocked_by=%v", rel.Blocked, rel.BlockedBy)
			}
			if rel.ViewerBlockedTarget != tc.actorBlockedTarget {
				t.Errorf("viewer_blocked_target=%v, want %v", rel.ViewerBlockedTarget, tc.actorBlockedTarget)
			}
			if rel.TargetBlockedViewer != tc.targetBlockedActor {
				t.Errorf("target_blocked_viewer=%v, want %v", rel.TargetBlockedViewer, tc.targetBlockedActor)
			}
		})
	}
}

// A relationship cached (60s) by a build that predates the named fields has
// only the legacy keys. The cache read path re-derives, so such an entry must
// never report a live block as absent.
func TestCachedRelationshipWithoutNamedFieldsIsRederived(t *testing.T) {
	var rel Relationship
	if err := json.Unmarshal([]byte(`{"blocked":true,"blocked_by":true,"follow_request_status":"none"}`), &rel); err != nil {
		t.Fatal(err)
	}
	rel.fillBlockDirections()
	if !rel.ViewerBlockedTarget || !rel.TargetBlockedViewer {
		t.Fatalf("stale cache entry not re-derived: %+v", rel)
	}
}
