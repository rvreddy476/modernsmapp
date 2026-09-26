package service

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

/*
Guards for the single comment_count writer and the comment_change frame.

The bug these pin: comment_count was incremented by BOTH the request path
and a Kafka consumer, replies were never counted, hidden comments stayed
counted, and every read came from a Scylla column nothing wrote. Each
mutation below must move the count exactly once, through
pgStore.AdjustCommentCount, and announce itself with publishCommentChange.
*/

var (
	lineComment  = regexp.MustCompile(`(?m)//.*$`)
	blockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

func srcOf(t *testing.T, path, fn string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != fn || fd.Body == nil {
			continue
		}
		body := string(raw[fset.Position(fd.Body.Pos()).Offset:fset.Position(fd.Body.End()).Offset])
		return lineComment.ReplaceAllString(blockComment.ReplaceAllString(body, " "), "")
	}
	t.Fatalf("%s: no function %s", path, fn)
	return ""
}

func TestEveryCommentMutationMovesTheCountOnceAndAnnounces(t *testing.T) {
	cases := []struct {
		file, fn, change string
		adjusts          int
	}{
		{"post.go", "CreateCommentPG", "CommentChangeCreated", 1},
		{"post.go", "CreateReply", "CommentChangeReplied", 1},
		{"post.go", "SoftDeleteComment", "CommentChangeDeleted", 1},
		{"post.go", "EditComment", "CommentChangeEdited", 0},
		{"post.go", "ToggleCommentLike", "CommentChangeReaction", 0},
		{"post.go", "ToggleCommentDislike", "CommentChangeReaction", 0},
		{"reports.go", "SetCommentModerationStatus", "CommentChangeModerated", 1},
		{"reports.go", "SubmitReport", "CommentChangeModerated", 1},
	}
	for _, c := range cases {
		body := srcOf(t, c.file, c.fn)
		if n := strings.Count(body, "s.pgStore.AdjustCommentCount("); n != c.adjusts {
			t.Errorf("%s adjusts comment_count %d times, want %d", c.fn, n, c.adjusts)
		}
		if strings.Contains(body, "adjustEngagementCount(ctx, s.commentCounter") || strings.Contains(body, `"comments", 1)`) || strings.Contains(body, `"comments", -1)`) {
			t.Errorf("%s still writes a second comment counter (sharded or post:eng)", c.fn)
		}
		if !strings.Contains(body, "s.publishCommentChange(") || !strings.Contains(body, c.change) {
			t.Errorf("%s does not publish comment_change %s", c.fn, c.change)
		}
	}
	// A delete only leaves the count when the comment was in it.
	if body := srcOf(t, "post.go", "SoftDeleteComment"); !strings.Contains(body, "if counted {") {
		t.Error("SoftDeleteComment decrements without checking the comment was counted")
	}
}

func TestNoCommentCountShardAndOneReadSource(t *testing.T) {
	raw, err := os.ReadFile("post.go")
	if err != nil {
		t.Fatal(err)
	}
	src := lineComment.ReplaceAllString(blockComment.ReplaceAllString(string(raw), " "), "")
	if strings.Contains(src, `EntityKind: "post_comment_count"`) {
		t.Fatal("a sharded comment counter is still constructed — its flush would overwrite the authoritative column")
	}
	// Every read of counts goes through the merged reader.
	if strings.Contains(src, "s.scyllaStore.GetCounts(") {
		t.Fatal("a read still takes counts straight from Scylla post_counters, whose comment_count nothing writes")
	}
	if !strings.Contains(src, "s.overlayCommentCounts(ctx, ids, countsByPost)") {
		t.Fatal("the batch read (what the feed hydrates from) does not overlay the PostgreSQL comment count")
	}
	for _, f := range []string{"my_uploads.go"} {
		b, _ := os.ReadFile(f)
		if strings.Contains(string(b), "s.scyllaStore.GetCounts(") {
			t.Errorf("%s still reads counts straight from Scylla", f)
		}
	}
}

func TestPGCounterConsumerNoLongerCountsComments(t *testing.T) {
	body := srcOf(t, "../engagement/consumers/pg_counter.go", "handleEvent")
	if !strings.Contains(body, "case engagement.EventCommentCreated, engagement.EventCommentDeleted:\n\t\treturn nil") {
		t.Fatal("PGCounterConsumer still increments comment_count — the second writer that doubled every comment")
	}
}

func TestCommentChangeFrame(t *testing.T) {
	post, comment, parent, actor := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	frame, err := BuildCommentChange(post, comment, &parent, CommentChangeReplied, actor, 7, now)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Type    string `json:"type"`
		Payload struct {
			EventID   string `json:"event_id"`
			Version   int64  `json:"version"`
			PostID    string `json:"post_id"`
			CommentID string `json:"comment_id"`
			ParentID  string `json:"parent_id"`
			Change    string `json:"change"`
			ActorID   string `json:"actor_id"`
			Comments  int64  `json:"comments"`
			Body      *string `json:"body"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(frame, &env); err != nil {
		t.Fatal(err)
	}
	if env.Type != "comment_change" || env.Payload.PostID != post.String() || env.Payload.CommentID != comment.String() ||
		env.Payload.ParentID != parent.String() || env.Payload.Change != "replied" || env.Payload.ActorID != actor.String() ||
		env.Payload.Comments != 7 || env.Payload.Version != now.UnixMicro() {
		t.Fatalf("frame %s", frame)
	}
	if _, err := uuid.Parse(env.Payload.EventID); err != nil {
		t.Fatal("event_id is not a uuid")
	}
	if env.Payload.Body != nil || strings.Contains(string(frame), "\"body\"") {
		t.Fatal("the frame carries a comment body — bodies go through the authorized list endpoint only")
	}
	top, _ := BuildCommentChange(post, comment, nil, CommentChangeCreated, actor, 1, now)
	if strings.Contains(string(top), "parent_id") {
		t.Fatal("a top-level comment frame must omit parent_id")
	}
}
