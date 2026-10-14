package events

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// user.mentioned from a comment (T-12) carries comment_id, which
// notification-service turns into /post/<post>?focusComment=<comment>. A post
// mention leaves it off the wire, so the existing contract is unchanged.
func TestUserMentionedPayloadCommentID(t *testing.T) {
	mentioned, author, post, comment := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	at := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)

	p := UserMentionedPayload(mentioned, author, post.String(), comment, at)
	if p.MentionedUserID != mentioned.String() || p.AuthorID != author.String() || p.PostID != post.String() ||
		p.CommentID != comment.String() || !p.OccurredAt.Equal(at) {
		t.Fatalf("comment payload = %+v", p)
	}

	b, err := json.Marshal(UserMentionedPayload(mentioned, author, post.String(), uuid.Nil, at))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "comment_id") {
		t.Fatalf("post mention must not carry comment_id: %s", b)
	}
}
