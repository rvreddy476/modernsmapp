package service

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/engagement"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Heart only and no reel repost (heart_only.go, founder decision 24 Oct).

// postContentTypes is every content_type a post row carries (the canonical
// four plus the legacy spellings still on old rows).
var postContentTypes = []string{"post", "poll", "article", "flick", "reel", "long_video", "video"}

var emojiReactions = []string{"love", "haha", "wow", "sad", "angry"}

// TestCheckPostReaction_HeartOnly: "like" passes; the five emojis, the
// private dislike, a case variant and junk are refused.
func TestCheckPostReaction_HeartOnly(t *testing.T) {
	if err := checkPostReaction("like"); err != nil {
		t.Fatalf("like refused: %v", err)
	}
	for _, r := range append(append([]string{}, emojiReactions...), "tune", "spark", "Like", "heart", " like") {
		if err := checkPostReaction(r); !errors.Is(err, ErrReactionNotAllowed) {
			t.Errorf("checkPostReaction(%q) = %v, want ErrReactionNotAllowed", r, err)
		}
	}
	if got := PostReactionsAllowed(); len(got) != 1 || got[0] != "like" {
		t.Fatalf("allowed = %v, want [like]", got)
	}
	// A caller mutating the details slice does not change the rule.
	PostReactionsAllowed()[0] = "love"
	if PostReactionsAllowed()[0] != "like" {
		t.Fatal("PostReactionsAllowed handed out the package slice")
	}
}

// engagementProbe is an EngagementPost seam that records whether the write
// got past its guard. It answers a post of contentType, or ErrPostNotFound
// when contentType is "" so nothing further runs.
type engagementProbe struct {
	contentType string
	calls       int
}

func (p *engagementProbe) load(_ context.Context, postID, _ uuid.UUID) (*postgres.Post, error) {
	p.calls++
	if p.contentType == "" {
		return nil, ErrPostNotFound
	}
	return &postgres.Post{ID: postID, AuthorID: uuid.New(), ContentType: p.contentType, Visibility: "public"}, nil
}

// Every post-reaction write refuses a non-heart reaction BEFORE the post is
// read — so for every content type — and lets the heart through to the post
// read.
func TestPostReactionWrites_RefuseEmojiOnEveryContentType(t *testing.T) {
	ctx := context.Background()
	for _, ct := range postContentTypes {
		for _, emoji := range emojiReactions {
			probe := &engagementProbe{contentType: ct}
			liked := newFakeLikedStore()
			svc := NewForHandlerTests(HandlerTestDeps{EngagementPost: probe.load, LikedReels: liked})
			post, viewer := uuid.New(), uuid.New()

			if err := svc.React(ctx, post, viewer, emoji); !errors.Is(err, ErrReactionNotAllowed) {
				t.Errorf("%s React(%s) = %v, want ErrReactionNotAllowed", ct, emoji, err)
			}
			if _, err := svc.ToggleReaction(ctx, post, viewer, emoji); !errors.Is(err, ErrReactionNotAllowed) {
				t.Errorf("%s ToggleReaction(%s) = %v, want ErrReactionNotAllowed", ct, emoji, err)
			}
			if err := svc.ReactToReel(ctx, post, viewer, emoji); !errors.Is(err, ErrReactionNotAllowed) {
				t.Errorf("%s ReactToReel(%s) = %v, want ErrReactionNotAllowed", ct, emoji, err)
			}
			if probe.calls != 0 {
				t.Errorf("%s %s: the post was read %d times before the refusal", ct, emoji, probe.calls)
			}
			if n := len(liked.rows[viewer]); n != 0 {
				t.Errorf("%s %s: the liked list was written (%d rows)", ct, emoji, n)
			}
		}
	}
}

func TestPostReactionWrites_AcceptTheHeart(t *testing.T) {
	ctx := context.Background()
	probe := &engagementProbe{}
	liked := newFakeLikedStore()
	liked.err = errors.New("index down") // stops ReactToReel after its first write
	svc := NewForHandlerTests(HandlerTestDeps{EngagementPost: probe.load, LikedReels: liked})
	post, viewer := uuid.New(), uuid.New()

	if err := svc.React(ctx, post, viewer, "like"); !errors.Is(err, ErrPostNotFound) {
		t.Fatalf("React(like) = %v, want it to reach the post read (ErrPostNotFound)", err)
	}
	if _, err := svc.ToggleReaction(ctx, post, viewer, "like"); !errors.Is(err, ErrPostNotFound) {
		t.Fatalf("ToggleReaction(like) = %v, want it to reach the post read (ErrPostNotFound)", err)
	}
	if probe.calls != 2 {
		t.Fatalf("post reads = %d, want 2", probe.calls)
	}
	if err := svc.ReactToReel(ctx, post, viewer, "like"); err == nil || errors.Is(err, ErrReactionNotAllowed) {
		t.Fatalf("ReactToReel(like) = %v, want it to reach the liked-list write", err)
	}
}

// An old emoji reads as the heart; none and the private dislike do not.
func TestHeartViewerReaction_OldEmojiReadsAsLiked(t *testing.T) {
	for _, stored := range append(append([]string{}, emojiReactions...), "like", "spark", "supernova") {
		if got := heartViewerReaction(stored); got != "like" {
			t.Errorf("heartViewerReaction(%q) = %q, want like", stored, got)
		}
		d := &PostDetail{}
		applyViewerReaction(d, stored)
		if d.ViewerReaction == nil || *d.ViewerReaction != "like" || !d.HasReacted {
			t.Errorf("stored %q: viewer_reaction=%v has_reacted=%v, want like/true", stored, d.ViewerReaction, d.HasReacted)
		}
	}
	for _, stored := range []string{"", "tune"} {
		if got := heartViewerReaction(stored); got != "" {
			t.Errorf("heartViewerReaction(%q) = %q, want empty", stored, got)
		}
		d := &PostDetail{}
		applyViewerReaction(d, stored)
		if d.ViewerReaction != nil || d.HasReacted {
			t.Errorf("stored %q read as reacted", stored)
		}
	}
}

// The breakdown counts every person who reacted, whatever the emoji, as a
// like: nobody's old reaction vanishes from the number.
func TestFoldPostReactionCounts_EveryReactionCountsAsALike(t *testing.T) {
	got := foldPostReactionCounts(&postgres.ReactionCounts{Like: 4, Love: 3, Haha: 2, Wow: 1, Sad: 1, Angry: 1, Total: 12})
	want := postgres.ReactionCounts{Like: 12, Total: 12}
	if got == nil || *got != want {
		t.Fatalf("fold = %+v, want %+v", got, want)
	}
	if foldPostReactionCounts(nil) != nil {
		t.Fatal("fold(nil) != nil")
	}
}

// deadRedisLimiter fails open at once (no Redis in unit tests), as the
// limiter does in production when Redis errors.
func deadRedisLimiter() *engagement.RateLimiter {
	rdb := redis.NewClient(&redis.Options{
		Addr:       "unit-test:0",
		MaxRetries: -1,
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("no redis in unit tests")
		},
	})
	return engagement.NewRateLimiter(rdb)
}

func repostSvc(contentType, visibility string) *Service {
	return NewForHandlerTests(HandlerTestDeps{
		RateLimiter: deadRedisLimiter(),
		RepostTarget: func(_ context.Context, postID uuid.UUID) (*postgres.Post, error) {
			return &postgres.Post{ID: postID, AuthorID: uuid.New(), ContentType: contentType, Visibility: visibility}, nil
		},
	})
}

func wantRepostRefused(t *testing.T, label string, err error, contentType string) {
	t.Helper()
	var refused *RepostNotAllowedError
	if !errors.As(err, &refused) || refused.ContentType != contentType {
		t.Fatalf("%s: err = %v, want RepostNotAllowedError{%s}", label, err, contentType)
	}
}

// A reel is refused on both repost kinds of POST /repost and POST /share.
func TestRepost_ReelIsRefused(t *testing.T) {
	ctx := context.Background()
	for _, ct := range []string{"flick", "reel"} {
		svc := repostSvc(ct, "public")
		for _, typ := range []string{"plain", "quote"} {
			_, err := svc.CreateRepost(ctx, CreateRepostInput{UserID: uuid.New(), PostID: uuid.New(), Type: typ, QuoteText: "nice"})
			wantRepostRefused(t, ct+" CreateRepost "+typ, err, ct)
		}
		for _, typ := range []string{"repost", "quote"} {
			_, err := svc.SharePost(ctx, uuid.New(), uuid.New(), typ, "nice")
			wantRepostRefused(t, ct+" SharePost "+typ, err, ct)
			wantRepostRefused(t, ct+" ShareReel "+typ, svc.ShareReel(ctx, uuid.New(), uuid.New(), typ), ct)
		}
	}
}

// A feed post and a long video pass the reel guard: with a private target
// the next check (visibility) is what answers, so the guard let them by.
func TestRepost_FeedPostAndLongVideoAreAllowed(t *testing.T) {
	ctx := context.Background()
	for _, ct := range []string{"post", "poll", "long_video", "video"} {
		svc := repostSvc(ct, "private")
		for _, typ := range []string{"plain", "quote"} {
			_, err := svc.CreateRepost(ctx, CreateRepostInput{UserID: uuid.New(), PostID: uuid.New(), Type: typ, QuoteText: "nice"})
			if err == nil || err.Error() != "NOT_ELIGIBLE" {
				t.Fatalf("%s CreateRepost %s = %v, want the visibility check (NOT_ELIGIBLE)", ct, typ, err)
			}
		}
		for _, typ := range []string{"repost", "quote"} {
			_, err := svc.SharePost(ctx, uuid.New(), uuid.New(), typ, "nice")
			if err == nil || err.Error() != "CIRCLE_SHARE_RESTRICTED" {
				t.Fatalf("%s SharePost %s = %v, want the circle check", ct, typ, err)
			}
		}
	}
	// A reel's external share is not a repost: it too reaches the circle
	// check rather than the reel guard.
	if _, err := repostSvc("flick", "private").SharePost(ctx, uuid.New(), uuid.New(), "external", ""); err == nil || err.Error() != "CIRCLE_SHARE_RESTRICTED" {
		t.Fatalf("flick external share = %v, want the circle check", err)
	}
}

func TestRepostGuardHelpers(t *testing.T) {
	for _, ct := range postContentTypes {
		short := ct == "flick" || ct == "reel"
		if got := checkRepostContentType(ct) != nil; got != short {
			t.Errorf("checkRepostContentType(%q) refused=%v, want %v", ct, got, short)
		}
		if got := repostableContent(ct, "public"); got == short {
			t.Errorf("repostableContent(%q, public) = %v", ct, got)
		}
		if repostableContent(ct, "private") {
			t.Errorf("repostableContent(%q, private) = true", ct)
		}
	}
	for typ, want := range map[string]bool{"repost": true, "quote": true, "external": false, "direct": false, "": false} {
		if isRepostShareType(typ) != want {
			t.Errorf("isRepostShareType(%q) != %v", typ, want)
		}
	}
}

// The reads that answer viewer_reaction / has_reacted / is_repostable, and
// the writes, go through the helpers above. The helpers' behaviour is
// tested; this pins that each entry point still calls them (most of these
// read Scylla, which unit tests do not stand up).
func TestHeartOnlyCallSites(t *testing.T) {
	want := map[string][]string{
		"React":             {"checkPostReaction"},
		"ToggleReaction":    {"checkPostReaction", "foldPostReactionCounts"},
		"ReactToReel":       {"checkPostReaction"},
		"GetMyReaction":     {"heartViewerReaction"},
		"GetReelReaction":   {"heartViewerReaction"},
		"GetReelFeed":       {"heartViewerReaction"},
		"GetPost":           {"applyViewerReaction", "repostableContent"},
		"loadPostsByIDs":    {"applyViewerReaction", "repostableContent"},
		"GetReactionCounts": {"foldPostReactionCounts"},
		"CreateRepost":      {"checkRepostContentType", "loadRepostTarget"},
		"SharePost":         {"checkRepostContentType", "loadRepostTarget"},
		"ShareReel":         {"checkRepostContentType", "loadRepostTarget"},
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]map[string]bool{}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Body == nil || len(fn.Recv.List) != 1 {
				continue
			}
			star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			if recv, ok := star.X.(*ast.Ident); !ok || recv.Name != "Service" {
				continue
			}
			if _, tracked := want[fn.Name.Name]; !tracked {
				continue
			}
			seen := map[string]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					switch f := call.Fun.(type) {
					case *ast.Ident:
						seen[f.Name] = true
					case *ast.SelectorExpr:
						seen[f.Sel.Name] = true
					}
				}
				return true
			})
			calls[fn.Name.Name] = seen
		}
	}
	for fn, helpers := range want {
		seen, ok := calls[fn]
		if !ok {
			t.Errorf("%s: method not found", fn)
			continue
		}
		for _, h := range helpers {
			if !seen[h] {
				t.Errorf("%s no longer calls %s (heart_only.go)", fn, h)
			}
		}
	}
}
