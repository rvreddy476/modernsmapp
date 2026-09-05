package service

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// Scheduled publish (2026-09-05): a scheduled post reaches its author and
// nobody else on every surface, even from the hydration cache — the same
// rule and the same choke point as is_processing.

func TestProcessingFilter_DropsScheduledForNonAuthorsKeepsForAuthor(t *testing.T) {
	author := uuid.New()
	viewer := uuid.New()
	scheduledID := uuid.New()
	liveID := uuid.New()

	page := func() []HydratedPost {
		at := "2026-09-05T12:06:00Z"
		return []HydratedPost{
			{ID: scheduledID, AuthorID: author, IsScheduled: true, PublishAt: &at},
			{ID: liveID, AuthorID: author},
		}
	}

	got := applyProcessingFilter(viewer, page())
	if len(got) != 1 || got[0].ID != liveID {
		t.Fatalf("non-author must not receive a scheduled post; got %+v", got)
	}
	got = applyProcessingFilter(author, page())
	if len(got) != 2 || got[0].ID != scheduledID || !got[0].IsScheduled || got[0].PublishAt == nil {
		t.Fatalf("author must keep their own scheduled post with publish_at; got %+v", got)
	}
}

// The feed item forwards the contract fields post-service emits.
func TestHydratedPostCarriesScheduleAndTagFields(t *testing.T) {
	raw := `{"id":"` + uuid.New().String() + `","author_id":"` + uuid.New().String() + `",
		"publish_at":"2026-09-05T12:06:00Z","is_scheduled":true,
		"hashtags":["momentum","test"],"mentions":["call.usera"],"is_processing":false}`
	var hp HydratedPost
	if err := json.Unmarshal([]byte(raw), &hp); err != nil {
		t.Fatal(err)
	}
	if !hp.IsScheduled || hp.PublishAt == nil || *hp.PublishAt != "2026-09-05T12:06:00Z" {
		t.Fatalf("schedule fields dropped: %+v", hp)
	}
	out, _ := json.Marshal(hp)
	var back map[string]json.RawMessage
	_ = json.Unmarshal(out, &back)
	if string(back["hashtags"]) != `["momentum","test"]` || string(back["mentions"]) != `["call.usera"]` {
		t.Fatalf("tags not forwarded: hashtags=%s mentions=%s", back["hashtags"], back["mentions"])
	}
	if string(back["is_scheduled"]) != "true" || string(back["publish_at"]) != `"2026-09-05T12:06:00Z"` {
		t.Fatalf("schedule not forwarded: %s", out)
	}
	// Live posts always say is_scheduled:false explicitly.
	out, _ = json.Marshal(HydratedPost{})
	_ = json.Unmarshal(out, &back)
	if string(back["is_scheduled"]) != "false" {
		t.Fatalf("is_scheduled must never be omitted: %s", out)
	}
}
