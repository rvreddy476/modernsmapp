package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/atpost/post-service/internal/store/postgres"
)

// Founder rule (2026-10-21): every account has a channel from the moment it
// is created — named after the person, with their username as the handle (a
// free variant when that handle is taken). The owner renames it and changes
// the handle any time; handles stay unique. Created from UserRegistered, and
// lazily wherever a channel is needed, so an account whose event was missed
// (or that predates the rule) is never told to "create a channel first".

// defaultChannelHandleAttempts bounds the retry when another account claims
// the chosen handle between the lookup and the insert.
const defaultChannelHandleAttempts = 3

// DefaultChannelName is the name a new account's channel starts with: the
// display name, else the username, else a generic name, cut to the limit.
func DefaultChannelName(displayName, username string) string {
	for _, candidate := range []string{displayName, username} {
		candidate = strings.Join(strings.Fields(candidate), " ")
		if utf8.RuneCountInString(candidate) > MaxChannelNameRunes {
			candidate = strings.TrimSpace(string([]rune(candidate)[:MaxChannelNameRunes]))
		}
		if name, err := NormalizeChannelName(candidate); err == nil {
			return name
		}
	}
	return "My channel"
}

// EnsureDefaultChannel returns the account's channel, creating the default
// one when it has none. created reports whether this call made it.
// Idempotent and race-safe: a concurrent create for the same account (the
// event and a lazy path at once) resolves to the one row the unique index
// kept.
func (s *Service) EnsureDefaultChannel(ctx context.Context, userID uuid.UUID, displayName, username string) (ch *postgres.Channel, created bool, err error) {
	if s.channels == nil {
		return nil, false, errors.New("channel store not configured")
	}
	if userID == uuid.Nil {
		return nil, false, errors.New("default channel: nil user id")
	}
	if existing, err := s.channels.GetChannelByUserID(ctx, userID); err != nil {
		return nil, false, err
	} else if existing != nil {
		return existing, false, nil
	}

	name := DefaultChannelName(displayName, username)
	seed := username
	if strings.TrimSpace(seed) == "" {
		seed = displayName
	}
	base := SlugifyHandle(seed)
	for attempt := 0; attempt < defaultChannelHandleAttempts; attempt++ {
		handle, err := s.freeHandle(ctx, base)
		if err != nil {
			return nil, false, err
		}
		row := &postgres.Channel{UserID: userID, Name: name, Handle: handle}
		err = s.channels.CreateChannel(ctx, row)
		switch {
		case err == nil:
			slog.InfoContext(ctx, "default channel created", "user_id", userID, "handle", handle)
			return row, true, nil
		case errors.Is(err, postgres.ErrChannelExists):
			existing, getErr := s.channels.GetChannelByUserID(ctx, userID)
			if getErr != nil {
				return nil, false, getErr
			}
			if existing == nil {
				return nil, false, fmt.Errorf("default channel: exists but not found for %s", userID)
			}
			return existing, false, nil
		case errors.Is(err, postgres.ErrHandleTaken):
			continue // claimed in between; pick the next free variant
		default:
			return nil, false, err
		}
	}
	return nil, false, fmt.Errorf("default channel: no free handle for %q after %d attempts", base, defaultChannelHandleAttempts)
}

// ensureChannelFromProfile is the lazy path: seeds the default channel from
// the profile service (username, display name). The lookup is best-effort;
// without it the channel is still created, from a generic seed.
func (s *Service) ensureChannelFromProfile(ctx context.Context, userID uuid.UUID) (*postgres.Channel, error) {
	var displayName, username string
	if s.profileServiceURL != "" {
		profiles, err := s.fetchCommentProfiles(ctx, &userID, []string{userID.String()})
		if err != nil {
			slog.WarnContext(ctx, "default channel: profile lookup skipped", "user_id", userID, "err", err)
		} else if p, ok := profiles[userID]; ok {
			displayName, username = p.DisplayName, p.Username
		}
	}
	ch, _, err := s.EnsureDefaultChannel(ctx, userID, displayName, username)
	return ch, err
}
