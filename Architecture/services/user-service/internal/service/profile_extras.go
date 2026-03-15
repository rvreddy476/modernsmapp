package service

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/user-service/internal/store"
	"github.com/google/uuid"
)

const maxPins = 3

// --- Profile Pins ---

// PinContent pins a content item for a user, enforcing a max of 3 pins per user.
func (s *Service) PinContent(ctx context.Context, userID uuid.UUID, contentType, contentID string, order int) (*store.ProfilePin, error) {
	cid, err := uuid.Parse(contentID)
	if err != nil {
		return nil, fmt.Errorf("invalid content_id: %w", err)
	}

	count, err := s.store.CountPins(ctx, userID)
	if err != nil {
		return nil, err
	}
	if count >= maxPins {
		return nil, fmt.Errorf("MAX_PINS_REACHED")
	}

	pin := &store.ProfilePin{
		UserID:      userID,
		ContentType: contentType,
		ContentID:   cid,
		PinOrder:    order,
	}
	if err := s.store.PinContent(ctx, pin); err != nil {
		return nil, err
	}
	return pin, nil
}

// UnpinContent removes a pin.
func (s *Service) UnpinContent(ctx context.Context, userID uuid.UUID, contentType, contentID string) error {
	return s.store.UnpinContent(ctx, userID, contentType, contentID)
}

// GetProfilePins returns all pins for a user.
func (s *Service) GetProfilePins(ctx context.Context, userID uuid.UUID) ([]store.ProfilePin, error) {
	return s.store.GetPins(ctx, userID)
}

// --- Portfolio ---

// AddPortfolioItem creates a new portfolio entry for a user.
func (s *Service) AddPortfolioItem(ctx context.Context, userID uuid.UUID, title, desc, itemType, url string, mediaID *uuid.UUID, order int) (*store.PortfolioItem, error) {
	item := &store.PortfolioItem{
		UserID:      userID,
		Title:       title,
		Description: desc,
		Type:        itemType,
		URL:         url,
		MediaID:     mediaID,
		SortOrder:   order,
	}
	if err := s.store.CreatePortfolioItem(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

// UpdatePortfolioItem updates an existing portfolio item (must be owned by the user).
func (s *Service) UpdatePortfolioItem(ctx context.Context, item *store.PortfolioItem) error {
	return s.store.UpdatePortfolioItem(ctx, item)
}

// DeletePortfolioItem removes a portfolio item if owned by userID.
func (s *Service) DeletePortfolioItem(ctx context.Context, id, userID uuid.UUID) error {
	return s.store.DeletePortfolioItem(ctx, id, userID)
}

// GetPortfolio returns all portfolio items for a user.
func (s *Service) GetPortfolio(ctx context.Context, userID uuid.UUID) ([]store.PortfolioItem, error) {
	return s.store.GetPortfolio(ctx, userID)
}

// --- QR Code ---

// GetOrCreateQRCode returns an existing QR code or creates one for the user.
func (s *Service) GetOrCreateQRCode(ctx context.Context, userID uuid.UUID, handle string) (*store.ProfileQRCode, error) {
	qr, err := s.store.GetQRCode(ctx, userID)
	if err != nil {
		return nil, err
	}
	if qr != nil {
		return qr, nil
	}

	// Create new QR code
	qr = &store.ProfileQRCode{
		UserID:    userID,
		QRUrl:     fmt.Sprintf("https://atpost.me/qr/%s", userID),
		ShortLink: fmt.Sprintf("https://atpost.me/%s", handle),
	}
	if err := s.store.UpsertQRCode(ctx, qr); err != nil {
		return nil, err
	}
	return qr, nil
}

// TrackQRScan increments the scan count for a user's QR code.
func (s *Service) TrackQRScan(ctx context.Context, userID uuid.UUID) error {
	return s.store.IncrementQRScan(ctx, userID)
}

// --- Digital Wellbeing ---

// GetWellbeing returns a user's wellbeing settings (or defaults if not set).
func (s *Service) GetWellbeing(ctx context.Context, userID uuid.UUID) (*store.DigitalWellbeing, error) {
	return s.store.GetWellbeing(ctx, userID)
}

// UpdateWellbeing saves a user's wellbeing settings.
func (s *Service) UpdateWellbeing(ctx context.Context, w *store.DigitalWellbeing) error {
	return s.store.UpsertWellbeing(ctx, w)
}

// LogScreenTime records screen time for the current day.
func (s *Service) LogScreenTime(ctx context.Context, userID uuid.UUID, minutes, sessions int) error {
	return s.store.UpsertScreenTimeLog(ctx, userID, time.Now().UTC(), minutes, sessions)
}

// GetScreenTime returns the last 30 days of screen-time logs for a user.
func (s *Service) GetScreenTime(ctx context.Context, userID uuid.UUID) ([]store.ScreenTimeLog, error) {
	return s.store.GetScreenTimeLog(ctx, userID, 30)
}
