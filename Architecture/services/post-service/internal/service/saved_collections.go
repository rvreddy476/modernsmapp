package service

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Saved collections (2026-10-24, migration 074).
//
//	GET    /v1/saved/collections                  the default collection, then the caller's collections
//	POST   /v1/saved/collections                  {name} -> 201 collection
//	PATCH  /v1/saved/collections/:collectionId    {name} -> collection
//	DELETE /v1/saved/collections/:collectionId    items move back to the default
//	PUT    /v1/saved/:savedId/collection          {collection_id: uuid|null}
//	PUT    /v1/posts/:postId/bookmark/collection  {collection_id: uuid|null}
//	POST   /v1/posts/:postId/bookmark             optional {collection_id}
//	GET    /v1/posts/bookmarks?collection_id=default|<uuid>
//	GET    /v1/saved?collection_id=default|<uuid>
//
// An item is in exactly one collection; collection_id null is the default
// collection ("All Saved"), which always exists, cannot be renamed or
// deleted, and is where a plain save lands. At most MaxSavedCollections per
// user; names are 1-MaxCollectionNameLength characters after trimming,
// unique per user case-insensitively, and may not be the default's name.
// Every read and write is scoped to the caller: another user's collection
// id answers exactly like one that does not exist (404).

const (
	// MaxSavedCollections is the per-user ceiling on named collections.
	MaxSavedCollections = 50
	// MaxCollectionNameLength is the name ceiling, in characters.
	MaxCollectionNameLength = 40
)

var (
	// ErrInvalidCollectionName: empty, too long, control characters, or a
	// name reserved for the default collection.
	ErrInvalidCollectionName = errors.New("collection name must be 1-40 characters and not a reserved name")
	// ErrInvalidCollectionID: a collection_id that is neither a UUID nor
	// "default".
	ErrInvalidCollectionID = errors.New("collection_id must be a collection id or \"default\"")

	ErrCollectionNotFound  = postgres.ErrCollectionNotFound
	ErrCollectionNameTaken = postgres.ErrCollectionNameTaken
	ErrCollectionLimit     = postgres.ErrCollectionLimit
	ErrSavedItemNotFound   = postgres.ErrSavedItemNotFound
)

// reservedCollectionNames are the default collection's spellings (current
// and pre-074), compared case-insensitively.
var reservedCollectionNames = map[string]bool{"all saved": true, "default": true}

// NormalizeCollectionName trims the name, folds inner whitespace runs to one
// space, and enforces the length, alphabet and reserved-name rules.
func NormalizeCollectionName(raw string) (string, error) {
	if !utf8.ValidString(raw) {
		return "", ErrInvalidCollectionName
	}
	for _, r := range raw {
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return "", ErrInvalidCollectionName
		}
	}
	name := strings.Join(strings.Fields(raw), " ")
	if n := utf8.RuneCountInString(name); n < 1 || n > MaxCollectionNameLength {
		return "", ErrInvalidCollectionName
	}
	if reservedCollectionNames[strings.ToLower(name)] {
		return "", ErrInvalidCollectionName
	}
	return name, nil
}

// SavedCollection is one collection as the API returns it.
type SavedCollection struct {
	// ID is null for the default collection.
	ID        *uuid.UUID `json:"id"`
	Name      string     `json:"name"`
	Count     int64      `json:"count"`
	IsDefault bool       `json:"is_default"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

func savedCollectionFromRow(r *postgres.SavedCollectionRow) *SavedCollection {
	id, created, updated := r.ID, r.CreatedAt, r.UpdatedAt
	return &SavedCollection{ID: &id, Name: r.Name, Count: r.Count, CreatedAt: &created, UpdatedAt: &updated}
}

// savedCollectionStore is the slice of the Postgres store the collection
// routes use; an interface so the rules can be tested without a database.
type savedCollectionStore interface {
	ListSavedCollections(ctx context.Context, userID uuid.UUID) (int64, []postgres.SavedCollectionRow, error)
	GetSavedCollection(ctx context.Context, userID, id uuid.UUID) (*postgres.SavedCollectionRow, error)
	FindSavedCollectionByName(ctx context.Context, userID uuid.UUID, name string) (*postgres.SavedCollectionRow, error)
	CreateSavedCollection(ctx context.Context, userID uuid.UUID, name string, max int) (*postgres.SavedCollectionRow, error)
	RenameSavedCollection(ctx context.Context, userID, id uuid.UUID, name string) (*postgres.SavedCollectionRow, error)
	DeleteSavedCollection(ctx context.Context, userID, id uuid.UUID) (int64, error)
	MoveSavedItem(ctx context.Context, userID, savedID uuid.UUID, collectionID *uuid.UUID) (*postgres.SavedItem, error)
	MoveSavedTarget(ctx context.Context, userID uuid.UUID, targetType string, targetID uuid.UUID, collectionID *uuid.UUID) (*postgres.SavedItem, error)
	SaveItem(ctx context.Context, userID uuid.UUID, targetType string, targetID uuid.UUID, collectionID *uuid.UUID) (*postgres.SavedItem, error)
	ListSavedItems(ctx context.Context, userID uuid.UUID, scope postgres.SavedScope, limit int, cursor string) ([]postgres.SavedItem, string, error)
}

func (s *Service) savedStore() (savedCollectionStore, error) {
	if s.savedCollections != nil {
		return s.savedCollections, nil
	}
	if s.pgStore != nil {
		return s.pgStore, nil
	}
	return nil, ErrAuthoringStoreUnavailable
}

// ListSavedCollections is the default collection followed by the caller's
// collections, oldest first.
func (s *Service) ListSavedCollections(ctx context.Context, userID uuid.UUID) ([]SavedCollection, error) {
	st, err := s.savedStore()
	if err != nil {
		return nil, err
	}
	defaultCount, rows, err := st.ListSavedCollections(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]SavedCollection, 0, len(rows)+1)
	out = append(out, SavedCollection{Name: postgres.DefaultCollectionName, Count: defaultCount, IsDefault: true})
	for i := range rows {
		out = append(out, *savedCollectionFromRow(&rows[i]))
	}
	return out, nil
}

// CreateSavedCollection creates a collection for the caller.
func (s *Service) CreateSavedCollection(ctx context.Context, userID uuid.UUID, rawName string) (*SavedCollection, error) {
	name, err := NormalizeCollectionName(rawName)
	if err != nil {
		return nil, err
	}
	st, err := s.savedStore()
	if err != nil {
		return nil, err
	}
	row, err := st.CreateSavedCollection(ctx, userID, name, MaxSavedCollections)
	if err != nil {
		return nil, err
	}
	return savedCollectionFromRow(row), nil
}

// RenameSavedCollection renames one of the caller's collections.
func (s *Service) RenameSavedCollection(ctx context.Context, userID, id uuid.UUID, rawName string) (*SavedCollection, error) {
	name, err := NormalizeCollectionName(rawName)
	if err != nil {
		return nil, err
	}
	st, err := s.savedStore()
	if err != nil {
		return nil, err
	}
	row, err := st.RenameSavedCollection(ctx, userID, id, name)
	if err != nil {
		return nil, err
	}
	return savedCollectionFromRow(row), nil
}

// DeleteSavedCollection deletes one of the caller's collections; its items
// move back to the default collection. moved is how many did.
func (s *Service) DeleteSavedCollection(ctx context.Context, userID, id uuid.UUID) (int64, error) {
	st, err := s.savedStore()
	if err != nil {
		return 0, err
	}
	return st.DeleteSavedCollection(ctx, userID, id)
}

// MoveSavedItem moves one of the caller's saved items; collectionID nil is
// the default collection.
func (s *Service) MoveSavedItem(ctx context.Context, userID, savedID uuid.UUID, collectionID *uuid.UUID) (*postgres.SavedItem, error) {
	st, err := s.savedStore()
	if err != nil {
		return nil, err
	}
	return st.MoveSavedItem(ctx, userID, savedID, collectionID)
}

// MoveBookmark moves a saved post, addressed by the post.
func (s *Service) MoveBookmark(ctx context.Context, userID, postID uuid.UUID, collectionID *uuid.UUID) (*postgres.SavedItem, error) {
	st, err := s.savedStore()
	if err != nil {
		return nil, err
	}
	return st.MoveSavedTarget(ctx, userID, "post", postID, collectionID)
}

// resolveLegacyCollectionName turns the pre-074 collection_name of
// POST /v1/saved into a collection: "" or the default's name is the default
// (nil); an existing name (case-insensitive) is that collection; any other
// name creates one, under the same rules as POST /v1/saved/collections.
func (s *Service) resolveLegacyCollectionName(ctx context.Context, st savedCollectionStore, userID uuid.UUID, raw string) (*uuid.UUID, error) {
	trimmed := strings.Join(strings.Fields(raw), " ")
	if trimmed == "" || reservedCollectionNames[strings.ToLower(trimmed)] {
		return nil, nil
	}
	if row, err := st.FindSavedCollectionByName(ctx, userID, trimmed); err == nil {
		return &row.ID, nil
	} else if !errors.Is(err, ErrCollectionNotFound) {
		return nil, err
	}
	name, err := NormalizeCollectionName(trimmed)
	if err != nil {
		return nil, err
	}
	row, err := st.CreateSavedCollection(ctx, userID, name, MaxSavedCollections)
	if errors.Is(err, ErrCollectionNameTaken) {
		// Created concurrently: use it.
		row, err = st.FindSavedCollectionByName(ctx, userID, name)
	}
	if err != nil {
		return nil, err
	}
	return &row.ID, nil
}

// SaveItem saves target for the caller: into collectionID when set, else
// into the collection legacyName names (resolveLegacyCollectionName), else
// into the default collection (an item already saved then stays where it is).
func (s *Service) SaveItem(ctx context.Context, userID uuid.UUID, targetType string, targetID uuid.UUID, collectionID *uuid.UUID, legacyName string) (*postgres.SavedItem, error) {
	st, err := s.savedStore()
	if err != nil {
		return nil, err
	}
	if collectionID == nil && legacyName != "" {
		if collectionID, err = s.resolveLegacyCollectionName(ctx, st, userID, legacyName); err != nil {
			return nil, err
		}
	}
	return st.SaveItem(ctx, userID, targetType, targetID, collectionID)
}

// ResolveSavedScope parses a collection_id query value for the caller: ""
// is every saved item, "default" the default collection, a UUID one of the
// caller's collections (ErrCollectionNotFound for anyone else's).
func (s *Service) ResolveSavedScope(ctx context.Context, userID uuid.UUID, raw string) (postgres.SavedScope, error) {
	raw = strings.TrimSpace(raw)
	switch strings.ToLower(raw) {
	case "":
		return postgres.SavedScope{}, nil
	case "default":
		return postgres.SavedScope{DefaultOnly: true}, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return postgres.SavedScope{}, ErrInvalidCollectionID
	}
	st, err := s.savedStore()
	if err != nil {
		return postgres.SavedScope{}, err
	}
	if _, err := st.GetSavedCollection(ctx, userID, id); err != nil {
		return postgres.SavedScope{}, err
	}
	return postgres.SavedScope{CollectionID: &id}, nil
}

// ListSavedItems is the caller's saved items within scope.
func (s *Service) ListSavedItems(ctx context.Context, userID uuid.UUID, scope postgres.SavedScope, limit int, cursor string) ([]postgres.SavedItem, string, error) {
	st, err := s.savedStore()
	if err != nil {
		return nil, "", err
	}
	return st.ListSavedItems(ctx, userID, scope, limit, cursor)
}

// AddBookmarkTo is AddBookmark into a collection (nil = the plain save).
func (s *Service) AddBookmarkTo(ctx context.Context, userID, postID uuid.UUID, collectionID *uuid.UUID) error {
	if collectionID == nil {
		return s.AddBookmark(ctx, userID, postID)
	}
	if err := s.checkEngagementVisibility(ctx, postID, userID); err != nil {
		return err
	}
	st, err := s.savedStore()
	if err != nil {
		return err
	}
	if _, err := st.SaveItem(ctx, userID, "post", postID, collectionID); err != nil {
		return err
	}
	s.invalidateFeedHydration(ctx, userID, postID)
	return nil
}
