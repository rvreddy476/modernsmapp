package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ModuleProfile represents a per-module profile override (postbook/posttube/postgram).
// Media (avatar, banner, watermark) is managed via owner_media_slots in the media service.
type ModuleProfile struct {
	ID                uuid.UUID       `json:"id"`
	UserID            uuid.UUID       `json:"user_id"`
	Module            string          `json:"module"`
	UseGlobalIdentity bool            `json:"use_global_identity"`
	NameOverride      *string         `json:"name_override,omitempty"`
	Links             json.RawMessage `json:"links"`
	Defaults          json.RawMessage `json:"defaults"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// HandleHistoryEntry represents a username change record.
type HandleHistoryEntry struct {
	ID            uuid.UUID `json:"id"`
	UserID        uuid.UUID `json:"user_id"`
	OldUsername    string    `json:"old_username"`
	NewUsername    string    `json:"new_username"`
	ChangedAt     time.Time `json:"changed_at"`
	CooldownUntil time.Time `json:"cooldown_until"`
}

// UpsertModuleProfileParams groups fields for creating/updating a module profile.
// Media (avatar, banner, watermark) is managed via owner_media_slots in the media service.
type UpsertModuleProfileParams struct {
	UseGlobalIdentity *bool           `json:"use_global_identity,omitempty"`
	NameOverride      *string         `json:"name_override,omitempty"`
	Links             json.RawMessage `json:"links,omitempty"`
	Defaults          json.RawMessage `json:"defaults,omitempty"`
}

const allModuleProfileCols = `id, user_id, module, use_global_identity, name_override,
	links, defaults, created_at, updated_at`

func scanModuleProfile(row pgx.Row) (*ModuleProfile, error) {
	var mp ModuleProfile
	err := row.Scan(
		&mp.ID, &mp.UserID, &mp.Module, &mp.UseGlobalIdentity, &mp.NameOverride,
		&mp.Links, &mp.Defaults,
		&mp.CreatedAt, &mp.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &mp, nil
}

// GetModuleProfile returns a single module profile for a user+module.
func (s *Store) GetModuleProfile(ctx context.Context, userID uuid.UUID, module string) (*ModuleProfile, error) {
	return scanModuleProfile(s.db.QueryRow(ctx,
		`SELECT `+allModuleProfileCols+` FROM profile.module_profiles WHERE user_id = $1 AND module = $2`,
		userID, module,
	))
}

// GetModuleProfiles returns all module profiles for a user.
func (s *Store) GetModuleProfiles(ctx context.Context, userID uuid.UUID) ([]ModuleProfile, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+allModuleProfileCols+` FROM profile.module_profiles WHERE user_id = $1 ORDER BY module`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var profiles []ModuleProfile
	for rows.Next() {
		var mp ModuleProfile
		if err := rows.Scan(
			&mp.ID, &mp.UserID, &mp.Module, &mp.UseGlobalIdentity, &mp.NameOverride,
			&mp.Links, &mp.Defaults,
			&mp.CreatedAt, &mp.UpdatedAt,
		); err != nil {
			return nil, err
		}
		profiles = append(profiles, mp)
	}
	return profiles, rows.Err()
}

// UpsertModuleProfile creates or updates a module profile.
func (s *Store) UpsertModuleProfile(ctx context.Context, userID uuid.UUID, module string, params UpsertModuleProfileParams) (*ModuleProfile, error) {
	useGlobal := true
	if params.UseGlobalIdentity != nil {
		useGlobal = *params.UseGlobalIdentity
	}

	links := json.RawMessage("[]")
	if params.Links != nil {
		links = params.Links
	}
	defaults := json.RawMessage("{}")
	if params.Defaults != nil {
		defaults = params.Defaults
	}

	return scanModuleProfile(s.db.QueryRow(ctx, `
		INSERT INTO profile.module_profiles
			(id, user_id, module, use_global_identity, name_override,
			 links, defaults, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, NOW(), NOW())
		ON CONFLICT (user_id, module) DO UPDATE SET
			use_global_identity = COALESCE($3, profile.module_profiles.use_global_identity),
			name_override = $4,
			links = COALESCE($5, profile.module_profiles.links),
			defaults = COALESCE($6, profile.module_profiles.defaults),
			updated_at = NOW()
		RETURNING `+allModuleProfileCols,
		userID, module, useGlobal, params.NameOverride, links, defaults,
	))
}

// DeleteModuleProfile removes a module profile row.
func (s *Store) DeleteModuleProfile(ctx context.Context, userID uuid.UUID, module string) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM profile.module_profiles WHERE user_id = $1 AND module = $2`,
		userID, module,
	)
	return err
}

// ---------------------------------------------------------------
// Handle History
// ---------------------------------------------------------------

// InsertHandleHistory records a username change.
func (s *Store) InsertHandleHistory(ctx context.Context, userID uuid.UUID, oldUsername, newUsername string) (*HandleHistoryEntry, error) {
	var h HandleHistoryEntry
	err := s.db.QueryRow(ctx, `
		INSERT INTO profile.handle_history (id, user_id, old_username, new_username, changed_at, cooldown_until)
		VALUES (gen_random_uuid(), $1, $2, $3, NOW(), NOW() + INTERVAL '30 days')
		RETURNING id, user_id, old_username, new_username, changed_at, cooldown_until
	`, userID, oldUsername, newUsername).Scan(
		&h.ID, &h.UserID, &h.OldUsername, &h.NewUsername, &h.ChangedAt, &h.CooldownUntil,
	)
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// GetLatestHandleChange returns the most recent handle change for a user.
func (s *Store) GetLatestHandleChange(ctx context.Context, userID uuid.UUID) (*HandleHistoryEntry, error) {
	var h HandleHistoryEntry
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, old_username, new_username, changed_at, cooldown_until
		FROM profile.handle_history
		WHERE user_id = $1
		ORDER BY changed_at DESC
		LIMIT 1
	`, userID).Scan(&h.ID, &h.UserID, &h.OldUsername, &h.NewUsername, &h.ChangedAt, &h.CooldownUntil)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &h, nil
}

// ResolveHandle looks up the current user for an old username (for redirect support).
// Returns the user_id of the most recent change that used this old_username, if within 90 days.
func (s *Store) ResolveHandle(ctx context.Context, oldUsername string) (*uuid.UUID, *string, error) {
	var userID uuid.UUID
	var newUsername string
	err := s.db.QueryRow(ctx, `
		SELECT user_id, new_username
		FROM profile.handle_history
		WHERE old_username = $1 AND changed_at > NOW() - INTERVAL '90 days'
		ORDER BY changed_at DESC
		LIMIT 1
	`, oldUsername).Scan(&userID, &newUsername)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	return &userID, &newUsername, nil
}

// GetHandleHistory returns the handle change history for a user.
func (s *Store) GetHandleHistory(ctx context.Context, userID uuid.UUID, limit, offset int) ([]HandleHistoryEntry, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, old_username, new_username, changed_at, cooldown_until
		FROM profile.handle_history
		WHERE user_id = $1
		ORDER BY changed_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []HandleHistoryEntry
	for rows.Next() {
		var h HandleHistoryEntry
		if err := rows.Scan(&h.ID, &h.UserID, &h.OldUsername, &h.NewUsername, &h.ChangedAt, &h.CooldownUntil); err != nil {
			return nil, err
		}
		entries = append(entries, h)
	}
	return entries, rows.Err()
}
