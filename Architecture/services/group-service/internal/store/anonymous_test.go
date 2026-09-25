package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

/*
The masking contract.

A leaked author id cannot be un-leaked. These are the cheapest possible
proofs that it is not leaked, and they need no database: marshal the struct
and look at the bytes.
*/

func TestAnonymousPostMarshalsAliasNotAuthor(t *testing.T) {
	author := uuid.New()
	alias := uuid.New()
	p := GroupPostV2{
		ID:          uuid.New(),
		GroupID:     uuid.New(),
		AuthorID:    author.String(),
		ContentType: "text",
		Status:      "published",
		IsAnonymous: true,
		AnonAlias:   &alias,
	}

	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// The whole feature, in one assertion.
	if strings.Contains(string(b), author.String()) {
		t.Fatalf("the real author id appears in the JSON of an anonymous post:\n%s", b)
	}

	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["author_id"] != alias.String() {
		t.Fatalf("author_id = %v, want the alias %s", got["author_id"], alias)
	}
	if got["is_anonymous"] != true {
		t.Fatal("is_anonymous is not set on the wire, so a client cannot know to skip the profile lookup")
	}
	if _, present := got["anon_alias"]; present {
		t.Fatal("anon_alias is on the wire beside the masked author_id — carrying both lets a client correlate posts by the same author, which is exactly what the alias exists to prevent")
	}
}

/*
An ordinary post is byte-identical to before.

This is the don't-break-mobile test. The mobile app reads this shape and is
not being changed, so MarshalJSON must not drop, rename or reorder anything
on the non-anonymous path.
*/
func TestOrdinaryPostMarshalIsUnchanged(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	author := uuid.New()
	p := GroupPostV2{
		ID:             uuid.New(),
		GroupID:        uuid.New(),
		AuthorID:       author.String(),
		ContentType:    "text",
		TypePayload:    json.RawMessage(`{}`),
		Attachments:    json.RawMessage(`[]`),
		Status:         "published",
		SparkCount:     3,
		CommentCount:   2,
		EchoCount:      1,
		ViewCount:      10,
		CreatedAt:      now,
		UpdatedAt:      now,
		IsPinned:       true,
		IsAnnouncement: true,
		ViewerSparked:  true,
	}

	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Every key a client may already depend on.
	for _, key := range []string{
		"id", "group_id", "author_id", "content_type", "type_payload",
		"attachments", "needs_approval", "is_pinned", "is_announcement",
		"status", "spark_count", "comment_count", "echo_count", "view_count",
		"created_at", "updated_at", "viewer_sparked", "viewer_echoed",
		"viewer_stashed", "viewer_reaction", "reaction_counts",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("key %q disappeared from an ordinary post — mobile reads this shape", key)
		}
	}

	if got["author_id"] != author.String() {
		t.Fatal("a non-anonymous post must carry its real author")
	}
	if got["is_anonymous"] != false {
		t.Fatal("is_anonymous must be present and false, not omitted — a client testing it must get an answer")
	}
}

/*
A row that is anonymous but has no alias must not fall back to the author.

The CHECK constraint in migration 013 makes this unreachable through the
database, but this is the one place where being wrong leaks a real identity,
so the marshaller does not rely on the constraint holding.
*/
func TestAnonymousPostWithNoAliasStillHidesTheAuthor(t *testing.T) {
	author := uuid.New()
	p := GroupPostV2{
		AuthorID:    author.String(),
		IsAnonymous: true,
		AnonAlias:   nil,
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), author.String()) {
		t.Fatalf("an anonymous post with no alias fell back to the real author:\n%s", b)
	}
}

// The author's own comment on their own anonymous post carries the POST's
// alias, so they read as the same pseudonymous person rather than as a second
// anonymous party — and never as themselves.
func TestAnonymousCommentMarshalsPostAliasNotUser(t *testing.T) {
	user := uuid.New()
	alias := uuid.New()
	c := GroupPostComment{
		ID:          uuid.New(),
		PostID:      uuid.New(),
		UserID:      user.String(),
		Body:        "replying to my own post",
		IsAnonymous: true,
		AnonAlias:   &alias,
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), user.String()) {
		t.Fatalf("the commenter's real id appears in an anonymous comment:\n%s", b)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["user_id"] != alias.String() {
		t.Fatalf("user_id = %v, want the post's alias %s", got["user_id"], alias)
	}
}

func TestOrdinaryCommentMarshalIsUnchanged(t *testing.T) {
	user := uuid.New()
	c := GroupPostComment{UserID: user.String(), Body: "hello"}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	if got["user_id"] != user.String() {
		t.Fatal("a non-anonymous comment must carry its real author")
	}
}

/*
Every alias is its own.

A per-user alias, or one generated once above a loop, would let anyone who
sees two anonymous posts know they came from the same person — and in a group
of a dozen that usually names them. This is the invariant the cross-post loop
has to preserve, so it is pinned here at the source.
*/
func TestEachAliasIsDistinct(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 100; i++ {
		a := NewAnonAlias()
		if a == nil {
			t.Fatal("NewAnonAlias returned nil")
		}
		if _, dup := seen[a.String()]; dup {
			t.Fatalf("NewAnonAlias repeated %s — two anonymous posts would be linkable", a)
		}
		seen[a.String()] = struct{}{}
	}
}

// The media grid has its own struct and does not pass through MarshalJSON,
// so it is masked in SQL instead. This pins the query.
func TestMediaListingMasksAnonymousAuthors(t *testing.T) {
	body := funcBody(t, "group.go", "ListGroupMedia")
	if !strings.Contains(body, "CASE WHEN is_anonymous") {
		t.Fatal("ListGroupMedia selects author_id unmasked — GroupMediaItem is its own struct and never reaches GroupPostV2.MarshalJSON, so an anonymous post with a photo would be named in the media tab while masked in the feed")
	}
	if !strings.Contains(body, "anon_alias") {
		t.Fatal("ListGroupMedia does not substitute the alias")
	}
}
