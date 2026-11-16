package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// feed-04: POST /v1/posts/:postId/react (ToggleReaction) drops the viewer's
// cached feed row, as the other like routes do. Redis is the package's RESP
// stand-in (post_cache_redis_test.go); the reaction row and its Scylla copy
// are fakes.

type fakeReactionToggles struct {
	out postgres.ReactionToggle
	err error

	steps      []string // what the "transaction" did, in order
	eventTypes []string // outbox events written
	committed  int
}

// ToggleReactionMirrored runs the hooks the way the store does: the event
// and then the mirror, inside the transaction, and a mirror error rolls back.
func (f *fakeReactionToggles) ToggleReactionMirrored(ctx context.Context, _ string, _, _ uuid.UUID, _ string, event postgres.ReactionEventFunc, mirror postgres.ReactionMirrorFunc) (postgres.ReactionToggle, error) {
	if f.err != nil {
		return postgres.ReactionToggle{}, f.err
	}
	f.steps = append(f.steps, "row")
	if event != nil {
		if eventType, _, ok := event(f.out); ok {
			f.eventTypes = append(f.eventTypes, eventType)
			f.steps = append(f.steps, "event")
		}
	}
	if mirror != nil {
		f.steps = append(f.steps, "mirror")
		if err := mirror(ctx, f.out); err != nil {
			f.steps = append(f.steps, "rollback")
			return postgres.ReactionToggle{}, err
		}
	}
	f.steps = append(f.steps, "commit")
	f.committed++
	return f.out, nil
}

func (f *fakeReactionToggles) GetReactionCounts(context.Context, string, uuid.UUID) (*postgres.ReactionCounts, error) {
	return &postgres.ReactionCounts{Like: 1, Total: 1}, nil
}

type fakeReactionMirror struct {
	reacts, unreacts int
	lastType         string
	// err is returned by both writes; wrote says whether the row changed
	// before it (false: nothing was written).
	err   error
	wrote bool
}

func (m *fakeReactionMirror) React(_ context.Context, _, _ uuid.UUID, reaction string) (bool, error) {
	m.reacts++
	m.lastType = reaction
	if m.err != nil {
		return m.wrote, m.err
	}
	return true, nil
}

func (m *fakeReactionMirror) Unreact(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	m.unreacts++
	if m.err != nil {
		return m.wrote, m.err
	}
	return true, nil
}

func hydrationKey(viewer, post uuid.UUID) string {
	return fmt.Sprintf("feed:hydrate:%s:%s", viewer, post)
}

type toggleRig struct {
	svc    *Service
	redis  *respServer
	mirror *fakeReactionMirror
}

func newToggleRig(t *testing.T, store *fakeReactionToggles) toggleRig {
	t.Helper()
	resp := newRESPServer(t)
	rdb := redis.NewClient(&redis.Options{Addr: resp.ln.Addr().String(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	mirror := &fakeReactionMirror{}
	svc := &Service{
		rdb:         rdb,
		rateLimiter: deadRedisLimiter(),
		engagementPostSeam: func(_ context.Context, postID, _ uuid.UUID) (*postgres.Post, error) {
			return &postgres.Post{ID: postID, AuthorID: uuid.New(), ContentType: "post", Visibility: "public"}, nil
		},
		reactionToggleSeam: store,
		reactionMirrorSeam: mirror,
	}
	return toggleRig{svc: svc, redis: resp, mirror: mirror}
}

func (r toggleRig) seed(keys ...string) {
	r.redis.mu.Lock()
	defer r.redis.mu.Unlock()
	for _, k := range keys {
		r.redis.data[k] = `{"viewer_reaction":null}`
	}
}

func (r toggleRig) has(key string) bool {
	r.redis.mu.Lock()
	defer r.redis.mu.Unlock()
	_, ok := r.redis.data[key]
	return ok
}

// Every outcome that changes the viewer's state (a like set, a reaction
// changed, a like removed) removes feed:hydrate:<viewer>:<post>, and only
// that key: another viewer's row and another post's row stay cached.
func TestToggleReaction_InvalidatesTheViewersFeedHydration(t *testing.T) {
	cases := map[string]postgres.ReactionToggle{
		"set":     {NewType: "like", PrevType: "", IsSet: true},
		"changed": {NewType: "like", PrevType: "love", IsSet: true},
		"removed": {NewType: "", PrevType: "like", IsSet: false},
	}
	for name, outcome := range cases {
		t.Run(name, func(t *testing.T) {
			rig := newToggleRig(t, &fakeReactionToggles{out: outcome})
			viewer, post := uuid.New(), uuid.New()
			otherViewer, otherPost := uuid.New(), uuid.New()
			mine := hydrationKey(viewer, post)
			theirs := hydrationKey(otherViewer, post)
			elsewhere := hydrationKey(viewer, otherPost)
			rig.seed(mine, theirs, elsewhere)

			res, err := rig.svc.ToggleReaction(context.Background(), post, viewer, "like")
			if err != nil {
				t.Fatal(err)
			}
			if res.IsSet != outcome.IsSet || res.ReactionType != outcome.NewType {
				t.Fatalf("result = %+v, want the store's outcome %+v", res, outcome)
			}
			if rig.has(mine) {
				t.Fatalf("%s is still cached after a reaction was %s: the next feed read shows the old state for up to 5 minutes", mine, name)
			}
			if !rig.has(theirs) || !rig.has(elsewhere) {
				t.Fatal("the invalidation removed a key that is not this viewer's row for this post")
			}
			if outcome.IsSet && rig.mirror.reacts != 1 || !outcome.IsSet && rig.mirror.unreacts != 1 {
				t.Fatalf("scylla copy: reacts=%d unreacts=%d for outcome %+v", rig.mirror.reacts, rig.mirror.unreacts, outcome)
			}
		})
	}
}

// A toggle that failed changed nothing, so the cached row is still right
// and stays.
func TestToggleReaction_FailedToggleKeepsTheCachedRow(t *testing.T) {
	rig := newToggleRig(t, &fakeReactionToggles{err: errors.New("postgres down")})
	viewer, post := uuid.New(), uuid.New()
	rig.seed(hydrationKey(viewer, post))

	if _, err := rig.svc.ToggleReaction(context.Background(), post, viewer, "like"); err == nil {
		t.Fatal("store error swallowed")
	}
	if !rig.has(hydrationKey(viewer, post)) {
		t.Fatal("the cached row was dropped although nothing changed")
	}
}
