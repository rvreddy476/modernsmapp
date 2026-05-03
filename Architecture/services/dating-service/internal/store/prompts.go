package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrPromptNotFound is returned when no prompt answer matches the lookup.
var ErrPromptNotFound = errors.New("not_found: prompt not found")

// PromptCatalogItem is the static client-facing prompt catalog entry. The
// catalog is hard-coded for v1 and exposed via GET /v1/dating/prompts/catalog.
type PromptCatalogItem struct {
	ID       int    `json:"id"`
	Question string `json:"question"`
}

// ListPrompts returns the user's answered prompts.
func (s *Store) ListPrompts(ctx context.Context, userID uuid.UUID) ([]Prompt, error) {
	rows, err := s.db.Query(ctx, `
        SELECT id, user_id, prompt_id, answer, created_at, updated_at
        FROM dating_prompts WHERE user_id = $1
        ORDER BY prompt_id ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list prompts: %w", err)
	}
	defer rows.Close()

	var out []Prompt
	for rows.Next() {
		var p Prompt
		if err := rows.Scan(&p.ID, &p.UserID, &p.PromptID, &p.Answer, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan prompt: %w", err)
		}
		out = append(out, p)
	}
	return out, nil
}

// UpsertPrompt inserts or updates the answer for a (user, prompt_id) pair.
func (s *Store) UpsertPrompt(ctx context.Context, userID uuid.UUID, promptID int, answer string) (*Prompt, error) {
	row := s.db.QueryRow(ctx, `
        INSERT INTO dating_prompts (user_id, prompt_id, answer)
        VALUES ($1, $2, $3)
        ON CONFLICT (user_id, prompt_id) DO UPDATE
            SET answer = EXCLUDED.answer, updated_at = now()
        RETURNING id, user_id, prompt_id, answer, created_at, updated_at`,
		userID, promptID, answer)
	p := &Prompt{}
	if err := row.Scan(&p.ID, &p.UserID, &p.PromptID, &p.Answer, &p.CreatedAt, &p.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPromptNotFound
		}
		return nil, fmt.Errorf("upsert prompt: %w", err)
	}
	return p, nil
}

// DeletePrompt removes the answer for a (user, prompt_id) pair.
func (s *Store) DeletePrompt(ctx context.Context, userID uuid.UUID, promptID int) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM dating_prompts WHERE user_id = $1 AND prompt_id = $2`, userID, promptID)
	if err != nil {
		return fmt.Errorf("delete prompt: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrPromptNotFound
	}
	return nil
}
