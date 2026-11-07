// post_pins.go pins a post to the top of its author's profile, up to
// MaxPinnedPosts. Moved out of post.go unchanged.

package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// TogglePin sets or unsets pinned status, enforcing max 3 pinned per author.
// MaxPinnedPosts is how many posts one author may pin to their profile.
const MaxPinnedPosts = 3

// ErrPinLimit refuses a pin past MaxPinnedPosts (409 PIN_LIMIT, details
// {max: 3}). Pinning a post that is already pinned is not counted.
var ErrPinLimit = errors.New("PIN_LIMIT")

// pinStore is the pin read and write; the Postgres store satisfies it.
type pinStore interface {
	CountPinnedByAuthor(ctx context.Context, authorID, exceptPostID uuid.UUID) (int, error)
	SetPinned(ctx context.Context, postID, authorID uuid.UUID, pinned bool) error
}

func (s *Service) pinReads() pinStore {
	if s.pins != nil {
		return s.pins
	}
	return s.pgStore
}

func (s *Service) TogglePin(ctx context.Context, postID, authorID uuid.UUID, pinned bool) error {
	pins := s.pinReads()
	if pinned {
		count, err := pins.CountPinnedByAuthor(ctx, authorID, postID)
		if err != nil {
			return err
		}
		if count >= MaxPinnedPosts {
			return ErrPinLimit
		}
	}
	return pins.SetPinned(ctx, postID, authorID, pinned)
}
