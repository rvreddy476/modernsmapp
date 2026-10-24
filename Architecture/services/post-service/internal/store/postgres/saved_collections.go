package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Saved collections (migration 074, 2026-10-24).
//
// A saved item sits in exactly one collection: saved_items.collection_id,
// NULL = the default collection ("All Saved"). Every statement here is
// scoped by user_id, so another user's collection id behaves exactly like
// one that does not exist (ErrCollectionNotFound): a viewer can never see,
// fill, rename or delete someone else's collections.

// DefaultCollectionName is the default collection's name, stored in
// saved_items.collection_name for items that are in no named collection.
const DefaultCollectionName = "All Saved"

var (
	// ErrCollectionNotFound: no such collection for this user.
	ErrCollectionNotFound = errors.New("saved collection not found")
	// ErrCollectionNameTaken: the user already has a collection of that
	// name (case-insensitive).
	ErrCollectionNameTaken = errors.New("a collection with this name already exists")
	// ErrCollectionLimit: the user already has the maximum number of
	// collections.
	ErrCollectionLimit = errors.New("collection limit reached")
	// ErrSavedItemNotFound: the item is not in this user's saved items.
	ErrSavedItemNotFound = errors.New("saved item not found")
)

// SavedCollectionRow is one named collection with its item count.
type SavedCollectionRow struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Name      string
	Count     int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SavedScope narrows a saved-items read. The zero value is every item.
type SavedScope struct {
	// CollectionName is the pre-collections ?collection=<name> filter
	// (exact collection_name).
	CollectionName string
	// CollectionID keeps one named collection's items.
	CollectionID *uuid.UUID
	// DefaultOnly keeps the default collection's items (collection_id NULL).
	DefaultOnly bool
}

// where returns the SQL condition for scope over alias (e.g. "saved_items"
// or "si"), appending its arguments.
func (sc SavedScope) where(alias string, args []any) (string, []any) {
	switch {
	case sc.DefaultOnly:
		return " AND " + alias + ".collection_id IS NULL", args
	case sc.CollectionID != nil:
		args = append(args, *sc.CollectionID)
		return fmt.Sprintf(" AND %s.collection_id = $%d", alias, len(args)), args
	case sc.CollectionName != "":
		args = append(args, sc.CollectionName)
		return fmt.Sprintf(" AND %s.collection_name = $%d", alias, len(args)), args
	}
	return "", args
}

// savedCollectionsLock serialises one user's collection writes, so the
// count check and the insert in CreateSavedCollection cannot race.
func savedCollectionsLock(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('saved_collections:' || $1::text, 0))`, userID)
	return err
}

// ListSavedCollections returns the number of items in the user's default
// collection and their named collections, oldest first.
func (s *Store) ListSavedCollections(ctx context.Context, userID uuid.UUID) (int64, []SavedCollectionRow, error) {
	var defaultCount int64
	if err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM saved_items WHERE user_id = $1 AND collection_id IS NULL`, userID).Scan(&defaultCount); err != nil {
		return 0, nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.user_id, c.name, c.created_at, c.updated_at, COUNT(si.id)
		  FROM saved_collections c
		  LEFT JOIN saved_items si ON si.collection_id = c.id AND si.user_id = c.user_id
		 WHERE c.user_id = $1
		 GROUP BY c.id
		 ORDER BY c.created_at, c.id`, userID)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	out := []SavedCollectionRow{}
	for rows.Next() {
		var r SavedCollectionRow
		if err := rows.Scan(&r.ID, &r.UserID, &r.Name, &r.CreatedAt, &r.UpdatedAt, &r.Count); err != nil {
			return 0, nil, err
		}
		out = append(out, r)
	}
	return defaultCount, out, rows.Err()
}

// GetSavedCollection returns the user's collection, or ErrCollectionNotFound
// (also for another user's id).
func (s *Store) GetSavedCollection(ctx context.Context, userID, id uuid.UUID) (*SavedCollectionRow, error) {
	var r SavedCollectionRow
	err := s.db.QueryRow(ctx, `
		SELECT c.id, c.user_id, c.name, c.created_at, c.updated_at,
		       (SELECT COUNT(*) FROM saved_items si WHERE si.collection_id = c.id AND si.user_id = c.user_id)
		  FROM saved_collections c
		 WHERE c.id = $1 AND c.user_id = $2`, id, userID).
		Scan(&r.ID, &r.UserID, &r.Name, &r.CreatedAt, &r.UpdatedAt, &r.Count)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCollectionNotFound
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// FindSavedCollectionByName returns the user's collection named name
// (case-insensitive), or ErrCollectionNotFound.
func (s *Store) FindSavedCollectionByName(ctx context.Context, userID uuid.UUID, name string) (*SavedCollectionRow, error) {
	var id uuid.UUID
	err := s.db.QueryRow(ctx,
		`SELECT id FROM saved_collections WHERE user_id = $1 AND lower(name) = lower($2)`, userID, name).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCollectionNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.GetSavedCollection(ctx, userID, id)
}

// CreateSavedCollection creates a collection named name (already validated)
// unless the user has max collections (ErrCollectionLimit) or one of that
// name (ErrCollectionNameTaken).
func (s *Store) CreateSavedCollection(ctx context.Context, userID uuid.UUID, name string, max int) (*SavedCollectionRow, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := savedCollectionsLock(ctx, tx, userID); err != nil {
		return nil, err
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM saved_collections WHERE user_id = $1`, userID).Scan(&n); err != nil {
		return nil, err
	}
	if n >= max {
		return nil, ErrCollectionLimit
	}
	r := SavedCollectionRow{ID: uuid.New(), UserID: userID, Name: name}
	err = tx.QueryRow(ctx, `
		INSERT INTO saved_collections (id, user_id, name, created_at, updated_at)
		VALUES ($1, $2, $3, NOW(), NOW())
		RETURNING created_at, updated_at`, r.ID, userID, name).Scan(&r.CreatedAt, &r.UpdatedAt)
	if IsUniqueViolation(err) {
		return nil, ErrCollectionNameTaken
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &r, nil
}

// RenameSavedCollection renames the user's collection and keeps its items'
// collection_name in step, in one transaction.
func (s *Store) RenameSavedCollection(ctx context.Context, userID, id uuid.UUID, name string) (*SavedCollectionRow, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := savedCollectionsLock(ctx, tx, userID); err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE saved_collections SET name = $3, updated_at = NOW()
		 WHERE id = $1 AND user_id = $2`, id, userID, name)
	if IsUniqueViolation(err) {
		return nil, ErrCollectionNameTaken
	}
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrCollectionNotFound
	}
	if _, err := tx.Exec(ctx, `
		UPDATE saved_items SET collection_name = $3
		 WHERE collection_id = $1 AND user_id = $2`, id, userID, name); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetSavedCollection(ctx, userID, id)
}

// DeleteSavedCollection deletes the user's collection after moving its
// items back to the default collection, in one transaction. moved is how
// many items moved.
func (s *Store) DeleteSavedCollection(ctx context.Context, userID, id uuid.UUID) (moved int64, err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := savedCollectionsLock(ctx, tx, userID); err != nil {
		return 0, err
	}
	var locked uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id FROM saved_collections WHERE id = $1 AND user_id = $2 FOR UPDATE`, id, userID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrCollectionNotFound
	}
	if err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE saved_items SET collection_id = NULL, collection_name = $3
		 WHERE collection_id = $1 AND user_id = $2`, id, userID, DefaultCollectionName)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM saved_collections WHERE id = $1 AND user_id = $2`, id, userID); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// lockCollectionNameTx returns the name of the user's collection id, share-
// locked so a concurrent delete waits for this transaction; nil id is the
// default collection.
func lockCollectionNameTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, id *uuid.UUID) (string, error) {
	if id == nil {
		return DefaultCollectionName, nil
	}
	var name string
	err := tx.QueryRow(ctx,
		`SELECT name FROM saved_collections WHERE id = $1 AND user_id = $2 FOR SHARE`, *id, userID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrCollectionNotFound
	}
	return name, err
}

const savedItemCols = `id, user_id, target_type, target_id, collection_name, collection_id, created_at`

func scanSavedItem(row pgx.Row) (*SavedItem, error) {
	var it SavedItem
	if err := row.Scan(&it.ID, &it.UserID, &it.TargetType, &it.TargetID, &it.CollectionName, &it.CollectionID, &it.CreatedAt); err != nil {
		return nil, err
	}
	return &it, nil
}

// MoveSavedItem moves the user's saved item savedID into collection id (nil
// = the default). ErrCollectionNotFound for a collection that is not the
// user's, ErrSavedItemNotFound for an item that is not theirs.
func (s *Store) MoveSavedItem(ctx context.Context, userID, savedID uuid.UUID, collectionID *uuid.UUID) (*SavedItem, error) {
	return s.moveSaved(ctx, userID, collectionID, `id = $1`, savedID)
}

// MoveSavedTarget is MoveSavedItem addressed by what was saved.
func (s *Store) MoveSavedTarget(ctx context.Context, userID uuid.UUID, targetType string, targetID uuid.UUID, collectionID *uuid.UUID) (*SavedItem, error) {
	return s.moveSaved(ctx, userID, collectionID, `target_type = $4 AND target_id = $1`, targetID, targetType)
}

func (s *Store) moveSaved(ctx context.Context, userID uuid.UUID, collectionID *uuid.UUID, match string, key uuid.UUID, extra ...any) (*SavedItem, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	name, err := lockCollectionNameTx(ctx, tx, userID, collectionID)
	if err != nil {
		return nil, err
	}
	args := append([]any{key, userID, name}, extra...)
	var cid any
	if collectionID != nil {
		cid = *collectionID
	}
	args = append(args, cid)
	q := fmt.Sprintf(`
		UPDATE saved_items SET collection_id = $%d::uuid, collection_name = $3
		 WHERE user_id = $2 AND %s
		RETURNING `+savedItemCols, len(args), match)
	it, err := scanSavedItem(tx.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSavedItemNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return it, nil
}

// SaveItem saves target for the user.
//
// collectionID nil: into the default collection, and an item that is
// already saved keeps the collection it is in (a repeated save — a retry,
// the bookmark button — never pulls it out of one). collectionID set: into
// that collection (the user's, else ErrCollectionNotFound), moving an item
// already saved elsewhere.
func (s *Store) SaveItem(ctx context.Context, userID uuid.UUID, targetType string, targetID uuid.UUID, collectionID *uuid.UUID) (*SavedItem, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	name, err := lockCollectionNameTx(ctx, tx, userID, collectionID)
	if err != nil {
		return nil, err
	}
	conflict := `DO NOTHING`
	if collectionID != nil {
		conflict = `DO UPDATE SET collection_id = EXCLUDED.collection_id, collection_name = EXCLUDED.collection_name`
	}
	var cid any
	if collectionID != nil {
		cid = *collectionID
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO saved_items (id, user_id, target_type, target_id, collection_name, collection_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6::uuid, NOW())
		ON CONFLICT (user_id, target_type, target_id) `+conflict,
		uuid.New(), userID, targetType, targetID, name, cid); err != nil {
		return nil, err
	}
	it, err := scanSavedItem(tx.QueryRow(ctx, `
		SELECT `+savedItemCols+` FROM saved_items
		 WHERE user_id = $1 AND target_type = $2 AND target_id = $3`, userID, targetType, targetID))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return it, nil
}
