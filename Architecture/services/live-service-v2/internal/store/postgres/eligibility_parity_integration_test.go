package postgres_test

// Going-live eligibility and chat text against a real Postgres (2 Oct 2026),
// and the in-memory store held to the same answers: completed streams (the
// new-streamer viewer cap), who is in the room, the blocked-word match with
// any Unicode, and chat text stored as sent. Same guard as the other
// integration tests: LIVE_V2_TEST_DSN and a database whose name ends in
// _test.

import (
	"context"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	dbschema "github.com/atpost/live-service-v2/database"
	"github.com/atpost/live-service-v2/internal/store/postgres"
	"github.com/atpost/live-service-v2/internal/storetest"
)

// eligStore is what eligibility and chat read and write.
type eligStore interface {
	CreateStream(ctx context.Context, p postgres.CreateStreamParams) (*postgres.LiveStream, error)
	CountCompletedStreams(ctx context.Context, creatorID uuid.UUID, minLive time.Duration) (int, error)
	IsViewerPresent(ctx context.Context, streamID, userID uuid.UUID) (bool, error)
	ApplyPresence(ctx context.Context, streamID, userID uuid.UUID, present bool) (*postgres.LiveStream, bool, error)
	AddWordFilter(ctx context.Context, streamID uuid.UUID, word string, addedBy uuid.UUID) error
	RemoveWordFilter(ctx context.Context, streamID uuid.UUID, word string) error
	ListWordFilters(ctx context.Context, streamID uuid.UUID) ([]string, error)
	MatchesWordFilter(ctx context.Context, streamID uuid.UUID, text string) (bool, error)
	InsertChatMessage(ctx context.Context, streamID, userID uuid.UUID, text string) (*postgres.ChatMessage, error)
	ListRecentChatMessages(ctx context.Context, streamID uuid.UUID, limit int) ([]*postgres.ChatMessage, error)
}

// endPlacer writes a stream's final state (the stores only ever write now).
type endPlacer func(id uuid.UUID, status string, started, ended *time.Time, reason *string)

var eligTexts = []string{
	"great stream 😀🔥",
	"us 👨‍👩‍👧‍👦 watching",
	"🇮🇳 🇯🇵 🇧🇷",
	"నమస్తే, ఈ లైవ్ చాలా బాగుంది",
	"नमस्ते, यह लाइव बहुत अच्छा है",
	"వావ్ 😍 बहुत बढ़िया 👍🏽 ❤️ café ñ 日本語 🏳️‍🌈",
	"plain ascii",
}

func eligScenario(t *testing.T, s eligStore, place endPlacer) map[string]any {
	t.Helper()
	ctx := context.Background()
	out := map[string]any{}
	mk := func(creator uuid.UUID) *postgres.LiveStream {
		id := uuid.New()
		st, err := s.CreateStream(ctx, postgres.CreateStreamParams{
			CreatorUserID: creator, LiveKitRoom: "stream_" + id.String(), Title: "elig", Visibility: "public",
		})
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	now := time.Now().UTC().Truncate(time.Second)
	ended := func(creator uuid.UUID, onAir time.Duration, reason string) {
		start := now.Add(-onAir)
		place(mk(creator).ID, postgres.StatusEnded, &start, &now, &reason)
	}

	// --- completed streams ---
	creator, other := uuid.New(), uuid.New()
	ended(creator, 5*time.Minute, "host_ended")             // counts: exactly the minimum
	ended(creator, 5*time.Minute-time.Second, "host_ended") // too short
	ended(creator, time.Hour, "admin_stopped")              // stopped by an admin
	ended(creator, time.Hour, "host_lost")                  // counts
	noMedia := "no_media"
	place(mk(creator).ID, postgres.StatusFailed, nil, &now, &noMedia) // failed
	failedStart := now.Add(-time.Hour)
	place(mk(creator).ID, postgres.StatusFailed, &failedStart, &now, &noMedia) // failed, even with times on it
	roomFinished := "room_finished"
	place(mk(creator).ID, postgres.StatusEnded, nil, &now, &roomFinished) // ended, never on air
	started := now.Add(-time.Hour)
	live := mk(creator)
	place(live.ID, postgres.StatusLive, &started, nil, nil) // still on air
	ended(other, time.Hour, "host_ended")                   // somebody else's
	count := func(who uuid.UUID, minLive time.Duration) int {
		n, err := s.CountCompletedStreams(ctx, who, minLive)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	out["completed, 5m"] = count(creator, 5*time.Minute)
	out["completed, 1h"] = count(creator, time.Hour)
	out["completed, 1s"] = count(creator, time.Second)
	out["completed, other creator"] = count(other, 5*time.Minute)
	out["completed, nobody"] = count(uuid.New(), 5*time.Minute)

	// --- who is in the room ---
	a, b := uuid.New(), uuid.New()
	for _, step := range []struct {
		u       uuid.UUID
		present bool
	}{{a, true}, {b, true}, {b, false}} {
		if _, _, err := s.ApplyPresence(ctx, live.ID, step.u, step.present); err != nil {
			t.Fatal(err)
		}
	}
	present := func(stream, u uuid.UUID) bool {
		p, err := s.IsViewerPresent(ctx, stream, u)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	out["present"] = []bool{present(live.ID, a), present(live.ID, b), present(live.ID, uuid.New()), present(mk(creator).ID, a), present(uuid.New(), a)}

	// --- blocked words ---
	match := func(texts ...string) []bool {
		res := make([]bool, len(texts))
		for i, text := range texts {
			m, err := s.MatchesWordFilter(ctx, live.ID, text)
			if err != nil {
				t.Fatal(err)
			}
			res[i] = m
		}
		return res
	}
	add := func(words ...string) {
		for _, w := range words {
			if err := s.AddWordFilter(ctx, live.ID, w, creator); err != nil {
				t.Fatalf("add %q: %v", w, err)
			}
		}
	}
	clear := func() {
		words, err := s.ListWordFilters(ctx, live.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range words {
			if err := s.RemoveWordFilter(ctx, live.ID, w); err != nil {
				t.Fatal(err)
			}
		}
	}
	out["no words"] = match(eligTexts...)
	// LIKE's wildcards as blocked words block only themselves.
	add("_", "%")
	out["wildcards vs unicode"] = match(eligTexts...)
	out["wildcards vs themselves"] = match("snake_case", "50% off", "nothing here")
	clear()
	add("100%", "a_b", `back\slash`)
	out["wildcards inside a word"] = match("100% sure", "1000 sure", "a_b", "axb", `back\slash`, "backslash")
	clear()
	add("చెత్త", "बकवास", "🖕", "ÉCOLE", "Spam")
	words, _ := s.ListWordFilters(ctx, live.ID)
	sort.Strings(words)
	out["stored lowercased"] = words
	out["unicode words"] = match("ఇది చెత్త లైవ్", "यह बकवास है", "take this 🖕", "vive l'école", "L'ÉCOLE", "SPAMMY", "sp am", "👍")
	out["unicode words vs clean text"] = match(eligTexts...)
	clear()

	// --- chat text is stored as sent ---
	chat := mk(creator)
	for _, text := range eligTexts {
		msg, err := s.InsertChatMessage(ctx, chat.ID, a, text)
		if err != nil {
			t.Fatalf("insert %q: %v", text, err)
		}
		if msg.Text != text {
			t.Fatalf("insert answered %q for %q", msg.Text, text)
		}
	}
	if _, err := s.InsertChatMessage(ctx, chat.ID, a, strings.Repeat("😀", 500)); err != nil {
		t.Fatalf("500 emoji (2000 bytes): %v", err)
	}
	list, err := s.ListRecentChatMessages(ctx, chat.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, m := range list {
		texts = append(texts, m.Text)
	}
	sort.Strings(texts)
	out["chat texts"] = texts
	return out
}

func TestIntegrationEligibilityMirrorsPostgres(t *testing.T) {
	dsn := os.Getenv("LIVE_V2_TEST_DSN")
	if dsn == "" {
		t.Skip("LIVE_V2_TEST_DSN not set")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("LIVE_V2_TEST_DSN does not parse")
	}
	if name := strings.TrimPrefix(u.Path, "/"); !strings.HasSuffix(name, "_test") {
		t.Fatalf("refusing to run against database %q: the name must end in _test", name)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.BootstrapSchema(ctx, pool, dbschema.SetupSQL, dbschema.Migrations); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	pg := postgres.New(pool)
	mem := storetest.New()

	want := eligScenario(t, pg, func(id uuid.UUID, status string, started, ended *time.Time, reason *string) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`UPDATE live_streams SET status = $2, started_at = $3, ended_at = $4, ended_reason = $5 WHERE id = $1`,
			id, status, started, ended, reason); err != nil {
			t.Fatalf("place: %v", err)
		}
	})
	got := eligScenario(t, mem, func(id uuid.UUID, status string, started, ended *time.Time, reason *string) {
		st := mem.Streams[id]
		st.Status, st.StartedAt, st.EndedAt, st.EndedReason = status, started, ended, reason
	})

	for k, w := range want {
		if g, ok := got[k]; !ok || !reflect.DeepEqual(g, w) {
			t.Errorf("%s\n  postgres: %v\n  memstore: %v", k, w, got[k])
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("%s: answered by the memory store only", k)
		}
	}

	// And the answers are the expected ones, not merely equal.
	chatWant := append([]string{strings.Repeat("😀", 500)}, eligTexts...)
	sort.Strings(chatWant)
	none := make([]bool, len(eligTexts))
	expect := map[string]any{
		"completed, 5m":               2,
		"completed, 1h":               1,
		"completed, 1s":               3,
		"completed, other creator":    1,
		"completed, nobody":           0,
		"present":                     []bool{true, false, false, false, false},
		"no words":                    none,
		"wildcards vs unicode":        none,
		"wildcards vs themselves":     []bool{true, true, false},
		"wildcards inside a word":     []bool{true, false, true, false, true, false},
		"stored lowercased":           []string{"spam", "école", "बकवास", "చెత్త", "🖕"},
		"unicode words":               []bool{true, true, true, true, true, true, false, false},
		"unicode words vs clean text": none,
		"chat texts":                  chatWant,
	}
	if len(want) != len(expect) {
		t.Errorf("the scenario answered %d things, %d are expected", len(want), len(expect))
	}
	for k, e := range expect {
		if !reflect.DeepEqual(want[k], e) {
			t.Errorf("%s = %v, want %v", k, want[k], e)
		}
	}

	// Postgres only: the column limits count characters, not bytes.
	live, err := pg.CreateStream(ctx, postgres.CreateStreamParams{
		CreatorUserID: uuid.New(), LiveKitRoom: "stream_" + uuid.NewString(), Title: "limits", Visibility: "public",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pg.InsertChatMessage(ctx, live.ID, uuid.New(), strings.Repeat("న", 500)); err != nil {
		t.Errorf("500 Telugu characters (1500 bytes): %v", err)
	}
	if _, err := pg.InsertChatMessage(ctx, live.ID, uuid.New(), strings.Repeat("😀", 501)); err == nil {
		t.Errorf("501 characters stored")
	}
	if err := pg.AddWordFilter(ctx, live.ID, strings.Repeat("న", 100), live.CreatorUserID); err != nil {
		t.Errorf("a 100-character Telugu word (300 bytes): %v", err)
	}
	if err := pg.AddWordFilter(ctx, live.ID, strings.Repeat("న", 101), live.CreatorUserID); err == nil {
		t.Errorf("a 101-character word stored")
	}
	if m, err := pg.MatchesWordFilter(ctx, live.ID, "x "+strings.Repeat("న", 100)+" y"); err != nil || !m {
		t.Errorf("the long Telugu word does not match: %v %v", m, err)
	}
}
