package service

// Pulse (dating) mechanics notices, 2026-10-02.
//
//	dating.safety.scam_alert  → dating_scam_alert   (M17) safety class
//	dating.date_checkin.due   → dating_date_checkin (M14) ordinary class
//
// A scam alert tells someone that a person they matched with in the last 90
// days was removed for scam behaviour. It is a SAFETY notice: it is delivered
// through CreateSafetyAlertNotification, the same class as a panic page, so no
// category toggle, mute or quiet hours can hold it back. Only an account
// suppression (deactivated, deletion scheduled) stops it. The removed person
// is never the actor, so no avatar or profile of theirs rides the inbox row.
//
// A date check-in asks how a planned date went. It is an ordinary dating
// notice: suppression, the master push toggle and quiet hours apply, exactly
// as for every other dating engagement push.

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	DatingScamAlertType   = "dating_scam_alert"
	DatingDateCheckinType = "dating_date_checkin"
	// DatingMatchEntityType is the entity both notices point at.
	DatingMatchEntityType = "dating_match"
	// DatingScamAlertDeepLink is the safety centre, never the removed profile.
	DatingScamAlertDeepLink = "/dating/safety"

	datingScamAlertTitle   = "Safety notice"
	datingDateCheckinTitle = "How did it go?"

	datingScamAlertWarning  = "Never send money, gift cards or codes to anyone you met here."
	datingScamAlertNoName   = "Someone you matched with on Pulse was removed for scam behaviour. " + datingScamAlertWarning
	datingDateCheckinNoName = "Tell us how your date went. It takes a moment."
	datingFirstNameMaxRunes = 40
)

// CleanDatingFirstName is the one place a first name from a dating payload is
// made fit for push copy: surrounding space trimmed, invalid UTF-8 dropped and
// the result capped at 40 runes. Callers must never log the name.
func CleanDatingFirstName(name string) string {
	name = strings.TrimSpace(strings.ToValidUTF8(name, ""))
	if utf8.RuneCountInString(name) > datingFirstNameMaxRunes {
		name = strings.TrimSpace(string([]rune(name)[:datingFirstNameMaxRunes]))
	}
	return name
}

// DatingScamAlertCopy is the push title and body for a scam alert. name must
// already be cleaned; an empty name gives the anonymous copy.
func DatingScamAlertCopy(name string) (title, body string) {
	if name == "" {
		return datingScamAlertTitle, datingScamAlertNoName
	}
	return datingScamAlertTitle, name + ", who you matched with on Pulse, was removed for scam behaviour. " + datingScamAlertWarning
}

// DatingDateCheckinCopy is the push title and body for a date check-in. name
// must already be cleaned; an empty name gives the anonymous copy.
func DatingDateCheckinCopy(name string) (title, body string) {
	if name == "" {
		return datingDateCheckinTitle, datingDateCheckinNoName
	}
	return datingDateCheckinTitle, "Tell us how your date with " + name + " went. It takes a moment."
}

// DatingCheckinNotification is one person's "how did it go?" notice.
type DatingCheckinNotification struct {
	RecipientID uuid.UUID
	MatchID     uuid.UUID
	FirstName   string // cleaned; "" when unknown
	DeepLink    string
	CreatedAt   time.Time
	// Identity keys the idempotent inbox write so a redelivered event does
	// not ask twice.
	Identity string
}

// RenderDatingCheckin is the push copy and the per-match collapse key.
func RenderDatingCheckin(n DatingCheckinNotification) RenderOverride {
	title, body := DatingDateCheckinCopy(n.FirstName)
	return RenderOverride{
		Title:       title,
		Body:        body,
		CollapseKey: DatingDateCheckinType + ":" + n.MatchID.String() + ":" + n.RecipientID.String(),
	}
}

// CreateDatingCheckinNotification delivers a date check-in through the
// ordinary resolution path (suppression, master push toggle, quiet hours). No
// actor: the other person is named in the copy only.
func (s *Service) CreateDatingCheckinNotification(ctx context.Context, n DatingCheckinNotification) error {
	if s.recipientSuppressed(ctx, n.RecipientID, DatingDateCheckinType) {
		return nil
	}
	decision := s.resolveGeneralDelivery(ctx, n.RecipientID, DatingDateCheckinType)
	return s.deliverWithDecision(ctx, decision, n.RecipientID, uuid.Nil, DatingDateCheckinType,
		DatingMatchEntityType, n.MatchID, n.DeepLink, n.CreatedAt, n.Identity, RenderDatingCheckin(n))
}
