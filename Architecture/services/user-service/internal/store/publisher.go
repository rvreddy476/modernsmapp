package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// EnsurePublisherResult holds the outcome of the atomic ensure-publisher flow.
type EnsurePublisherResult struct {
	AccountHandle string  `json:"account_handle"`
	Channel       Channel `json:"channel"`
	WasNewHandle  bool    `json:"was_new_handle"`
	WasNewChannel bool    `json:"was_new_channel"`
}

// EnsurePublisher atomically ensures the user has a handle and a default channel.
// It uses SELECT ... FOR UPDATE on the users row to serialize concurrent calls
// for the same user. If the user already has both, it returns them immediately.
//
// genHandle is a function that generates a candidate handle from a display name.
// It is called up to maxAttempts times if collisions occur.
func (s *Store) EnsurePublisher(ctx context.Context, userID uuid.UUID, genHandle func(string) string) (*EnsurePublisherResult, error) {
	const maxAttempts = 5

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	result := &EnsurePublisherResult{}

	// 1. Lock user row and read current handle + display_name.
	//    If the user doesn't exist yet (e.g. created in identity-platform but not
	//    synced to user-service), insert a minimal row so the flow can proceed.
	var handle *string
	var displayName string
	err = tx.QueryRow(ctx,
		`SELECT handle, display_name FROM users WHERE id = $1 FOR UPDATE`,
		userID,
	).Scan(&handle, &displayName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Auto-provision a minimal user row.
			now := time.Now()
			_, insertErr := tx.Exec(ctx,
				`INSERT INTO users (id, display_name, created_at, updated_at) VALUES ($1, $2, $3, $3)
				 ON CONFLICT (id) DO NOTHING`,
				userID, "User", now,
			)
			if insertErr != nil {
				return nil, fmt.Errorf("auto-provision user row: %w", insertErr)
			}
			displayName = "User"
			// Re-lock the newly created row.
			err = tx.QueryRow(ctx,
				`SELECT handle, display_name FROM users WHERE id = $1 FOR UPDATE`,
				userID,
			).Scan(&handle, &displayName)
			if err != nil {
				return nil, fmt.Errorf("lock new user row: %w", err)
			}
		} else {
			return nil, fmt.Errorf("lock user row: %w", err)
		}
	}

	// 2. Ensure account handle exists.
	if handle != nil && *handle != "" {
		result.AccountHandle = *handle
	} else {
		// Generate a unique handle.
		var candidate string
		for i := 0; i < maxAttempts; i++ {
			candidate = genHandle(displayName)
			var exists bool
			err = tx.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM handles WHERE handle = $1)`,
				candidate,
			).Scan(&exists)
			if err != nil {
				return nil, fmt.Errorf("check handle availability: %w", err)
			}
			if !exists {
				break
			}
			if i == maxAttempts-1 {
				return nil, fmt.Errorf("could not generate unique handle after %d attempts", maxAttempts)
			}
		}

		// Reserve the handle globally.
		_, err = tx.Exec(ctx,
			`INSERT INTO handles (handle, owner_type, owner_id) VALUES ($1, 'account', $2)`,
			candidate, userID,
		)
		if err != nil {
			return nil, fmt.Errorf("reserve account handle: %w", err)
		}

		// Set handle on user.
		_, err = tx.Exec(ctx,
			`UPDATE users SET handle = $1 WHERE id = $2`,
			candidate, userID,
		)
		if err != nil {
			return nil, fmt.Errorf("set user handle: %w", err)
		}

		result.AccountHandle = candidate
		result.WasNewHandle = true
	}

	// 3. Ensure default channel exists.
	var ch Channel
	err = tx.QueryRow(ctx, `
		SELECT id, user_id, handle, name, description, avatar_media_id, banner_media_id,
			category, country, language, contact_email, collab_status, content_schedule,
			subscriber_count, is_verified, is_default, created_at, updated_at
		FROM channels WHERE user_id = $1 AND is_default = TRUE LIMIT 1
	`, userID).Scan(
		&ch.ID, &ch.UserID, &ch.Handle, &ch.Name, &ch.Description, &ch.AvatarMediaID, &ch.BannerMediaID,
		&ch.Category, &ch.Country, &ch.Language, &ch.ContactEmail, &ch.CollabStatus, &ch.ContentSchedule,
		&ch.SubscriberCount, &ch.IsVerified, &ch.IsDefault, &ch.CreatedAt, &ch.UpdatedAt,
	)
	if err == nil {
		// Default channel already exists.
		result.Channel = ch
	} else if errors.Is(err, pgx.ErrNoRows) {
		// Create default channel.
		channelHandle := result.AccountHandle

		// Check if account handle is already used by another channel.
		var handleTaken bool
		err = tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM handles WHERE handle = $1 AND owner_type = 'channel')`,
			channelHandle,
		).Scan(&handleTaken)
		if err != nil {
			return nil, fmt.Errorf("check channel handle: %w", err)
		}
		if handleTaken {
			channelHandle = channelHandle + "_ch"
		}

		channelName := displayName
		if channelName == "" {
			channelName = "My Channel"
		}

		now := time.Now()
		ch = Channel{
			ID:        uuid.New(),
			UserID:    userID,
			Handle:    channelHandle,
			Name:      channelName,
			IsDefault: true,
			CreatedAt: now,
			UpdatedAt: now,
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO channels (id, user_id, handle, name, description, avatar_media_id, banner_media_id,
				category, country, language, contact_email, collab_status, content_schedule,
				subscriber_count, is_verified, is_default, created_at, updated_at)
			VALUES ($1, $2, $3, $4, '', NULL, NULL, '', '', '', '', 'closed', '', 0, FALSE, TRUE, $5, $5)
		`, ch.ID, userID, channelHandle, ch.Name, now)
		if err != nil {
			return nil, fmt.Errorf("create default channel: %w", err)
		}

		// Register channel handle (only if different from account handle).
		if channelHandle != result.AccountHandle {
			_, err = tx.Exec(ctx,
				`INSERT INTO handles (handle, owner_type, owner_id) VALUES ($1, 'channel', $2)`,
				channelHandle, ch.ID,
			)
			if err != nil {
				return nil, fmt.Errorf("reserve channel handle: %w", err)
			}
		}

		// Add owner as channel member.
		_, err = tx.Exec(ctx,
			`INSERT INTO channel_members (channel_id, user_id, role) VALUES ($1, $2, 'owner')`,
			ch.ID, userID,
		)
		if err != nil {
			return nil, fmt.Errorf("add channel owner: %w", err)
		}

		result.Channel = ch
		result.WasNewChannel = true
	} else {
		return nil, fmt.Errorf("query default channel: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	return result, nil
}
