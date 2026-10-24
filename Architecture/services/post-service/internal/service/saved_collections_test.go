package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// memSavedStore is an in-memory savedCollectionStore with the Postgres
// store's contract: every read and write scoped to the user, names unique
// per user case-insensitively, the limit checked before an insert, delete
// moving items back to the default. The SQL itself is pinned by
// saved_collections_integration_test.go (store package).
type memSavedStore struct {
	mu          sync.Mutex
	collections map[uuid.UUID]*postgres.SavedCollectionRow
	items       map[uuid.UUID]*postgres.SavedItem
	maxSeen     []int
	creates     []string
	renames     []string
}

func newMemSavedStore() *memSavedStore {
	return &memSavedStore{collections: map[uuid.UUID]*postgres.SavedCollectionRow{}, items: map[uuid.UUID]*postgres.SavedItem{}}
}

func (m *memSavedStore) own(userID, id uuid.UUID) (*postgres.SavedCollectionRow, error) {
	c, ok := m.collections[id]
	if !ok || c.UserID != userID {
		return nil, postgres.ErrCollectionNotFound
	}
	return c, nil
}

func (m *memSavedStore) count(c *postgres.SavedCollectionRow) int64 {
	var n int64
	for _, it := range m.items {
		if it.UserID == c.UserID && it.CollectionID != nil && *it.CollectionID == c.ID {
			n++
		}
	}
	return n
}

func (m *memSavedStore) ListSavedCollections(_ context.Context, userID uuid.UUID) (int64, []postgres.SavedCollectionRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var def int64
	for _, it := range m.items {
		if it.UserID == userID && it.CollectionID == nil {
			def++
		}
	}
	out := []postgres.SavedCollectionRow{}
	for _, c := range m.collections {
		if c.UserID == userID {
			cp := *c
			cp.Count = m.count(c)
			out = append(out, cp)
		}
	}
	for i := 1; i < len(out); i++ { // oldest first
		for j := i; j > 0 && out[j].CreatedAt.Before(out[j-1].CreatedAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return def, out, nil
}

func (m *memSavedStore) GetSavedCollection(_ context.Context, userID, id uuid.UUID) (*postgres.SavedCollectionRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.own(userID, id)
	if err != nil {
		return nil, err
	}
	cp := *c
	cp.Count = m.count(c)
	return &cp, nil
}

func (m *memSavedStore) FindSavedCollectionByName(_ context.Context, userID uuid.UUID, name string) (*postgres.SavedCollectionRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.collections {
		if c.UserID == userID && strings.EqualFold(c.Name, name) {
			cp := *c
			return &cp, nil
		}
	}
	return nil, postgres.ErrCollectionNotFound
}

func (m *memSavedStore) CreateSavedCollection(_ context.Context, userID uuid.UUID, name string, max int) (*postgres.SavedCollectionRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maxSeen = append(m.maxSeen, max)
	m.creates = append(m.creates, name)
	n := 0
	for _, c := range m.collections {
		if c.UserID == userID {
			n++
			if strings.EqualFold(c.Name, name) {
				return nil, postgres.ErrCollectionNameTaken
			}
		}
	}
	if n >= max {
		return nil, postgres.ErrCollectionLimit
	}
	now := time.Now().Add(time.Duration(len(m.collections)) * time.Millisecond)
	c := &postgres.SavedCollectionRow{ID: uuid.New(), UserID: userID, Name: name, CreatedAt: now, UpdatedAt: now}
	m.collections[c.ID] = c
	cp := *c
	return &cp, nil
}

func (m *memSavedStore) RenameSavedCollection(_ context.Context, userID, id uuid.UUID, name string) (*postgres.SavedCollectionRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.renames = append(m.renames, name)
	c, err := m.own(userID, id)
	if err != nil {
		return nil, err
	}
	for _, o := range m.collections {
		if o.UserID == userID && o.ID != id && strings.EqualFold(o.Name, name) {
			return nil, postgres.ErrCollectionNameTaken
		}
	}
	c.Name = name
	for _, it := range m.items {
		if it.CollectionID != nil && *it.CollectionID == id {
			it.CollectionName = name
		}
	}
	cp := *c
	return &cp, nil
}

func (m *memSavedStore) DeleteSavedCollection(_ context.Context, userID, id uuid.UUID) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.own(userID, id); err != nil {
		return 0, err
	}
	var moved int64
	for _, it := range m.items {
		if it.UserID == userID && it.CollectionID != nil && *it.CollectionID == id {
			it.CollectionID, it.CollectionName = nil, postgres.DefaultCollectionName
			moved++
		}
	}
	delete(m.collections, id)
	return moved, nil
}

func (m *memSavedStore) place(userID uuid.UUID, it *postgres.SavedItem, collectionID *uuid.UUID) error {
	if collectionID == nil {
		it.CollectionID, it.CollectionName = nil, postgres.DefaultCollectionName
		return nil
	}
	c, err := m.own(userID, *collectionID)
	if err != nil {
		return err
	}
	id := c.ID
	it.CollectionID, it.CollectionName = &id, c.Name
	return nil
}

func (m *memSavedStore) MoveSavedItem(_ context.Context, userID, savedID uuid.UUID, collectionID *uuid.UUID) (*postgres.SavedItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if collectionID != nil {
		if _, err := m.own(userID, *collectionID); err != nil {
			return nil, err
		}
	}
	it, ok := m.items[savedID]
	if !ok || it.UserID != userID {
		return nil, postgres.ErrSavedItemNotFound
	}
	if err := m.place(userID, it, collectionID); err != nil {
		return nil, err
	}
	cp := *it
	return &cp, nil
}

func (m *memSavedStore) MoveSavedTarget(ctx context.Context, userID uuid.UUID, targetType string, targetID uuid.UUID, collectionID *uuid.UUID) (*postgres.SavedItem, error) {
	m.mu.Lock()
	var found uuid.UUID
	for id, it := range m.items {
		if it.UserID == userID && it.TargetType == targetType && it.TargetID == targetID {
			found = id
		}
	}
	m.mu.Unlock()
	if found == uuid.Nil {
		if collectionID != nil {
			if _, err := m.GetSavedCollection(ctx, userID, *collectionID); err != nil {
				return nil, err
			}
		}
		return nil, postgres.ErrSavedItemNotFound
	}
	return m.MoveSavedItem(ctx, userID, found, collectionID)
}

func (m *memSavedStore) SaveItem(_ context.Context, userID uuid.UUID, targetType string, targetID uuid.UUID, collectionID *uuid.UUID) (*postgres.SavedItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if collectionID != nil {
		if _, err := m.own(userID, *collectionID); err != nil {
			return nil, err
		}
	}
	for _, it := range m.items {
		if it.UserID == userID && it.TargetType == targetType && it.TargetID == targetID {
			if collectionID != nil {
				_ = m.place(userID, it, collectionID)
			}
			cp := *it
			return &cp, nil
		}
	}
	it := &postgres.SavedItem{ID: uuid.New(), UserID: userID, TargetType: targetType, TargetID: targetID, CreatedAt: time.Now()}
	_ = m.place(userID, it, collectionID)
	m.items[it.ID] = it
	cp := *it
	return &cp, nil
}

func (m *memSavedStore) ListSavedItems(_ context.Context, userID uuid.UUID, scope postgres.SavedScope, _ int, _ string) ([]postgres.SavedItem, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []postgres.SavedItem{}
	for _, it := range m.items {
		if it.UserID != userID {
			continue
		}
		switch {
		case scope.DefaultOnly && it.CollectionID != nil:
			continue
		case scope.CollectionID != nil && (it.CollectionID == nil || *it.CollectionID != *scope.CollectionID):
			continue
		}
		out = append(out, *it)
	}
	return out, "", nil
}

func savedSvc() (*Service, *memSavedStore) {
	st := newMemSavedStore()
	return &Service{savedCollections: st}, st
}

func TestNormalizeCollectionName(t *testing.T) {
	ok := map[string]string{
		"Trips":                 "Trips",
		"  Weekend   recipes  ": "Weekend recipes",
		strings.Repeat("a", 40): strings.Repeat("a", 40),
		strings.Repeat("é", 40): strings.Repeat("é", 40), // characters, not bytes
		"Ideas\tfor\nlater":     "Ideas for later",
		"All Saved stuff":       "All Saved stuff",
		"नमस्ते":                "नमस्ते",
	}
	for in, want := range ok {
		got, err := NormalizeCollectionName(in)
		if err != nil || got != want {
			t.Errorf("NormalizeCollectionName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "   ", strings.Repeat("a", 41), strings.Repeat("é", 41),
		"All Saved", "all saved", "  ALL   SAVED ", "default", "Default", "bad\x00name", "bad\x7fname", "\xff"} {
		if _, err := NormalizeCollectionName(bad); !errors.Is(err, ErrInvalidCollectionName) {
			t.Errorf("NormalizeCollectionName(%q) = %v; want ErrInvalidCollectionName", bad, err)
		}
	}
}

func TestCreateSavedCollectionLimitAndName(t *testing.T) {
	s, st := savedSvc()
	ctx := context.Background()
	user := uuid.New()
	for i := 0; i < MaxSavedCollections; i++ {
		if _, err := s.CreateSavedCollection(ctx, user, "c"+strings.Repeat("x", i%30)+string(rune('A'+i%26))+uuid.NewString()[:4]); err != nil {
			t.Fatalf("collection %d: %v", i+1, err)
		}
	}
	if _, err := s.CreateSavedCollection(ctx, user, "one too many"); !errors.Is(err, ErrCollectionLimit) {
		t.Fatalf("collection 51: %v, want ErrCollectionLimit", err)
	}
	for _, max := range st.maxSeen {
		if max != 50 {
			t.Fatalf("the store was asked with max %d, want 50", max)
		}
	}
	// Another user is unaffected by the first one's 50.
	if _, err := s.CreateSavedCollection(ctx, uuid.New(), "Trips"); err != nil {
		t.Fatalf("another user's first collection: %v", err)
	}
	// An invalid name never reaches the store; the stored name is trimmed.
	before := len(st.creates)
	if _, err := s.CreateSavedCollection(ctx, uuid.New(), strings.Repeat("n", 41)); !errors.Is(err, ErrInvalidCollectionName) {
		t.Fatalf("41 characters: %v", err)
	}
	if _, err := s.CreateSavedCollection(ctx, uuid.New(), "All Saved"); !errors.Is(err, ErrInvalidCollectionName) {
		t.Fatalf("reserved name: %v", err)
	}
	if len(st.creates) != before {
		t.Fatal("an invalid name reached the store")
	}
	got, err := s.CreateSavedCollection(ctx, uuid.New(), "  Road   trip ")
	if err != nil || got.Name != "Road trip" || got.ID == nil || got.IsDefault {
		t.Fatalf("created %+v, %v", got, err)
	}
}

func TestSavedCollectionsAreOwnerOnly(t *testing.T) {
	s, _ := savedSvc()
	ctx := context.Background()
	alice, bob := uuid.New(), uuid.New()
	trip, err := s.CreateSavedCollection(ctx, alice, "Trips")
	if err != nil {
		t.Fatal(err)
	}
	post := uuid.New()
	if _, err := s.SaveItem(ctx, alice, "post", post, trip.ID, ""); err != nil {
		t.Fatal(err)
	}
	bobItem, err := s.SaveItem(ctx, bob, "post", uuid.New(), nil, "")
	if err != nil {
		t.Fatal(err)
	}

	// Bob cannot see, rename, delete, fill or filter by Alice's collection.
	list, _ := s.ListSavedCollections(ctx, bob)
	if len(list) != 1 || !list[0].IsDefault {
		t.Fatalf("bob sees %+v, want only his default", list)
	}
	if _, err := s.RenameSavedCollection(ctx, bob, *trip.ID, "Mine now"); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("rename: %v", err)
	}
	if _, err := s.DeleteSavedCollection(ctx, bob, *trip.ID); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.MoveSavedItem(ctx, bob, bobItem.ID, trip.ID); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("move into another's collection: %v", err)
	}
	if _, err := s.SaveItem(ctx, bob, "post", uuid.New(), trip.ID, ""); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("save into another's collection: %v", err)
	}
	if _, err := s.ResolveSavedScope(ctx, bob, trip.ID.String()); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("filter by another's collection: %v", err)
	}
	// Nor move Alice's saved item.
	aliceItems, _, _ := s.ListSavedItems(ctx, alice, postgres.SavedScope{}, 20, "")
	if _, err := s.MoveSavedItem(ctx, bob, aliceItems[0].ID, nil); !errors.Is(err, ErrSavedItemNotFound) {
		t.Fatalf("move another's item: %v", err)
	}
	// Alice still has everything.
	list, _ = s.ListSavedCollections(ctx, alice)
	if len(list) != 2 || list[1].Name != "Trips" || list[1].Count != 1 {
		t.Fatalf("alice: %+v", list)
	}
}

func TestMoveAndDeleteFallBackToDefault(t *testing.T) {
	s, _ := savedSvc()
	ctx := context.Background()
	user := uuid.New()
	trips, _ := s.CreateSavedCollection(ctx, user, "Trips")
	food, _ := s.CreateSavedCollection(ctx, user, "Food")
	a, _ := s.SaveItem(ctx, user, "post", uuid.New(), nil, "")
	b, _ := s.SaveItem(ctx, user, "post", uuid.New(), nil, "")
	c, _ := s.SaveItem(ctx, user, "post", uuid.New(), nil, "")

	if it, err := s.MoveSavedItem(ctx, user, a.ID, trips.ID); err != nil || it.CollectionID == nil || *it.CollectionID != *trips.ID || it.CollectionName != "Trips" {
		t.Fatalf("move a: %+v %v", it, err)
	}
	if _, err := s.MoveBookmark(ctx, user, b.TargetID, trips.ID); err != nil {
		t.Fatalf("move b by post: %v", err)
	}
	if _, err := s.MoveSavedItem(ctx, user, c.ID, food.ID); err != nil {
		t.Fatal(err)
	}
	// Back to the default with nil.
	if it, err := s.MoveSavedItem(ctx, user, c.ID, nil); err != nil || it.CollectionID != nil || it.CollectionName != "All Saved" {
		t.Fatalf("move c home: %+v %v", it, err)
	}
	// A plain re-save keeps a filed item where it is.
	if it, _ := s.SaveItem(ctx, user, "post", a.TargetID, nil, ""); it.CollectionID == nil || *it.CollectionID != *trips.ID {
		t.Fatalf("re-save pulled the item out of its collection: %+v", it)
	}
	moved, err := s.DeleteSavedCollection(ctx, user, *trips.ID)
	if err != nil || moved != 2 {
		t.Fatalf("delete: moved=%d %v, want 2", moved, err)
	}
	list, _ := s.ListSavedCollections(ctx, user)
	if len(list) != 2 || !list[0].IsDefault || list[0].Count != 3 || list[1].Name != "Food" || list[1].Count != 0 {
		t.Fatalf("after delete: %+v", list)
	}
	def, _ := s.ResolveSavedScope(ctx, user, "default")
	items, _, _ := s.ListSavedItems(ctx, user, def, 20, "")
	if len(items) != 3 {
		t.Fatalf("default holds %d items, want all 3", len(items))
	}
	// Not saved: 404 on the post route.
	if _, err := s.MoveBookmark(ctx, user, uuid.New(), food.ID); !errors.Is(err, ErrSavedItemNotFound) {
		t.Fatalf("move unsaved post: %v", err)
	}
}

func TestLegacyCollectionNameResolvesUnderTheRules(t *testing.T) {
	s, st := savedSvc()
	ctx := context.Background()
	user := uuid.New()
	trips, _ := s.CreateSavedCollection(ctx, user, "Trips")

	it, err := s.SaveItem(ctx, user, "post", uuid.New(), nil, "  trips ")
	if err != nil || it.CollectionID == nil || *it.CollectionID != *trips.ID {
		t.Fatalf("existing name (case-insensitive): %+v %v", it, err)
	}
	for _, def := range []string{"", "All Saved", "all saved", "default"} {
		it, err := s.SaveItem(ctx, user, "post", uuid.New(), nil, def)
		if err != nil || it.CollectionID != nil {
			t.Fatalf("legacy %q: %+v %v, want the default", def, it, err)
		}
	}
	it, err = s.SaveItem(ctx, user, "post", uuid.New(), nil, "Brand new")
	if err != nil || it.CollectionID == nil || it.CollectionName != "Brand new" {
		t.Fatalf("new legacy name creates a collection: %+v %v", it, err)
	}
	if st.maxSeen[len(st.maxSeen)-1] != MaxSavedCollections {
		t.Fatal("a legacy-name create skipped the limit")
	}
	if _, err := s.SaveItem(ctx, user, "post", uuid.New(), nil, strings.Repeat("z", 41)); !errors.Is(err, ErrInvalidCollectionName) {
		t.Fatalf("legacy name past 40 characters: %v", err)
	}
	// collection_id wins over a legacy name.
	it, _ = s.SaveItem(ctx, user, "post", uuid.New(), trips.ID, "Something else")
	if it.CollectionID == nil || *it.CollectionID != *trips.ID {
		t.Fatalf("collection_id must win: %+v", it)
	}
}

func TestResolveSavedScope(t *testing.T) {
	s, _ := savedSvc()
	ctx := context.Background()
	user := uuid.New()
	trips, _ := s.CreateSavedCollection(ctx, user, "Trips")
	if sc, err := s.ResolveSavedScope(ctx, user, ""); err != nil || sc.DefaultOnly || sc.CollectionID != nil {
		t.Fatalf("empty: %+v %v", sc, err)
	}
	if sc, err := s.ResolveSavedScope(ctx, user, "DEFAULT"); err != nil || !sc.DefaultOnly {
		t.Fatalf("default: %+v %v", sc, err)
	}
	if sc, err := s.ResolveSavedScope(ctx, user, trips.ID.String()); err != nil || sc.CollectionID == nil || *sc.CollectionID != *trips.ID {
		t.Fatalf("own: %+v %v", sc, err)
	}
	if _, err := s.ResolveSavedScope(ctx, user, "nope"); !errors.Is(err, ErrInvalidCollectionID) {
		t.Fatalf("junk: %v", err)
	}
	if _, err := s.ResolveSavedScope(ctx, user, uuid.NewString()); !errors.Is(err, ErrCollectionNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestRenameValidatesAndDefaultComesFirst(t *testing.T) {
	s, st := savedSvc()
	ctx := context.Background()
	user := uuid.New()
	a, _ := s.CreateSavedCollection(ctx, user, "Alpha")
	b, _ := s.CreateSavedCollection(ctx, user, "Beta")
	if _, err := s.RenameSavedCollection(ctx, user, *a.ID, "BETA"); !errors.Is(err, ErrCollectionNameTaken) {
		t.Fatalf("rename onto a taken name (case-insensitive): %v", err)
	}
	before := len(st.renames)
	if _, err := s.RenameSavedCollection(ctx, user, *a.ID, "default"); !errors.Is(err, ErrInvalidCollectionName) {
		t.Fatalf("rename to the reserved name: %v", err)
	}
	if len(st.renames) != before {
		t.Fatal("an invalid rename reached the store")
	}
	if got, err := s.RenameSavedCollection(ctx, user, *a.ID, " alpha  two "); err != nil || got.Name != "alpha two" {
		t.Fatalf("rename: %+v %v", got, err)
	}
	// Renaming to its own name in another case is allowed.
	if _, err := s.RenameSavedCollection(ctx, user, *b.ID, "beta"); err != nil {
		t.Fatalf("case change of own name: %v", err)
	}
	list, _ := s.ListSavedCollections(ctx, user)
	if len(list) != 3 || !list[0].IsDefault || list[0].ID != nil || list[0].Name != "All Saved" || list[1].Name != "alpha two" {
		t.Fatalf("list: %+v", list)
	}
}

// Saving straight into a collection passes the same read gate as the plain
// bookmark: a post the caller may not open is never written.
func TestAddBookmarkToChecksTheReadGate(t *testing.T) {
	s, st := savedSvc()
	ctx := context.Background()
	user := uuid.New()
	trips, _ := s.CreateSavedCollection(ctx, user, "Trips")
	hidden, open := uuid.New(), uuid.New()
	s.engagementPostSeam = func(_ context.Context, postID, _ uuid.UUID) (*postgres.Post, error) {
		if postID == hidden {
			return nil, ErrPostNotVisible
		}
		return &postgres.Post{ID: postID}, nil
	}
	if err := s.AddBookmarkTo(ctx, user, hidden, trips.ID); !errors.Is(err, ErrPostNotVisible) {
		t.Fatalf("hidden post: %v", err)
	}
	if len(st.items) != 0 {
		t.Fatal("a post the caller may not open was saved")
	}
	if err := s.AddBookmarkTo(ctx, user, open, trips.ID); err != nil {
		t.Fatal(err)
	}
	items, _, _ := s.ListSavedItems(ctx, user, postgres.SavedScope{CollectionID: trips.ID}, 20, "")
	if len(items) != 1 || items[0].TargetID != open {
		t.Fatalf("saved into the collection: %+v", items)
	}
}
