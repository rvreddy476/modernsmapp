package events

import (
	"testing"

	"github.com/atpost/shared/events"
)

// A comment mention (post-service T-12 sets comment_id) opens the comment,
// like every other comment notification; a post mention opens the post.
func TestMentionDeepLink(t *testing.T) {
	post := "11111111-1111-4111-8111-111111111111"
	comment := "66666666-6666-4666-8666-666666666666"
	if got, want := mentionDeepLink(events.UserMentionedPayload{PostID: post}), "/post/"+post; got != want {
		t.Fatalf("post mention = %q, want %q", got, want)
	}
	got := mentionDeepLink(events.UserMentionedPayload{PostID: post, CommentID: comment})
	if want := "/post/" + post + "?focusComment=" + comment; got != want {
		t.Fatalf("comment mention = %q, want %q", got, want)
	}
}
