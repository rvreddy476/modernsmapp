package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ProfilePin represents a pinned content item on a user's profile.
type ProfilePin struct {
	UserID      uuid.UUID `json:"user_id"`
	ContentType string    `json:"content_type"`
	ContentID   uuid.UUID `json:"content_id"`
	PinOrder    int       `json:"pin_order"`
	PinnedAt    time.Time `json:"pinned_at"`
}

// PortfolioItem represents a portfolio entry on a user's profile.
type PortfolioItem struct {
	ID          uuid.UUID  `json:"id"`
	UserID      uuid.UUID  `json:"user_id"`
	Title       string     `json:"title"`
	Description string     `json:"description,omitempty"`
	Type        string     `json:"type"`
	URL         string     `json:"url,omitempty"`
	MediaID     *uuid.UUID `json:"media_id,omitempty"`
	SortOrder   int        `json:"sort_order"`
	CreatedAt   time.Time  `json:"created_at"`
}

// ProfileQRCode represents a user's QR code for profile sharing.
type ProfileQRCode struct {
	UserID    uuid.UUID `json:"user_id"`
	QRUrl     string    `json:"qr_url"`
	ShortLink string    `json:"short_link"`
	ScanCount int64     `json:"scan_count"`
	CreatedAt time.Time `json:"created_at"`
}

// DigitalWellbeing stores a user's wellbeing/screen-time preferences.
type DigitalWellbeing struct {
	UserID            uuid.UUID  `json:"user_id"`
	DailyLimitMins    *int       `json:"daily_limit_mins,omitempty"`
	FocusModeEnabled  bool       `json:"focus_mode_enabled"`
	FocusModeUntil    *time.Time `json:"focus_mode_until,omitempty"`
	BedtimeStart      *string    `json:"bedtime_start,omitempty"`
	BedtimeEnd        *string    `json:"bedtime_end,omitempty"`
	NudgeIntervalMins int        `json:"nudge_interval_mins"`
	HideLikeCounts    bool       `json:"hide_like_counts"`
	DetoxModeUntil    *time.Time `json:"detox_mode_until,omitempty"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// ScreenTimeLog represents a daily screen-time record.
type ScreenTimeLog struct {
	ID           uuid.UUID `json:"id"`
	UserID       uuid.UUID `json:"user_id"`
	Date         time.Time `json:"date"`
	Minutes      int       `json:"minutes"`
	SessionCount int       `json:"session_count"`
}

// --- Profile Pins ---

// PinContent inserts or replaces a pin for a user.
func (s *Store) PinContent(ctx context.Context, pin *ProfilePin) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO profile_pins (user_id, content_type, content_id, pin_order, pinned_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (user_id, content_type, content_id)
		DO UPDATE SET pin_order = EXCLUDED.pin_order, pinned_at = NOW()
	`, pin.UserID, pin.ContentType, pin.ContentID, pin.PinOrder)
	return err
}

// UnpinContent removes a specific pin.
func (s *Store) UnpinContent(ctx context.Context, userID uuid.UUID, contentType, contentID string) error {
	cid, err := uuid.Parse(contentID)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `
		DELETE FROM profile_pins WHERE user_id = $1 AND content_type = $2 AND content_id = $3
	`, userID, contentType, cid)
	return err
}

// GetPins returns all pins for a user ordered by pin_order.
func (s *Store) GetPins(ctx context.Context, userID uuid.UUID) ([]ProfilePin, error) {
	rows, err := s.db.Query(ctx, `
		SELECT user_id, content_type, content_id, pin_order, pinned_at
		FROM profile_pins
		WHERE user_id = $1
		ORDER BY pin_order, pinned_at
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pins []ProfilePin
	for rows.Next() {
		var p ProfilePin
		if err := rows.Scan(&p.UserID, &p.ContentType, &p.ContentID, &p.PinOrder, &p.PinnedAt); err != nil {
			return nil, err
		}
		pins = append(pins, p)
	}
	return pins, rows.Err()
}

// CountPins returns the number of pins for a user.
func (s *Store) CountPins(ctx context.Context, userID uuid.UUID) (int, error) {
	var count int
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM profile_pins WHERE user_id = $1`, userID).Scan(&count)
	return count, err
}

// --- Portfolio ---

// CreatePortfolioItem inserts a new portfolio item.
func (s *Store) CreatePortfolioItem(ctx context.Context, item *PortfolioItem) error {
	item.ID = uuid.New()
	return s.db.QueryRow(ctx, `
		INSERT INTO portfolio_items (id, user_id, title, description, type, url, media_id, sort_order, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
		RETURNING created_at
	`, item.ID, item.UserID, item.Title, item.Description, item.Type, item.URL, item.MediaID, item.SortOrder,
	).Scan(&item.CreatedAt)
}

// UpdatePortfolioItem updates an existing portfolio item owned by the user.
func (s *Store) UpdatePortfolioItem(ctx context.Context, item *PortfolioItem) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE portfolio_items
		SET title = $1, description = $2, url = $3, sort_order = $4
		WHERE id = $5 AND user_id = $6
	`, item.Title, item.Description, item.URL, item.SortOrder, item.ID, item.UserID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// DeletePortfolioItem removes a portfolio item that belongs to userID.
func (s *Store) DeletePortfolioItem(ctx context.Context, id, userID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM portfolio_items WHERE id = $1 AND user_id = $2
	`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// GetPortfolio returns all portfolio items for a user ordered by sort_order.
func (s *Store) GetPortfolio(ctx context.Context, userID uuid.UUID) ([]PortfolioItem, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, title, COALESCE(description,''), type, COALESCE(url,''), media_id, sort_order, created_at
		FROM portfolio_items
		WHERE user_id = $1
		ORDER BY sort_order, created_at
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []PortfolioItem
	for rows.Next() {
		var it PortfolioItem
		if err := rows.Scan(&it.ID, &it.UserID, &it.Title, &it.Description, &it.Type, &it.URL, &it.MediaID, &it.SortOrder, &it.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// --- QR Codes ---

// UpsertQRCode creates or replaces a user's QR code entry.
func (s *Store) UpsertQRCode(ctx context.Context, qr *ProfileQRCode) error {
	return s.db.QueryRow(ctx, `
		INSERT INTO profile_qr_codes (user_id, qr_url, short_link, scan_count, created_at)
		VALUES ($1, $2, $3, 0, NOW())
		ON CONFLICT (user_id) DO UPDATE
		SET qr_url = EXCLUDED.qr_url, short_link = EXCLUDED.short_link
		RETURNING scan_count, created_at
	`, qr.UserID, qr.QRUrl, qr.ShortLink).Scan(&qr.ScanCount, &qr.CreatedAt)
}

// GetQRCode returns the QR code record for a user, or nil if none exists.
func (s *Store) GetQRCode(ctx context.Context, userID uuid.UUID) (*ProfileQRCode, error) {
	var qr ProfileQRCode
	err := s.db.QueryRow(ctx, `
		SELECT user_id, qr_url, short_link, scan_count, created_at
		FROM profile_qr_codes WHERE user_id = $1
	`, userID).Scan(&qr.UserID, &qr.QRUrl, &qr.ShortLink, &qr.ScanCount, &qr.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &qr, nil
}

// IncrementQRScan atomically increments the scan_count for a user's QR code.
func (s *Store) IncrementQRScan(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE profile_qr_codes SET scan_count = scan_count + 1 WHERE user_id = $1
	`, userID)
	return err
}

// --- Digital Wellbeing ---

// UpsertWellbeing creates or replaces a user's digital wellbeing settings.
func (s *Store) UpsertWellbeing(ctx context.Context, w *DigitalWellbeing) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO digital_wellbeing
			(user_id, daily_limit_mins, focus_mode_enabled, focus_mode_until,
			 bedtime_start, bedtime_end, nudge_interval_mins, hide_like_counts,
			 detox_mode_until, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			daily_limit_mins    = EXCLUDED.daily_limit_mins,
			focus_mode_enabled  = EXCLUDED.focus_mode_enabled,
			focus_mode_until    = EXCLUDED.focus_mode_until,
			bedtime_start       = EXCLUDED.bedtime_start,
			bedtime_end         = EXCLUDED.bedtime_end,
			nudge_interval_mins = EXCLUDED.nudge_interval_mins,
			hide_like_counts    = EXCLUDED.hide_like_counts,
			detox_mode_until    = EXCLUDED.detox_mode_until,
			updated_at          = NOW()
	`, w.UserID, w.DailyLimitMins, w.FocusModeEnabled, w.FocusModeUntil,
		w.BedtimeStart, w.BedtimeEnd, w.NudgeIntervalMins, w.HideLikeCounts,
		w.DetoxModeUntil)
	return err
}

// GetWellbeing returns a user's digital wellbeing settings, or a default if none set.
func (s *Store) GetWellbeing(ctx context.Context, userID uuid.UUID) (*DigitalWellbeing, error) {
	var w DigitalWellbeing
	err := s.db.QueryRow(ctx, `
		SELECT user_id, daily_limit_mins, focus_mode_enabled, focus_mode_until,
		       bedtime_start::TEXT, bedtime_end::TEXT,
		       nudge_interval_mins, hide_like_counts, detox_mode_until, updated_at
		FROM digital_wellbeing WHERE user_id = $1
	`, userID).Scan(
		&w.UserID, &w.DailyLimitMins, &w.FocusModeEnabled, &w.FocusModeUntil,
		&w.BedtimeStart, &w.BedtimeEnd,
		&w.NudgeIntervalMins, &w.HideLikeCounts, &w.DetoxModeUntil, &w.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return &DigitalWellbeing{UserID: userID, NudgeIntervalMins: 30}, nil
		}
		return nil, err
	}
	return &w, nil
}

// UpsertScreenTimeLog adds minutes/sessions to today's log using ON CONFLICT upsert.
func (s *Store) UpsertScreenTimeLog(ctx context.Context, userID uuid.UUID, date time.Time, minutes, sessions int) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO screen_time_log (user_id, date, minutes, session_count)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, date) DO UPDATE
		SET minutes       = screen_time_log.minutes + EXCLUDED.minutes,
		    session_count = screen_time_log.session_count + EXCLUDED.session_count
	`, userID, date, minutes, sessions)
	return err
}

// GetScreenTimeLog returns the last N days of screen time for a user.
func (s *Store) GetScreenTimeLog(ctx context.Context, userID uuid.UUID, days int) ([]ScreenTimeLog, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, date, minutes, session_count
		FROM screen_time_log
		WHERE user_id = $1 AND date >= CURRENT_DATE - $2::int
		ORDER BY date DESC
	`, userID, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []ScreenTimeLog
	for rows.Next() {
		var l ScreenTimeLog
		if err := rows.Scan(&l.ID, &l.UserID, &l.Date, &l.Minutes, &l.SessionCount); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}
