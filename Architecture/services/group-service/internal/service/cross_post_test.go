package service

import (
	"os"
	"strings"
	"testing"
)

/*
Cross-posting invariants.

Four of these protect against leaks or duplicates that cannot be undone once
shipped, so they are pinned structurally rather than left to review.
*/

/*
The replay lookup runs before anything that could change its answer.

Same lesson as the group-create replay path: a retry must not be counted
twice by the rate limiter, and must not be re-subjected to checks the first
attempt already passed.
*/
func TestCrossPostAnswersReplayBeforeRateLimiting(t *testing.T) {
	body := funcSource(t, "cross_post.go", "CrossPost")

	replay := strings.Index(body, "FindPostRequests")
	if replay < 0 {
		t.Fatal("CrossPost does not look up prior attempts — a retry after a partial failure would post again to the groups that already succeeded")
	}
	limit := strings.Index(body, "rl:group_crosspost:")
	if limit < 0 {
		t.Fatal("CrossPost has no rate limit")
	}
	if replay > limit {
		t.Fatal("the rate limit runs before the replay lookup, so retrying one intent spends the allowance twice")
	}
}

// The cap is checked before any write, and before the limiter — an oversized
// request should cost nothing at all.
func TestCrossPostCapIsEnforcedBeforeAnyWork(t *testing.T) {
	body := funcSource(t, "cross_post.go", "CrossPost")
	cap := strings.Index(body, "MaxCrossPostTargets")
	loop := strings.Index(body, "for _, gid := range targets")
	if cap < 0 || loop < 0 {
		t.Fatal("CrossPost no longer caps its targets or no longer loops — this guard protects nothing")
	}
	if cap > loop {
		t.Fatal("the target cap is checked inside or after the loop, so an oversized batch is partly written before it is refused")
	}
}

/*
Each copy gets its own alias.

One alias shared across the copies would let anyone who can see two of the
target groups match them and know the posts share an author — which in a
small group names them. The bug shape is hoisting the call above the loop,
which reads as tidier and is wrong.
*/
func TestEachCrossPostCopyMintsItsOwnAlias(t *testing.T) {
	evaluator := funcSource(t, "cross_post.go", "createOnePost")
	if !strings.Contains(evaluator, "store.NewAnonAlias()") {
		t.Fatal("createOnePost does not mint an alias, so either anonymity is broken or the alias is being passed in from the caller — and an alias minted once above the loop is shared by every copy, which makes them linkable")
	}

	batch := funcSource(t, "cross_post.go", "CrossPost")
	if strings.Contains(batch, "NewAnonAlias") {
		t.Fatal("CrossPost mints an alias itself — if it is generated once and reused across targets, two anonymous copies become linkable and identify their author")
	}
}

/*
A target that disallows anonymity is skipped, never downgraded.

The irreversible one: posting under the author's name because the target
refused anonymity publishes an identity against an explicit choice.
*/
func TestAnonymousCrossPostIsSkippedNotDeanonymised(t *testing.T) {
	body := funcSource(t, "cross_post.go", "createOnePost")

	if !strings.Contains(body, "OutcomeAnonNotAllowed") {
		t.Fatal("createOnePost has no anonymous-not-allowed outcome, so either the gate is missing or it does something else with the post")
	}
	for _, bad := range []string{
		"params.IsAnonymous = false",
		"IsAnonymous: false",
		"post.IsAnonymous = false",
	} {
		if strings.Contains(body, bad) {
			t.Fatalf("createOnePost contains %q — that is the downgrade path, and it publishes the author's name in a group they chose believing they were anonymous", bad)
		}
	}
}

/*
The outcome vocabulary stays lossy where it must.

Distinguishing "no such group" from "private and you are not a member" turns
cross-posting into a probe for which private groups exist.
*/
func TestCrossPostDoesNotDistinguishMissingFromPrivate(t *testing.T) {
	src := readSourceFile(t, "cross_post.go")
	if !strings.Contains(src, `OutcomeUnavailable = "unavailable"`) {
		t.Fatal("the collapsed unavailable outcome is gone")
	}
	// Only the OUTCOME DECLARATIONS. Searching the whole file matches
	// g.Status == "deleted", which is a status comparison the code needs, and
	// a guard that fires on correct code gets deleted rather than heeded.
	const block = "const ("
	from := strings.Index(src, block)
	to := strings.Index(src[from:], ")")
	if from < 0 || to < 0 {
		t.Fatal("the outcome constant block is gone")
	}
	decls := src[from : from+to]
	for _, leaky := range []string{
		`"group_not_found"`, `"not_found"`, `"group_private"`, `"private"`,
		`"deleted"`, `"archived"`,
	} {
		if strings.Contains(decls, leaky) {
			t.Fatalf("cross_post.go defines the outcome %s — separating these from `unavailable` lets someone enumerate private groups by cross-posting at them", leaky)
		}
	}
}

// The uncached readers, or A0's ban fix is undone five times over.
func TestCrossPostUsesTheUncachedMembershipReader(t *testing.T) {
	body := funcSource(t, "cross_post.go", "createOnePost")
	// The CALL. The comment above the membership read names
	// CheckMembershipCached to explain why it is NOT used, so a bare search
	// matches the prose and fails on correct code.
	if strings.Contains(body, "s.store.CheckMembershipCached(") {
		t.Fatal("createOnePost uses the cached membership reader — the gm: key lives five minutes and is why a banned member could keep commenting, and using it here reopens that window for posting")
	}
	if !strings.Contains(body, "GetActiveMember") || !strings.Contains(body, "CheckBanned") {
		t.Fatal("createOnePost does not read membership and ban state directly")
	}
}

/*
The single-post response shape is unchanged.

CreateGroupPostV2 must keep returning the bare post. Returning a batch result
unconditionally would break every existing caller, including the mobile app
which is explicitly not being changed.
*/
func TestSinglePostStillReturnsTheBarePost(t *testing.T) {
	sig := readSourceFile(t, "group.go")
	if !strings.Contains(sig, "func (s *Service) CreateGroupPostV2(ctx context.Context, actorID, groupID uuid.UUID, params CreateGroupPostV2Params) (*store.GroupPostV2, error)") {
		t.Fatal("CreateGroupPostV2 no longer returns (*store.GroupPostV2, error) — every existing caller, including mobile, reads that shape")
	}
}

// A refusal for one target must not fail the batch, and a real error must.
func TestCrossPostSeparatesRefusalsFromFailures(t *testing.T) {
	body := funcSource(t, "cross_post.go", "CrossPost")
	if !strings.Contains(body, "if err != nil {") || !strings.Contains(body, "return nil, err") {
		t.Fatal("CrossPost does not fail on a real error — reporting a success count over a failed write is worse than an error the caller can retry")
	}
	evaluator := funcSource(t, "cross_post.go", "createOnePost")
	// A policy refusal returns an outcome with a nil error; only infrastructure
	// failures return err. If refusals became errors the first banned group
	// would abort the whole batch.
	for _, outcome := range []string{"OutcomeNotAMember", "OutcomeBanned", "OutcomeNotPermitted"} {
		if !strings.Contains(evaluator, "return "+outcome+", nil, nil") {
			t.Errorf("%s is not returned as a plain outcome — if it is an error instead, one refused group aborts every other target", outcome)
		}
	}
}

func readSourceFile(t *testing.T, path string) string {
	t.Helper()
	return readFileForTest(t, path)
}

func readFileForTest(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
