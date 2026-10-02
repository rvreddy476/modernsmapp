// Who liked you (mechanic M4, DATING_LIKED_YOU_GATE_ENABLED).
//
// The people who sparked the caller, as a grid. With the gate on, only a pass
// holder sees who they are. Everyone else gets the count and blurred cards,
// and the server sends nothing that identifies the sender: no user id, no
// name, no note, no photo id and no full-image URL. The one image a locked
// card shows is served by GET /liked-you/:sparkId/photo, which only ever
// redirects to the server-blurred variant.
//
// The same rule holds on every other path to the sender: the incoming spark
// list is redacted the same way, accepting a locked spark is refused (a match
// would reveal them), the person card no longer opens because of an incoming
// spark alone, and the spark notification carries no actor. A free user
// still meets the sender like anyone else, in the deck.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// ErrLikedYouLocked: a locked spark cannot be accepted. Maps to 403
// LIKED_YOU_LOCKED.
var ErrLikedYouLocked = errors.New("forbidden: a pass is needed to see who sparked you")

// LikedYouPhotoPath is the route of a locked card's blurred image.
func LikedYouPhotoPath(sparkID uuid.UUID) string {
	return "/v1/dating/liked-you/" + sparkID.String() + "/photo"
}

// LikedYouItem is one card of the grid. A locked card carries only SparkID,
// Super, CreatedAt and the blurred PhotoURL.
type LikedYouItem struct {
	SparkID   uuid.UUID `json:"spark_id"`
	Super     bool      `json:"super,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// PhotoURL: locked, the blurred route above; unlocked, the person's
	// primary photo route for this viewer. Omitted when there is none.
	PhotoURL string `json:"photo_url,omitempty"`
	// Person and Note are present only when unlocked.
	Person *PersonCard `json:"person,omitempty"`
	Note   *string     `json:"note,omitempty"`
	// NoteHidden (M13): the recipient's filter hides the note.
	NoteHidden string `json:"note_hidden,omitempty"`
}

// LikedYouResponse is GET /v1/dating/liked-you.
type LikedYouResponse struct {
	// Total counts every visible incoming spark, across pages.
	Total    int            `json:"total"`
	Unlocked bool           `json:"unlocked"`
	Items    []LikedYouItem `json:"items"`
}

// LockedIncomingSpark is an incoming spark as GET /sparks/incoming shows it
// to a caller without the entitlement while the gate is on.
type LockedIncomingSpark struct {
	ID        uuid.UUID `json:"id"`
	Super     bool      `json:"super,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	PhotoURL  string    `json:"photo_url"`
	Locked    bool      `json:"locked"`
}

// likedYouUnlocked reports whether the caller may see who sparked them: the
// gate is off, or they hold an unexpired pass. A failed pass lookup locks —
// an outage must never reveal a sender.
func (s *Service) likedYouUnlocked(ctx context.Context, userID uuid.UUID) bool {
	if !s.mechanics.LikedYouGate {
		return true
	}
	premium, err := s.store.IsPremium(ctx, userID)
	if err != nil {
		slog.Warn("liked you: premium lookup failed; keeping the grid locked", "user_id", userID, "error", err)
		return false
	}
	return premium
}

// LikedYou returns the caller's grid.
func (s *Service) LikedYou(ctx context.Context, userID uuid.UUID, limit, offset int) (*LikedYouResponse, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id required")
	}
	total, err := s.store.CountIncomingSparks(ctx, userID)
	if err != nil {
		return nil, err
	}
	sparks, err := s.store.ListIncomingSparks(ctx, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	out := &LikedYouResponse{Total: total, Unlocked: s.likedYouUnlocked(ctx, userID), Items: make([]LikedYouItem, 0, len(sparks))}
	if !out.Unlocked {
		for _, sp := range sparks {
			out.Items = append(out.Items, LikedYouItem{
				SparkID: sp.ID, Super: sp.IsSuper, CreatedAt: sp.CreatedAt, PhotoURL: LikedYouPhotoPath(sp.ID),
			})
		}
		return out, nil
	}
	s.markHiddenNotes(ctx, userID, sparks)
	for _, sp := range s.decorateIncomingSparks(ctx, userID, sparks) {
		item := LikedYouItem{SparkID: sp.ID, Super: sp.IsSuper, CreatedAt: sp.CreatedAt, Person: sp.Person, Note: sp.Note, NoteHidden: sp.NoteHidden}
		if sp.Person != nil {
			item.PhotoURL = sp.Person.PrimaryPhotoURL
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// IncomingSparksView is GET /sparks/incoming: the decorated list, or, for a
// caller the gate locks, the same sparks with everything identifying the
// sender removed.
func (s *Service) IncomingSparksView(ctx context.Context, userID uuid.UUID, limit, offset int) (any, error) {
	if s.likedYouUnlocked(ctx, userID) {
		return s.ListIncomingSparks(ctx, userID, limit, offset)
	}
	sparks, err := s.store.ListIncomingSparks(ctx, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]LockedIncomingSpark, 0, len(sparks))
	for _, sp := range sparks {
		out = append(out, LockedIncomingSpark{ID: sp.ID, Super: sp.IsSuper, CreatedAt: sp.CreatedAt, PhotoURL: LikedYouPhotoPath(sp.ID), Locked: true})
	}
	return out, nil
}

// LikedYouPhotoURL authorizes the recipient for the blurred image of one
// incoming spark's sender and returns media-service's short-lived signed URL
// for it. Always the blurred variant, whoever asks. Anyone but the
// recipient, a declined or blocked spark, or a gone sender is
// store.ErrPhotoNotFound.
func (s *Service) LikedYouPhotoURL(ctx context.Context, viewerID, sparkID uuid.UUID) (string, error) {
	sp, err := s.store.GetVisibleIncomingSpark(ctx, sparkID, viewerID)
	if err != nil {
		if errors.Is(err, store.ErrSparkNotFound) {
			return "", store.ErrPhotoNotFound
		}
		return "", err
	}
	photo, err := s.store.PrimaryApprovedPhoto(ctx, sp.FromUserID)
	if err != nil {
		return "", err
	}
	if s.mediaPhotos == nil {
		return "", fmt.Errorf("%w: no media photo client configured", ErrPhotoMediaUnavailable)
	}
	u, err := s.mediaPhotos.PhotoDeliveryURL(ctx, photo.MediaID, photo.UserID, PhotoVariantBlurred)
	if errors.Is(err, ErrPhotoMediaNotFound) || errors.Is(err, ErrPhotoMediaNotReady) {
		return "", store.ErrPhotoNotFound
	}
	return u, err
}
