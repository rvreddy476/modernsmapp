package service

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// SG-M3: generation never runs inside a GET; on-demand runs are de-duplicated
// per viewer and type and bounded per process; an empty list is cached
// briefly.

func onDemandService(t *testing.T, slots int, gen func(context.Context, uuid.UUID) error) (*Service, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return &Service{rdb: rdb, genSlots: make(chan struct{}, slots), generate: gen}, mr
}

func TestRequestGenerationRunsOncePerWindow(t *testing.T) {
	var runs atomic.Int32
	done := make(chan struct{}, 4)
	svc, mr := onDemandService(t, 4, func(context.Context, uuid.UUID) error {
		runs.Add(1)
		done <- struct{}{}
		return nil
	})
	viewer := uuid.New()
	if !svc.requestGeneration(viewer, "friend") {
		t.Fatal("the first request did not start a run")
	}
	if svc.requestGeneration(viewer, "friend") {
		t.Fatal("a second request inside the window started another run")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the background run never happened")
	}
	if runs.Load() != 1 {
		t.Fatalf("runs = %d, want 1", runs.Load())
	}
	if ttl := mr.TTL(onDemandLockKey(viewer, "friend")); ttl <= 0 || ttl > onDemandLockTTL {
		t.Fatalf("lock TTL %s, want (0, %s]", ttl, onDemandLockTTL)
	}
	// Another type is its own window.
	if !svc.requestGeneration(viewer, "follow") {
		t.Fatal("a follow request was blocked by the friend window")
	}
	<-done
}

func TestRequestGenerationFullHouseGivesTheWindowBack(t *testing.T) {
	release := make(chan struct{})
	svc, mr := onDemandService(t, 1, func(context.Context, uuid.UUID) error { <-release; return nil })
	defer close(release)
	if !svc.requestGeneration(uuid.New(), "friend") {
		t.Fatal("the first run did not start")
	}
	second := uuid.New()
	if svc.requestGeneration(second, "friend") {
		t.Fatal("a run started past the concurrency bound")
	}
	if mr.Exists(onDemandLockKey(second, "friend")) {
		t.Fatal("a dropped run kept its window, so the viewer could not ask again for 10 minutes")
	}
}

func TestRequestGenerationWithoutRedisDoesNothing(t *testing.T) {
	svc := &Service{genSlots: make(chan struct{}, 1), generate: func(context.Context, uuid.UUID) error {
		t.Error("generation ran with no de-duplication available")
		return nil
	}}
	if svc.requestGeneration(uuid.New(), "friend") {
		t.Fatal("requestGeneration reported a run without Redis")
	}
}

func TestEmptyListIsCachedBriefly(t *testing.T) {
	svc, mr := onDemandService(t, 1, nil)
	viewer := uuid.New()
	svc.cacheItems(context.Background(), viewer, "friend", []SuggestionItem{})
	key := suggestionsCacheKey(viewer, "friend")
	if ttl := mr.TTL(key); ttl <= 0 || ttl > emptyCacheTTL {
		t.Fatalf("empty list TTL %s, want (0, %s]", ttl, emptyCacheTTL)
	}
	raw, _ := mr.Get(key)
	var items []SuggestionItem
	if err := json.Unmarshal([]byte(raw), &items); err != nil || len(items) != 0 {
		t.Fatalf("cached empty list = %q", raw)
	}
	svc.cacheItems(context.Background(), viewer, "friend", itemsFor(uuid.New()))
	if ttl := mr.TTL(key); ttl != time.Hour {
		t.Fatalf("non-empty friend list TTL %s, want 1h", ttl)
	}
}

// GetSuggestions is a GET handler's whole body: it must never run a batch
// itself, only ask for one.
func TestGetSuggestionsNeverRunsTheBatchInline(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "suggestion.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	inline := map[string]bool{"RunBatchForUser": true, "runFriendBatchForUser": true, "runFollowBatchForUser": true}
	found, asks := false, false
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "GetSuggestions" {
			continue
		}
		found = true
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if inline[sel.Sel.Name] {
					t.Errorf("GetSuggestions calls %s inside the request", sel.Sel.Name)
				}
				if sel.Sel.Name == "requestGeneration" {
					asks = true
				}
			}
			return true
		})
	}
	if !found {
		t.Fatal("suggestion.go has no GetSuggestions; this guard would pass while checking nothing")
	}
	if !asks {
		t.Fatal("GetSuggestions never asks for background generation; a new viewer would wait for the 6-hour batch")
	}
}
