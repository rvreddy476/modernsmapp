package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/graph-service/internal/service"
	"github.com/atpost/graph-service/internal/store"
	"github.com/google/uuid"
)

// Security walk 10 Nov 2026: at the edge (the gateway stamps
// X-Graph-Write-Source) the person who was blocked must not learn it. The
// target's block of the viewer reads as no block on both relationship
// routes; the viewer's own block is still reported; in-cluster callers (no
// header, pinned by relationship_blocks_handler_test.go) still see both.

func edgeRelGet(t *testing.T, fake relationshipReader, viewer, target uuid.UUID) (string, blockFields) {
	t.Helper()
	r := relationshipRouter(fake)
	req := httptest.NewRequest(http.MethodGet, "/v1/graph/relationship?other_id="+target.String(), nil)
	req.Header.Set("X-User-Id", viewer.String())
	req.Header.Set(GraphWriteSourceHeader, "api-gateway")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("edge GET status %d: %s", w.Code, w.Body)
	}
	var env struct {
		Data blockFields `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body)
	}
	return w.Body.String(), env.Data
}

func edgeRelBatch(t *testing.T, fake relationshipReader, viewer, target uuid.UUID) (string, blockFields) {
	t.Helper()
	r := relationshipRouter(fake)
	body := `{"target_ids":["` + target.String() + `"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/graph/relationships/batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", viewer.String())
	req.Header.Set(GraphWriteSourceHeader, "api-gateway")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("edge batch status %d: %s", w.Code, w.Body)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body)
	}
	raw, ok := got[target.String()]
	if !ok {
		t.Fatalf("batch has no entry for the target: %s", w.Body)
	}
	var f blockFields
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("decode entry: %v", err)
	}
	return string(raw), f
}

// At the edge: the viewer's own block is reported, the target's never.
var edgeBlockCases = []blockCase{
	{name: "viewer blocked target", viewerBlocksTarget: true,
		wantSingleBlocked: false, wantBatchBlocked: true, wantViewerBlocked: true, wantTargetBlockedMe: false},
	{name: "target blocked viewer", targetBlocksViewer: true,
		wantSingleBlocked: false, wantBatchBlocked: false, wantViewerBlocked: false, wantTargetBlockedMe: false},
	{name: "mutual block", viewerBlocksTarget: true, targetBlocksViewer: true,
		wantSingleBlocked: false, wantBatchBlocked: true, wantViewerBlocked: true, wantTargetBlockedMe: false},
	{name: "no block"},
}

func TestEdgeRelationshipNeverTellsTheBlockedParty(t *testing.T) {
	for _, tc := range edgeBlockCases {
		t.Run(tc.name, func(t *testing.T) {
			viewer, target := uuid.New(), uuid.New()
			f := seedBlocks(viewer, target, tc)

			_, single := edgeRelGet(t, f, viewer, target)
			assertBlockFields(t, "edge GET /v1/graph/relationship", single,
				tc.wantSingleBlocked, tc.wantViewerBlocked, tc.wantTargetBlockedMe)

			_, batch := edgeRelBatch(t, f, viewer, target)
			assertBlockFields(t, "edge POST /v1/graph/relationships/batch", batch,
				tc.wantBatchBlocked, tc.wantViewerBlocked, tc.wantTargetBlockedMe)
		})
	}
}

// The anti-oracle property: being blocked is byte-for-byte the same answer
// as no relationship at all, on both routes.
func TestEdgeBlockedByTargetIsIndistinguishableFromNoBlock(t *testing.T) {
	viewer, target := uuid.New(), uuid.New()
	blocked := seedBlocks(viewer, target, blockCase{targetBlocksViewer: true})
	none := seedBlocks(viewer, target, blockCase{})

	gotBlocked, _ := edgeRelGet(t, blocked, viewer, target)
	gotNone, _ := edgeRelGet(t, none, viewer, target)
	if gotBlocked != gotNone {
		t.Fatalf("edge GET differs when the target blocked the viewer:\nblocked %s\n   none %s", gotBlocked, gotNone)
	}
	gotBlocked, _ = edgeRelBatch(t, blocked, viewer, target)
	gotNone, _ = edgeRelBatch(t, none, viewer, target)
	if gotBlocked != gotNone {
		t.Fatalf("edge batch differs when the target blocked the viewer:\nblocked %s\n   none %s", gotBlocked, gotNone)
	}
}

// The mask copies; it must not edit the service's (possibly cached) value.
func TestEdgeMaskDoesNotMutateItsInput(t *testing.T) {
	rel := &service.Relationship{Blocked: true, TargetBlockedViewer: true}
	_ = edgeRelationship(rel)
	if !rel.Blocked || !rel.TargetBlockedViewer {
		t.Fatal("edgeRelationship edited the service's value in place")
	}
	id := uuid.New()
	var r store.Relationship
	r.MarkTargetBlockedViewer()
	in := map[uuid.UUID]store.Relationship{id: r}
	_ = edgeRelationshipBatch(in)
	if !in[id].BlockedBy || !in[id].TargetBlockedViewer {
		t.Fatal("edgeRelationshipBatch edited the service's map in place")
	}
}
