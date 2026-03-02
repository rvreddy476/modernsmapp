package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Collection struct {
	ID          uuid.UUID `json:"id"`
	UserID      uuid.UUID `json:"user_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	CoverURL    *string   `json:"cover_url,omitempty"`
	Visibility  string    `json:"visibility"`
	ItemCount   int       `json:"item_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CollectionItem struct {
	ID           uuid.UUID  `json:"id"`
	CollectionID uuid.UUID  `json:"collection_id"`
	PostID       *uuid.UUID `json:"post_id,omitempty"`
	MediaURL     *string    `json:"media_url,omitempty"`
	Caption      string     `json:"caption"`
	SortOrder    int        `json:"sort_order"`
	CreatedAt    time.Time  `json:"created_at"`
}

type OnThisDay struct {
	ID         uuid.UUID `json:"id"`
	UserID     uuid.UUID `json:"user_id"`
	MemoryDate string    `json:"memory_date"`
	PostID     uuid.UUID `json:"post_id"`
	YearsAgo   int       `json:"years_ago"`
	Snippet    string    `json:"snippet"`
	MediaURL   *string   `json:"media_url,omitempty"`
}

type Preferences struct {
	UserID           uuid.UUID `json:"user_id"`
	Enabled          bool      `json:"enabled"`
	HiddenYears      []int     `json:"hidden_years"`
	HiddenPeopleIDs  []string  `json:"hidden_people_ids"`
	NotificationTime string    `json:"notification_time"`
}

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// --- Collections ---

func (s *Store) CreateCollection(ctx context.Context, c *Collection) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO memories.collections (id, user_id, title, description, cover_url, visibility, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
	`, c.ID, c.UserID, c.Title, c.Description, c.CoverURL, c.Visibility, c.CreatedAt)
	return err
}

func (s *Store) GetCollection(ctx context.Context, id uuid.UUID) (*Collection, error) {
	var c Collection
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, title, description, cover_url, visibility, item_count, created_at, updated_at
		FROM memories.collections WHERE id = $1
	`, id).Scan(&c.ID, &c.UserID, &c.Title, &c.Description, &c.CoverURL, &c.Visibility, &c.ItemCount, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) ListCollections(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Collection, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, title, description, cover_url, visibility, item_count, created_at, updated_at
		FROM memories.collections WHERE user_id = $1
		ORDER BY created_at DESC LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var collections []Collection
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.UserID, &c.Title, &c.Description, &c.CoverURL, &c.Visibility, &c.ItemCount, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		collections = append(collections, c)
	}
	return collections, nil
}

func (s *Store) UpdateCollection(ctx context.Context, id uuid.UUID, title, description, visibility string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE memories.collections SET title=$2, description=$3, visibility=$4, updated_at=NOW()
		WHERE id=$1
	`, id, title, description, visibility)
	return err
}

func (s *Store) DeleteCollection(ctx context.Context, id, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM memories.collections WHERE id = $1 AND user_id = $2`, id, userID)
	return err
}

// --- Collection Items ---

func (s *Store) AddCollectionItem(ctx context.Context, item *CollectionItem) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO memories.collection_items (id, collection_id, post_id, media_url, caption, sort_order, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, item.ID, item.CollectionID, item.PostID, item.MediaURL, item.Caption, item.SortOrder, item.CreatedAt)
	if err != nil {
		return err
	}
	// Increment item count
	_, err = s.db.Exec(ctx, `UPDATE memories.collections SET item_count = item_count + 1, updated_at = NOW() WHERE id = $1`, item.CollectionID)
	return err
}

func (s *Store) ListCollectionItems(ctx context.Context, collectionID uuid.UUID, limit, offset int) ([]CollectionItem, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, collection_id, post_id, media_url, caption, sort_order, created_at
		FROM memories.collection_items WHERE collection_id = $1
		ORDER BY sort_order, created_at LIMIT $2 OFFSET $3
	`, collectionID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []CollectionItem
	for rows.Next() {
		var item CollectionItem
		if err := rows.Scan(&item.ID, &item.CollectionID, &item.PostID, &item.MediaURL, &item.Caption, &item.SortOrder, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Store) RemoveCollectionItem(ctx context.Context, itemID, collectionID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM memories.collection_items WHERE id = $1 AND collection_id = $2`, itemID, collectionID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		_, _ = s.db.Exec(ctx, `UPDATE memories.collections SET item_count = item_count - 1, updated_at = NOW() WHERE id = $1`, collectionID)
	}
	return nil
}

// --- On This Day ---

func (s *Store) GetOnThisDay(ctx context.Context, userID uuid.UUID, date string) ([]OnThisDay, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, memory_date, post_id, years_ago, snippet, media_url
		FROM memories.on_this_day WHERE user_id = $1 AND memory_date = $2
		ORDER BY years_ago DESC
	`, userID, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []OnThisDay
	for rows.Next() {
		var m OnThisDay
		if err := rows.Scan(&m.ID, &m.UserID, &m.MemoryDate, &m.PostID, &m.YearsAgo, &m.Snippet, &m.MediaURL); err != nil {
			return nil, err
		}
		memories = append(memories, m)
	}
	return memories, nil
}

func (s *Store) UpsertOnThisDay(ctx context.Context, m *OnThisDay) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO memories.on_this_day (id, user_id, memory_date, post_id, years_ago, snippet, media_url, generated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (user_id, memory_date, post_id) DO UPDATE SET
			years_ago = EXCLUDED.years_ago, snippet = EXCLUDED.snippet, media_url = EXCLUDED.media_url, generated_at = NOW()
	`, m.ID, m.UserID, m.MemoryDate, m.PostID, m.YearsAgo, m.Snippet, m.MediaURL)
	return err
}

// GenerateOnThisDay queries posts from previous years on the same month/day.
func (s *Store) GenerateOnThisDay(ctx context.Context, userID uuid.UUID, today time.Time) ([]OnThisDay, error) {
	month := int(today.Month())
	day := today.Day()
	currentYear := today.Year()
	dateStr := today.Format("2006-01-02")

	rows, err := s.db.Query(ctx, `
		SELECT id, text, created_at,
		       (SELECT url FROM media_assets WHERE id = ANY(
		           SELECT media_id FROM post_media WHERE post_id = posts.id LIMIT 1
		       ) LIMIT 1) as media_url
		FROM posts
		WHERE author_id = $1
		  AND EXTRACT(MONTH FROM created_at) = $2
		  AND EXTRACT(DAY FROM created_at) = $3
		  AND EXTRACT(YEAR FROM created_at) < $4
		ORDER BY created_at DESC
		LIMIT 20
	`, userID, month, day, currentYear)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []OnThisDay
	for rows.Next() {
		var postID uuid.UUID
		var text string
		var createdAt time.Time
		var mediaURL *string
		if err := rows.Scan(&postID, &text, &createdAt, &mediaURL); err != nil {
			continue
		}
		snippet := text
		if len(snippet) > 150 {
			snippet = snippet[:150]
		}
		m := OnThisDay{
			ID:         uuid.New(),
			UserID:     userID,
			MemoryDate: dateStr,
			PostID:     postID,
			YearsAgo:   currentYear - createdAt.Year(),
			Snippet:    snippet,
			MediaURL:   mediaURL,
		}
		memories = append(memories, m)
		_ = s.UpsertOnThisDay(ctx, &m)
	}
	return memories, nil
}

// --- Preferences ---

func (s *Store) GetPreferences(ctx context.Context, userID uuid.UUID) (*Preferences, error) {
	var p Preferences
	err := s.db.QueryRow(ctx, `
		SELECT user_id, enabled, hidden_years, hidden_people_ids, notification_time::text
		FROM memories.preferences WHERE user_id = $1
	`, userID).Scan(&p.UserID, &p.Enabled, &p.HiddenYears, &p.HiddenPeopleIDs, &p.NotificationTime)
	if err != nil {
		// Return defaults if no row
		return &Preferences{
			UserID:           userID,
			Enabled:          true,
			HiddenYears:      []int{},
			HiddenPeopleIDs:  []string{},
			NotificationTime: "09:00",
		}, nil
	}
	return &p, nil
}

func (s *Store) UpdatePreferences(ctx context.Context, p *Preferences) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO memories.preferences (user_id, enabled, hidden_years, hidden_people_ids, notification_time, updated_at)
		VALUES ($1, $2, $3, $4, $5::time, NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			enabled = EXCLUDED.enabled, hidden_years = EXCLUDED.hidden_years,
			hidden_people_ids = EXCLUDED.hidden_people_ids, notification_time = EXCLUDED.notification_time,
			updated_at = NOW()
	`, p.UserID, p.Enabled, p.HiddenYears, p.HiddenPeopleIDs, p.NotificationTime)
	return err
}
