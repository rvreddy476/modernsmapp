package events

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/events"
	"github.com/segmentio/kafka-go"
)

// Module 2 M2-P0-1 / M2-P0-2 consumer tests.
//
// These drive the REAL consumer against a fake OpenSearch, so they cover
// the whole path — envelope decode, the eligibility gate, the revision
// guard, hashtag side effects, and the retry ladder — rather than
// re-asserting the pure predicate that shared/events already tests.

// --- fake OpenSearch --------------------------------------------------------

type fakeOS struct {
	mu sync.Mutex
	// docs holds indexed post documents by id.
	docs map[string]map[string]any
	// hashtagBumps counts IncrementHashtagUse calls per tag.
	hashtagBumps map[string]int
	// deletes records post ids that were deleted, in order.
	deletes []string
	// failPostWrites, while > 0, makes every posts_v1 write fail with 503
	// and decrements. Used to exercise the retry ladder.
	failPostWrites int
	// fences records authors erased via the M2-P0-7 deletion fence.
	fences map[string]bool
	srv    *httptest.Server
}

// toInt64 normalizes a JSON number back to int64.
func toInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	}
	return 0
}

func newFakeOS(t *testing.T) *fakeOS {
	t.Helper()
	f := &fakeOS{
		docs:         map[string]map[string]any{},
		hashtagBumps: map[string]int{},
		fences:       map[string]bool{},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeOS) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	path := strings.Trim(r.URL.Path, "/")
	parts := strings.Split(path, "/")

	// initIndices: HEAD /<index> — report every index as already present
	// so the store never tries to create one.
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Scripted _update — the atomic compare-and-apply primitive.
	//
	// NOTE: this reimplements the Painless script's conflict rules in Go.
	// It therefore proves the CALLERS pass the right intent, not that the
	// Painless is correct. Only the live-OpenSearch suite can prove that,
	// which is exactly why the concurrency test lives there.
	if len(parts) >= 3 && parts[0] == "posts_v1" && parts[1] == "_update" {
		id := parts[2]
		if f.failPostWrites > 0 {
			f.failPostWrites--
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"simulated outage"}`))
			return
		}
		var body struct {
			Script struct {
				Params struct {
					Rev            int64          `json:"rev"`
					AutoRev        bool           `json:"auto_rev"`
					Removed        bool           `json:"removed"`
					AuthorErased   bool           `json:"author_erased"`
					MaxOrdinaryRev int64          `json:"max_ordinary_rev"`
					Doc            map[string]any `json:"doc"`
					Tombstone      map[string]any `json:"tombstone"`
				} `json:"params"`
			} `json:"script"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		p := body.Script.Params

		var stored int64
		storedRemoved := false
		storedErased := false
		if existing, ok := f.docs[id]; ok {
			if v, ok := existing["search_rev"]; ok {
				stored = toInt64(v)
			}
			storedRemoved, _ = existing["removed"].(bool)
			storedErased, _ = existing["author_erased"].(bool)
		}

		incoming := p.Rev
		if p.AutoRev {
			// Overflow-safe, mirroring the Painless.
			if stored >= p.MaxOrdinaryRev {
				incoming = stored
			} else {
				incoming = stored + 1
			}
		}

		apply := incoming > stored ||
			(incoming == stored && p.Removed && !storedRemoved)

		// Erasure is absolute and not expressible as a number.
		if apply && storedErased && !p.Removed {
			apply = false
		}

		if apply {
			next := map[string]any{}
			src := p.Doc
			if p.Removed {
				src = p.Tombstone
			}
			for k, v := range src {
				next[k] = v
			}
			next["search_rev"] = float64(incoming)
			if storedErased || (p.Removed && p.AuthorErased) {
				next["author_erased"] = true
			}
			f.docs[id] = next
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":"updated"}`))
		return
	}

	// Author fence index.
	if len(parts) >= 3 && parts[0] == "author_fences_v1" && parts[1] == "_doc" {
		id := parts[2]
		switch r.Method {
		case http.MethodPut, http.MethodPost:
			f.fences[id] = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"result":"created"}`))
			return
		case http.MethodGet:
			if !f.fences[id] {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"found":false}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"found":true,"_source":{"author_id":"` + id + `"}}`))
			return
		}
	}

	// _update_by_query — the author sweep.
	if len(parts) >= 2 && parts[0] == "posts_v1" && parts[1] == "_update_by_query" {
		var body struct {
			Script struct {
				Params struct {
					AuthorID string `json:"author_id"`
					FenceRev int64  `json:"fence_rev"`
				} `json:"params"`
			} `json:"script"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		author := body.Script.Params.AuthorID
		updated := 0
		for id, doc := range f.docs {
			if a, _ := doc["author_id"].(string); a != author {
				continue
			}
			f.docs[id] = map[string]any{
				"post_id":       id,
				"author_id":     author,
				"review_status": "removed",
				"visibility":    "removed",
				"removed":       true,
				"author_erased": true,
				"search_rev":    float64(body.Script.Params.FenceRev),
			}
			updated++
		}
		w.WriteHeader(http.StatusOK)
		// Report a clean sweep: no conflicts, no failures, no timeout.
		// The conflicted case is exercised against live OpenSearch.
		_, _ = w.Write([]byte(fmt.Sprintf(
			`{"updated":%d,"version_conflicts":0,"timed_out":false,"failures":[]}`, updated)))
		return
	}

	if len(parts) >= 3 && parts[0] == "posts_v1" && parts[1] == "_doc" {
		id := parts[2]
		switch r.Method {
		case http.MethodPut, http.MethodPost:
			if f.failPostWrites > 0 {
				f.failPostWrites--
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":"simulated outage"}`))
				return
			}
			var doc map[string]any
			_ = json.NewDecoder(r.Body).Decode(&doc)
			f.docs[id] = doc
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"result":"created"}`))
			return
		case http.MethodGet:
			doc, ok := f.docs[id]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"found":false}`))
				return
			}
			rev := doc["search_rev"]
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(fmt.Sprintf(`{"found":true,"_source":{"search_rev":%v}}`, rev)))
			return
		case http.MethodDelete:
			if f.failPostWrites > 0 {
				f.failPostWrites--
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			f.deletes = append(f.deletes, id)
			delete(f.docs, id)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"result":"deleted"}`))
			return
		}
	}

	if len(parts) >= 3 && parts[0] == "hashtags_v1" && parts[1] == "_update" {
		f.hashtagBumps[parts[2]]++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":"updated"}`))
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{}`))
}

// indexed reports whether the post is PUBLICLY RETRIEVABLE, which is the
// property that actually matters — not merely whether a document exists.
// A removal leaves a tombstone behind to preserve the revision mark, and
// a tombstone is excluded by the mandatory public filter
// (visibility=public AND review_status=approved) that every query applies.
func (f *fakeOS) indexed(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, ok := f.docs[id]
	if !ok {
		return false
	}
	return doc["visibility"] == "public" && doc["review_status"] == "approved"
}

// tombstoned reports that a removal marker is present carrying a revision.
func (f *fakeOS) tombstoned(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, ok := f.docs[id]
	if !ok {
		return false
	}
	removed, _ := doc["removed"].(bool)
	return removed
}

// tombstoneLeaksContent reports whether a removal marker retained any of
// the fields that could disclose what the post said.
func (f *fakeOS) tombstoneLeaksContent(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, ok := f.docs[id]
	if !ok {
		return false
	}
	for _, field := range []string{"text", "hashtags", "author_username"} {
		if v, present := doc[field]; present && v != nil && v != "" {
			return true
		}
	}
	return false
}

// rev reports the stored revision, or 0 when no document exists.
func (f *fakeOS) rev(id string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, ok := f.docs[id]
	if !ok {
		return 0
	}
	return toInt64(doc["search_rev"])
}

// exists reports whether any document (live or tombstone) is present.
func (f *fakeOS) exists(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.docs[id]
	return ok
}

func (f *fakeOS) bumps(tag string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hashtagBumps[tag]
}

func (f *fakeOS) failNextPostWrites(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failPostWrites = n
}

// newTestConsumer wires a Consumer to the fake OpenSearch with no Kafka
// reader — the tests call processMessage directly.
func newTestConsumer(t *testing.T, f *fakeOS) *Consumer {
	t.Helper()
	store, err := search.New(f.srv.URL)
	if err != nil {
		t.Fatalf("search.New: %v", err)
	}
	p := defaultRetryPolicy()
	p.BaseDelay = time.Millisecond // keep tests fast
	p.MaxDelay = 2 * time.Millisecond
	return &Consumer{store: store, retry: p, topic: "test", groupID: "test"}
}

func msg(t *testing.T, eventType string, payload any) kafka.Message {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	env := events.EventEnvelope{EventType: eventType, Payload: raw}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return kafka.Message{Value: b}
}

// --- M2-P0-1: one event per review state ------------------------------------

// Acceptance 1: "one event per review state and visibility proves only
// public+approved content is retrievable", including a legacy event with
// no review status at all.
func TestPostCreated_OnlyPublicApprovedIsIndexed(t *testing.T) {
	cases := []struct {
		name       string
		visibility string
		review     string
		wantIndex  bool
	}{
		{"public+approved", "public", "approved", true},
		{"public+pending", "public", "pending", false},
		{"public+flagged", "public", "flagged", false},
		{"public+rejected", "public", "rejected", false},
		{"public+needs_changes", "public", "needs_changes", false},
		{"public+missing_status_legacy", "public", "", false},
		{"followers+approved", "followers", "approved", false},
		{"private+approved", "private", "approved", false},
		{"unlisted+approved", "unlisted", "approved", false},
		{"private+pending", "private", "pending", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeOS(t)
			c := newTestConsumer(t, f)
			id := "post-" + tc.name

			err := c.processMessage(context.Background(), msg(t, events.PostCreated,
				events.PostCreatedPayload{
					PostID:       id,
					AuthorID:     "author-1",
					Text:         "hello #diwali",
					Visibility:   tc.visibility,
					ReviewStatus: tc.review,
					SearchRev:    1,
					CreatedAt:    time.Now().UTC(),
				}))
			if err != nil {
				t.Fatalf("processMessage: %v", err)
			}

			if got := f.indexed(id); got != tc.wantIndex {
				t.Fatalf("indexed=%v, want %v (visibility=%q review=%q)",
					got, tc.wantIndex, tc.visibility, tc.review)
			}

			// Acceptance 1 also covers DERIVED signals. M2-P0-4 changed
			// how that is achieved: instead of maintaining a hashtag
			// counter and trying to decrement it on every removal path,
			// nothing writes to hashtags_v1 at all any more. Every
			// hashtag surface aggregates over posts_v1 live, so a tag's
			// visibility is a function of the post documents that exist
			// right now, and removal needs no compensating action.
			//
			// The consumer must therefore never touch the counter index.
			if got := f.bumps("diwali"); got != 0 {
				t.Fatalf("consumer wrote to the hashtags_v1 counter index (%d times); "+
					"hashtag surfaces are derived from posts_v1 and the counter "+
					"index is increment-only, so writing to it reintroduces the "+
					"stale-suggestion defect", got)
			}
		})
	}
}

// --- M2-P0-2: transition contract ------------------------------------------

func eligibilityMsg(t *testing.T, id, visibility, review string, deleted bool, rev int64) kafka.Message {
	t.Helper()
	return msg(t, events.PostSearchEligibilityChanged, events.PostSearchEligibilityChangedPayload{
		PostID:       id,
		AuthorID:     "author-1",
		Visibility:   visibility,
		ReviewStatus: review,
		Deleted:      deleted,
		SearchRev:    rev,
		Text:         "hello #diwali",
		CreatedAt:    time.Now().UTC().Add(-time.Hour),
		ChangedAt:    time.Now().UTC(),
	})
}

// Approval makes a held post searchable; rejection takes it back out.
func TestEligibility_ApprovalThenRejectionRoundTrip(t *testing.T) {
	f := newFakeOS(t)
	c := newTestConsumer(t, f)
	ctx := context.Background()

	// Created while pending — not indexed.
	if err := c.processMessage(ctx, msg(t, events.PostCreated, events.PostCreatedPayload{
		PostID: "p1", AuthorID: "a", Text: "hello #diwali",
		Visibility: "public", ReviewStatus: "pending", SearchRev: 1,
	})); err != nil {
		t.Fatal(err)
	}
	if f.indexed("p1") {
		t.Fatal("pending post must not be indexed on creation")
	}

	// Approved at rev 2 — becomes searchable.
	if err := c.processMessage(ctx, eligibilityMsg(t, "p1", "public", "approved", false, 2)); err != nil {
		t.Fatal(err)
	}
	if !f.indexed("p1") {
		t.Fatal("approved post must be indexed")
	}

	// Rejected at rev 3 — removed again.
	if err := c.processMessage(ctx, eligibilityMsg(t, "p1", "public", "rejected", false, 3)); err != nil {
		t.Fatal(err)
	}
	if f.indexed("p1") {
		t.Fatal("rejected post must be removed from the public index")
	}
}

// The resurrection guard. Kafka orders only within a partition and DLQ
// replay reorders by construction, so a late "approved" must never undo a
// newer rejection.
func TestEligibility_StaleApprovalCannotResurrectRejectedPost(t *testing.T) {
	f := newFakeOS(t)
	c := newTestConsumer(t, f)
	ctx := context.Background()

	if err := c.processMessage(ctx, eligibilityMsg(t, "p1", "public", "approved", false, 5)); err != nil {
		t.Fatal(err)
	}
	if err := c.processMessage(ctx, eligibilityMsg(t, "p1", "public", "rejected", false, 6)); err != nil {
		t.Fatal(err)
	}
	if f.indexed("p1") {
		t.Fatal("precondition: post should be removed at rev 6")
	}

	// A stale approval (rev 5) is redelivered AFTER the rejection.
	if err := c.processMessage(ctx, eligibilityMsg(t, "p1", "public", "approved", false, 5)); err != nil {
		t.Fatal(err)
	}
	if f.indexed("p1") {
		t.Fatal("a stale approval must NOT resurrect a rejected post")
	}
}

// Duplicate delivery is expected (at-least-once) and must be inert. With
// hashtags derived from posts_v1 there is no counter to inflate, so the
// property to hold is that repeated delivery leaves both the document and
// the revision exactly where a single delivery would.
func TestEligibility_DuplicateDeliveryIsIdempotent(t *testing.T) {
	f := newFakeOS(t)
	c := newTestConsumer(t, f)
	ctx := context.Background()

	m := eligibilityMsg(t, "p1", "public", "approved", false, 2)
	for i := 0; i < 5; i++ {
		if err := c.processMessage(ctx, m); err != nil {
			t.Fatalf("delivery %d: %v", i, err)
		}
	}
	if !f.indexed("p1") {
		t.Fatal("post should be indexed")
	}
	if got := f.rev("p1"); got != 2 {
		t.Fatalf("rev = %d after 5 duplicate deliveries, want 2", got)
	}
	if got := f.bumps("diwali"); got != 0 {
		t.Fatalf("no hashtag counter writes expected, got %d", got)
	}
}

// A revisionless event cannot be ordered, so it fails closed by removing
// rather than risking a stale upsert.
func TestEligibility_MissingRevisionFailsClosed(t *testing.T) {
	f := newFakeOS(t)
	c := newTestConsumer(t, f)
	ctx := context.Background()

	if err := c.processMessage(ctx, eligibilityMsg(t, "p1", "public", "approved", false, 3)); err != nil {
		t.Fatal(err)
	}
	if !f.indexed("p1") {
		t.Fatal("precondition: post should be indexed")
	}

	if err := c.processMessage(ctx, eligibilityMsg(t, "p1", "public", "approved", false, 0)); err != nil {
		t.Fatal(err)
	}
	if f.indexed("p1") {
		t.Fatal("an unorderable (rev=0) event must fail closed and remove the doc")
	}
}

// Deletion and visibility downgrade both remove, regardless of review state.
func TestEligibility_DeletionAndVisibilityDowngradeRemove(t *testing.T) {
	for _, tc := range []struct {
		name       string
		visibility string
		review     string
		deleted    bool
	}{
		{"deleted", "public", "approved", true},
		{"downgraded_to_followers", "followers", "approved", false},
		{"downgraded_to_private", "private", "approved", false},
		{"taken_down_via_rejected", "public", "rejected", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeOS(t)
			c := newTestConsumer(t, f)
			ctx := context.Background()

			if err := c.processMessage(ctx, eligibilityMsg(t, "p1", "public", "approved", false, 1)); err != nil {
				t.Fatal(err)
			}
			if !f.indexed("p1") {
				t.Fatal("precondition: indexed")
			}
			if err := c.processMessage(ctx,
				eligibilityMsg(t, "p1", tc.visibility, tc.review, tc.deleted, 2)); err != nil {
				t.Fatal(err)
			}
			if f.indexed("p1") {
				t.Fatalf("%s must remove the document", tc.name)
			}
		})
	}
}

// An explicit takedown must remove the post even if the eligibility event
// is delayed or lost entirely.
func TestContentTakenDown_RemovesPostIndependently(t *testing.T) {
	f := newFakeOS(t)
	c := newTestConsumer(t, f)
	ctx := context.Background()

	if err := c.processMessage(ctx, eligibilityMsg(t, "p1", "public", "approved", false, 1)); err != nil {
		t.Fatal(err)
	}
	if !f.indexed("p1") {
		t.Fatal("precondition: indexed")
	}

	if err := c.processMessage(ctx, msg(t, events.ContentTakenDown, events.ContentTakenDownPayload{
		EntityType: "post", EntityID: "p1", Reason: "policy", DeletedAt: time.Now().UTC(),
	})); err != nil {
		t.Fatal(err)
	}
	if f.indexed("p1") {
		t.Fatal("takedown must remove the post from the index")
	}

	// The removal must not itself become a disclosure: the marker keeps
	// the revision and nothing else.
	if !f.tombstoned("p1") {
		t.Fatal("takedown must leave a revision-bearing tombstone, not a bare delete")
	}
	if f.tombstoneLeaksContent("p1") {
		t.Fatal("a tombstone must not retain post text or hashtags")
	}

	// A takedown for a non-post entity must not touch posts_v1.
	if err := c.processMessage(ctx, msg(t, events.ContentTakenDown, events.ContentTakenDownPayload{
		EntityType: "comment", EntityID: "c1",
	})); err != nil {
		t.Fatal(err)
	}
	if f.tombstoned("c1") {
		t.Fatal("a comment takedown must not write to posts_v1")
	}
}

// --- M2-P0-2: durable retry -------------------------------------------------

// Failure injection: a transient OpenSearch outage must not cause a
// removal to be lost. This is the case that matters most — an unapplied
// removal leaves unsafe content publicly searchable.
func TestRetry_TransientOutageStillAppliesRemoval(t *testing.T) {
	f := newFakeOS(t)
	c := newTestConsumer(t, f)
	ctx := context.Background()

	if err := c.processMessage(ctx, eligibilityMsg(t, "p1", "public", "approved", false, 1)); err != nil {
		t.Fatal(err)
	}
	if !f.indexed("p1") {
		t.Fatal("precondition: indexed")
	}

	// Fail the next two delete attempts, then recover.
	f.failNextPostWrites(2)
	err := c.retry.retry(ctx, func() error {
		return c.processMessage(ctx, eligibilityMsg(t, "p1", "public", "rejected", false, 2))
	})
	if err != nil {
		t.Fatalf("retry ladder should have recovered from a transient outage: %v", err)
	}
	if f.indexed("p1") {
		t.Fatal("removal must survive a transient OpenSearch outage")
	}
}

// When the outage outlasts the retry budget the error surfaces, so the
// caller dead-letters instead of silently committing the offset.
func TestRetry_ExhaustedBudgetSurfacesError(t *testing.T) {
	f := newFakeOS(t)
	c := newTestConsumer(t, f)
	ctx := context.Background()

	f.failNextPostWrites(100)
	err := c.retry.retry(ctx, func() error {
		return c.processMessage(ctx, eligibilityMsg(t, "p1", "public", "approved", false, 1))
	})
	if err == nil {
		t.Fatal("a persistent outage must surface an error so the message is dead-lettered")
	}
}

// Shutdown must not be mistaken for a permanent failure: the message stays
// uncommitted for redelivery rather than being dropped.
func TestRetry_ContextCancellationAborts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := retryPolicy{MaxAttempts: 5, BaseDelay: time.Second, MaxDelay: time.Second}

	calls := 0
	start := time.Now()
	err := p.retry(ctx, func() error { calls++; return fmt.Errorf("boom") })
	if err == nil {
		t.Fatal("expected an error")
	}
	if calls != 1 {
		t.Fatalf("cancelled context should stop after the first attempt, got %d", calls)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("cancellation should abort immediately, not sleep out the backoff")
	}
}

func TestBackoff_IsBoundedAndJittered(t *testing.T) {
	p := defaultRetryPolicy()
	distinct := map[time.Duration]struct{}{}
	for i := 0; i < 50; i++ {
		d := p.backoff(3)
		if d > p.MaxDelay {
			t.Fatalf("backoff %v exceeded MaxDelay %v", d, p.MaxDelay)
		}
		distinct[d] = struct{}{}
	}
	if len(distinct) < 2 {
		t.Fatal("backoff must be jittered so replicas don't synchronize after an outage")
	}
}

// --- DLQ attempt bookkeeping ------------------------------------------------

func TestDLQAttemptHeader_RoundTripsAndReplacesInPlace(t *testing.T) {
	headers := []kafka.Header{{Key: "x-other", Value: []byte("keep")}}

	headers = setDLQAttempt(headers, 1)
	if dlqAttempt(kafka.Message{Headers: headers}) != 1 {
		t.Fatal("attempt 1 not read back")
	}

	headers = setDLQAttempt(headers, 2)
	if dlqAttempt(kafka.Message{Headers: headers}) != 2 {
		t.Fatal("attempt 2 not read back")
	}

	var attemptHeaders, otherHeaders int
	for _, h := range headers {
		switch h.Key {
		case dlqAttemptHeader:
			attemptHeaders++
		case "x-other":
			otherHeaders++
		}
	}
	if attemptHeaders != 1 {
		t.Fatalf("attempt header duplicated %d times — the count would be unreadable", attemptHeaders)
	}
	if otherHeaders != 1 {
		t.Fatal("unrelated headers must be preserved for debugging")
	}
}

func TestEventTypeOf_LabelsParkedAlertsUsefully(t *testing.T) {
	m := msg(t, events.ContentTakenDown, events.ContentTakenDownPayload{EntityType: "post", EntityID: "p1"})
	if got := eventTypeOf(m); got != events.ContentTakenDown {
		t.Fatalf("eventTypeOf = %q, want %q", got, events.ContentTakenDown)
	}
	if got := eventTypeOf(kafka.Message{Value: []byte("not json")}); got != "unknown" {
		t.Fatalf("undecodable message should label as unknown, got %q", got)
	}
}

// --- Codex review P0-2: creation and legacy deletes ------------------------

// The review's exact sequence: a removal lands at rev 2, then a delayed
// PostCreated at rev 1 arrives. PostCreated used to call an unconditional
// IndexPost, so it simply overwrote the tombstone with a public approved
// document — no concurrency needed.
func TestPostCreated_CannotOverwriteANewerRemoval(t *testing.T) {
	f := newFakeOS(t)
	c := newTestConsumer(t, f)
	ctx := context.Background()

	if err := c.processMessage(ctx, eligibilityMsg(t, "p1", "public", "rejected", false, 2)); err != nil {
		t.Fatal(err)
	}
	if f.indexed("p1") {
		t.Fatal("precondition: the post should be removed at rev 2")
	}

	// Replayed creation, carrying the creation-time revision.
	if err := c.processMessage(ctx, msg(t, events.PostCreated, events.PostCreatedPayload{
		PostID: "p1", AuthorID: "a", Text: "hello #diwali",
		Visibility: "public", ReviewStatus: "approved", SearchRev: 1,
	})); err != nil {
		t.Fatal(err)
	}

	if f.indexed("p1") {
		t.Fatal("a replayed PostCreated resurrected removed content — " +
			"creation must go through the same revision barrier as every other write")
	}
}

// The three legacy delete events are still produced. Each used to hard-
// delete, which erased the revision barrier and let any older approval
// recreate the document. They must now leave a barrier behind.
func TestLegacyDeleteEvents_RaiseTheBarrierInsteadOfErasingIt(t *testing.T) {
	cases := []struct {
		name     string
		eventMsg func(t *testing.T) kafka.Message
	}{
		{"post_deleted", func(t *testing.T) kafka.Message {
			return msg(t, events.PostDeleted, events.PostDeletedPayload{PostID: "p1"})
		}},
		{"upload_deleted", func(t *testing.T) kafka.Message {
			return msg(t, events.UploadDeleted, events.UploadDeletedPayload{PostID: "p1"})
		}},
		{"crosspost_removed", func(t *testing.T) kafka.Message {
			return msg(t, events.CrosspostRemoved, events.CrosspostRemovedPayload{TargetPostID: "p1"})
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeOS(t)
			c := newTestConsumer(t, f)
			ctx := context.Background()

			// Approved and indexed at rev 5.
			if err := c.processMessage(ctx, eligibilityMsg(t, "p1", "public", "approved", false, 5)); err != nil {
				t.Fatal(err)
			}
			if !f.indexed("p1") {
				t.Fatal("precondition: indexed")
			}

			// The legacy delete arrives.
			if err := c.processMessage(ctx, tc.eventMsg(t)); err != nil {
				t.Fatal(err)
			}
			if f.indexed("p1") {
				t.Fatal("the delete did not remove the document")
			}
			if !f.exists("p1") {
				t.Fatal("the delete hard-erased the document, destroying the revision " +
					"barrier — a stale approval could now recreate it")
			}
			if got := f.rev("p1"); got <= 5 {
				t.Fatalf("rev after delete = %d, want > 5: a delete must RAISE the "+
					"barrier so the approval that preceded it cannot be replayed", got)
			}

			// Replaying the approval that preceded the delete must not work.
			if err := c.processMessage(ctx, eligibilityMsg(t, "p1", "public", "approved", false, 5)); err != nil {
				t.Fatal(err)
			}
			if f.indexed("p1") {
				t.Fatalf("%s: a replayed approval resurrected deleted content", tc.name)
			}
		})
	}
}

// --- Codex review P0-7: author erasure fence -------------------------------

// Account deletion must survive replay of every event that could recreate
// the account's content.
func TestUserDeletion_FencesAgainstEveryStaleEvent(t *testing.T) {
	f := newFakeOS(t)
	c := newTestConsumer(t, f)
	ctx := context.Background()

	const author = "author-1"

	if err := c.processMessage(ctx, msg(t, events.PostCreated, events.PostCreatedPayload{
		PostID: "p1", AuthorID: author, Text: "hello #diwali",
		Visibility: "public", ReviewStatus: "approved", SearchRev: 1,
	})); err != nil {
		t.Fatal(err)
	}
	if !f.indexed("p1") {
		t.Fatal("precondition: indexed")
	}

	if err := c.processMessage(ctx, msg(t, events.EventUserDeletionRequested,
		events.UserDeletionRequestedPayload{UserID: author})); err != nil {
		t.Fatal(err)
	}
	if f.indexed("p1") {
		t.Fatal("the deleted account's post is still publicly retrievable")
	}

	// Replay everything that could bring it back.
	replays := []kafka.Message{
		msg(t, events.PostCreated, events.PostCreatedPayload{
			PostID: "p1", AuthorID: author, Text: "hello #diwali",
			Visibility: "public", ReviewStatus: "approved", SearchRev: 1,
		}),
		eligibilityMsg(t, "p1", "public", "approved", false, 99),
		// A post that was never indexed before the erasure.
		msg(t, events.PostCreated, events.PostCreatedPayload{
			PostID: "p2", AuthorID: author, Text: "another #diwali",
			Visibility: "public", ReviewStatus: "approved", SearchRev: 1,
		}),
	}
	for i, m := range replays {
		if err := c.processMessage(ctx, m); err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
	}

	if f.indexed("p1") {
		t.Fatal("an erased account's post was recreated by a replayed event")
	}
	if f.indexed("p2") {
		t.Fatal("an erased account's previously-unindexed post was created " +
			"by a replayed event — the author fence did not hold")
	}
}

// The fence must not retain the erased account's content.
func TestUserDeletion_FenceRetainsNoPostText(t *testing.T) {
	f := newFakeOS(t)
	c := newTestConsumer(t, f)
	ctx := context.Background()

	const author = "author-1"
	if err := c.processMessage(ctx, msg(t, events.PostCreated, events.PostCreatedPayload{
		PostID: "p1", AuthorID: author, Text: "sensitive personal detail #diwali",
		Visibility: "public", ReviewStatus: "approved", SearchRev: 1,
	})); err != nil {
		t.Fatal(err)
	}
	if err := c.processMessage(ctx, msg(t, events.EventUserDeletionRequested,
		events.UserDeletionRequestedPayload{UserID: author})); err != nil {
		t.Fatal(err)
	}

	if f.tombstoneLeaksContent("p1") {
		t.Fatal("the erasure fence retained post text — it must be usable as " +
			"a genuine erasure, not merely as hiding")
	}
}

// --- Codex review P0-3: durability of the offset handoff -------------------

// When the DLQ cannot accept the message there is no durable copy, so the
// offset must not advance. sendToDLQ reports that with its return value.
func TestSendToDLQ_ReportsNoDurableHandoffWhenDisabled(t *testing.T) {
	f := newFakeOS(t)
	c := newTestConsumer(t, f) // no DLQ writer configured

	if c.sendToDLQ(context.Background(), msg(t, events.PostDeleted,
		events.PostDeletedPayload{PostID: "p1"}), errBoom) {
		t.Fatal("sendToDLQ reported a durable handoff with no DLQ configured — " +
			"the caller would commit the offset and lose the message")
	}
}

var errBoom = fmt.Errorf("boom")
