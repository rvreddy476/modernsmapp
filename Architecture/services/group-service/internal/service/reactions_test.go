package service

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

/*
Guards for group post reactions.

The allowlist lives in two places by necessity — Go refuses before the row is
touched, the CHECK constraint refuses if Go is ever bypassed — so the first
test pins them together. The rest are structural: every engagement write
passes the same gate, and validation happens before that gate so a rejected
reaction never costs a database round trip, let alone a row.
*/

// reactionCheckList pulls the quoted values out of the first
// `reaction IN ('a', 'b', …)` in a SQL file.
func reactionCheckList(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	m := regexp.MustCompile(`reaction IN \(([^)]*)\)`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatalf("%s has no `reaction IN (...)` CHECK", path)
	}
	var out []string
	for _, q := range regexp.MustCompile(`'([a-z_]+)'`).FindAllStringSubmatch(m[1], -1) {
		out = append(out, q[1])
	}
	sort.Strings(out)
	return out
}

func TestReactionAllowlistMatchesDatabaseCheck(t *testing.T) {
	want := append([]string(nil), ReactionAllowlist...)
	sort.Strings(want)
	for _, path := range []string{
		"../../database/migrations/016_group_post_reactions.sql",
		"../../database/setup.sql",
	} {
		got := reactionCheckList(t, path)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s CHECK allows %v but service.ReactionAllowlist is %v — a reaction one side accepts and the other refuses is a 500 or a silent hole", path, got, want)
		}
	}
	for _, must := range []string{"like", "love", "smile"} {
		found := false
		for _, r := range ReactionAllowlist {
			found = found || r == must
		}
		if !found {
			t.Errorf("allowlist must cover %q (the brief names Like, Love and Smile)", must)
		}
	}
}

func TestValidateReaction(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"like", "like", true},
		{" LOVE ", "love", true},
		{"Smile", "smile", true},
		{"heart", "", false},
		{"", "", false},
		{"like;drop", "", false},
		{"👍", "", false},
	}
	for _, c := range cases {
		got, err := ValidateReaction(c.in)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("ValidateReaction(%q) = (%q, %v), want (%q, ok=%v)", c.in, got, err, c.want, c.ok)
		}
		if err != nil && !strings.Contains(err.Error(), "invalid") {
			// handleServiceError maps "invalid" to 422 VALIDATION_ERROR; any
			// other wording falls to a 500.
			t.Errorf("ValidateReaction(%q) error %q must contain \"invalid\" to reach the client as 422", c.in, err)
		}
	}
}

// Every engagement write goes through engagementGate — the new reaction
// routes and the legacy spark pair alike. Before this, spark checked only
// post∈group, so a non-member could spark inside a private group.
func TestEveryEngagementWritePassesTheGate(t *testing.T) {
	for _, fn := range []struct{ file, name string }{
		{"reactions.go", "SetGroupPostReaction"},
		{"reactions.go", "RemoveGroupPostReaction"},
		{"group.go", "SparkGroupPost"},
		{"group.go", "UnsparkGroupPost"},
	} {
		body := codeOnly(funcSource(t, fn.file, fn.name))
		if !strings.Contains(body, "s.engagementGate(") {
			t.Errorf("%s does not call engagementGate — a banned or non-member viewer can write engagement", fn.name)
		}
	}
	gate := codeOnly(funcSource(t, "reactions.go", "engagementGate"))
	for _, must := range []string{"s.checkGroupAccess(", "s.store.CheckBanned(", "p.GroupID != groupID", `p.Status != "published"`} {
		if !strings.Contains(gate, must) {
			t.Errorf("engagementGate lost %q", must)
		}
	}
}

// Validation runs before the gate, and the gate before the store write, so a
// refused request never opens a transaction.
func TestReactionIsValidatedBeforeAnyRead(t *testing.T) {
	body := codeOnly(funcSource(t, "reactions.go", "SetGroupPostReaction"))
	v := strings.Index(body, "ValidateReaction(")
	g := strings.Index(body, "s.engagementGate(")
	w := strings.Index(body, "s.store.SetGroupPostReaction(")
	if v < 0 || g < 0 || w < 0 || !(v < g && g < w) {
		t.Fatalf("SetGroupPostReaction must validate, then gate, then write (positions validate=%d gate=%d write=%d)", v, g, w)
	}
}

// The response is read back from the rows, not assembled from the request:
// both writes end in reactionState, and reactionState reads all three parts.
func TestWritesAnswerWithReadBackState(t *testing.T) {
	for _, name := range []string{"SetGroupPostReaction", "RemoveGroupPostReaction"} {
		body := codeOnly(funcSource(t, "reactions.go", name))
		if !strings.Contains(body, "return s.reactionState(") {
			t.Errorf("%s does not return the read-back state", name)
		}
	}
	rs := codeOnly(funcSource(t, "reactions.go", "reactionState"))
	for _, must := range []string{"GetGroupPostSparkCount(", "GetGroupPostReactionCounts(", "GetViewerReaction("} {
		if !strings.Contains(rs, must) {
			t.Errorf("reactionState does not read %s", must)
		}
	}
}

// A first reaction is what a spark was: member stats and the sparked event
// fire once, on insert only — never on a replace, never on a no-op retry.
func TestEventAndMemberStatsOnlyOnInsert(t *testing.T) {
	body := codeOnly(funcSource(t, "reactions.go", "SetGroupPostReaction"))
	i := strings.Index(body, "if change.Inserted {")
	if i < 0 {
		t.Fatal("SetGroupPostReaction has no `if change.Inserted` branch")
	}
	after := body[i:]
	for _, must := range []string{"IncrementMemberSparks(", "PublishGroupPostSparked("} {
		if !strings.Contains(after, must) {
			t.Errorf("%s is not inside the Inserted branch", must)
		}
		if strings.Contains(body[:i], must) {
			t.Errorf("%s is called before the Inserted check — a replace would double-count", must)
		}
	}
}
