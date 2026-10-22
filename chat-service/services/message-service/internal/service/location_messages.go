package service

import (
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Shared location (founder, 23 Oct 2026: WhatsApp's "Send your current
// location"), in DMs and groups.
//
// PRIVACY. A location is the most sensitive thing a chat carries:
//
//   - it rides MessageExtra, which the stores seal with contentcrypt
//     (ScopeMessageExtra / ScopeIntentExtra / the outbox and idempotency
//     scopes), so it is encrypted at rest like message text;
//   - the message text is EMPTY, so search (which indexes the event's Text)
//     and push (which renders "shared a location") never see the place —
//     the inbox preview is the fixed LocationPreview;
//   - coordinates are NEVER logged: validation errors name the field, never
//     the value;
//   - a pending message request refuses it (only plain text may open a
//     request), exactly like media and post cards.
//
// Live location (continuous sharing) is out of scope; see the report note.
const MessageTypeLocation = "location"

// LocationPreview is the inbox / last-message preview for a location.
const LocationPreview = "📍 Location"

// Location limits.
const (
	// LocationPlaceMaxRunes caps label and address.
	LocationPlaceMaxRunes = 200
	// LocationMaxAccuracyM: a fix worse than 100 km is not a location.
	LocationMaxAccuracyM = 100_000
	// locationScale rounds coordinates to 5 decimals (~1.1 m at the
	// equator): enough to find a door, no more precision than that kept.
	locationScale = 1e5
)

// LocationInput is a location on a send. Pointers so a missing coordinate
// is told apart from 0 (the equator and the prime meridian are real places).
type LocationInput struct {
	Lat       *float64 `json:"lat"`
	Lng       *float64 `json:"lng"`
	AccuracyM *float64 `json:"accuracy_m,omitempty"`
	Label     string   `json:"label,omitempty"`
	Address   string   `json:"address,omitempty"`
}

// LocationInfo is the stored and wire shape of a shared location.
type LocationInfo struct {
	Lat       float64  `json:"lat"`
	Lng       float64  `json:"lng"`
	AccuracyM *float64 `json:"accuracy_m,omitempty"`
	Label     string   `json:"label,omitempty"`
	Address   string   `json:"address,omitempty"`
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func roundCoord(v float64) float64 { return math.Round(v*locationScale) / locationScale }

// validPlaceText: valid UTF-8, at most LocationPlaceMaxRunes, no control
// characters (a label is one line of display text).
func validPlaceText(field, s string) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("%w: location %s is not valid text", ErrInvalidMessageContent, field)
	}
	if utf8.RuneCountInString(s) > LocationPlaceMaxRunes {
		return fmt.Errorf("%w: location %s exceeds %d characters", ErrInvalidMessageContent, field, LocationPlaceMaxRunes)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: location %s has control characters", ErrInvalidMessageContent, field)
		}
	}
	return nil
}

// validateLocation checks a location's shape. Errors never carry a value.
func validateLocation(in *LocationInput) error {
	if in == nil || in.Lat == nil || in.Lng == nil {
		return fmt.Errorf("%w: location lat and lng are required", ErrInvalidMessageContent)
	}
	lat, lng := *in.Lat, *in.Lng
	if !finite(lat) || lat < -90 || lat > 90 {
		return fmt.Errorf("%w: location lat must be -90..90", ErrInvalidMessageContent)
	}
	if !finite(lng) || lng < -180 || lng > 180 {
		return fmt.Errorf("%w: location lng must be -180..180", ErrInvalidMessageContent)
	}
	if a := in.AccuracyM; a != nil && (!finite(*a) || *a < 0 || *a > LocationMaxAccuracyM) {
		return fmt.Errorf("%w: location accuracy_m must be 0..%d", ErrInvalidMessageContent, LocationMaxAccuracyM)
	}
	if err := validPlaceText("label", in.Label); err != nil {
		return err
	}
	return validPlaceText("address", in.Address)
}

// normalizeLocation is the stored form of a VALIDATED input: coordinates
// rounded to 5 decimals, accuracy to whole metres, text trimmed. -0 is
// stored as 0 so equal places hash and compare equal.
func normalizeLocation(in *LocationInput) *LocationInfo {
	if in == nil || in.Lat == nil || in.Lng == nil {
		return nil
	}
	out := &LocationInfo{
		Lat:     roundCoord(*in.Lat) + 0,
		Lng:     roundCoord(*in.Lng) + 0,
		Label:   strings.TrimSpace(in.Label),
		Address: strings.TrimSpace(in.Address),
	}
	if in.AccuracyM != nil {
		a := math.Round(*in.AccuracyM) + 0
		out.AccuracyM = &a
	}
	return out
}

// locationInputOf turns a stored location back into a send input (forward).
func locationInputOf(l *LocationInfo) *LocationInput {
	if l == nil {
		return nil
	}
	lat, lng := l.Lat, l.Lng
	in := &LocationInput{Lat: &lat, Lng: &lng, Label: l.Label, Address: l.Address}
	if l.AccuracyM != nil {
		a := *l.AccuracyM
		in.AccuracyM = &a
	}
	return in
}

// validateLocationSend is the location branch of validateSendInput: a
// location carries exactly the location — no text, attachment or card.
func validateLocationSend(in SendInput) error {
	if strings.TrimSpace(in.Text) != "" {
		return fmt.Errorf("%w: location messages carry no text", ErrInvalidMessageContent)
	}
	if in.MediaID != nil || in.DurationMs != 0 || len(in.Waveform) > 0 {
		return fmt.Errorf("%w: media fields are not allowed on location messages", ErrInvalidMessageContent)
	}
	if nonNil(in.StickerID) || in.GIF != nil || nonNil(in.ProfileUserID) || nonNil(in.SoundID) || nonNil(in.StreamID) {
		return fmt.Errorf("%w: card fields are not allowed on location messages", ErrInvalidMessageContent)
	}
	return validateLocation(in.Location)
}
