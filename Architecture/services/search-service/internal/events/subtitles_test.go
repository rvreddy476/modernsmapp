package events

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/events"
	"github.com/segmentio/kafka-go"
)

// MediaSubtitlesChanged → has_subtitles (2026-09-27), driven through the
// real consumer and the real store against a fake OpenSearch.
//
// The fake reimplements setPostSubtitlesScript's rules in Go (drop older,
// no-op on equal, leave tombstones alone), so these tests prove the
// consumer sends the right intent — which posts, which value, which
// version — not that the Painless is correct; that needs a live cluster.

type subtitleOS struct {
	mu      sync.Mutex
	docs    map[string]map[string]any
	updates int
	fail    int // while > 0, every update answers 503
	scripts []string
	srv     *httptest.Server
}

func newSubtitleOS(t *testing.T) *subtitleOS {
	t.Helper()
	f := &subtitleOS{docs: map[string]map[string]any{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *subtitleOS) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) == 3 && parts[0] == search.IndexPosts && parts[1] == "_update" {
		if f.fail > 0 {
			f.fail--
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"simulated outage"}`))
			return
		}
		var body struct {
			Script struct {
				Source string `json:"source"`
				Params struct {
					Has     bool  `json:"has"`
					Version int64 `json:"version"`
				} `json:"params"`
			} `json:"script"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.scripts = append(f.scripts, body.Script.Source)
		doc, ok := f.docs[parts[2]]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"type":"document_missing_exception"},"status":404}`))
			return
		}
		stored := int64(-1)
		if v, ok := doc["subtitles_version"]; ok {
			stored = v.(int64)
		}
		p := body.Script.Params
		switch {
		case doc["removed"] == true, p.Version < stored:
			// no-op
		case p.Version == stored && doc["has_subtitles"] == p.Has:
			// no-op
		default:
			doc["has_subtitles"] = p.Has
			doc["subtitles_version"] = p.Version
			f.updates++
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":"updated"}`))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{}`))
}

func (f *subtitleOS) has(id string) (value, present bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.docs[id]["has_subtitles"]
	if !ok {
		return false, false
	}
	return v.(bool), true
}

type fakePostsByMedia struct {
	posts map[string][]string
	err   error
	asked []string
}

func (f *fakePostsByMedia) PostIDsByMedia(_ context.Context, mediaID string) ([]string, error) {
	f.asked = append(f.asked, mediaID)
	return f.posts[mediaID], f.err
}

func subtitleConsumer(t *testing.T, fos *subtitleOS, lookup PostsByMedia) *Consumer {
	t.Helper()
	store, err := search.New(fos.srv.URL)
	if err != nil {
		t.Fatalf("search.New: %v", err)
	}
	p := defaultRetryPolicy()
	p.BaseDelay, p.MaxDelay = time.Millisecond, 2*time.Millisecond
	c := &Consumer{store: store, retry: p, topic: "media.events", groupID: "test"}
	if lookup != nil {
		c.WithPostsByMedia(lookup)
	}
	return c
}

func subtitlesMsg(t *testing.T, mediaID string, has bool, langs []string, at time.Time) kafka.Message {
	t.Helper()
	raw, err := json.Marshal(events.MediaSubtitlesChangedPayload{
		MediaID: mediaID, HasPublishedSubtitles: has, Languages: langs,
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(events.EventEnvelope{
		EventID: "e-" + at.Format(time.RFC3339Nano), EventType: events.MediaSubtitlesChanged,
		OccurredAt: at, Payload: raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{Value: b}
}

func TestSubtitlesChangedSetsAndClearsTheFlagOnEveryPost(t *testing.T) {
	fos := newSubtitleOS(t)
	fos.docs["p1"] = map[string]any{"post_id": "p1"}
	fos.docs["p2"] = map[string]any{"post_id": "p2", "has_subtitles": false}
	fos.docs["other"] = map[string]any{"post_id": "other"}
	lookup := &fakePostsByMedia{posts: map[string][]string{"m1": {"p1", "p2"}}}
	c := subtitleConsumer(t, fos, lookup)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)

	if err := c.processMessage(ctx, subtitlesMsg(t, "m1", true, []string{"en"}, t0)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"p1", "p2"} {
		if v, ok := fos.has(id); !ok || !v {
			t.Fatalf("%s: has_subtitles not set", id)
		}
	}
	if _, ok := fos.has("other"); ok {
		t.Fatal("a post that does not attach the media was touched")
	}
	if len(lookup.asked) != 1 || lookup.asked[0] != "m1" {
		t.Fatalf("post-service was asked about %v", lookup.asked)
	}
	if !strings.Contains(fos.scripts[0], "subtitles_version") || !strings.Contains(fos.scripts[0], "removed") {
		t.Fatalf("the update is not the ordered, tombstone-safe script: %s", fos.scripts[0])
	}

	// Every track unpublished: cleared.
	if err := c.processMessage(ctx, subtitlesMsg(t, "m1", false, []string{}, t0.Add(time.Second))); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"p1", "p2"} {
		if v, ok := fos.has(id); !ok || v {
			t.Fatalf("%s: has_subtitles not cleared", id)
		}
	}
}

func TestSubtitlesChangedIsIdempotentAndOrdered(t *testing.T) {
	fos := newSubtitleOS(t)
	fos.docs["p1"] = map[string]any{"post_id": "p1"}
	c := subtitleConsumer(t, fos, &fakePostsByMedia{posts: map[string][]string{"m1": {"p1"}}})
	ctx := context.Background()
	t0 := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)

	on := subtitlesMsg(t, "m1", true, []string{"en"}, t0.Add(2*time.Second))
	for i := 0; i < 3; i++ { // a redelivered snapshot
		if err := c.processMessage(ctx, on); err != nil {
			t.Fatal(err)
		}
	}
	if v, _ := fos.has("p1"); !v || fos.updates != 1 {
		t.Fatalf("redelivery must converge on one write: has=%v writes=%d", v, fos.updates)
	}

	// An OLDER snapshot arriving late (another partition) is dropped.
	if err := c.processMessage(ctx, subtitlesMsg(t, "m1", false, nil, t0)); err != nil {
		t.Fatal(err)
	}
	if v, _ := fos.has("p1"); !v {
		t.Fatal("a stale 'no captions' snapshot overwrote a newer 'captions'")
	}
	if got := fos.docs["p1"]["subtitles_version"]; got != t0.Add(2*time.Second).UnixMicro() {
		t.Fatalf("version stamp = %v, want the event's OccurredAt in microseconds", got)
	}
}

func TestSubtitlesChangedLeavesTombstonesAndMissingDocsAlone(t *testing.T) {
	fos := newSubtitleOS(t)
	fos.docs["gone"] = map[string]any{"post_id": "gone", "removed": true}
	c := subtitleConsumer(t, fos, &fakePostsByMedia{posts: map[string][]string{"m1": {"gone", "never-indexed"}}})

	if err := c.processMessage(context.Background(), subtitlesMsg(t, "m1", true, []string{"en"}, time.Now())); err != nil {
		t.Fatalf("a tombstone or an unindexed post is not an error: %v", err)
	}
	if _, ok := fos.has("gone"); ok {
		t.Fatal("a removed document was given has_subtitles")
	}
}

func TestSubtitlesChangedFailuresRetryRatherThanDrop(t *testing.T) {
	t.Run("no post lookup wired", func(t *testing.T) {
		c := subtitleConsumer(t, newSubtitleOS(t), nil)
		err := c.processMessage(context.Background(), subtitlesMsg(t, "m1", true, []string{"en"}, time.Now()))
		if !errors.Is(err, errPostsByMediaNotConfigured) {
			t.Fatalf("want errPostsByMediaNotConfigured, got %v", err)
		}
	})
	t.Run("post-service unreachable", func(t *testing.T) {
		c := subtitleConsumer(t, newSubtitleOS(t), &fakePostsByMedia{err: errors.New("503")})
		if err := c.processMessage(context.Background(), subtitlesMsg(t, "m1", true, nil, time.Now())); err == nil {
			t.Fatal("an unresolved lookup must be an error (retry, then DLQ), not 'no posts'")
		}
	})
	t.Run("opensearch outage", func(t *testing.T) {
		fos := newSubtitleOS(t)
		fos.docs["p1"] = map[string]any{"post_id": "p1"}
		fos.fail = 1000
		c := subtitleConsumer(t, fos, &fakePostsByMedia{posts: map[string][]string{"m1": {"p1"}}})
		if err := c.processMessage(context.Background(), subtitlesMsg(t, "m1", true, nil, time.Now())); err == nil {
			t.Fatal("a failed index write must surface")
		}
	})
	t.Run("no media id is ignored, not retried forever", func(t *testing.T) {
		lookup := &fakePostsByMedia{}
		c := subtitleConsumer(t, newSubtitleOS(t), lookup)
		if err := c.processMessage(context.Background(), subtitlesMsg(t, "", true, nil, time.Now())); err != nil {
			t.Fatal(err)
		}
		if len(lookup.asked) != 0 {
			t.Fatal("post-service was asked about an empty media id")
		}
	})
}

func TestSubtitlesVersionWithoutOccurredAtIsZero(t *testing.T) {
	if v := subtitlesVersion(events.EventEnvelope{}); v != 0 {
		t.Fatalf("got %d", v)
	}
}
