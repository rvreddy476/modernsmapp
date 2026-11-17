package events

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/atpost/shared/events"
	"github.com/segmentio/kafka-go"
)

// Metadata edits reach search (lv-29 / LV-A-5, post_content.go).
//
// post-service re-emits PostSearchEligibilityChanged from the edit
// transaction with a bumped search_rev. These tests drive the real consumer
// against the fake OpenSearch and pin what search does with it: the new
// title, text, tags, hashtags, category and language replace the old ones;
// an older revision never overwrites a newer edit; and an edit of a post
// that is not publicly visible never indexes it.

// editPayload is the eligibility payload plus the optional creator metadata
// the post-service lane adds (the lane contract).
type editPayload struct {
	events.PostSearchEligibilityChangedPayload
	Tags     []string `json:"tags,omitempty"`
	Hashtags []string `json:"hashtags,omitempty"`
	Category string   `json:"category,omitempty"`
	Language string   `json:"language,omitempty"`
}

func editMsg(t *testing.T, id string, rev int64, title, text string, tags, hashtags []string, category, language string) kafka.Message {
	t.Helper()
	return msg(t, events.PostSearchEligibilityChanged, editPayload{
		PostSearchEligibilityChangedPayload: events.PostSearchEligibilityChangedPayload{
			PostID: id, AuthorID: "author-1", Visibility: "public", ReviewStatus: "approved",
			SearchRev: rev, Title: title, Text: text, ContentType: "long_video",
			CreatedAt: time.Now().UTC().Add(-time.Hour), ChangedAt: time.Now().UTC(),
		},
		Tags: tags, Hashtags: hashtags, Category: category, Language: language,
	})
}

func strs(v any) []string {
	raw, _ := json.Marshal(v)
	var out []string
	_ = json.Unmarshal(raw, &out)
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		if m[x] == 0 {
			return false
		}
		m[x]--
	}
	return true
}

func TestMetadataEdit_ReplacesTitleTextTagsCategoryAndLanguage(t *testing.T) {
	f := newFakeOS(t)
	c := newTestConsumer(t, f)
	ctx := context.Background()
	id := "edit-1"

	if err := c.processMessage(ctx, editMsg(t, id, 2, "Draft title", "first cut #draft",
		[]string{"rough"}, nil, "music", "en")); err != nil {
		t.Fatal(err)
	}
	if err := c.processMessage(ctx, editMsg(t, id, 3, "Final title", "the release #final",
		[]string{"Final Cut", "final cut", "  ", "Director's cut"}, []string{"#Premiere", "final", "bad tag"},
		" Education ", "HI")); err != nil {
		t.Fatal(err)
	}

	doc := f.doc(id)
	if doc["title"] != "Final title" || doc["text"] != "the release #final" {
		t.Fatalf("title/text not replaced: %v / %v", doc["title"], doc["text"])
	}
	if got := strs(doc["tags"]); !sameSet(got, []string{"Final Cut", "Director's cut"}) {
		t.Fatalf("tags = %v", got)
	}
	if got := strs(doc["hashtags"]); !sameSet(got, []string{"final", "premiere"}) {
		t.Fatalf("hashtags = %v (want the text tag and the hashtag column, no stale #draft)", got)
	}
	if doc["category"] != "education" || doc["language"] != "hi" {
		t.Fatalf("category/language = %v / %v", doc["category"], doc["language"])
	}
	if f.rev(id) != 3 {
		t.Fatalf("rev = %d, want 3", f.rev(id))
	}

	// Clearing the metadata on a later edit clears it in the index.
	if err := c.processMessage(ctx, editMsg(t, id, 4, "Final title", "the release", nil, nil, "", "")); err != nil {
		t.Fatal(err)
	}
	doc = f.doc(id)
	for _, field := range []string{"tags", "hashtags", "category", "language"} {
		if v, ok := doc[field]; ok && v != nil && v != "" {
			t.Fatalf("%s survived an edit that cleared it: %v", field, v)
		}
	}
}

func TestMetadataEdit_StaleRevisionCannotOverwriteNewerEdit(t *testing.T) {
	f := newFakeOS(t)
	c := newTestConsumer(t, f)
	ctx := context.Background()
	id := "edit-stale"

	if err := c.processMessage(ctx, editMsg(t, id, 5, "Final title", "new", []string{"new"}, nil, "news", "en")); err != nil {
		t.Fatal(err)
	}
	// The older edit is delivered late (DLQ replay, another partition).
	if err := c.processMessage(ctx, editMsg(t, id, 4, "Draft title", "old", []string{"old"}, nil, "music", "fr")); err != nil {
		t.Fatal(err)
	}
	// And the same edit twice is a no-op.
	if err := c.processMessage(ctx, editMsg(t, id, 5, "Final title", "new", []string{"new"}, nil, "news", "en")); err != nil {
		t.Fatal(err)
	}
	doc := f.doc(id)
	if doc["title"] != "Final title" || doc["category"] != "news" || doc["language"] != "en" {
		t.Fatalf("stale edit overwrote the newer one: %v", doc)
	}
	if got := strs(doc["tags"]); !sameSet(got, []string{"new"}) {
		t.Fatalf("tags = %v", got)
	}
	if f.rev(id) != 5 {
		t.Fatalf("rev = %d, want 5", f.rev(id))
	}
}

// An edit of a video that is not publicly visible must not index it, even
// when a (buggy) producer puts the body and metadata on the event.
func TestMetadataEdit_NonPublicVideoNeverEntersTheIndex(t *testing.T) {
	type variant struct {
		name       string
		visibility string
		review     string
		scheduled  bool
		deleted    bool
		tier       string
	}
	for _, v := range []variant{
		{name: "unlisted", visibility: "unlisted", review: "approved"},
		{name: "private", visibility: "private", review: "approved"},
		{name: "followers", visibility: "followers", review: "approved"},
		{name: "processing", visibility: "public", review: "pending"},
		{name: "rejected", visibility: "public", review: "rejected"},
		{name: "restricted", visibility: "public", review: "restricted"},
		{name: "scheduled", visibility: "public", review: "approved", scheduled: true},
		{name: "deleted", visibility: "public", review: "approved", deleted: true},
		{name: "members-only", visibility: "public", review: "approved", tier: "7f1d2c9e-0000-4000-8000-000000000001"},
	} {
		for _, previouslyIndexed := range []bool{false, true} {
			name := v.name
			if previouslyIndexed {
				name += "/was-public"
			}
			t.Run(name, func(t *testing.T) {
				f := newFakeOS(t)
				c := newTestConsumer(t, f)
				ctx := context.Background()
				id := "edit-hidden-" + v.name
				if previouslyIndexed {
					if err := c.processMessage(ctx, editMsg(t, id, 2, "Old title", "old", []string{"old"}, nil, "music", "en")); err != nil {
						t.Fatal(err)
					}
				}
				p := editPayload{
					PostSearchEligibilityChangedPayload: events.PostSearchEligibilityChangedPayload{
						PostID: id, AuthorID: "author-1", Visibility: v.visibility, ReviewStatus: v.review,
						Scheduled: v.scheduled, Deleted: v.deleted, TierRequiredID: v.tier,
						SearchRev: 3, Title: "Secret title", Text: "secret #hidden", ContentType: "long_video",
						CreatedAt: time.Now().UTC(), ChangedAt: time.Now().UTC(),
					},
					Tags: []string{"secret"}, Hashtags: []string{"hidden"}, Category: "secret", Language: "en",
				}
				if err := c.processMessage(ctx, msg(t, events.PostSearchEligibilityChanged, p)); err != nil {
					t.Fatal(err)
				}
				if f.indexed(id) {
					t.Fatalf("a %s edit made the video searchable: %v", v.name, f.doc(id))
				}
				doc := f.doc(id)
				for _, field := range []string{"title", "text", "tags", "hashtags", "category", "language"} {
					if val, ok := doc[field]; ok && val != nil && val != "" {
						t.Fatalf("%s left %s=%v on the document", v.name, field, val)
					}
				}
			})
		}
	}
}

func TestPostCreated_IndexesCreatorMetadata(t *testing.T) {
	f := newFakeOS(t)
	c := newTestConsumer(t, f)
	type createdPayload struct {
		events.PostCreatedPayload
		Tags     []string `json:"tags"`
		Category string   `json:"category"`
		Language string   `json:"language"`
	}
	id := "created-meta"
	if err := c.processMessage(context.Background(), msg(t, events.PostCreated, createdPayload{
		PostCreatedPayload: events.PostCreatedPayload{
			PostID: id, AuthorID: "author-1", Text: "hello #world", Visibility: "public",
			ContentType: "long_video", ReviewStatus: "approved", SearchRev: 1, CreatedAt: time.Now().UTC(),
		},
		Tags: []string{"travel"}, Category: "Travel", Language: "en",
	})); err != nil {
		t.Fatal(err)
	}
	doc := f.doc(id)
	if got := strs(doc["tags"]); !sameSet(got, []string{"travel"}) || doc["category"] != "travel" || doc["language"] != "en" {
		t.Fatalf("metadata not indexed on create: %v", doc)
	}
}

// A field of the wrong JSON type is skipped; the rest of the edit lands and
// the partition is not held.
func TestMetadataEdit_MalformedFieldIsSkippedNotFatal(t *testing.T) {
	f := newFakeOS(t)
	c := newTestConsumer(t, f)
	raw := json.RawMessage(`{"post_id":"edit-bad","author_id":"a","visibility":"public","review_status":"approved",
		"search_rev":2,"title":"New title","text":"x","content_type":"long_video",
		"tags":"not-a-list","category":42,"language":"te"}`)
	env, _ := json.Marshal(events.EventEnvelope{EventType: events.PostSearchEligibilityChanged, Payload: raw})
	if err := c.processMessage(context.Background(), kafka.Message{Value: env}); err != nil {
		t.Fatalf("malformed metadata failed the event: %v", err)
	}
	doc := f.doc("edit-bad")
	if doc["title"] != "New title" || doc["language"] != "te" {
		t.Fatalf("edit not applied: %v", doc)
	}
	if _, ok := doc["tags"]; ok {
		t.Fatalf("wrong-typed tags indexed: %v", doc["tags"])
	}
	if _, ok := doc["category"]; ok {
		t.Fatalf("wrong-typed category indexed: %v", doc["category"])
	}
}

func TestMergeHashtags_NormalisesAndRejectsNonTokens(t *testing.T) {
	got := mergeHashtags([]string{"a"}, []string{"#A", "B", "two words", "", "#", "c-d", "ok_1"})
	if !sameSet(got, []string{"a", "b", "ok_1"}) {
		t.Fatalf("mergeHashtags = %v", got)
	}
}
