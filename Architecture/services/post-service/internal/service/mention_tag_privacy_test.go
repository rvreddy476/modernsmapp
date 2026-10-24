package service

import (
	"context"
	"encoding/json"
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

// "Who can mention or tag me" (2026-10-24, mention_tag_privacy.go). A
// mention / tag of someone whose who_can_mention / who_can_tag does not
// allow the author is dropped silently: no error, the text kept as written,
// nothing stored, nothing notified.

// privacyWorld is a fake user-service (username -> id) plus a fake
// graph-service internal gate that resolves mention / tag the way
// graph-service does: everyone allows, following needs the TARGET to follow
// the author, no_one and unknown deny, a block denies everything.
type privacyWorld struct {
	mu        sync.Mutex
	users     map[string]uuid.UUID
	failUser  map[string]bool
	mention   map[uuid.UUID]string
	tag       map[uuid.UUID]string
	followsMe map[uuid.UUID]bool // target follows the author
	blocked   map[uuid.UUID]bool
	graphDown bool
	asked     map[string]int // action -> calls
}

func newPrivacyWorld(t *testing.T) (*privacyWorld, string, string) {
	t.Helper()
	w := &privacyWorld{
		users: map[string]uuid.UUID{}, failUser: map[string]bool{},
		mention: map[uuid.UUID]string{}, tag: map[uuid.UUID]string{},
		followsMe: map[uuid.UUID]bool{}, blocked: map[uuid.UUID]bool{},
		asked: map[string]int{},
	}
	users := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		name := strings.ToLower(strings.TrimPrefix(req.URL.Path, "/v1/users/by-username/"))
		w.mu.Lock()
		id, ok := w.users[name]
		fail := w.failUser[name]
		w.mu.Unlock()
		switch {
		case fail:
			rw.WriteHeader(http.StatusInternalServerError)
		case !ok:
			rw.WriteHeader(http.StatusNotFound)
		default:
			_ = json.NewEncoder(rw).Encode(map[string]any{"data": map[string]any{"id": id.String(), "username": name}})
		}
	}))
	t.Cleanup(users.Close)
	graph := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/internal/graph/can" {
			rw.WriteHeader(http.StatusNotFound)
			return
		}
		var body canRequest
		_ = json.NewDecoder(req.Body).Decode(&body)
		w.mu.Lock()
		defer w.mu.Unlock()
		w.asked[body.Action]++
		if w.graphDown {
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}
		data := map[string]bool{}
		for _, raw := range body.TargetIDs {
			id, _ := uuid.Parse(raw)
			var setting string
			switch body.Action {
			case "mention":
				setting = w.mention[id]
			case "tag":
				setting = w.tag[id]
			default:
				data[raw] = true // view_posts / comment: not under test here
				continue
			}
			allowed := false
			switch setting {
			case "everyone":
				allowed = true
			case "following":
				allowed = w.followsMe[id]
			}
			data[raw] = allowed && !w.blocked[id]
		}
		_ = json.NewEncoder(rw).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(graph.Close)
	return w, users.URL, graph.URL
}

// person registers a username with its two settings.
func (w *privacyWorld) person(name, mention, tag string) uuid.UUID {
	id := uuid.New()
	w.users[strings.ToLower(name)] = id
	w.mention[id] = mention
	w.tag[id] = tag
	return id
}

func wireWorld(s *Service, usersURL, graphURL string) {
	s.userServiceURL = usersURL
	s.graphServiceURL = graphURL
	s.internalServiceKey = "test-key"
	s.httpClient = &http.Client{Timeout: 2 * time.Second}
}

// The cast every test below uses: one person per setting value, including
// "following" both ways round.
type cast struct {
	open, fan, stranger, closed, blockedOpen uuid.UUID
}

func castOf(w *privacyWorld) cast {
	c := cast{
		open:        w.person("open", "everyone", "everyone"),
		fan:         w.person("fan", "following", "following"),       // follows the author (below)
		stranger:    w.person("stranger", "following", "following"),  // does not follow the author
		closed:      w.person("closed", "no_one", "no_one"),          // nobody may
		blockedOpen: w.person("blockedopen", "everyone", "everyone"), // everyone, but a block
	}
	w.followsMe[c.fan] = true // fan's "following" therefore admits the author
	w.blocked[c.blockedOpen] = true
	return c
}

func TestCreatePostDropsDisallowedMentionsAndTagsSilently(t *testing.T) {
	r := newCreateRig(t)
	w, usersURL, graphURL := newPrivacyWorld(t)
	wireWorld(r.svc, usersURL, graphURL)
	c := castOf(w)

	var mu sync.Mutex
	notified := []uuid.UUID{}
	r.svc.postMentionPublisher = func(_ context.Context, mentioned, author uuid.UUID, _ string) error {
		mu.Lock()
		defer mu.Unlock()
		notified = append(notified, mentioned)
		return nil
	}

	text := "hi @open @fan @stranger @closed @blockedopen @ghost"
	in := r.input("post", nil)
	in.Text = text
	in.Mentions = []string{"open", "closed"}
	in.TaggedUserIDs = []uuid.UUID{c.open, c.fan, c.stranger, c.closed, c.blockedOpen}

	p, err := r.svc.CreatePost(context.Background(), in)
	if err != nil {
		t.Fatalf("a disallowed mention / tag must never fail the create: %v", err)
	}
	if len(r.written) != 1 {
		t.Fatalf("writes = %d", len(r.written))
	}
	row := r.written[0]
	if row.Text != text || p.Text != text {
		t.Fatalf("the text must be kept as written: %q", row.Text)
	}
	// ghost names no account: plain text that links to nobody, kept.
	if want := []string{"open", "fan", "ghost"}; !reflect.DeepEqual(row.MentionUsernames, want) {
		t.Fatalf("stored mentions = %v, want %v", row.MentionUsernames, want)
	}
	if want := []uuid.UUID{c.open, c.fan}; !reflect.DeepEqual(row.TaggedUserIDs, want) {
		t.Fatalf("stored tags = %v, want %v", row.TaggedUserIDs, want)
	}
	if !reflect.DeepEqual(p.MentionUsernames, row.MentionUsernames) || !reflect.DeepEqual(p.TaggedUserIDs, row.TaggedUserIDs) {
		t.Fatalf("the create response must carry the filtered lists: %v %v", p.MentionUsernames, p.TaggedUserIDs)
	}

	// Notifications: exactly the allowed, found people.
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(notified)
		mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond) // nothing else may arrive
	mu.Lock()
	defer mu.Unlock()
	got := map[uuid.UUID]bool{}
	for _, id := range notified {
		got[id] = true
	}
	if len(notified) != 2 || !got[c.open] || !got[c.fan] {
		t.Fatalf("notified %v, want exactly open %v and fan %v", notified, c.open, c.fan)
	}
}

// Each value on its own, both settings, and the following relation the
// right way round.
func TestAllowedMentionsAndTagsPerSetting(t *testing.T) {
	w, usersURL, graphURL := newPrivacyWorld(t)
	s := &Service{}
	wireWorld(s, usersURL, graphURL)
	author := uuid.New()
	w.users["me"] = author
	c := castOf(w)
	ctx := context.Background()

	cases := []struct {
		name string
		id   uuid.UUID
		want bool
	}{
		{"open", c.open, true},
		{"fan", c.fan, true},
		{"stranger", c.stranger, false},
		{"closed", c.closed, false},
		{"blockedopen", c.blockedOpen, false},
	}
	for _, tc := range cases {
		gotM := s.allowedMentions(ctx, author, []string{tc.name})
		if (len(gotM) == 1) != tc.want {
			t.Errorf("mention @%s: kept=%v, want %v", tc.name, gotM, tc.want)
		}
		gotT := s.allowedTags(ctx, author, []uuid.UUID{tc.id})
		if (len(gotT) == 1) != tc.want {
			t.Errorf("tag %s: kept=%v, want %v", tc.name, gotT, tc.want)
		}
	}
	// The two settings are independent.
	split := w.person("split", "no_one", "everyone")
	if got := s.allowedMentions(ctx, author, []string{"split"}); len(got) != 0 {
		t.Errorf("split: mention kept %v under who_can_mention=no_one", got)
	}
	if got := s.allowedTags(ctx, author, []uuid.UUID{split}); len(got) != 1 {
		t.Errorf("split: tag dropped %v under who_can_tag=everyone", got)
	}
	// A self-mention always stays (and is never sent to the gate).
	if got := s.allowedMentions(ctx, author, []string{"me"}); !reflect.DeepEqual(got, []string{"me"}) {
		t.Errorf("self-mention: %v", got)
	}
}

// Fail closed: graph-service down drops every resolved mention and every
// tag; a username user-service failed on is dropped; an unknown one stays.
func TestAllowedMentionsAndTagsFailClosed(t *testing.T) {
	w, usersURL, graphURL := newPrivacyWorld(t)
	s := &Service{}
	wireWorld(s, usersURL, graphURL)
	author := uuid.New()
	c := castOf(w)
	ctx := context.Background()

	w.graphDown = true
	if got := s.allowedMentions(ctx, author, []string{"open", "ghost"}); !reflect.DeepEqual(got, []string{"ghost"}) {
		t.Fatalf("graph down: mentions = %v, want only the unknown handle", got)
	}
	if got := s.allowedTags(ctx, author, []uuid.UUID{c.open}); len(got) != 0 {
		t.Fatalf("graph down: tags = %v, want none", got)
	}
	w.graphDown = false
	w.failUser["open"] = true
	if got := s.allowedMentions(ctx, author, []string{"open", "fan"}); !reflect.DeepEqual(got, []string{"fan"}) {
		t.Fatalf("user-service failure: mentions = %v, want [fan]", got)
	}
}

// Notification time re-checks the setting: a scheduled post's stored list
// predates a change, so the fan-out asks again and skips who now refuses.
func TestPublishPostMentionsRechecksAndNotifiesOnlyAllowed(t *testing.T) {
	w, usersURL, graphURL := newPrivacyWorld(t)
	var sent []uuid.UUID
	s := &Service{postMentionPublisher: func(_ context.Context, mentioned, _ uuid.UUID, _ string) error {
		sent = append(sent, mentioned)
		return nil
	}}
	wireWorld(s, usersURL, graphURL)
	author := uuid.New()
	w.users["me"] = author
	c := castOf(w)
	// open switched mentions off after the post was scheduled.
	w.mention[c.open] = "no_one"
	w.users["fan.old"] = c.fan // a second handle for the same account

	n := s.publishPostMentions(context.Background(), uuid.New(), author,
		[]string{"open", "fan", "fan.old", "stranger", "closed", "blockedopen", "ghost", "me"})
	if n != 1 || !reflect.DeepEqual(sent, []uuid.UUID{c.fan}) {
		t.Fatalf("sent %d %v, want exactly fan %v once", n, sent, c.fan)
	}
}

// The owner edit applies the same drop to the re-derived mention list and
// the replaced tag list, and never errors over it.
func TestPostEditDropsDisallowedMentionsAndTags(t *testing.T) {
	w, usersURL, graphURL := newPrivacyWorld(t)
	s := &Service{}
	wireWorld(s, usersURL, graphURL)
	author := uuid.New()
	c := castOf(w)
	current := &postgres.Post{ID: uuid.New(), AuthorID: author, ContentType: "post",
		Text: "old", MentionUsernames: []string{"open", "closed"}}

	text := "now with @fan @stranger"
	tags := []uuid.UUID{c.closed, c.open, c.stranger, c.fan}
	patch, err := s.buildPostEditPatch(context.Background(), current, PostEditInput{Text: &text, TaggedUserIDs: &tags})
	if err != nil {
		t.Fatalf("edit refused: %v", err)
	}
	// The stored "closed" (allowed once, now no_one) leaves on this edit.
	if patch.MentionUsernames == nil || !reflect.DeepEqual(*patch.MentionUsernames, []string{"fan", "open"}) {
		t.Fatalf("mentions patch = %v, want [fan open]", patch.MentionUsernames)
	}
	if patch.TaggedUserIDs == nil || !reflect.DeepEqual(*patch.TaggedUserIDs, []uuid.UUID{c.open, c.fan}) {
		t.Fatalf("tags patch = %v, want [open fan]", patch.TaggedUserIDs)
	}

	// An explicit mentions list replaces the stored one.
	explicit := []string{"@stranger", "open"}
	patch, err = s.buildPostEditPatch(context.Background(), current, PostEditInput{Mentions: &explicit})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*patch.MentionUsernames, []string{"open"}) || patch.TaggedUserIDs != nil {
		t.Fatalf("explicit mentions: %v tags=%v", *patch.MentionUsernames, patch.TaggedUserIDs)
	}

	// An edit that touches neither leaves both alone.
	title := "t"
	patch, err = s.buildPostEditPatch(context.Background(), current, PostEditInput{Title: &title})
	if err != nil || patch.MentionUsernames != nil || patch.TaggedUserIDs != nil {
		t.Fatalf("untouched edit set mentions/tags: %v %v %v", err, patch.MentionUsernames, patch.TaggedUserIDs)
	}
	// The create rules still apply: too many tags is a 422, not a drop.
	many := make([]uuid.UUID, MaxTaggedUsers+1)
	for i := range many {
		many[i] = uuid.New()
	}
	if _, err := s.buildPostEditPatch(context.Background(), current, PostEditInput{TaggedUserIDs: &many}); err == nil {
		t.Fatal("more than MaxTaggedUsers tags must be refused")
	}
}
