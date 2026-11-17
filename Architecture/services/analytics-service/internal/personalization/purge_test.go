package personalization

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// reels-8: account erasure deletes every per-viewer key the warmer writes.
func TestViewerSignalKeysCoverEveryPerViewerKey(t *testing.T) {
	id := uuid.MustParse("6f1c2b3a-4d5e-4f60-8a7b-9c0d1e2f3a4b")
	got := strings.Join(ViewerSignalKeys(id), ",")
	want := strings.Join([]string{
		"user:affinities:" + id.String(),
		"user:media_prefs:" + id.String(),
		"user:topic_affinity:" + id.String(),
		"user:completions:" + id.String(),
	}, ",")
	if got != want {
		t.Fatalf("keys = %s, want %s", got, want)
	}
	// Every user-scoped key format the warmer declares is in the erase set.
	for _, f := range []string{AffinityKeyFmt, MediaPrefsKeyFmt, TopicAffinityKeyFmt, CompletionsKeyFmt} {
		if !strings.Contains(got, strings.Replace(f, "%s", id.String(), 1)) {
			t.Fatalf("%s is not erased", f)
		}
	}
}

func TestDeleteViewerSignalsWithoutRedisIsANoOp(t *testing.T) {
	var w *Warmer
	if err := w.DeleteViewerSignals(context.Background(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	if err := (&Warmer{}).DeleteViewerSignals(context.Background(), uuid.New()); err != nil {
		t.Fatal(err)
	}
}
