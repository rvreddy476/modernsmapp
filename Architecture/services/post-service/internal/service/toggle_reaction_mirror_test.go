package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	postEvents "github.com/atpost/post-service/internal/events"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// feed-30: POST /v1/posts/:postId/react (Service.ToggleReaction). The Scylla
// copy is written inside the reaction's transaction, under the viewer's lock
// on the post, and a copy that cannot be written fails the toggle. The store
// here is the fake in toggle_reaction_hydration_test.go, which runs the
// hooks in the store's order; the lock and the SQL are proved against
// Postgres in store/postgres/reactions_toggle_integration_test.go (CI).

// The copy is made between the row write and the commit, not after it.
func TestToggleReaction_MirrorsInsideTheTransaction(t *testing.T) {
	store := &fakeReactionToggles{out: postgres.ReactionToggle{NewType: "like", IsSet: true}}
	rig := newToggleRig(t, store)

	if _, err := rig.svc.ToggleReaction(context.Background(), uuid.New(), uuid.New(), "like"); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(store.steps); got != "[row mirror commit]" {
		t.Fatalf("store steps = %s, want the Scylla copy between the row and the commit", got)
	}
	if rig.mirror.reacts != 1 || rig.mirror.unreacts != 0 || rig.mirror.lastType != "like" {
		t.Fatalf("mirror: reacts=%d unreacts=%d type=%q", rig.mirror.reacts, rig.mirror.unreacts, rig.mirror.lastType)
	}
}

// A copy that wrote nothing fails the toggle: the row is rolled back, the
// caller gets the error, and the cached feed row (still right) stays.
func TestToggleReaction_MirrorFailureRollsTheToggleBack(t *testing.T) {
	for name, outcome := range map[string]postgres.ReactionToggle{
		"like":   {NewType: "like", IsSet: true},
		"unlike": {PrevType: "like"},
	} {
		t.Run(name, func(t *testing.T) {
			store := &fakeReactionToggles{out: outcome}
			rig := newToggleRig(t, store)
			rig.mirror.err = errors.New("scylla timeout")
			viewer, post := uuid.New(), uuid.New()
			rig.seed(hydrationKey(viewer, post))

			res, err := rig.svc.ToggleReaction(context.Background(), post, viewer, "like")
			if err == nil {
				t.Fatalf("a toggle whose Scylla copy failed answered success: %+v", res)
			}
			if store.committed != 0 {
				t.Fatal("the reaction row was committed although its Scylla copy failed")
			}
			if got := fmt.Sprint(store.steps); got != "[row mirror rollback]" {
				t.Fatalf("store steps = %s", got)
			}
			if !rig.has(hydrationKey(viewer, post)) {
				t.Fatal("the cached row was dropped although nothing changed")
			}
		})
	}
}

// The copy was written and only its counter update failed: the toggle
// stands (rolling back would leave the feed card ahead of the row).
func TestToggleReaction_MirrorWrittenButCounterFailed_Commits(t *testing.T) {
	store := &fakeReactionToggles{out: postgres.ReactionToggle{NewType: "like", IsSet: true}}
	rig := newToggleRig(t, store)
	rig.mirror.err, rig.mirror.wrote = errors.New("counter write timeout"), true

	res, err := rig.svc.ToggleReaction(context.Background(), uuid.New(), uuid.New(), "like")
	if err != nil || !res.IsSet {
		t.Fatalf("toggle = %+v, %v; want it to stand", res, err)
	}
	if store.committed != 1 {
		t.Fatal("the reaction row was not committed")
	}
}

// What the store reports is what is announced and copied: a toggle that
// settled on a row another writer had already put there (nothing added,
// nothing removed) announces nothing, and its copy is the idempotent write
// of the state that is stored.
func TestToggleReaction_SettledToggleAnnouncesNothing(t *testing.T) {
	cases := map[string]struct {
		out                      postgres.ReactionToggle
		wantReacts, wantUnreacts int
	}{
		"already liked by another writer": {postgres.ReactionToggle{NewType: "like", PrevType: "like", IsSet: true}, 1, 0},
		"already removed":                 {postgres.ReactionToggle{}, 0, 1},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			store := &fakeReactionToggles{out: c.out}
			rig := newToggleRig(t, store)
			rig.svc.producer = announcingProducer()

			res, err := rig.svc.ToggleReaction(context.Background(), uuid.New(), uuid.New(), "like")
			if err != nil {
				t.Fatal(err)
			}
			if res.IsSet != c.out.IsSet {
				t.Fatalf("result = %+v, want the stored state %+v", res, c.out)
			}
			if len(store.eventTypes) != 0 {
				t.Fatalf("a toggle that changed nothing announced %v", store.eventTypes)
			}
			if rig.mirror.reacts != c.wantReacts || rig.mirror.unreacts != c.wantUnreacts {
				t.Fatalf("mirror: reacts=%d unreacts=%d", rig.mirror.reacts, rig.mirror.unreacts)
			}
		})
	}
}

// A real add and a real removal are each announced once, from the outcome.
func TestToggleReaction_AnnouncesTheOutcomeOnce(t *testing.T) {
	cases := map[string]struct {
		out  postgres.ReactionToggle
		want string
	}{
		"added":   {postgres.ReactionToggle{NewType: "like", IsSet: true}, events.PostReacted},
		"removed": {postgres.ReactionToggle{PrevType: "like"}, events.PostUnreacted},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			store := &fakeReactionToggles{out: c.out}
			rig := newToggleRig(t, store)
			rig.svc.producer = announcingProducer()

			if _, err := rig.svc.ToggleReaction(context.Background(), uuid.New(), uuid.New(), "like"); err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(store.eventTypes) != fmt.Sprint([]string{c.want}) {
				t.Fatalf("events = %v, want one %s", store.eventTypes, c.want)
			}
			if got := fmt.Sprint(store.steps); got != "[row event mirror commit]" {
				t.Fatalf("store steps = %s", got)
			}
		})
	}
}

func TestMirrorReactionToggle(t *testing.T) {
	post, user := uuid.New(), uuid.New()
	boom := errors.New("boom")
	cases := []struct {
		name                     string
		toggle                   postgres.ReactionToggle
		err                      error
		wrote                    bool
		wantErr                  bool
		wantReacts, wantUnreacts int
	}{
		{"set copies the reaction", postgres.ReactionToggle{NewType: "like", IsSet: true}, nil, false, false, 1, 0},
		{"removed deletes the copy", postgres.ReactionToggle{PrevType: "like"}, nil, false, false, 0, 1},
		{"nothing written is an error", postgres.ReactionToggle{NewType: "like", IsSet: true}, boom, false, true, 1, 0},
		{"nothing removed is an error", postgres.ReactionToggle{PrevType: "like"}, boom, false, true, 0, 1},
		{"written, counter failed: stands", postgres.ReactionToggle{NewType: "like", IsSet: true}, boom, true, false, 1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &fakeReactionMirror{err: c.err, wrote: c.wrote}
			err := mirrorReactionToggle(context.Background(), m, post, user, c.toggle)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, want error %v", err, c.wantErr)
			}
			if m.reacts != c.wantReacts || m.unreacts != c.wantUnreacts {
				t.Fatalf("reacts=%d unreacts=%d", m.reacts, m.unreacts)
			}
		})
	}
}

// announcingProducer makes ToggleReaction build its outbox event (it skips
// the event when no producer is configured). Nothing is published through it.
func announcingProducer() *postEvents.Producer { return &postEvents.Producer{} }
