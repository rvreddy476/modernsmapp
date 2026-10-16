package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Comment @mentions (T-12). The rules under test: the commenter is never
// notified, a block in either direction drops the mention, a user who cannot
// open the post is never told about it, repeated handles are one lookup and
// one event, and the per-comment cap is the post cap.

func TestCommentMentionHandlesDedupesAndCaps(t *testing.T) {
	got := commentMentionHandles("@Sam hi @sam and @SAM, also @call.usera. and @bob @bob")
	want := []string{"Sam", "call.usera", "bob"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("handles = %v, want %v", got, want)
	}

	var b strings.Builder
	for i := 0; i < 25; i++ {
		fmt.Fprintf(&b, "@user%02d ", i)
	}
	if got := commentMentionHandles(b.String()); len(got) != maxMentionsPerPost || maxMentionsPerComment != maxMentionsPerPost {
		t.Fatalf("cap: got %d handles (cap %d), want the post cap %d", len(got), maxMentionsPerComment, maxMentionsPerPost)
	}
	if got := commentMentionHandles("no mentions here, email a@"); len(got) != 0 {
		t.Fatalf("no handles expected, got %v", got)
	}
}

// fakeRelGraph is the GraphRelationships the block check reads.
type fakeRelGraph struct {
	rels map[string]ViewerRelationship
	err  error
	mu   sync.Mutex
	seen []string
}

func (g *fakeRelGraph) Following(context.Context, string) ([]string, error) { return nil, nil }

func (g *fakeRelGraph) RelationshipBatch(_ context.Context, viewerID string, targets []string) (map[string]ViewerRelationship, error) {
	g.mu.Lock()
	g.seen = append(g.seen, viewerID)
	g.mu.Unlock()
	if g.err != nil {
		return nil, g.err
	}
	out := map[string]ViewerRelationship{}
	for _, id := range targets {
		out[id] = g.rels[id] // zero value = no relationship, answered
	}
	return out, nil
}

// mentionRig is a Service wired to a fake user-service (handle → id), a fake
// graph-service (view_posts can + follow lookup) and a fake relationship graph.
type mentionRig struct {
	svc       *Service
	graph     *fakeRelGraph
	commenter uuid.UUID
	author    uuid.UUID
	users     map[string]uuid.UUID
	// cannotView is the set of users graph-service denies view_posts on the
	// author (a private account they do not follow, for example).
	cannotView map[uuid.UUID]bool
	// followers follow the author.
	followers map[uuid.UUID]bool
}

func newMentionRig(t *testing.T) *mentionRig {
	t.Helper()
	r := &mentionRig{
		graph:      &fakeRelGraph{rels: map[string]ViewerRelationship{}},
		commenter:  uuid.New(),
		author:     uuid.New(),
		users:      map[string]uuid.UUID{},
		cannotView: map[uuid.UUID]bool{},
		followers:  map[uuid.UUID]bool{},
	}
	users := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		handle := strings.TrimPrefix(req.URL.Path, "/v1/users/by-username/")
		id, ok := r.users[strings.ToLower(handle)]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": id.String(), "username": handle}})
	}))
	t.Cleanup(users.Close)
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/v1/internal/graph/can":
			var body canRequest
			_ = json.NewDecoder(req.Body).Decode(&body)
			viewer, _ := uuid.Parse(body.ViewerID)
			data := map[string]bool{}
			for _, id := range body.TargetIDs {
				data[id] = !r.cannotView[viewer]
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		case "/v1/graph/relationship":
			viewer, _ := uuid.Parse(req.URL.Query().Get("user_id"))
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"follows": r.followers[viewer]}})
		default:
			t.Errorf("unexpected graph call %s %s", req.Method, req.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(graph.Close)
	r.svc = &Service{
		userServiceURL:     users.URL,
		graphServiceURL:    graph.URL,
		internalServiceKey: "test-key",
		httpClient:         &http.Client{Timeout: 2 * time.Second},
		storyAudience:      NewStoryAudience(r.graph),
	}
	return r
}

func (r *mentionRig) user(handle string) uuid.UUID {
	id := uuid.New()
	r.users[strings.ToLower(handle)] = id
	return id
}

func (r *mentionRig) post(visibility string) *postgres.Post {
	return &postgres.Post{ID: uuid.New(), AuthorID: r.author, Visibility: visibility}
}

func TestCommentMentionRecipientsRules(t *testing.T) {
	r := newMentionRig(t)
	r.users["me"] = r.commenter
	sam := r.user("sam")
	blockedByMe := r.user("blockedbyme")
	blocksMe := r.user("blocksme")
	outsider := r.user("outsider")
	postAuthor := r.author
	r.users["author"] = postAuthor
	r.graph.rels[blockedByMe.String()] = ViewerRelationship{Blocked: true}
	r.graph.rels[blocksMe.String()] = ViewerRelationship{BlockedBy: true}
	r.cannotView[outsider] = true

	body := "@me @sam @Sam @blockedbyme @blocksme @outsider @ghost @author"
	got := r.svc.commentMentionRecipients(context.Background(), r.post("public"), r.commenter, body)
	want := []uuid.UUID{sam, postAuthor}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recipients = %v, want [sam author] %v", got, want)
	}
	if len(r.graph.seen) != 1 || r.graph.seen[0] != r.commenter.String() {
		t.Fatalf("block check must be one batch from the commenter's side, saw %v", r.graph.seen)
	}
}

func TestCommentMentionRecipientsRespectFollowersOnlyPost(t *testing.T) {
	r := newMentionRig(t)
	follower := r.user("fan")
	r.user("stranger")
	r.followers[follower] = true

	got := r.svc.commentMentionRecipients(context.Background(), r.post("followers"), r.commenter, "@fan @stranger")
	if !reflect.DeepEqual(got, []uuid.UUID{follower}) {
		t.Fatalf("followers-only post: recipients = %v, want only the follower %v", got, follower)
	}
	// A private post shared with nobody: no one but the author may open it.
	if got := r.svc.commentMentionRecipients(context.Background(), r.post("private"), r.commenter, "@fan @stranger"); len(got) != 0 {
		t.Fatalf("private post: recipients = %v, want none", got)
	}
}

func TestCommentMentionRecipientsTwoHandlesOneUser(t *testing.T) {
	r := newMentionRig(t)
	id := r.user("old.name")
	r.users["newname"] = id
	got := r.svc.commentMentionRecipients(context.Background(), r.post("public"), r.commenter, "@old.name @newname")
	if !reflect.DeepEqual(got, []uuid.UUID{id}) {
		t.Fatalf("recipients = %v, want one entry for %v", got, id)
	}
}

func TestCommentMentionRecipientsFailClosedOnGraph(t *testing.T) {
	r := newMentionRig(t)
	r.user("sam")
	r.graph.err = errors.New("graph down")
	if got := r.svc.commentMentionRecipients(context.Background(), r.post("public"), r.commenter, "@sam"); len(got) != 0 {
		t.Fatalf("graph outage: recipients = %v, want none", got)
	}
	// No graph wired at all is unresolved too, never "nobody is blocked".
	r.svc.storyAudience = nil
	if got := r.svc.commentMentionRecipients(context.Background(), r.post("public"), r.commenter, "@sam"); len(got) != 0 {
		t.Fatalf("no graph: recipients = %v, want none", got)
	}
}

type sentMention struct{ mentioned, commenter, post, comment uuid.UUID }

func TestPublishCommentMentionsSendsOneEventPerRecipient(t *testing.T) {
	r := newMentionRig(t)
	sam := r.user("sam")
	ana := r.user("ana")
	r.users["me"] = r.commenter
	post := r.post("public")
	comment := uuid.New()

	var mu sync.Mutex
	var sent []sentMention
	publish := func(_ context.Context, mentioned, commenter, postID, commentID uuid.UUID) error {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, sentMention{mentioned, commenter, postID, commentID})
		return nil
	}
	n := r.svc.publishCommentMentions(context.Background(), post, r.commenter, comment, "@sam @ana @sam @me", publish)
	want := []sentMention{{sam, r.commenter, post.ID, comment}, {ana, r.commenter, post.ID, comment}}
	if n != 2 || !reflect.DeepEqual(sent, want) {
		t.Fatalf("sent %d = %+v, want %+v", n, sent, want)
	}
}

func TestAnnounceCommentMentionsUsesSeamAndSkipsWithoutPublisher(t *testing.T) {
	r := newMentionRig(t)
	sam := r.user("sam")
	// No producer and no seam: nothing to publish with, and nothing panics.
	r.svc.announceCommentMentions(r.post("public"), r.commenter, uuid.New(), "@sam")

	done := make(chan uuid.UUID, 1)
	r.svc.commentMentionPublisher = func(_ context.Context, mentioned, _, _, _ uuid.UUID) error {
		done <- mentioned
		return nil
	}
	r.svc.announceCommentMentions(r.post("public"), r.commenter, uuid.New(), "hey @sam")
	select {
	case got := <-done:
		if got != sam {
			t.Fatalf("published for %v, want %v", got, sam)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no mention published")
	}
}
