package store

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Topics are created, featured and given icons only from the admin console
// (audit M3): the writes below sit behind an admin-service token
// (http/admin_topics.go), and each lands with its moderation_actions row in
// one transaction like every other token-path write.

const (
	ActionTopicCreate = "topic_create"
	ActionTopicUpdate = "topic_update"
)

// AdminTopicUpdate is a partial topic edit; nil fields are kept.
type AdminTopicUpdate struct {
	Name        *string
	Description *string
	IconURL     *string
	IsFeatured  *bool
}

const topicColumns = `id, name, slug, description, icon_url, parent_topic_id,
	question_count, follower_count, is_featured, created_at`

func scanTopic(row pgx.Row) (*Topic, error) {
	t := &Topic{}
	err := row.Scan(&t.ID, &t.Name, &t.Slug, &t.Description, &t.IconURL, &t.ParentTopicID,
		&t.QuestionCount, &t.FollowerCount, &t.IsFeatured, &t.CreatedAt)
	return t, err
}

func topicWriteErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return adminErr(ErrAdminConflict, "a topic with that name or slug already exists")
		case "23503":
			return adminErr(ErrAdminInvalid, "parent_topic_id does not name a topic")
		}
	}
	return err
}

// AdminCreateTopic creates one topic and records who did it.
func (s *Store) AdminCreateTopic(ctx context.Context, actor uuid.UUID, p CreateTopicParams, reason string) (*Topic, error) {
	slug := p.Slug
	if slug == "" {
		slug = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(p.Name), " ", "-"))
	}
	var out *Topic
	err := s.withAdminTx(ctx, func(tx pgx.Tx) error {
		t, err := scanTopic(tx.QueryRow(ctx, `
			INSERT INTO topics (name, slug, description, icon_url, parent_topic_id, is_featured)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING `+topicColumns,
			p.Name, slug, p.Description, p.IconURL, p.ParentTopicID, p.IsFeatured))
		if err != nil {
			return topicWriteErr(err)
		}
		if _, err := insertModerationActionTx(ctx, tx, nil, actor, ActionTopicCreate, "topic", t.ID, reason,
			map[string]any{"name": t.Name, "slug": t.Slug, "is_featured": t.IsFeatured}); err != nil {
			return err
		}
		out = t
		return nil
	})
	return out, err
}

// AdminUpdateTopic edits one topic (name, description, icon, featured) and
// records who did it.
func (s *Store) AdminUpdateTopic(ctx context.Context, actor, topicID uuid.UUID, u AdminTopicUpdate, reason string) (*Topic, error) {
	var out *Topic
	err := s.withAdminTx(ctx, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT true FROM topics WHERE id = $1 FOR UPDATE`, topicID).Scan(&exists); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return adminErr(ErrAdminNotFound, "topic not found")
			}
			return err
		}
		t, err := scanTopic(tx.QueryRow(ctx, `
			UPDATE topics SET
				name = COALESCE($2, name),
				description = COALESCE($3, description),
				icon_url = COALESCE($4, icon_url),
				is_featured = COALESCE($5, is_featured)
			WHERE id = $1
			RETURNING `+topicColumns,
			topicID, u.Name, u.Description, u.IconURL, u.IsFeatured))
		if err != nil {
			return topicWriteErr(err)
		}
		meta := map[string]any{}
		if u.IsFeatured != nil {
			meta["is_featured"] = *u.IsFeatured
		}
		if u.IconURL != nil {
			meta["icon_url"] = *u.IconURL
		}
		if _, err := insertModerationActionTx(ctx, tx, nil, actor, ActionTopicUpdate, "topic", topicID, reason, meta); err != nil {
			return err
		}
		out = t
		return nil
	})
	return out, err
}
