package service

import (
	"context"
	"encoding/json"
	"errors"
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

// memFollowStore is an in-memory hashtagFollowStore with the store's
// contract (idempotent follow, the limit only on a NEW follow).
type memFollowStore struct {
	mu        sync.Mutex
	follows   map[uuid.UUID]map[string]time.Time
	blocked   map[string]bool
	posts     []postgres.Post
	maxSeen   []int
	followed  []string
	lastTags  []string
	lastSince time.Time
	lastUser  uuid.UUID
}

func newMemFollowStore() *memFollowStore {
	return &memFollowStore{follows: map[uuid.UUID]map[string]time.Time{}, blocked: map[string]bool{}}
}

func (m *memFollowStore) FollowHashtag(_ context.Context, userID uuid.UUID, tag string, max int) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maxSeen = append(m.maxSeen, max)
	m.followed = append(m.followed, tag)
	f := m.follows[userID]
	if f == nil {
		f = map[string]time.Time{}
		m.follows[userID] = f
	}
	if _, ok := f[tag]; ok {
		return false, nil
	}
	if len(f) >= max {
		return false, postgres.ErrHashtagFollowLimit
	}
	f[tag] = time.Now()
	return true, nil
}

func (m *memFollowStore) UnfollowHashtag(_ context.Context, userID uuid.UUID, tag string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.follows[userID][tag]
	delete(m.follows[userID], tag)
	return ok, nil
}

func (m *memFollowStore) IsFollowingHashtag(_ context.Context, userID uuid.UUID, tag string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.follows[userID][tag]
	return ok, nil
}

func (m *memFollowStore) ListFollowedHashtags(_ context.Context, userID uuid.UUID, _ int, _ string) ([]postgres.FollowedHashtag, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []postgres.FollowedHashtag{}
	for t, at := range m.follows[userID] {
		out = append(out, postgres.FollowedHashtag{Tag: t, FollowedAt: at})
	}
	return out, "", nil
}

func (m *memFollowStore) FollowedHashtagTags(_ context.Context, userID uuid.UUID) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var tags []string
	for t := range m.follows[userID] {
		tags = append(tags, t)
	}
	return tags, nil
}

func (m *memFollowStore) GetPostsByHashtags(_ context.Context, tags []string, viewerID uuid.UUID, _ int, _ string, since time.Time) ([]postgres.Post, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastTags, m.lastSince, m.lastUser = tags, since, viewerID
	out := make([]postgres.Post, len(m.posts))
	copy(out, m.posts)
	return out, "", nil
}

func (m *memFollowStore) GetBlockedHashtags(_ context.Context, tags []string) ([]string, error) {
	var out []string
	for _, t := range tags {
		if m.blocked[t] {
			out = append(out, t)
		}
	}
	return out, nil
}

// The followed tag is the form a post's hashtags are stored in, from either
// path a post takes (caption parser or the explicit field).
func TestNormalizeFollowTagMatchesPostHashtags(t *testing.T) {
	for _, raw := range []string{"#Cricket", "cricket", "CRICKET", "  #IPL_2026 ", "हिन्दी", "#Ünïcödé"} {
		got, err := NormalizeFollowTag(raw)
		if err != nil {
			t.Fatalf("NormalizeFollowTag(%q): %v", raw, err)
		}
		explicit, _ := NormalizeExplicitHashtags([]string{raw})
		if len(explicit) != 1 || explicit[0] != got {
			t.Errorf("%q: follow tag %q, post explicit field %v", raw, got, explicit)
		}
		caption := strings.TrimSpace(raw)
		if !strings.HasPrefix(caption, "#") {
			caption = "#" + caption
		}
		if parsed := extractHashtags("post " + caption); len(parsed) != 1 || parsed[0] != got {
			t.Errorf("%q: follow tag %q, caption parser %v", raw, got, parsed)
		}
	}
	for _, bad := range []string{"", "#", "two words", "semi;colon", strings.Repeat("a", 51), "#a#b"} {
		if _, err := NormalizeFollowTag(bad); !errors.Is(err, ErrInvalidHashtag) {
			t.Errorf("NormalizeFollowTag(%q) = %v, want ErrInvalidHashtag", bad, err)
		}
	}
}

func TestFollowHashtagIsIdempotentAndBounded(t *testing.T) {
	st := newMemFollowStore()
	s := &Service{hashtagFollows: st}
	ctx := context.Background()
	user := uuid.New()

	for _, raw := range []string{"#Cricket", "cricket", "CRICKET"} {
		got, err := s.FollowHashtag(ctx, user, raw)
		if err != nil || got.Tag != "cricket" || !got.Following || got.DisplayName != "#cricket" {
			t.Fatalf("follow %q: %+v %v", raw, got, err)
		}
	}
	if n := len(st.follows[user]); n != 1 {
		t.Fatalf("three follows of one tag made %d rows", n)
	}
	for _, max := range st.maxSeen {
		if max != 500 {
			t.Fatalf("store asked with max %d, want 500", max)
		}
	}
	if state, _ := s.HashtagFollowStateFor(ctx, user, "#CRICKET"); !state.Following {
		t.Fatal("state should be following")
	}
	for i := 0; i < 2; i++ {
		got, err := s.UnfollowHashtag(ctx, user, "#Cricket")
		if err != nil || got.Following || got.Tag != "cricket" {
			t.Fatalf("unfollow %d: %+v %v", i, got, err)
		}
	}
	if state, _ := s.HashtagFollowStateFor(ctx, user, "cricket"); state.Following {
		t.Fatal("state should be not following")
	}

	// A moderation-blocked tag cannot be followed, and the store never sees
	// the attempt; it can always be unfollowed.
	st.blocked["banned"] = true
	before := len(st.followed)
	if _, err := s.FollowHashtag(ctx, user, "#Banned"); !errors.Is(err, ErrHashtagUnavailable) {
		t.Fatalf("blocked: %v", err)
	}
	if len(st.followed) != before {
		t.Fatal("a blocked tag reached the store")
	}
	if _, err := s.UnfollowHashtag(ctx, user, "banned"); err != nil {
		t.Fatalf("unfollow blocked: %v", err)
	}
	// Invalid tags never reach the store either.
	if _, err := s.FollowHashtag(ctx, user, "two words"); !errors.Is(err, ErrInvalidHashtag) {
		t.Fatalf("invalid: %v", err)
	}
	if len(st.followed) != before {
		t.Fatal("an invalid tag reached the store")
	}
}

// The followed-posts read keeps the hashtag page's rules: authors the viewer
// may not see (a private account, a block either way) drop out, and a
// members-only post reaches a non-member redacted.
func TestFollowedHashtagPostsRespectBlocksAndMembersRedaction(t *testing.T) {
	viewer := uuid.New()
	open, private, blocker := uuid.New(), uuid.New(), uuid.New()
	tier := uuid.New()
	denied := map[uuid.UUID]bool{private: true, blocker: true}
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body canRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		data := map[string]bool{}
		for _, raw := range body.TargetIDs {
			id, _ := uuid.Parse(raw)
			data[raw] = body.Action == "view_posts" && !denied[id]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer graph.Close()

	st := newMemFollowStore()
	st.follows[viewer] = map[string]time.Time{"cricket": time.Now(), "ipl": time.Now()}
	gatedID := uuid.New()
	st.posts = []postgres.Post{
		{ID: uuid.New(), AuthorID: open, Text: "open post #cricket", Hashtags: []string{"cricket"}, Visibility: "public"},
		{ID: uuid.New(), AuthorID: private, Text: "private account #cricket", Hashtags: []string{"cricket"}, Visibility: "public"},
		{ID: uuid.New(), AuthorID: blocker, Text: "blocked #ipl", Hashtags: []string{"ipl"}, Visibility: "public"},
		{ID: gatedID, AuthorID: open, Text: "members only body #ipl", Hashtags: []string{"ipl"}, Visibility: "public", TierRequiredID: &tier},
	}
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	s := &Service{
		hashtagFollows:        st,
		graphServiceURL:       graph.URL,
		httpClient:            &http.Client{Timeout: 2 * time.Second},
		now:                   func() time.Time { return now },
		followedPostsDecorate: func(_ context.Context, d []PostDetail) ([]PostDetail, error) { return d, nil },
		creatorTiersSeam: func(context.Context, uuid.UUID) ([]CreatorTierRef, error) {
			return []CreatorTierRef{{ID: tier, CreatorID: open, PricePaise: 4900, IsActive: true}}, nil
		},
	}

	got, _, err := s.FollowedHashtagPosts(context.Background(), viewer, 20, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d posts, want the open author's two", len(got))
	}
	for _, d := range got {
		if d.Post.AuthorID != open {
			t.Fatalf("a post by an author the viewer may not see leaked: %v", d.Post.AuthorID)
		}
		if d.Post.ID == gatedID {
			if !d.Post.BodyRedacted || strings.Contains(d.Post.Text, "members only body") {
				t.Fatalf("the members-only body reached a non-member: redacted=%v text=%q", d.Post.BodyRedacted, d.Post.Text)
			}
		} else if d.Post.BodyRedacted {
			t.Fatal("an open post was redacted")
		}
	}
	tags := append([]string(nil), st.lastTags...)
	if len(tags) != 2 || st.lastUser != viewer || !st.lastSince.Equal(now.Add(-followedPostsWindow)) {
		t.Fatalf("store asked tags=%v user=%v since=%v", tags, st.lastUser, st.lastSince)
	}

	// No followed tags: an empty page, and the posts are never read.
	st.lastTags = nil
	empty, _, err := s.FollowedHashtagPosts(context.Background(), uuid.New(), 20, "")
	if err != nil || len(empty) != 0 || st.lastTags != nil {
		t.Fatalf("no follows: %v %v asked=%v", empty, err, st.lastTags)
	}
	if !reflect.DeepEqual(empty, []PostDetail{}) {
		t.Fatalf("no follows must be [] not null: %#v", empty)
	}
}
