package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// User represents a core user record (no profile data).
type User struct {
	ID         uuid.UUID `json:"id"`
	Status     string    `json:"status"`
	IsVerified bool      `json:"is_verified"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// UserSettings holds a user's privacy settings (messaging/privacy spec v2 §5.1).
//
// AccountVisibility / AllowMessagesFrom / AllowCommentsFrom are the legacy
// pre-spec columns, retained so existing callers (the web settings page) keep
// working. WhoCanMessage is the authoritative field the permission resolver
// reads — AllowMessagesFrom is no longer consulted for messaging decisions.
type UserSettings struct {
	UserID            uuid.UUID `json:"user_id"`
	AccountVisibility string    `json:"account_visibility"`
	AllowMessagesFrom string    `json:"allow_messages_from"`
	AllowCommentsFrom string    `json:"allow_comments_from"`

	WhoCanMessage                 string `json:"who_can_message"`
	WhoCanSendConnectionRequest   string `json:"who_can_send_connection_request"`
	WhoCanCall                    string `json:"who_can_call"`
	WhoCanAddToGroups             string `json:"who_can_add_to_groups"`
	WhoCanSeeOnlineStatus         string `json:"who_can_see_online_status"`
	WhoCanSeeReadReceipts         string `json:"who_can_see_read_receipts"`
	WhoCanSeeLastSeen             string `json:"who_can_see_last_seen"`
	WhoCanSeeProfilePhoto         string `json:"who_can_see_profile_photo"`
	AllowPhoneDiscovery           bool   `json:"allow_phone_discovery"`
	AllowContactSyncMatch         bool   `json:"allow_contact_sync_match"`
	DiscoverableByPhoneToContacts bool   `json:"discoverable_by_phone_to_contacts"`
	StrictPrivacyMode             bool   `json:"strict_privacy_mode"`
	BlockUnknownCalls             bool   `json:"block_unknown_calls"`
	AutoFilterAbusiveContent      bool   `json:"auto_filter_abusive_content"`
	Under18Mode                   bool   `json:"under_18_mode"`

	// Trusted Circle per-feature toggles (friends-sheets spec §3.3).
	TcCloseFriendsPosts bool `json:"tc_close_friends_posts"`
	TcLocationPings     bool `json:"tc_location_pings"`
	TcAfterHoursPosts   bool `json:"tc_after_hours_posts"`
	TcAudioRoomInvite   bool `json:"tc_audio_room_invite"`

	PrivacyVersion int `json:"privacy_version"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// userSettingsColumns is the column list shared by GetSettings and the
// UpdateSettings RETURNING clause, in scanUserSettings order.
const userSettingsColumns = `user_id, account_visibility, allow_messages_from, allow_comments_from,
	who_can_message, who_can_send_connection_request, who_can_call, who_can_add_to_groups,
	who_can_see_online_status, who_can_see_read_receipts, who_can_see_last_seen, who_can_see_profile_photo,
	allow_phone_discovery, allow_contact_sync_match, discoverable_by_phone_to_contacts,
	strict_privacy_mode, block_unknown_calls, auto_filter_abusive_content, under_18_mode,
	tc_close_friends_posts, tc_location_pings, tc_after_hours_posts, tc_audio_room_invite,
	privacy_version, created_at, updated_at`

// scanUserSettings scans a row matching userSettingsColumns into us.
func scanUserSettings(row pgx.Row, us *UserSettings) error {
	return row.Scan(
		&us.UserID, &us.AccountVisibility, &us.AllowMessagesFrom, &us.AllowCommentsFrom,
		&us.WhoCanMessage, &us.WhoCanSendConnectionRequest, &us.WhoCanCall, &us.WhoCanAddToGroups,
		&us.WhoCanSeeOnlineStatus, &us.WhoCanSeeReadReceipts, &us.WhoCanSeeLastSeen, &us.WhoCanSeeProfilePhoto,
		&us.AllowPhoneDiscovery, &us.AllowContactSyncMatch, &us.DiscoverableByPhoneToContacts,
		&us.StrictPrivacyMode, &us.BlockUnknownCalls, &us.AutoFilterAbusiveContent, &us.Under18Mode,
		&us.TcCloseFriendsPosts, &us.TcLocationPings, &us.TcAfterHoursPosts, &us.TcAudioRoomInvite,
		&us.PrivacyVersion, &us.CreatedAt, &us.UpdatedAt,
	)
}

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// CreateUser creates a core user record with default settings (called by the
// event consumer). New users get the strict spec defaults; a minor (under18)
// additionally locks down calls, group-adds and profile-photo visibility per
// the Strict Privacy Mode rules (spec §5.4).
func (s *Store) CreateUser(ctx context.Context, id uuid.UUID, under18 bool) error {
	now := time.Now()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO usr.users (id, status, is_verified, created_at, updated_at)
		VALUES ($1, 'active', false, $2, $2)
		ON CONFLICT (id) DO NOTHING
	`, id, now)
	if err != nil {
		return err
	}

	whoCanCall, whoCanAddToGroups, whoCanSeeProfilePhoto := "connections_only", "connections_only", "everyone"
	if under18 {
		whoCanCall, whoCanAddToGroups, whoCanSeeProfilePhoto = "no_one", "no_one", "connections_only"
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO usr.user_settings
			(user_id, account_visibility, allow_messages_from, allow_comments_from,
			 under_18_mode, who_can_call, who_can_add_to_groups, who_can_see_profile_photo,
			 created_at, updated_at)
		VALUES ($1, 'public', 'everyone', 'everyone', $3, $4, $5, $6, $2, $2)
		ON CONFLICT (user_id) DO NOTHING
	`, id, now, under18, whoCanCall, whoCanAddToGroups, whoCanSeeProfilePhoto)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// GetUser returns a core user record.
func (s *Store) GetUser(ctx context.Context, id uuid.UUID) (*User, error) {
	var u User
	err := s.db.QueryRow(ctx, `
		SELECT id, status, is_verified, created_at, updated_at
		FROM usr.users
		WHERE id = $1
	`, id).Scan(&u.ID, &u.Status, &u.IsVerified, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

// GetSettings returns user privacy settings.
func (s *Store) GetSettings(ctx context.Context, userID uuid.UUID) (*UserSettings, error) {
	var us UserSettings
	row := s.db.QueryRow(ctx, `SELECT `+userSettingsColumns+` FROM usr.user_settings WHERE user_id = $1`, userID)
	if err := scanUserSettings(row, &us); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &us, nil
}

// ListUsers returns all active users with pagination.
func (s *Store) ListUsers(ctx context.Context, limit, offset int) ([]User, int, error) {
	var total int
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM usr.users WHERE status = 'active'`).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := s.db.Query(ctx, `
		SELECT id, status, is_verified, created_at, updated_at
		FROM usr.users
		WHERE status = 'active'
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Status, &u.IsVerified, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return users, total, nil
}

// UpdateSettings writes the full settings row and bumps privacy_version.
//
// privacy_version is incremented on every update so downstream permission
// caches (spec §6.2) can be keyed by it — a privacy change invalidates a
// stale cache on the next read without an explicit delete.
func (s *Store) UpdateSettings(ctx context.Context, settings *UserSettings) (*UserSettings, error) {
	var us UserSettings
	row := s.db.QueryRow(ctx, `
		UPDATE usr.user_settings SET
			account_visibility = $2,
			allow_messages_from = $3,
			allow_comments_from = $4,
			who_can_message = $5,
			who_can_send_connection_request = $6,
			who_can_call = $7,
			who_can_add_to_groups = $8,
			who_can_see_online_status = $9,
			who_can_see_read_receipts = $10,
			who_can_see_last_seen = $11,
			who_can_see_profile_photo = $12,
			allow_phone_discovery = $13,
			allow_contact_sync_match = $14,
			discoverable_by_phone_to_contacts = $15,
			strict_privacy_mode = $16,
			block_unknown_calls = $17,
			auto_filter_abusive_content = $18,
			under_18_mode = $19,
			tc_close_friends_posts = $20,
			tc_location_pings = $21,
			tc_after_hours_posts = $22,
			tc_audio_room_invite = $23,
			privacy_version = privacy_version + 1,
			updated_at = NOW()
		WHERE user_id = $1
		RETURNING `+userSettingsColumns,
		settings.UserID, settings.AccountVisibility, settings.AllowMessagesFrom, settings.AllowCommentsFrom,
		settings.WhoCanMessage, settings.WhoCanSendConnectionRequest, settings.WhoCanCall, settings.WhoCanAddToGroups,
		settings.WhoCanSeeOnlineStatus, settings.WhoCanSeeReadReceipts, settings.WhoCanSeeLastSeen, settings.WhoCanSeeProfilePhoto,
		settings.AllowPhoneDiscovery, settings.AllowContactSyncMatch, settings.DiscoverableByPhoneToContacts,
		settings.StrictPrivacyMode, settings.BlockUnknownCalls, settings.AutoFilterAbusiveContent, settings.Under18Mode,
		settings.TcCloseFriendsPosts, settings.TcLocationPings, settings.TcAfterHoursPosts, settings.TcAudioRoomInvite,
	)
	if err := scanUserSettings(row, &us); err != nil {
		return nil, err
	}
	return &us, nil
}
