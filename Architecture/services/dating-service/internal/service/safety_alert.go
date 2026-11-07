// Trusted-contact view of a panic alert (dating Android audit, 7 Nov).
//
// notification-service tells a trusted contact "someone who trusts you needs
// help" with a deep link that carries only the alert id. The app then reads
// GET /v1/dating/safety/alerts/:id, which answers only to a current trusted
// contact of the member, only inside the alert window, and includes the
// exact point only under the same per-contact consent rule the paging path
// uses (panicNotifyContact). Coordinates never travel in a push payload or
// sit in an inbox row.
package service

import (
	"context"
	"errors"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// DefaultPanicAlertWindow is how long after a panic its trusted contacts may
// open the alert.
const DefaultPanicAlertWindow = 24 * time.Hour

var (
	// ErrSafetyAlertNotFound: no such alert, or the caller is not one of
	// the member's trusted contacts. One refusal for both, so the route
	// never confirms that an alert exists to anyone else.
	ErrSafetyAlertNotFound = errors.New("safety alert not found")
	// ErrSafetyAlertExpired: the caller is a recipient but the alert window
	// has ended.
	ErrSafetyAlertExpired = errors.New("this safety alert has expired")
)

// SafetyAlertLocation is the member's exact point at the panic.
type SafetyAlertLocation struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// SafetyAlert is what one trusted contact sees of a panic.
//
// Location is the exact point only when the member consented to share it
// with THIS contact (store.TrustedContact.SharesPanicLocation), the incident
// has one, and it is not resolved; otherwise it is null and LocationShared
// is false. There is no coarse fallback: without consent the contact gets
// no location at all, exactly as when paging.
type SafetyAlert struct {
	AlertID        uuid.UUID            `json:"alert_id"`
	Status         string               `json:"status"`
	TriggeredAt    time.Time            `json:"triggered_at"`
	ExpiresAt      time.Time            `json:"expires_at"`
	LocationShared bool                 `json:"location_shared"`
	Location       *SafetyAlertLocation `json:"location"`
	// Person is the member's compact card; null when the contact may no
	// longer see them.
	Person *PersonCard `json:"person"`
}

// safetyAlertFor decides what recipient may see of inc, given the member's
// current trusted contacts (blocked pairs already left out by the store).
// The recipient check comes first so a stranger never learns whether an
// alert exists or has expired.
func safetyAlertFor(inc *store.PanicIncident, contacts []*store.TrustedContact, recipient uuid.UUID, now time.Time, window time.Duration) (*SafetyAlert, error) {
	if inc == nil || recipient == uuid.Nil || recipient == inc.UserID || inc.Anonymised || inc.SuspectedAbuse {
		return nil, ErrSafetyAlertNotFound
	}
	var tc *store.TrustedContact
	for _, c := range contacts {
		if c.ContactID == recipient {
			tc = c
			break
		}
	}
	if tc == nil {
		return nil, ErrSafetyAlertNotFound
	}
	expires := inc.CreatedAt.Add(window)
	if !now.Before(expires) {
		return nil, ErrSafetyAlertExpired
	}
	out := &SafetyAlert{AlertID: inc.ID, Status: inc.Status, TriggeredAt: inc.CreatedAt, ExpiresAt: expires}
	// The paging consent rule, read now, so a revoke since the panic holds.
	if p := panicNotifyContact(tc, inc); p.Latitude != nil && p.Longitude != nil && inc.Status != store.PanicStatusResolved {
		out.LocationShared = true
		out.Location = &SafetyAlertLocation{Latitude: *p.Latitude, Longitude: *p.Longitude}
	}
	return out, nil
}

// GetSafetyAlert returns alertID to recipientID when they are a current
// trusted contact of the member who pressed panic and the alert window is
// open (ErrSafetyAlertNotFound / ErrSafetyAlertExpired otherwise).
func (s *Service) GetSafetyAlert(ctx context.Context, recipientID, alertID uuid.UUID) (*SafetyAlert, error) {
	if recipientID == uuid.Nil || alertID == uuid.Nil {
		return nil, ErrSafetyAlertNotFound
	}
	inc, err := s.store.GetPanicIncident(ctx, alertID)
	if errors.Is(err, store.ErrPanicNotFound) {
		return nil, ErrSafetyAlertNotFound
	}
	if err != nil {
		return nil, err
	}
	contacts, err := s.store.ListTrustedContacts(ctx, inc.UserID)
	if err != nil {
		return nil, err
	}
	out, err := safetyAlertFor(inc, contacts, recipientID, time.Now(), s.safetyConfig().PanicAlertWindow)
	if err != nil {
		return nil, err
	}
	out.Person = s.personCards(ctx, recipientID, []uuid.UUID{inc.UserID})[inc.UserID]
	return out, nil
}
