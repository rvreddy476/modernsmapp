// Lane D3 service tests: blocks enforced on every surface, block side
// effects (match closed with dating.match.closed, sparks and stashes gone,
// both deck caches cleared), concurrent mutual sparks forming one match,
// unmatch needing two fresh sparks, pass and decline, recipient rules, the
// spark limit and the note filter. Integration tests skip without
// TEST_PG_DSN; the deck-cache test also needs REDIS_ADDR (DB 15).
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	datingevents "github.com/atpost/dating-service/internal/events"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

// recordingWriter captures produced Kafka messages.
type recordingWriter struct {
	mu   sync.Mutex
	msgs []kafka.Message
}

func (w *recordingWriter) WriteMessages(_ context.Context, msgs ...kafka.Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.msgs = append(w.msgs, msgs...)
	return nil
}

func (w *recordingWriter) Close() error { return nil }

// chatEnvelope is the shape chat-service's dating consumer decodes.
type chatEnvelope struct {
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
}

func (w *recordingWriter) events(t *testing.T) []chatEnvelope {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]chatEnvelope, 0, len(w.msgs))
	for _, m := range w.msgs {
		var env chatEnvelope
		if err := json.Unmarshal(m.Value, &env); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		out = append(out, env)
	}
	return out
}

// syncStubMessageClient is a concurrency-safe chat stub.
type syncStubMessageClient struct {
	mu    sync.Mutex
	calls int
}

func (c *syncStubMessageClient) CreateConversation(_ context.Context, _ CreateConversationRequest) (*CreateConversationResponse, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	time.Sleep(2 * time.Millisecond)
	return &CreateConversationResponse{ConversationID: uuid.NewString()}, nil
}

func newD3Svc(t *testing.T) (*Service, *store.Store, *recordingWriter) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping lane D3 service tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run against database %q: name must end in _test", cfg.ConnConfig.Database)
	}
	cfg.MaxConns = 20
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	ensureSchemaForTest(t, pool)
	st := store.New(pool)
	var rdb *redis.Client
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		rdb = redis.NewClient(&redis.Options{Addr: addr, DB: 15})
		t.Cleanup(func() { _ = rdb.Close() })
	}
	svc := New(st, rdb)
	rec := &recordingWriter{}
	svc.SetProducer(datingevents.NewProducerWithWriter(rec))
	svc.SetMessageClient(&syncStubMessageClient{})
	return svc, st, rec
}

// d3Match forms a match through two mutual sparks and returns its id.
func d3Match(t *testing.T, svc *Service, a, b uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	if _, _, err := svc.CreateSpark(ctx, b, a, "photo", "0", ""); err != nil {
		t.Fatalf("spark b->a: %v", err)
	}
	_, mid, err := svc.CreateSpark(ctx, a, b, "photo", "0", "")
	if err != nil || mid == nil {
		t.Fatalf("mutual spark a->b: match=%v err=%v", mid, err)
	}
	return *mid
}

func sparksBetween(t *testing.T, st *store.Store, a, b uuid.UUID) int {
	t.Helper()
	n := 0
	for _, pair := range [][2]uuid.UUID{{a, b}, {b, a}} {
		sent, err := st.ListSparksSent(context.Background(), pair[0])
		if err != nil {
			t.Fatalf("list sent: %v", err)
		}
		for _, sp := range sent {
			if sp.ToUserID == pair[1] {
				n++
			}
		}
	}
	return n
}

// openMatchesBetween counts open match rows for the pair, unfiltered.
func openMatchesBetween(t *testing.T, st *store.Store, a, b uuid.UUID) int {
	t.Helper()
	all, err := st.ListMatchesForExport(context.Background(), a)
	if err != nil {
		t.Fatalf("list matches: %v", err)
	}
	n := 0
	for _, m := range all {
		if (m.UserA == b || m.UserB == b) && (m.Status == "matched" || m.Status == "conversing" || m.Status == "quiet") {
			n++
		}
	}
	return n
}

func TestD3_BlockClosesMatchEmitsEventAndSeversPair(t *testing.T) {
	svc, st, rec := newD3Svc(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	seedActiveProfile(t, st, a)
	seedActiveProfile(t, st, b)
	mid := d3Match(t, svc, a, b)
	if err := st.AddStash(ctx, a, b, time.Time{}); err != nil {
		t.Fatalf("stash a->b: %v", err)
	}
	if err := st.AddStash(ctx, b, a, time.Time{}); err != nil {
		t.Fatalf("stash b->a: %v", err)
	}
	before := len(rec.events(t))

	if err := svc.Block(ctx, a, b); err != nil {
		t.Fatalf("block: %v", err)
	}

	m, err := st.GetMatch(ctx, mid)
	if err != nil {
		t.Fatalf("get match: %v", err)
	}
	if m.Status != "closed" || m.ClosedBy == nil || *m.ClosedBy != a {
		t.Fatalf("match after block = status %s closed_by %v; want closed by the blocker", m.Status, m.ClosedBy)
	}
	var closedEvent, blockedEvent bool
	for _, ev := range rec.events(t)[before:] {
		switch ev.EventType {
		case "dating.match.closed":
			var p struct {
				MatchID string `json:"match_id"`
			}
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				t.Fatalf("decode match.closed payload: %v", err)
			}
			if p.MatchID == mid.String() {
				closedEvent = true
			}
		case "dating.user.blocked":
			var p struct {
				BlockerID string `json:"blocker_id"`
				BlockedID string `json:"blocked_id"`
			}
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				t.Fatalf("decode user.blocked payload: %v", err)
			}
			blockedEvent = p.BlockerID == a.String() && p.BlockedID == b.String()
		}
	}
	if !closedEvent {
		t.Fatalf("no dating.match.closed with match_id %s; chat would keep the conversation open", mid)
	}
	if !blockedEvent {
		t.Fatalf("no dating.user.blocked for the pair")
	}
	if n := sparksBetween(t, st, a, b); n != 0 {
		t.Fatalf("%d sparks between the pair survive the block", n)
	}
	for _, pair := range [][2]uuid.UUID{{a, b}, {b, a}} {
		stashes, err := st.ListStash(ctx, pair[0])
		if err != nil {
			t.Fatalf("list stash: %v", err)
		}
		for _, s := range stashes {
			if s.CandidateID == pair[1] {
				t.Fatalf("stash %s -> %s survives the block", pair[0], pair[1])
			}
		}
	}
}

func TestD3_BlockClearsBothDeckCaches(t *testing.T) {
	svc, _, _ := newD3Svc(t)
	if svc.rdb == nil {
		t.Skip("REDIS_ADDR not set; skipping deck-cache test")
	}
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	svc.writePulseCache(ctx, a, &PulseResponse{Data: []PulseCard{{CandidateID: b}}, Meta: PulseMeta{Size: 1}})
	svc.writePulseCache(ctx, b, &PulseResponse{Data: []PulseCard{{CandidateID: a}}, Meta: PulseMeta{Size: 1}})
	if svc.readPulseCache(ctx, a) == nil || svc.readPulseCache(ctx, b) == nil {
		t.Fatalf("deck caches were not written")
	}
	if err := svc.Block(ctx, a, b); err != nil {
		t.Fatalf("block: %v", err)
	}
	if svc.readPulseCache(ctx, a) != nil {
		t.Fatalf("the blocker's cached deck survived the block")
	}
	if svc.readPulseCache(ctx, b) != nil {
		t.Fatalf("the blocked user's cached deck still holds the blocker")
	}
}

func TestD3_BlockedPairIsInvisibleEverywhere(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	seedActiveProfile(t, st, a)
	seedActiveProfile(t, st, b)
	mid := d3Match(t, svc, a, b)
	if err := svc.Block(ctx, b, a); err != nil {
		t.Fatalf("block: %v", err)
	}

	// Sparks, both directions.
	for _, pair := range [][2]uuid.UUID{{a, b}, {b, a}} {
		if _, _, err := svc.CreateSpark(ctx, pair[0], pair[1], "prompt", "p9", ""); !errors.Is(err, ErrCandidateUnavailable) {
			t.Fatalf("spark %s -> %s across a block: err=%v, want ErrCandidateUnavailable", pair[0], pair[1], err)
		}
	}
	// Match formation.
	if _, err := svc.FormMatch(ctx, a, b, nil); !errors.Is(err, ErrCandidateUnavailable) {
		t.Fatalf("FormMatch across a block: err=%v", err)
	}
	if n := openMatchesBetween(t, st, a, b); n != 0 {
		t.Fatalf("%d open matches for a blocked pair", n)
	}

	// Rows that exist anyway (legacy or a race) stay invisible.
	legacy, err := st.CreateSpark(ctx, b, a, "prompt", "legacy", "")
	if err != nil {
		t.Fatalf("seed legacy spark: %v", err)
	}
	incoming, err := svc.ListIncomingSparks(ctx, a, 50, 0)
	if err != nil {
		t.Fatalf("incoming: %v", err)
	}
	for _, sp := range incoming {
		if sp.ID == legacy.ID || sp.FromUserID == b {
			t.Fatalf("incoming sparks show the blocked user")
		}
	}
	for _, viewer := range []uuid.UUID{a, b} {
		matches, err := svc.ListMatches(ctx, viewer, "all")
		if err != nil {
			t.Fatalf("list matches: %v", err)
		}
		for _, m := range matches {
			if m.ID == mid {
				t.Fatalf("match list of %s shows the blocked pair's match", viewer)
			}
		}
		if _, err := svc.GetMatchForUser(ctx, mid, viewer); !errors.Is(err, store.ErrMatchNotFound) {
			t.Fatalf("GetMatchForUser across a block: err=%v", err)
		}
	}

	// Vouches.
	if _, err := svc.RequestVouch(ctx, a, b, "colleague", nil, ""); !errors.Is(err, ErrCandidateUnavailable) {
		t.Fatalf("vouch across a block: err=%v", err)
	}
	if _, err := st.CreateVouchRequest(ctx, b, a, "colleague", nil, ""); err != nil {
		t.Fatalf("seed legacy vouch: %v", err)
	}
	for _, viewer := range []uuid.UUID{a, uuid.Nil} {
		vouches, err := svc.ListVouchesFor(ctx, viewer, a, "")
		if err != nil {
			t.Fatalf("list vouches: %v", err)
		}
		for _, v := range vouches {
			if v.VoucherID == b {
				t.Fatalf("vouch list (viewer %s) shows a vouch across a block", viewer)
			}
		}
	}

	// Explain.
	if _, err := svc.ExplainCandidate(ctx, a, b); err == nil || !strings.HasPrefix(err.Error(), "not_found:") {
		t.Fatalf("explain across a block: err=%v, want not_found", err)
	}

	// Nebula / passed list.
	if _, err := st.RecordPass(ctx, a, b, ""); err != nil {
		t.Fatalf("seed pass: %v", err)
	}
	neb, err := svc.GetPulseNebulaPassed(ctx, a, 100, 0)
	if err != nil {
		t.Fatalf("nebula: %v", err)
	}
	for _, card := range neb.Data {
		if card.CandidateID == b {
			t.Fatalf("passed list shows the blocked user")
		}
	}
	passes, err := st.ListPassedCandidates(ctx, a, 100, 0)
	if err != nil {
		t.Fatalf("list passes: %v", err)
	}
	for _, p := range passes {
		if p.CandidateID == b {
			t.Fatalf("passed candidates include the blocked user")
		}
	}
	if _, err := st.GetCandidateForViewer(ctx, a, b); err == nil {
		t.Fatalf("nebula lookup returns the blocked user")
	}

	// Safety surfaces.
	if _, err := svc.ShareLocation(ctx, a, LocationShareRequest{ContactID: b, DurationMinutes: 30}); !errors.Is(err, ErrCandidateUnavailable) {
		t.Fatalf("share location across a block: err=%v", err)
	}
	if _, err := svc.ScheduleMeet(ctx, a, MeetRequest{WithUserID: b, When: time.Now().Add(time.Hour)}); !errors.Is(err, ErrCandidateUnavailable) {
		t.Fatalf("meet across a block: err=%v", err)
	}
}

func TestD3_ConcurrentMutualSparksFormOneMatch(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	for round := 0; round < 5; round++ {
		a, b := uuid.New(), uuid.New()
		seedActiveProfile(t, st, a)
		seedActiveProfile(t, st, b)

		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make(chan error, 16)
		launch := func(fn func() error) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if err := fn(); err != nil {
					errs <- err
				}
			}()
		}
		launch(func() error { _, _, err := svc.CreateSpark(ctx, a, b, "photo", "0", ""); return err })
		launch(func() error { _, _, err := svc.CreateSpark(ctx, b, a, "photo", "0", ""); return err })
		for i := 0; i < 6; i++ {
			launch(func() error { _, err := svc.FormMatch(ctx, a, b, nil); return err })
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("round %d: concurrent call failed: %v", round, err)
		}
		if n := openMatchesBetween(t, st, a, b); n != 1 {
			t.Fatalf("round %d: %d open matches for one pair, want exactly 1", round, n)
		}
	}
}

func TestD3_UnmatchRequiresTwoFreshSparks(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	seedActiveProfile(t, st, a)
	seedActiveProfile(t, st, b)
	first := d3Match(t, svc, a, b)

	if err := svc.CloseMatch(ctx, first, a); err != nil {
		t.Fatalf("unmatch: %v", err)
	}
	if n := sparksBetween(t, st, a, b); n != 0 {
		t.Fatalf("unmatch kept %d sparks between the pair", n)
	}
	if _, mid, err := svc.CreateSpark(ctx, a, b, "photo", "0", ""); err != nil || mid != nil {
		t.Fatalf("one fresh spark after unmatch: match=%v err=%v; want no match", mid, err)
	}
	_, mid, err := svc.CreateSpark(ctx, b, a, "photo", "0", "")
	if err != nil || mid == nil {
		t.Fatalf("second fresh spark: match=%v err=%v; want a new match", mid, err)
	}
	if *mid == first {
		t.Fatalf("re-match reused the closed match %s", first)
	}
}

func TestD3_PassExcludesFromNextDeckAndCachedDeck(t *testing.T) {
	svc, st, rec := newD3Svc(t)
	ctx := context.Background()
	gender := "d3-" + uuid.NewString()[:8]
	viewer, candidate, other := uuid.New(), uuid.New(), uuid.New()
	seedActiveProfile(t, st, viewer)
	seedActiveProfile(t, st, candidate)
	seedActiveProfile(t, st, other)
	for _, id := range []uuid.UUID{candidate, other} {
		if _, err := st.UpsertProfile(ctx, id, store.UpsertProfileParams{Gender: &gender}); err != nil {
			t.Fatalf("set gender: %v", err)
		}
	}
	if _, err := st.UpsertPreferences(ctx, viewer, store.UpsertPreferencesParams{InterestedInGender: &gender}); err != nil {
		t.Fatalf("set preference: %v", err)
	}
	inDeck := func(resp *PulseResponse, id uuid.UUID) bool {
		for _, c := range resp.Data {
			if c.CandidateID == id {
				return true
			}
		}
		return false
	}
	deck, err := svc.computePulseToday(ctx, viewer)
	if err != nil {
		t.Fatalf("deck: %v", err)
	}
	if !inDeck(deck, candidate) || !inDeck(deck, other) {
		t.Fatalf("seeded candidates missing from the first deck (%d cards)", len(deck.Data))
	}
	if svc.rdb != nil {
		svc.writePulseCache(ctx, viewer, deck)
	}
	before := len(rec.events(t))

	res, err := svc.PassCandidate(ctx, viewer, candidate, "")
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if !res.Passed || res.CandidateID != candidate || time.Until(res.CooldownUntil) < 29*24*time.Hour {
		t.Fatalf("pass result = %+v", res)
	}
	again, err := svc.PassCandidate(ctx, viewer, candidate, "")
	if err != nil || !again.CooldownUntil.Equal(res.CooldownUntil) {
		t.Fatalf("repeat pass = %+v, %v; want the same cooldown", again, err)
	}
	next, err := svc.computePulseToday(ctx, viewer)
	if err != nil {
		t.Fatalf("next deck: %v", err)
	}
	if inDeck(next, candidate) {
		t.Fatalf("passed candidate is in the next deck")
	}
	if !inDeck(next, other) {
		t.Fatalf("pass removed an unrelated candidate from the next deck")
	}
	if svc.rdb != nil {
		cached := svc.readPulseCache(ctx, viewer)
		if cached == nil || inDeck(cached, candidate) || !inDeck(cached, other) {
			t.Fatalf("cached deck after pass = %+v; want the candidate removed and the rest kept", cached)
		}
		if ttl, _ := svc.rdb.TTL(ctx, svc.cacheKey(viewer)).Result(); ttl <= 0 {
			t.Fatalf("cached deck lost its TTL (%v)", ttl)
		}
	}
	if after := len(rec.events(t)); after != before {
		t.Fatalf("pass emitted %d events; the candidate must not be told", after-before)
	}
	if _, err := svc.PassCandidate(ctx, viewer, viewer, ""); err == nil {
		t.Fatalf("passing yourself was accepted")
	}
}

func TestD3_DeclineHidesSparkAndNotifiesNobody(t *testing.T) {
	svc, st, rec := newD3Svc(t)
	ctx := context.Background()
	sender, recipient := uuid.New(), uuid.New()
	seedActiveProfile(t, st, sender)
	seedActiveProfile(t, st, recipient)
	sp, _, err := svc.CreateSpark(ctx, sender, recipient, "photo", "0", "hi")
	if err != nil {
		t.Fatalf("spark: %v", err)
	}
	before := len(rec.events(t))

	if _, err := svc.DeclineSpark(ctx, sp.ID, sender); !errors.Is(err, store.ErrSparkNotFound) {
		t.Fatalf("sender declined their own spark: err=%v", err)
	}
	declined, err := svc.DeclineSpark(ctx, sp.ID, recipient)
	if err != nil || declined.DeclinedAt == nil {
		t.Fatalf("decline = %+v, %v", declined, err)
	}
	again, err := svc.DeclineSpark(ctx, sp.ID, recipient)
	if err != nil || again.DeclinedAt == nil || !again.DeclinedAt.Equal(*declined.DeclinedAt) {
		t.Fatalf("repeat decline = %+v, %v; want idempotent", again, err)
	}
	incoming, err := svc.ListIncomingSparks(ctx, recipient, 50, 0)
	if err != nil {
		t.Fatalf("incoming: %v", err)
	}
	for _, s := range incoming {
		if s.ID == sp.ID {
			t.Fatalf("declined spark still in the recipient's incoming list")
		}
	}
	if after := len(rec.events(t)); after != before {
		t.Fatalf("decline emitted %d events; the sender must not be notified", after-before)
	}
	raw, _ := json.Marshal(declined)
	if strings.Contains(string(raw), "declined") {
		t.Fatalf("spark JSON exposes the decline: %s", raw)
	}
}

func TestD3_SparkRefusedToUnavailableRecipient(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	cases := []struct {
		name   string
		mutate func(t *testing.T, id uuid.UUID)
	}{
		{"suspended", func(t *testing.T, id uuid.UUID) {
			driveProfile(t, st, id, store.ProfileEventSuspend, store.ProfileActorAdmin)
		}},
		{"restricted", func(t *testing.T, id uuid.UUID) {
			driveProfile(t, st, id, store.ProfileEventRestrict, store.ProfileActorAdmin)
		}},
		{"deleted", func(t *testing.T, id uuid.UUID) {
			driveProfile(t, st, id, store.ProfileEventDelete, store.ProfileActorUser)
		}},
		{"under_18", func(t *testing.T, id uuid.UUID) {
			if _, err := st.SetProfileBirthDate(ctx, id, time.Now().AddDate(-16, 0, 0), store.BasicsSourceIdentity); err != nil {
				t.Fatalf("set minor birth date: %v", err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			from, to := uuid.New(), uuid.New()
			seedActiveProfile(t, st, from)
			seedActiveProfile(t, st, to)
			tc.mutate(t, to)
			if _, _, err := svc.CreateSpark(ctx, from, to, "photo", "0", ""); !errors.Is(err, ErrCandidateUnavailable) {
				t.Fatalf("spark to a %s recipient: err=%v, want ErrCandidateUnavailable", tc.name, err)
			}
		})
	}
	t.Run("missing", func(t *testing.T) {
		from := uuid.New()
		seedActiveProfile(t, st, from)
		if _, _, err := svc.CreateSpark(ctx, from, uuid.New(), "photo", "0", ""); !errors.Is(err, ErrCandidateUnavailable) {
			t.Fatalf("spark to a missing recipient: err=%v", err)
		}
	})
}

func TestD3_SparkRateLimitAtNPlusOne(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	from, to := uuid.New(), uuid.New()
	seedActiveProfile(t, st, from)
	seedActiveProfile(t, st, to)
	var first *store.Spark
	for i := 0; i < DefaultSparkDailyLimit; i++ {
		sp, _, err := svc.CreateSpark(ctx, from, to, "prompt", fmt.Sprintf("p%d", i), "")
		if err != nil {
			t.Fatalf("spark %d of %d: %v", i+1, DefaultSparkDailyLimit, err)
		}
		if first == nil {
			first = sp
		}
	}
	if _, _, err := svc.CreateSpark(ctx, from, to, "prompt", "p0", ""); err != nil {
		t.Fatalf("repeating an existing spark counted against the limit: %v", err)
	}
	if _, _, err := svc.CreateSpark(ctx, from, to, "prompt", "overflow", ""); !errors.Is(err, ErrSparkRateLimited) {
		t.Fatalf("spark %d: err=%v, want ErrSparkRateLimited", DefaultSparkDailyLimit+1, err)
	}
	if err := svc.RevokeSpark(ctx, first.ID, from); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, _, err := svc.CreateSpark(ctx, from, to, "prompt", "overflow", ""); !errors.Is(err, ErrSparkRateLimited) {
		t.Fatalf("revoke refunded the limit: err=%v", err)
	}
}

func TestD3_SparkNoteWithContactDetailsRefused(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	from, to := uuid.New(), uuid.New()
	seedActiveProfile(t, st, from)
	seedActiveProfile(t, st, to)
	for i, note := range []string{"call me on 98765 43210", "write to asha.k@example.com", "more at https://example.org/me"} {
		if _, _, err := svc.CreateSpark(ctx, from, to, "prompt", fmt.Sprintf("n%d", i), note); !errors.Is(err, ErrSparkNoteRefused) {
			t.Fatalf("note %q: err=%v, want ErrSparkNoteRefused", note, err)
		}
	}
	if _, _, err := svc.CreateSpark(ctx, from, to, "prompt", "clean", "love this answer"); err != nil {
		t.Fatalf("clean note refused: %v", err)
	}
}

func TestCheckSparkNote(t *testing.T) {
	t.Parallel()
	refused := []string{
		"+91 98765 43210",
		"ping me at someone@example.com",
		"www.example.net",
		"http://example.org",
	}
	for _, note := range refused {
		if err := checkSparkNote(note); !errors.Is(err, ErrSparkNoteRefused) {
			t.Errorf("checkSparkNote(%q) = %v, want refused", note, err)
		}
	}
	allowed := []string{"", "   ", "love this photo", "that trek looks amazing", "see you on atpost.com"}
	for _, note := range allowed {
		if err := checkSparkNote(note); err != nil {
			t.Errorf("checkSparkNote(%q) = %v, want allowed", note, err)
		}
	}
}
