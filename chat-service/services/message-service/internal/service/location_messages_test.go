package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	sharedEvents "github.com/atpost/chat-shared/events"
	"github.com/google/uuid"
)

// Shared location (founder, 23 Oct 2026). The coordinates used here are
// recognisable strings so a leak into an error, preview or event is caught.
const (
	testLat = 12.9715987
	testLng = 77.5945627
)

func f64(v float64) *float64 { return &v }

func validLocation() *LocationInput {
	return &LocationInput{Lat: f64(testLat), Lng: f64(testLng), AccuracyM: f64(12.4), Label: " Cubbon Park ", Address: "Kasturba Rd, Bengaluru"}
}

func TestValidateLocation(t *testing.T) {
	cases := []struct {
		name string
		in   *LocationInput
		ok   bool
	}{
		{"well formed", validLocation(), true},
		{"bare coordinates", &LocationInput{Lat: f64(testLat), Lng: f64(testLng)}, true},
		{"null island is a real place", &LocationInput{Lat: f64(0), Lng: f64(0)}, true},
		{"the poles and the antimeridian", &LocationInput{Lat: f64(-90), Lng: f64(180)}, true},
		{"label at the cap", &LocationInput{Lat: f64(1), Lng: f64(1), Label: strings.Repeat("é", LocationPlaceMaxRunes)}, true},
		{"missing", nil, false},
		{"missing lat", &LocationInput{Lng: f64(testLng)}, false},
		{"missing lng", &LocationInput{Lat: f64(testLat)}, false},
		{"lat above 90", &LocationInput{Lat: f64(90.00001), Lng: f64(1)}, false},
		{"lat below -90", &LocationInput{Lat: f64(-90.5), Lng: f64(1)}, false},
		{"lng above 180", &LocationInput{Lat: f64(1), Lng: f64(180.1)}, false},
		{"lng below -180", &LocationInput{Lat: f64(1), Lng: f64(-181)}, false},
		{"NaN", &LocationInput{Lat: f64(math.NaN()), Lng: f64(1)}, false},
		{"Inf", &LocationInput{Lat: f64(1), Lng: f64(math.Inf(1))}, false},
		{"negative accuracy", &LocationInput{Lat: f64(1), Lng: f64(1), AccuracyM: f64(-1)}, false},
		{"accuracy too coarse", &LocationInput{Lat: f64(1), Lng: f64(1), AccuracyM: f64(LocationMaxAccuracyM + 1)}, false},
		{"NaN accuracy", &LocationInput{Lat: f64(1), Lng: f64(1), AccuracyM: f64(math.NaN())}, false},
		{"label too long", &LocationInput{Lat: f64(1), Lng: f64(1), Label: strings.Repeat("a", LocationPlaceMaxRunes+1)}, false},
		{"address too long", &LocationInput{Lat: f64(1), Lng: f64(1), Address: strings.Repeat("a", LocationPlaceMaxRunes+1)}, false},
		{"control character", &LocationInput{Lat: f64(1), Lng: f64(1), Label: "a\nb"}, false},
		{"invalid utf-8", &LocationInput{Lat: f64(1), Lng: f64(1), Address: "\xff"}, false},
	}
	for _, tc := range cases {
		err := validateSendInput(SendInput{Type: MessageTypeLocation, Location: tc.in})
		if tc.ok != (err == nil) {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
		if err != nil && !errors.Is(err, ErrInvalidMessageContent) {
			t.Errorf("%s: err %v is not ErrInvalidMessageContent", tc.name, err)
		}
	}
}

// An error names the field, never the value: send errors are logged.
func TestLocationErrorsNeverCarryTheValue(t *testing.T) {
	for _, in := range []*LocationInput{
		{Lat: f64(91.12345), Lng: f64(testLng)},
		{Lat: f64(testLat), Lng: f64(181.98765)},
		{Lat: f64(testLat), Lng: f64(testLng), AccuracyM: f64(-7.25)},
		{Lat: f64(testLat), Lng: f64(testLng), Label: "Secret Hideout\n"},
	} {
		err := validateSendInput(SendInput{Type: MessageTypeLocation, Location: in})
		if err == nil {
			t.Fatal("invalid location accepted")
		}
		for _, leak := range []string{"91.1", "181.9", "7.25", "12.97", "77.59", "Secret"} {
			if strings.Contains(err.Error(), leak) {
				t.Fatalf("error %q leaks %q", err, leak)
			}
		}
	}
}

func TestLocationCarriesOnlyTheLocation(t *testing.T) {
	id := uuid.New()
	loc := validLocation()
	for name, in := range map[string]SendInput{
		"text":         {Type: MessageTypeLocation, Location: loc, Text: "meet me here"},
		"media":        {Type: MessageTypeLocation, Location: loc, MediaID: &id},
		"card":         {Type: MessageTypeLocation, Location: loc, SoundID: &id},
		"on text":      {Type: MessageTypeText, Text: "hi", Location: loc},
		"on media":     {Type: MessageTypeMedia, MediaID: &id, Location: loc},
		"on post":      {Type: MessageTypePost, PostID: &id, Location: loc},
		"on a sticker": {Type: MessageTypeSticker, StickerID: &id, Location: loc},
		"on a voice":   {Type: MessageTypeVoice, MediaID: &id, DurationMs: 1000, Location: loc},
	} {
		if err := validateSendInput(in); !errors.Is(err, ErrInvalidMessageContent) {
			t.Errorf("%s: err = %v, want ErrInvalidMessageContent", name, err)
		}
	}
}

func TestNormalizeLocation(t *testing.T) {
	got := normalizeLocation(validLocation())
	if got.Lat != 12.9716 || got.Lng != 77.59456 {
		t.Fatalf("coordinates = %v,%v, want 5-decimal rounding 12.9716,77.59456", got.Lat, got.Lng)
	}
	if got.AccuracyM == nil || *got.AccuracyM != 12 {
		t.Fatalf("accuracy = %v, want whole metres", got.AccuracyM)
	}
	if got.Label != "Cubbon Park" || got.Address != "Kasturba Rd, Bengaluru" {
		t.Fatalf("text not trimmed: %q / %q", got.Label, got.Address)
	}
	tiny := normalizeLocation(&LocationInput{Lat: f64(-0.000001), Lng: f64(-0.000004)})
	if tiny.Lat != 0 || math.Signbit(tiny.Lat) || math.Signbit(tiny.Lng) {
		t.Fatalf("a rounded -0 must store as 0: %v,%v", tiny.Lat, tiny.Lng)
	}
	if normalizeLocation(nil) != nil || locationInputOf(nil) != nil {
		t.Fatal("nil location must stay nil")
	}
	back := locationInputOf(got)
	if *back.Lat != got.Lat || *back.Lng != got.Lng || *back.AccuracyM != *got.AccuracyM || back.Label != got.Label {
		t.Fatalf("forward round trip changed the place: %+v", back)
	}
}

func (f *postFixture) sendLocation(from uuid.UUID, loc *LocationInput, key string) (*MessageResponse, error) {
	return f.svc.SendRich(context.Background(), from, f.convID, SendInput{Type: MessageTypeLocation, Location: loc, IdempotencyKey: key})
}

func TestLocationIsStoredSealedAndPreviewedWithoutThePlace(t *testing.T) {
	f := newPostFixture(t)
	resp, err := f.sendLocation(f.userA, validLocation(), "loc-1")
	if err != nil {
		t.Fatalf("send location: %v", err)
	}
	if resp.Type != MessageTypeLocation || resp.Text != "" || resp.Location == nil || resp.Location.Lat != 12.9716 {
		t.Fatalf("response = type %q text %q location %+v", resp.Type, resp.Text, resp.Location)
	}

	// The place rides the extra payload (sealed by contentcrypt in the
	// stores), rounded, and nowhere else on the intent.
	intent := f.store.intents["loc-1"]
	if intent == nil || intent.MessageText != "" {
		t.Fatalf("intent text must be empty: %+v", intent)
	}
	stored := decodeExtra(string(intent.Extra))
	if stored == nil || stored.Location == nil || stored.Location.Lng != 77.59456 {
		t.Fatalf("extra must carry the rounded location: %s", intent.Extra)
	}
	if strings.Contains(string(intent.Extra), "77.5945627") {
		t.Fatalf("unrounded coordinate stored: %s", intent.Extra)
	}
	if len(f.msgs.created) != 1 || f.msgs.created[0].Text != "" || !strings.Contains(f.msgs.created[0].Extra, `"location"`) {
		t.Fatalf("scylla row: %+v", f.msgs.created)
	}

	if f.store.lastPreview != LocationPreview || f.msgs.inboxPreview != LocationPreview {
		t.Fatalf("previews %q / %q, want %q", f.store.lastPreview, f.msgs.inboxPreview, LocationPreview)
	}

	// The event feeds search (Text) and push (Preview, type): no place.
	created, ok := f.store.events[sharedEvents.MessageCreated].(sharedEvents.MessageCreatedPayload)
	if !ok {
		t.Fatalf("no MessageCreated event: %v", f.store.events)
	}
	if created.Type != MessageTypeLocation || created.Text != "" || created.Preview != LocationPreview {
		t.Fatalf("event type %q text %q preview %q", created.Type, created.Text, created.Preview)
	}
	raw, _ := json.Marshal(created)
	for _, leak := range []string{"12.97", "77.59", "Cubbon", "Kasturba"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("MessageCreated leaks %q: %s", leak, raw)
		}
	}

	// The live frame renders the same bubble as history.
	body := map[string]interface{}{}
	addExtraToFrame(body, stored)
	if body["location"] == nil {
		t.Fatal("realtime frame lacks the location")
	}
}

// A pending message request refuses a location, like media and post cards.
func TestLocationCannotOpenAMessageRequest(t *testing.T) {
	f := newPostFixture(t)
	f.markRequest("")
	_, err := f.sendLocation(f.userA, validLocation(), "req-loc")
	// Its own reason, not the generic "first message must be text": the
	// refusal stays even if the request rule ever widens beyond text.
	if !errors.Is(err, ErrRequestFirstMessageInvalid) || !strings.Contains(err.Error(), "locations are not allowed") {
		t.Fatalf("location as a request's first message: err = %v, want ErrRequestFirstMessageInvalid", err)
	}
	if len(f.store.intents) != 0 || len(f.msgs.created) != 0 {
		t.Fatal("a refused request location must store nothing")
	}

	g := newPostFixture(t)
	g.markRequest("hi there")
	if _, err := g.sendLocation(g.userA, validLocation(), "req-loc-2"); !errors.Is(err, ErrAwaitingRequestAcceptance) {
		t.Fatalf("location after the one request message: err = %v", err)
	}
}

func TestLocationFlowsOnceTheRequestIsAccepted(t *testing.T) {
	f := newPostFixture(t)
	f.markRequest("hi there")
	f.store.conversations[f.convID].IsRequest = false
	f.store.latestRequest.Status = "accepted"
	if _, err := f.sendLocation(f.userA, validLocation(), "after-accept"); err != nil {
		t.Fatalf("location after acceptance: %v", err)
	}
}

// Two sends of different places under one key are a conflict, not a replay.
func TestLocationIsPartOfTheIdempotencyHash(t *testing.T) {
	f := newPostFixture(t)
	if _, err := f.sendLocation(f.userA, validLocation(), "same-key"); err != nil {
		t.Fatal(err)
	}
	other := validLocation()
	other.Lat = f64(28.6139)
	if _, err := f.sendLocation(f.userA, other, "same-key"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("different place under one key: err = %v, want ErrIdempotencyConflict", err)
	}
}
