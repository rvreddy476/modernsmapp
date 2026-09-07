package pipeline

import (
	"strings"
	"testing"

	"github.com/atpost/feed-service/internal/ranking"
)

// The token vocabulary is a contract between three places that never call
// each other: this projection, analytics-service's topic-affinity scan,
// and the ranker's matching. They agree only by producing the same strings
// from the same three columns, so the rules that make them agree —
// lowercase, '#'-stripped, deduplicated, comma-free — are pinned here.

func TestTopicTokens_Vocabulary(t *testing.T) {
	got := TopicTokens("Music", []string{"#Guitar", "practice"}, []string{"lesson"})
	want := []string{"cat:music", "tag:guitar", "tag:practice", "tag:lesson"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("TopicTokens = %v, want %v", got, want)
	}
}

func TestTopicTokens_CategoryComesFirstAndIsStable(t *testing.T) {
	// Deterministic order means an unchanged post writes a byte-identical
	// value on every pass, so the projection does not churn Redis and two
	// debugging sessions see the same key.
	a := TopicTokens("music", []string{"b", "a"}, []string{"c"})
	for i := 0; i < 10; i++ {
		if strings.Join(TopicTokens("music", []string{"b", "a"}, []string{"c"}), ",") != strings.Join(a, ",") {
			t.Fatal("TopicTokens is not deterministic")
		}
	}
	if a[0] != "cat:music" {
		t.Fatalf("first token is %q, want the category", a[0])
	}
}

func TestTopicTokens_Deduplicates(t *testing.T) {
	// A post whose hashtag repeats its tag must not get two chances to
	// match: TopicInterest takes the strongest token, and a duplicate
	// would also inflate the Jaccard union on the related surface.
	got := TopicTokens("", []string{"Guitar", "guitar"}, []string{"GUITAR"})
	if len(got) != 1 || got[0] != "tag:guitar" {
		t.Fatalf("TopicTokens = %v, want one deduplicated token", got)
	}
}

func TestTopicTokens_DropsEmptyAndCommaBearingTokens(t *testing.T) {
	// The comma is the projection's own separator. A token containing one
	// would split into two bogus tokens on read, so it is dropped rather
	// than escaped — a silently mangled token is worse than a missing one.
	got := TopicTokens("", []string{"", "   ", "a,b", "#", "ok"}, nil)
	if len(got) != 1 || got[0] != "tag:ok" {
		t.Fatalf("TopicTokens = %v, want only the usable token", got)
	}
}

func TestTopicTokens_BoundsAnOverTaggedPost(t *testing.T) {
	many := make([]string, 100)
	for i := range many {
		many[i] = string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	got := TopicTokens("music", many, many)
	if len(got) > maxTokensPerPost {
		t.Fatalf("a post with 200 tags produced %d tokens, want at most %d", len(got), maxTokensPerPost)
	}
}

func TestTopicTokens_NoSubjectMeansNoTokens(t *testing.T) {
	// The overwhelming majority of posts are in this state, and they must
	// produce nothing: an absent key and an empty key mean the same thing
	// to the reader, so the absent one is free.
	if got := TopicTokens("", nil, nil); len(got) != 0 {
		t.Fatalf("an untagged post produced %v, want no tokens", got)
	}
}

func TestTopicTokens_RoundTripsThroughTheRankersParser(t *testing.T) {
	// What this writes, ranking.ParseTopics must read back identically —
	// they are the two ends of the same wire.
	tokens := TopicTokens("Music", []string{"#Guitar", "Practice"}, []string{"lesson"})
	back := ranking.ParseTopics(strings.Join(tokens, ","))
	if strings.Join(back, ",") != strings.Join(tokens, ",") {
		t.Fatalf("round trip: wrote %v, read back %v", tokens, back)
	}
	if got := ranking.ParseTopics(""); got != nil {
		t.Fatalf("an empty projection parsed to %v, want nil", got)
	}
	if got := ranking.ParseTopics(",,  ,"); got != nil {
		t.Fatalf("a value of only separators parsed to %v, want nil", got)
	}
}
