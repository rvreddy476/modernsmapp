package service

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/atpost/suggestion-service/internal/store"
	"github.com/google/uuid"
)

/*
	"Follows you" (founder, 25 Oct 2026): people who follow the viewer and
	whom the viewer has not followed back are a People-you-may-know source,
	with reason FOLLOWS_YOU, bucket follows_you and the text "Follows you".
	The dead MUTUAL_FOLLOW reason ("You follow each other") it replaces could
	never fire: a candidate is never someone the viewer follows.
*/

func TestFollowsYouScoreWeighsRecency(t *testing.T) {
	today, week, month, year := followsYouScore(0), followsYouScore(7), followsYouScore(30), followsYouScore(365)
	if !(today > week && week > month && month > year) {
		t.Fatalf("score must fall with the follow's age: today %.2f, week %.2f, month %.2f, year %.2f", today, week, month, year)
	}
	if fourMutuals := 12.0 * math.Log1p(4); today <= fourMutuals {
		t.Fatalf("a follower from today scores %.2f, not above four mutual friends (%.2f)", today, fourMutuals)
	}
	if year < followsYouFloor || year <= 8.0 {
		t.Fatalf("an old follower scores %.2f; it must stay above the floor %.0f and a shared city (8)", year, followsYouFloor)
	}
	if got := followsYouScore(-3); got != today {
		t.Fatalf("a future follow time (clock skew) scores %.2f, want today's %.2f", got, today)
	}
}

func TestScoreLeadsWithFollowsYou(t *testing.T) {
	out := ScoreFriendCandidate(ScoringInput{FollowsYou: true, FollowsYouAgeDays: 1, MutualFriendCount: 2})
	if len(out.ReasonCodes) == 0 || out.ReasonCodes[0] != ReasonFollowsYou {
		t.Fatalf("reason codes %v: FOLLOWS_YOU must lead", out.ReasonCodes)
	}
	if out.ExplainText != "Follows you • 2 mutual friends" {
		t.Fatalf("explain text %q", out.ExplainText)
	}
	alone := ScoreFriendCandidate(ScoringInput{FollowsYou: true})
	if alone.ExplainText != FollowsYouText || FollowsYouText != "Follows you" || alone.Score <= 0 {
		t.Fatalf("a follower with no other signal: %+v", alone)
	}
	if none := ScoreFriendCandidate(ScoringInput{}); hasReason(none.ReasonCodes, ReasonFollowsYou) {
		t.Fatal("FOLLOWS_YOU without FollowsYou")
	}
}

func TestBucketAndReasonTypesNameFollowsYou(t *testing.T) {
	if got := categorizeBucket(3, 1, true, true, true, true, 0.5); got != BucketFollowsYou {
		t.Fatalf("a follower with mutual friends is bucket %q, want %q (the bucket the share cap counts)", got, BucketFollowsYou)
	}
	if got := categorizeBucket(3, 0, false, false, false, false, 0); got != "fof" {
		t.Fatalf("a non-follower with mutual friends is bucket %q, want fof", got)
	}
	reasons := computeReasonTypes(0, 0, false, false, false, false, true)
	if len(reasons) != 1 || reasons[0] != ReasonFollowsYou {
		t.Fatalf("dismiss-pattern reasons %v, want [FOLLOWS_YOU]", reasons)
	}
}

// The retired mutual-follow signal must be gone, not left dead.
func TestMutualFollowSignalIsGone(t *testing.T) {
	if _, ok := reflect.TypeOf(ScoringInput{}).FieldByName("MutualFollow"); ok {
		t.Error("ScoringInput.MutualFollow still exists")
	}
	if _, ok := reflect.TypeOf(store.PairSignals{}).FieldByName("MutualFollow"); ok {
		t.Error("store.PairSignals.MutualFollow still exists")
	}
	files, _ := filepath.Glob("*.go")
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				for _, dead := range []string{"MUTUAL_FOLLOW", "mutual_follow", "You follow each other"} {
					if strings.Contains(lit.Value, dead) {
						t.Errorf("%s still has %q", file, dead)
					}
				}
			}
			if id, ok := n.(*ast.Ident); ok && (id.Name == "RunHotSignalsRefresh" || id.Name == "BatchCheckMutualFollows") {
				t.Errorf("%s still names %s", file, id.Name)
			}
			return true
		})
	}
}

func cand(bucket string, score float32, reasons ...string) store.SuggestionCandidate {
	return store.SuggestionCandidate{CandidateID: uuid.New(), BaseScore: score, SourceBucket: bucket, ReasonCodes: reasons}
}

func TestCapFollowsYouShare(t *testing.T) {
	// Six followers outscore three others: without the cap they take the top.
	var in []store.SuggestionCandidate
	for i := 0; i < 6; i++ {
		in = append(in, cand(BucketFollowsYou, float32(100-i), ReasonFollowsYou))
	}
	for i := 0; i < 3; i++ {
		in = append(in, cand("fof", float32(50-i), "MUTUAL_FRIENDS"))
	}
	out := capFollowsYouShare(append([]store.SuggestionCandidate(nil), in...))
	if len(out) != len(in) {
		t.Fatalf("length %d, want %d", len(out), len(in))
	}
	var pattern strings.Builder
	for _, c := range out {
		if isFollowsYou(c) {
			pattern.WriteByte('F')
		} else {
			pattern.WriteByte('o')
		}
	}
	got := pattern.String()
	// While others remain, no prefix may exceed ceil(n/3) follows-you cards.
	lastOther, followers := strings.LastIndexByte(got, 'o'), 0
	for i := 0; i <= lastOther; i++ {
		if got[i] == 'F' {
			followers++
		}
		if followers > i/followsYouOneIn+1 {
			t.Fatalf("prefix %d holds %d follows-you cards (%s)", i+1, followers, got)
		}
	}
	if got != "FooFoFFFF" {
		t.Fatalf("order %s, want FooFoFFFF (one in three while others remain, then the rest)", got)
	}
	// Order within each group is kept.
	if out[0].CandidateID != in[0].CandidateID || out[3].CandidateID != in[1].CandidateID || out[1].CandidateID != in[6].CandidateID {
		t.Fatal("score order within a group was not kept")
	}

	// A list already within the cap is unchanged; so is a list of only one kind.
	within := []store.SuggestionCandidate{cand("fof", 9), cand(BucketFollowsYou, 8, ReasonFollowsYou), cand("group", 7), cand("fof", 6)}
	if got := capFollowsYouShare(append([]store.SuggestionCandidate(nil), within...)); !sameOrder(got, within) {
		t.Fatal("a list within the cap was reordered")
	}
	only := in[:6]
	if got := capFollowsYouShare(append([]store.SuggestionCandidate(nil), only...)); !sameOrder(got, only) {
		t.Fatal("a followers-only list was reordered or truncated")
	}
}

func sameOrder(a, b []store.SuggestionCandidate) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].CandidateID != b[i].CandidateID {
			return false
		}
	}
	return true
}

func TestMergeFollowsYou(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	viewer := uuid.New()
	fof := cand("fof", 20, "MUTUAL_FRIENDS")
	fof.ExplainText = "2 mutual friends"
	stale := cand(BucketFollowsYou, 30, ReasonFollowsYou) // unfollowed since the batch
	groupie := cand("group", 5, "COMMON_GROUPS")
	groupie.ExplainText = "Both in a group • Lives in Pune"
	newFollower := uuid.New()

	followers := []store.FollowerEdge{
		{UserID: newFollower, FollowedAt: now.Add(-time.Hour)},
		{UserID: groupie.CandidateID, FollowedAt: now.Add(-48 * time.Hour)},
		{UserID: viewer, FollowedAt: now},
	}
	out := mergeFollowsYou(viewer, []store.SuggestionCandidate{stale, fof, groupie}, followers, now, 0)

	byID := map[uuid.UUID]store.SuggestionCandidate{}
	for _, c := range out {
		byID[c.CandidateID] = c
	}
	if _, ok := byID[stale.CandidateID]; ok {
		t.Error("a stored follows-you card whose follower is gone was kept")
	}
	if _, ok := byID[viewer]; ok {
		t.Error("the viewer was suggested to themselves")
	}
	if got, ok := byID[fof.CandidateID]; !ok || got.BaseScore != 20 || got.SourceBucket != "fof" {
		t.Errorf("a non-follower candidate changed: %+v", got)
	}
	g := byID[groupie.CandidateID]
	if g.SourceBucket != BucketFollowsYou || g.ReasonCodes[0] != ReasonFollowsYou || !hasReason(g.ReasonCodes, "COMMON_GROUPS") {
		t.Errorf("a stored candidate who now follows the viewer was not upgraded: %+v", g)
	}
	if g.ExplainText != "Follows you • Both in a group" {
		t.Errorf("upgraded explain text %q", g.ExplainText)
	}
	if want := float32(5 + followsYouScore(2)); math.Abs(float64(g.BaseScore-want)) > 1e-3 {
		t.Errorf("upgraded score %.3f, want %.3f", g.BaseScore, want)
	}
	n, ok := byID[newFollower]
	if !ok || n.SourceBucket != BucketFollowsYou || n.ExplainText != FollowsYouText || n.SuggestionType != "friend" || n.ViewerID != viewer {
		t.Fatalf("a new follower was not added as a follows-you card: %+v", n)
	}
	if len(out) != 3 {
		t.Fatalf("%d candidates, want 3", len(out))
	}
	// Both followers outscore the fof candidate; the share cap puts it second.
	if out[0].CandidateID != groupie.CandidateID || out[1].CandidateID != fof.CandidateID || out[2].CandidateID != newFollower {
		t.Fatalf("order %v: want the top follower, then the non-follower (share cap), then the other follower", out)
	}
	for i := 1; i < len(out); i++ {
		if isFollowsYou(out[i]) == isFollowsYou(out[i-1]) && out[i].BaseScore > out[i-1].BaseScore {
			t.Fatalf("not score-ordered within a group: %v", out)
		}
	}

	// Re-merging an already-upgraded candidate does not add the score twice.
	again := mergeFollowsYou(viewer, out, followers, now, 0)
	for _, c := range again {
		if c.CandidateID == groupie.CandidateID && c.BaseScore != g.BaseScore {
			t.Fatalf("score added twice: %.3f then %.3f", g.BaseScore, c.BaseScore)
		}
	}

	if got := mergeFollowsYou(viewer, out, followers, now, 2); len(got) != 2 {
		t.Fatalf("limit 2 returned %d", len(got))
	}
}

type fakeFollowsYouReader struct {
	cooldowns    map[uuid.UUID]store.CooldownEntry
	cooldownErr  error
	followers    []store.FollowerEdge
	followersErr error
	gotExclude   []uuid.UUID
	readFollower bool
}

func (f *fakeFollowsYouReader) GetActiveCooldowns(context.Context, uuid.UUID) (map[uuid.UUID]store.CooldownEntry, error) {
	return f.cooldowns, f.cooldownErr
}

func (f *fakeFollowsYouReader) GetFollowersNotFollowedBack(_ context.Context, _ uuid.UUID, exclude []uuid.UUID) ([]store.FollowerEdge, error) {
	f.readFollower = true
	f.gotExclude = exclude
	return f.followers, f.followersErr
}

func TestLiveFollowsYouHonoursCooldownsAndFailsSafe(t *testing.T) {
	ctx, viewer, now := context.Background(), uuid.New(), time.Now()
	dismissed, follower := uuid.New(), uuid.New()
	stored := []store.SuggestionCandidate{cand(BucketFollowsYou, 30, ReasonFollowsYou)}

	r := &fakeFollowsYouReader{
		cooldowns: map[uuid.UUID]store.CooldownEntry{dismissed: {CandidateID: dismissed}},
		followers: []store.FollowerEdge{{UserID: follower, FollowedAt: now}},
	}
	out := liveFollowsYou(ctx, r, viewer, stored, now)
	if len(r.gotExclude) != 1 || r.gotExclude[0] != dismissed {
		t.Fatalf("exclude %v: a dismissed (cooled-down) follower must be excluded", r.gotExclude)
	}
	if len(out) != 1 || out[0].CandidateID != follower {
		t.Fatalf("got %v, want only the live follower (the stale stored card dropped)", out)
	}

	r = &fakeFollowsYouReader{cooldownErr: errors.New("down"), followers: []store.FollowerEdge{{UserID: follower, FollowedAt: now}}}
	if out := liveFollowsYou(ctx, r, viewer, stored, now); !sameOrder(out, stored) || r.readFollower {
		t.Fatal("a cooldown read failure must return the stored list unchanged without adding followers")
	}
	r = &fakeFollowsYouReader{followersErr: errors.New("down")}
	if out := liveFollowsYou(ctx, r, viewer, stored, now); !sameOrder(out, stored) {
		t.Fatal("a follower read failure must return the stored list unchanged")
	}
}

// callsIn reports whether fnName in file calls a function or method named callee.
func callsIn(t *testing.T, file, fnName, callee string) bool {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	f, err := parser.ParseFile(token.NewFileSet(), file, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != fnName || fn.Body == nil {
			continue
		}
		found := false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				found = found || fun.Name == callee
			case *ast.SelectorExpr:
				found = found || fun.Sel.Name == callee
			}
			return true
		})
		return found
	}
	t.Fatalf("%s: no function %s", file, fnName)
	return false
}

func TestFollowsYouIsWiredIntoBatchAndReadPath(t *testing.T) {
	for _, c := range []struct{ file, fn, callee, why string }{
		{"batch.go", "runFriendBatchForUser", "GetFollowersNotFollowedBack", "the batch must read the follows-you source"},
		{"batch.go", "runFriendBatchForUser", "capFollowsYouShare", "the batch must cap the follows-you share"},
		{"suggestion.go", "GetSuggestions", "liveFollowsYou", "the read path must merge live followers so a new one appears after the cache is cleared"},
	} {
		if !callsIn(t, c.file, c.fn, c.callee) {
			t.Errorf("%s does not call %s: %s", c.fn, c.callee, c.why)
		}
	}
}

// The batch scores only the newest followsYouBatchPool followers: every
// pooled person costs two more queries, so the full 1000-follower read must
// not all enter the pool.
func TestFollowsYouBatchPoolIsBounded(t *testing.T) {
	if followsYouBatchPool <= 0 || followsYouBatchPool >= store.FollowerScanLimit {
		t.Fatalf("followsYouBatchPool = %d, want 0 < pool < %d", followsYouBatchPool, store.FollowerScanLimit)
	}
	src, err := os.ReadFile("batch.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "if i < followsYouBatchPool {") {
		t.Fatal("runFriendBatchForUser must stop pooling followers at followsYouBatchPool")
	}
}
