package events

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

func reviewEnvelope(t *testing.T, p events.PostCopyrightReviewPayload) *events.EventEnvelope {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return &events.EventEnvelope{EventType: events.EventPostCopyrightReview, Payload: raw}
}

func blockedPayload() events.PostCopyrightReviewPayload {
	decided := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	return events.PostCopyrightReviewPayload{
		ReviewID: uuid.NewString(), PostID: uuid.NewString(), AuthorID: uuid.NewString(),
		State: events.CopyrightReviewBlocked, MatchedTitle: "Jailer 2", DecidedAt: &decided,
		DecidedBy: uuid.NewString(), OccurredAt: decided.Add(time.Second),
	}
}

type recorder struct {
	got    []postgres.CopyrightBlockInput
	voided []postgres.CopyrightBlockVoidInput
	err    error
}

func (r *recorder) RecordCopyrightBlock(_ context.Context, in postgres.CopyrightBlockInput) (bool, error) {
	if r.err != nil {
		return false, r.err
	}
	r.got = append(r.got, in)
	return true, nil
}

func (r *recorder) VoidCopyrightBlock(_ context.Context, in postgres.CopyrightBlockVoidInput) (bool, error) {
	if r.err != nil {
		return false, r.err
	}
	r.voided = append(r.voided, in)
	return true, nil
}

// clearedPayload is a reviewer's clear of an appealed block (post-service
// migration 076), decided an hour after blockedPayload's block.
func clearedPayload(block events.PostCopyrightReviewPayload) events.PostCopyrightReviewPayload {
	p := block
	decided := block.DecidedAt.Add(time.Hour)
	p.State, p.DecidedAt, p.DecidedBy, p.Appeal = events.CopyrightReviewCleared, &decided, uuid.NewString(), true
	p.OccurredAt = decided.Add(time.Second)
	return p
}

// A reviewer's clear of an appealed block voids that review's block: the
// author, the review, the clear's decision time (the store voids only a
// block older than it), the reviewer, reason appeal_cleared. Nothing is
// recorded.
func TestCopyrightReviewConsumer_AppealClearVoidsBlock(t *testing.T) {
	rec := &recorder{}
	c := &CopyrightReviewConsumer{store: rec}
	p := clearedPayload(blockedPayload())
	if err := c.handle(context.Background(), reviewEnvelope(t, p)); err != nil {
		t.Fatal(err)
	}
	if len(rec.got) != 0 || len(rec.voided) != 1 {
		t.Fatalf("recorded=%d voided=%d, want 0 and 1", len(rec.got), len(rec.voided))
	}
	v := rec.voided[0]
	if v.UserID.String() != p.AuthorID || v.ReviewID.String() != p.ReviewID || !v.ClearedAt.Equal(*p.DecidedAt) ||
		v.VoidedBy == nil || v.VoidedBy.String() != p.DecidedBy || v.Reason != postgres.CopyrightBlockVoidAppealCleared {
		t.Fatalf("void = %+v from %+v", v, p)
	}

	// A clear the event does not mark as an appeal still voids (a clear never
	// leaves a block standing), under its own reason; a system clear has no
	// reviewer.
	q := clearedPayload(blockedPayload())
	q.Appeal, q.DecidedBy = false, ""
	if err := c.handle(context.Background(), reviewEnvelope(t, q)); err != nil {
		t.Fatal(err)
	}
	if len(rec.voided) != 2 || rec.voided[1].Reason != postgres.CopyrightBlockVoidReviewCleared || rec.voided[1].VoidedBy != nil {
		t.Fatalf("plain clear voided as %+v", rec.voided[1:])
	}
}

// The appeal going back to a reviewer ("pending" with Appeal) changes
// nothing: the block counts until a reviewer clears it.
func TestCopyrightReviewConsumer_AppealPendingKeepsBlock(t *testing.T) {
	rec := &recorder{}
	c := &CopyrightReviewConsumer{store: rec}
	p := blockedPayload()
	p.State, p.DecidedAt, p.DecidedBy, p.Appeal = events.CopyrightReviewPending, nil, "", true
	if err := c.handle(context.Background(), reviewEnvelope(t, p)); err != nil {
		t.Fatal(err)
	}
	if len(rec.got) != 0 || len(rec.voided) != 0 {
		t.Fatalf("pending appeal wrote recorded=%v voided=%v", rec.got, rec.voided)
	}
}

// A store failure on the void is retried (an overturned block must not
// keep counting); a clear that names no author or review is permanent.
func TestCopyrightReviewConsumer_ClearFailurePolicy(t *testing.T) {
	c := &CopyrightReviewConsumer{store: &recorder{err: errors.New("db down")}}
	if err := c.handle(context.Background(), reviewEnvelope(t, clearedPayload(blockedPayload()))); err == nil || sharedkafka.IsPermanent(err) {
		t.Fatalf("store failure: err=%v, want a retryable error", err)
	}
	rec := &recorder{}
	ok := &CopyrightReviewConsumer{store: rec}
	for name, mutate := range map[string]func(*events.PostCopyrightReviewPayload){
		"author": func(p *events.PostCopyrightReviewPayload) { p.AuthorID = uuid.Nil.String() },
		"post":   func(p *events.PostCopyrightReviewPayload) { p.PostID = "x" },
		"review": func(p *events.PostCopyrightReviewPayload) { p.ReviewID = "" },
	} {
		p := clearedPayload(blockedPayload())
		mutate(&p)
		if err := ok.handle(context.Background(), reviewEnvelope(t, p)); !sharedkafka.IsPermanent(err) {
			t.Errorf("%s: err=%v, want permanent", name, err)
		}
	}
	if len(rec.voided) != 0 {
		t.Fatalf("an invalid clear voided %+v", rec.voided)
	}
}

// Only a blocked review is an upheld block; it is recorded with the
// author, the post, the review as its key and the decision time.
func TestCopyrightReviewConsumer_RecordsBlockedOnly(t *testing.T) {
	rec := &recorder{}
	c := &CopyrightReviewConsumer{store: rec}
	p := blockedPayload()
	if err := c.handle(context.Background(), reviewEnvelope(t, p)); err != nil {
		t.Fatal(err)
	}
	if len(rec.got) != 1 {
		t.Fatalf("recorded %d blocks", len(rec.got))
	}
	got := rec.got[0]
	if got.UserID.String() != p.AuthorID || got.PostID.String() != p.PostID || got.ReviewID.String() != p.ReviewID ||
		!got.BlockedAt.Equal(*p.DecidedAt) || got.DecidedBy == nil || got.DecidedBy.String() != p.DecidedBy {
		t.Fatalf("block = %+v from %+v", got, p)
	}
	for _, state := range []string{events.CopyrightReviewPending, events.CopyrightReviewCleared, ""} {
		q := blockedPayload()
		q.State = state
		if err := c.handle(context.Background(), reviewEnvelope(t, q)); err != nil {
			t.Fatal(err)
		}
	}
	other := reviewEnvelope(t, blockedPayload())
	other.EventType = events.PostCreated
	if err := c.handle(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if len(rec.got) != 1 {
		t.Fatalf("a non-block was recorded: %+v", rec.got)
	}
}

// A store failure is returned (the record is retried, never lost); a
// payload that names no author, post or review is permanent.
func TestCopyrightReviewConsumer_FailurePolicy(t *testing.T) {
	c := &CopyrightReviewConsumer{store: &recorder{err: errors.New("db down")}}
	if err := c.handle(context.Background(), reviewEnvelope(t, blockedPayload())); err == nil || sharedkafka.IsPermanent(err) {
		t.Fatalf("store failure: err=%v, want a retryable error", err)
	}
	ok := &CopyrightReviewConsumer{store: &recorder{}}
	for name, mutate := range map[string]func(*events.PostCopyrightReviewPayload){
		"author": func(p *events.PostCopyrightReviewPayload) { p.AuthorID = "x" },
		"post":   func(p *events.PostCopyrightReviewPayload) { p.PostID = uuid.Nil.String() },
		"review": func(p *events.PostCopyrightReviewPayload) { p.ReviewID = "" },
	} {
		p := blockedPayload()
		mutate(&p)
		if err := ok.handle(context.Background(), reviewEnvelope(t, p)); !sharedkafka.IsPermanent(err) {
			t.Errorf("%s: err=%v, want permanent", name, err)
		}
	}
	bad := &events.EventEnvelope{EventType: events.EventPostCopyrightReview, Payload: []byte(`{`)}
	if err := ok.handle(context.Background(), bad); !sharedkafka.IsPermanent(err) {
		t.Fatalf("undecodable: %v", err)
	}
}

// A block the SYSTEM made for a registered rights owner (post-service
// migration 075: no reviewer, reason owner_registered) counts toward the
// repeat-uploader pause exactly like a reviewer's block.
func TestCopyrightReviewConsumer_SystemBlockCounts(t *testing.T) {
	rec := &recorder{}
	c := &CopyrightReviewConsumer{store: rec}
	p := blockedPayload()
	p.DecidedBy, p.Reason, p.RightsOwner = "", events.CopyrightReasonOwnerRegistered, "Sun Pictures"
	if err := c.handle(context.Background(), reviewEnvelope(t, p)); err != nil {
		t.Fatal(err)
	}
	if len(rec.got) != 1 || rec.got[0].DecidedBy != nil || rec.got[0].ReviewID.String() != p.ReviewID || !rec.got[0].BlockedAt.Equal(*p.DecidedAt) {
		t.Fatalf("system block recorded as %+v", rec.got)
	}
}
