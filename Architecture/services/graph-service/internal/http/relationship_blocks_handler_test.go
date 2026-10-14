package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/graph-service/internal/service"
	"github.com/atpost/graph-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The two relationship read routes historically used the key "blocked" for
// OPPOSITE directions:
//
//	GET  /v1/graph/relationship         blocked = TARGET blocked the viewer
//	POST /v1/graph/relationships/batch  blocked = VIEWER blocked the target
//
// Both now also carry viewer_blocked_target and target_blocked_viewer, which
// mean the same thing on both routes. These tests pin all three keys on both
// routes for every block shape, so neither the new names nor the legacy key
// can drift.
//
// The fake models the blocks table. It answers exactly the questions the SQL
// asks (store.GetRelationshipFull columns 3-4 for the single route, the
// "blocked" / "blocked_by" queries of store.GetRelationshipBatch for the
// batch) and hands the raw facts to the SAME production mapping the store and
// service use, so a mapping change fails here. The SQL itself is pinned
// against Postgres in relationship_blocks_integration_test.go.

type blockEdge struct{ blocker, blocked uuid.UUID }

type fakeBlocksTable struct{ rows map[blockEdge]bool }

func (f *fakeBlocksTable) has(blocker, blocked uuid.UUID) bool {
	return f.rows[blockEdge{blocker, blocked}]
}

func (f *fakeBlocksTable) GetRelationship(_ context.Context, actorID, targetID uuid.UUID) (*service.Relationship, error) {
	full := &store.RelationshipFull{
		// EXISTS(... blocks WHERE blocker_id = $2(target) AND blocked_id = $1(actor))
		Blocked: f.has(targetID, actorID),
		// EXISTS(... blocks WHERE blocker_id = $1(actor) AND blocked_id = $2(target))
		BlockedBy: f.has(actorID, targetID),
	}
	return service.RelationshipFromFull(full), nil
}

func (f *fakeBlocksTable) GetRelationshipBatch(_ context.Context, viewerID uuid.UUID, targetIDs []uuid.UUID) (map[uuid.UUID]store.Relationship, error) {
	out := make(map[uuid.UUID]store.Relationship, len(targetIDs))
	for _, t := range targetIDs {
		r := store.Relationship{FollowRequestStatus: "none", ConnectionStatus: "none"}
		// "blocked": blocker_id = viewer AND blocked_id = ANY(targets)
		if f.has(viewerID, t) {
			r.MarkViewerBlockedTarget()
		}
		// "blocked_by": blocked_id = viewer AND blocker_id = ANY(targets)
		if f.has(t, viewerID) {
			r.MarkTargetBlockedViewer()
		}
		out[t] = r
	}
	return out, nil
}

type blockFields struct {
	Blocked             *bool `json:"blocked"`
	ViewerBlockedTarget *bool `json:"viewer_blocked_target"`
	TargetBlockedViewer *bool `json:"target_blocked_viewer"`
}

type blockCase struct {
	name                string
	viewerBlocksTarget  bool
	targetBlocksViewer  bool
	wantSingleBlocked   bool // legacy single-route meaning: target blocked viewer
	wantBatchBlocked    bool // legacy batch meaning: viewer blocked target
	wantViewerBlocked   bool
	wantTargetBlockedMe bool
}

var blockCases = []blockCase{
	{name: "viewer blocked target", viewerBlocksTarget: true,
		wantSingleBlocked: false, wantBatchBlocked: true, wantViewerBlocked: true, wantTargetBlockedMe: false},
	{name: "target blocked viewer", targetBlocksViewer: true,
		wantSingleBlocked: true, wantBatchBlocked: false, wantViewerBlocked: false, wantTargetBlockedMe: true},
	{name: "mutual block", viewerBlocksTarget: true, targetBlocksViewer: true,
		wantSingleBlocked: true, wantBatchBlocked: true, wantViewerBlocked: true, wantTargetBlockedMe: true},
	{name: "no block",
		wantSingleBlocked: false, wantBatchBlocked: false, wantViewerBlocked: false, wantTargetBlockedMe: false},
}

func relationshipRouter(fake relationshipReader) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(nil).WithCanonicalWriteSource(true, nil)
	h.relationships = fake
	h.RegisterRoutes(r)
	return r
}

func seedBlocks(viewer, target uuid.UUID, tc blockCase) *fakeBlocksTable {
	f := &fakeBlocksTable{rows: map[blockEdge]bool{}}
	if tc.viewerBlocksTarget {
		f.rows[blockEdge{viewer, target}] = true
	}
	if tc.targetBlocksViewer {
		f.rows[blockEdge{target, viewer}] = true
	}
	return f
}

func assertBlockFields(t *testing.T, route string, got blockFields, wantBlocked, wantViewer, wantTarget bool) {
	t.Helper()
	if got.Blocked == nil || got.ViewerBlockedTarget == nil || got.TargetBlockedViewer == nil {
		t.Fatalf("%s: a block key is missing from the response: %+v", route, got)
	}
	if *got.Blocked != wantBlocked {
		t.Errorf("%s: blocked = %v, want %v (legacy meaning must not change)", route, *got.Blocked, wantBlocked)
	}
	if *got.ViewerBlockedTarget != wantViewer {
		t.Errorf("%s: viewer_blocked_target = %v, want %v", route, *got.ViewerBlockedTarget, wantViewer)
	}
	if *got.TargetBlockedViewer != wantTarget {
		t.Errorf("%s: target_blocked_viewer = %v, want %v", route, *got.TargetBlockedViewer, wantTarget)
	}
}

func TestSingleRelationshipBlockDirections(t *testing.T) {
	for _, tc := range blockCases {
		t.Run(tc.name, func(t *testing.T) {
			viewer, target := uuid.New(), uuid.New()
			r := relationshipRouter(seedBlocks(viewer, target, tc))

			req := httptest.NewRequest(http.MethodGet,
				"/v1/graph/relationship?user_id="+viewer.String()+"&other_id="+target.String(), nil)
			resp := httptest.NewRecorder()
			r.ServeHTTP(resp, req)
			if resp.Code != http.StatusOK {
				t.Fatalf("status %d: %s", resp.Code, resp.Body.String())
			}
			var env struct {
				Data blockFields `json:"data"`
			}
			if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode: %v (%s)", err, resp.Body.String())
			}
			assertBlockFields(t, "GET /v1/graph/relationship", env.Data,
				tc.wantSingleBlocked, tc.wantViewerBlocked, tc.wantTargetBlockedMe)
		})
	}
}

func TestBatchRelationshipBlockDirections(t *testing.T) {
	for _, tc := range blockCases {
		t.Run(tc.name, func(t *testing.T) {
			viewer, target := uuid.New(), uuid.New()
			r := relationshipRouter(seedBlocks(viewer, target, tc))

			body := `{"viewer_id":"` + viewer.String() + `","target_ids":["` + target.String() + `"]}`
			req := httptest.NewRequest(http.MethodPost, "/v1/graph/relationships/batch", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			r.ServeHTTP(resp, req)
			if resp.Code != http.StatusOK {
				t.Fatalf("status %d: %s", resp.Code, resp.Body.String())
			}
			var got map[string]blockFields
			if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v (%s)", err, resp.Body.String())
			}
			entry, ok := got[target.String()]
			if !ok {
				t.Fatalf("batch has no entry for the target: %s", resp.Body.String())
			}
			assertBlockFields(t, "POST /v1/graph/relationships/batch", entry,
				tc.wantBatchBlocked, tc.wantViewerBlocked, tc.wantTargetBlockedMe)
		})
	}
}

// The named fields must agree across the two routes for the same pair, which
// is the whole point of adding them.
func TestBlockDirectionsAgreeAcrossRoutes(t *testing.T) {
	ctx := context.Background()
	for _, tc := range blockCases {
		t.Run(tc.name, func(t *testing.T) {
			viewer, target := uuid.New(), uuid.New()
			f := seedBlocks(viewer, target, tc)
			single, _ := f.GetRelationship(ctx, viewer, target)
			batch, _ := f.GetRelationshipBatch(ctx, viewer, []uuid.UUID{target})
			b := batch[target]
			if single.ViewerBlockedTarget != b.ViewerBlockedTarget || single.TargetBlockedViewer != b.TargetBlockedViewer {
				t.Fatalf("routes disagree: single=(%v,%v) batch=(%v,%v)",
					single.ViewerBlockedTarget, single.TargetBlockedViewer, b.ViewerBlockedTarget, b.TargetBlockedViewer)
			}
		})
	}
}
