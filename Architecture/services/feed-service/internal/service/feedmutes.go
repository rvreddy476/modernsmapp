package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// /v1/feed/mute takes effect (audit L1, 2026-10-30).
//
// The route wrote feed_mutes and two Redis sets that nothing read, and
// answered {"ok":true} while the muted account or topic kept appearing.
// Now the active mutes are read once per request (feedMutes, memoised):
//
//   - a muted USER joins the block/mute set (resolveBlockedSetUncached), so
//     every surface drops them before windowing, as with a graph mute;
//   - a muted TOPIC or HASHTAG joins the viewer's filter keywords at the
//     hydration tail (applyKeywordHideFilter), matched as a whole word, so
//     "tag" hides "#tag" and the word "tag" in the caption.
//
// Both fail closed like the rest of those filters. A service whose store
// does not keep mutes (tests with fakes) has none to apply.

// feedMuteStore reads active /v1/feed/mute entries; the Postgres MetaStore
// implements it.
type feedMuteStore interface {
	ActiveFeedMutes(ctx context.Context, userID uuid.UUID) (users []uuid.UUID, words []string, err error)
}

type feedMuteSet struct {
	users []uuid.UUID
	words []string
}

// feedMutes returns the viewer's active mutes, once per request.
func (s *Service) feedMutes(ctx context.Context, viewerID uuid.UUID) (feedMuteSet, error) {
	store, ok := s.feedback.(feedMuteStore)
	if !ok {
		return feedMuteSet{}, nil
	}
	m := memoFrom(ctx)
	if m != nil {
		m.mu.Lock()
		set, ok := m.mutes[viewerID]
		m.mu.Unlock()
		if ok {
			return set, nil
		}
	}
	users, words, err := store.ActiveFeedMutes(ctx, viewerID)
	if err != nil {
		return feedMuteSet{}, err
	}
	set := feedMuteSet{users: users}
	for _, w := range words {
		if w = normalizeMuteWord(w); w != "" {
			set.words = append(set.words, w)
		}
	}
	if m != nil {
		m.mu.Lock()
		m.mutes[viewerID] = set
		m.mu.Unlock()
	}
	return set, nil
}

// normalizeMuteWord lowercases a topic / hashtag and strips a leading '#',
// the form the keyword matcher expects.
func normalizeMuteWord(w string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(w), "#"))
}

// ErrInvalidMute is a /v1/feed/mute request the service refuses; the HTTP
// layer answers it with a fixed 400 message (audit L8: the raw store error
// used to be returned to the client).
var ErrInvalidMute = errors.New("invalid mute target")

// maxMuteWordLen bounds a topic / hashtag mute.
const maxMuteWordLen = 64

// validateMuteTarget checks a mute before it is stored: a user mute names
// a user id, a topic or hashtag is 1..64 characters.
func validateMuteTarget(targetType, targetID string) error {
	switch targetType {
	case "user":
		if _, err := uuid.Parse(targetID); err != nil {
			return fmt.Errorf("%w: a user mute takes a user id", ErrInvalidMute)
		}
	case "topic", "hashtag":
		w := normalizeMuteWord(targetID)
		if w == "" || utf8.RuneCountInString(w) > maxMuteWordLen {
			return fmt.Errorf("%w: a %s must be 1 to %d characters", ErrInvalidMute, targetType, maxMuteWordLen)
		}
	default:
		return fmt.Errorf("%w: target_type must be user, topic or hashtag", ErrInvalidMute)
	}
	return nil
}

// muteExpiry rejects an expiry already in the past.
func muteExpiry(expiresAt *time.Time, now time.Time) error {
	if expiresAt != nil && !expiresAt.After(now) {
		return fmt.Errorf("%w: expires_at is in the past", ErrInvalidMute)
	}
	return nil
}
