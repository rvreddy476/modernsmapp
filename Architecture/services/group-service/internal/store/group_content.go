package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type GroupRule struct {
	ID          uuid.UUID `json:"id"`
	GroupID     uuid.UUID `json:"group_id"`
	RuleOrder   int       `json:"rule_order"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

// --- Group Rules ---

// ListGroupRules returns rules for a group ordered by rule_order.
func (s *Store) ListGroupRules(ctx context.Context, groupID uuid.UUID) ([]GroupRule, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, group_id, rule_order, title, description, created_at
		FROM group_rules
		WHERE group_id = $1
		ORDER BY rule_order ASC
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []GroupRule
	for rows.Next() {
		var r GroupRule
		if err := rows.Scan(&r.ID, &r.GroupID, &r.RuleOrder, &r.Title, &r.Description, &r.CreatedAt); err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// ReplaceGroupRules deletes existing rules and inserts new ones atomically.
func (s *Store) ReplaceGroupRules(ctx context.Context, groupID uuid.UUID, rules []GroupRule) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `DELETE FROM group_rules WHERE group_id = $1`, groupID)
	if err != nil {
		return err
	}

	for i, r := range rules {
		_, err := tx.Exec(ctx, `
			INSERT INTO group_rules (group_id, rule_order, title, description)
			VALUES ($1, $2, $3, $4)
		`, groupID, i+1, r.Title, r.Description)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// ═══════════════════════════════════════════════════════════
// Word Blocklist
// ═══════════════════════════════════════════════════════════

func (s *Store) AddWordToBlocklist(ctx context.Context, groupID uuid.UUID, word string, addedBy uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO group_word_blocklist (group_id, word, added_by) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		groupID, word, addedBy)
	return err
}

func (s *Store) RemoveWordFromBlocklist(ctx context.Context, groupID uuid.UUID, word string) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM group_word_blocklist WHERE group_id = $1 AND word = $2`,
		groupID, word)
	return err
}

func (s *Store) GetWordBlocklist(ctx context.Context, groupID uuid.UUID) ([]string, error) {
	rows, err := s.db.Query(ctx,
		`SELECT word FROM group_word_blocklist WHERE group_id = $1 ORDER BY added_at DESC`,
		groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var words []string
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			return nil, err
		}
		words = append(words, w)
	}
	return words, rows.Err()
}

// ═══════════════════════════════════════════════════════════
// Group Channels
// ═══════════════════════════════════════════════════════════

type GroupChannel struct {
	ID          uuid.UUID `json:"id"`
	GroupID     uuid.UUID `json:"group_id"`
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	Description string    `json:"description"`
	IsArchived  bool      `json:"is_archived"`
	SortOrder   int       `json:"sort_order"`
	PostCount   int64     `json:"post_count"`
	CreatedBy   uuid.UUID `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Store) CreateGroupChannel(ctx context.Context, ch *GroupChannel) (*GroupChannel, error) {
	err := s.db.QueryRow(ctx,
		`INSERT INTO group_channels (group_id, name, type, description, sort_order, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 RETURNING id, group_id, name, type, description, is_archived, sort_order, post_count, created_by, created_at`,
		ch.GroupID, ch.Name, ch.Type, ch.Description, ch.SortOrder, ch.CreatedBy,
	).Scan(&ch.ID, &ch.GroupID, &ch.Name, &ch.Type, &ch.Description, &ch.IsArchived, &ch.SortOrder, &ch.PostCount, &ch.CreatedBy, &ch.CreatedAt)
	return ch, err
}

func (s *Store) ListGroupChannels(ctx context.Context, groupID uuid.UUID) ([]GroupChannel, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, group_id, name, type, description, is_archived, sort_order, post_count, created_by, created_at
		 FROM group_channels WHERE group_id = $1 AND is_archived = FALSE ORDER BY sort_order ASC, created_at ASC`,
		groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var channels []GroupChannel
	for rows.Next() {
		var ch GroupChannel
		if err := rows.Scan(&ch.ID, &ch.GroupID, &ch.Name, &ch.Type, &ch.Description, &ch.IsArchived, &ch.SortOrder, &ch.PostCount, &ch.CreatedBy, &ch.CreatedAt); err != nil {
			return nil, err
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

func (s *Store) DeleteGroupChannel(ctx context.Context, channelID, groupID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE group_channels SET is_archived = TRUE WHERE id = $1 AND group_id = $2`,
		channelID, groupID)
	return err
}

// ═══════════════════════════════════════════════════════════
// Group Wiki
// ═══════════════════════════════════════════════════════════

type WikiPage struct {
	ID        uuid.UUID  `json:"id"`
	GroupID   uuid.UUID  `json:"group_id"`
	Title     string     `json:"title"`
	Content   string     `json:"content"`
	CreatedBy uuid.UUID  `json:"created_by"`
	UpdatedBy *uuid.UUID `json:"updated_by,omitempty"`
	Version   int        `json:"version"`
	IsPinned  bool       `json:"is_pinned"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

func (s *Store) CreateWikiPage(ctx context.Context, groupID, createdBy uuid.UUID, title, content string) (*WikiPage, error) {
	p := &WikiPage{}
	err := s.db.QueryRow(ctx,
		`INSERT INTO group_wiki_pages (group_id, title, content, created_by)
		 VALUES ($1,$2,$3,$4)
		 RETURNING id, group_id, title, content, created_by, updated_by, version, is_pinned, created_at, updated_at`,
		groupID, title, content, createdBy,
	).Scan(&p.ID, &p.GroupID, &p.Title, &p.Content, &p.CreatedBy, &p.UpdatedBy, &p.Version, &p.IsPinned, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

// ErrWikiPageNotFound: no page with that id in that group.
var ErrWikiPageNotFound = errors.New("not found: wiki page not found")

// UpdateWikiPage edits a page of groupID. The WHERE names the group as well as
// the page: matching on the page id alone let a member of one group overwrite
// another group's page and read its content back (G-M3).
func (s *Store) UpdateWikiPage(ctx context.Context, groupID, pageID, updatedBy uuid.UUID, title, content string) (*WikiPage, error) {
	p := &WikiPage{}
	err := s.db.QueryRow(ctx,
		`UPDATE group_wiki_pages SET title=$3, content=$4, updated_by=$2, version=version+1, updated_at=NOW()
		 WHERE id=$1 AND group_id=$5
		 RETURNING id, group_id, title, content, created_by, updated_by, version, is_pinned, created_at, updated_at`,
		pageID, updatedBy, title, content, groupID,
	).Scan(&p.ID, &p.GroupID, &p.Title, &p.Content, &p.CreatedBy, &p.UpdatedBy, &p.Version, &p.IsPinned, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrWikiPageNotFound
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

// ListWikiPages returns one page of a group's wiki, bounded like every other
// list (it was unbounded, with full content).
func (s *Store) ListWikiPages(ctx context.Context, groupID uuid.UUID, limit, offset int) ([]WikiPage, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(ctx,
		`SELECT id, group_id, title, content, created_by, updated_by, version, is_pinned, created_at, updated_at
		 FROM group_wiki_pages WHERE group_id = $1 ORDER BY is_pinned DESC, updated_at DESC
		 LIMIT $2 OFFSET $3`,
		groupID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pages []WikiPage
	for rows.Next() {
		var p WikiPage
		if err := rows.Scan(&p.ID, &p.GroupID, &p.Title, &p.Content, &p.CreatedBy, &p.UpdatedBy, &p.Version, &p.IsPinned, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		pages = append(pages, p)
	}
	return pages, rows.Err()
}

func (s *Store) DeleteWikiPage(ctx context.Context, pageID, groupID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM group_wiki_pages WHERE id = $1 AND group_id = $2`,
		pageID, groupID)
	return err
}
