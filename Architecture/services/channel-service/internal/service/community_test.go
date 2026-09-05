package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestVisibilityMapping(t *testing.T) {
	for ct, want := range map[string]string{
		"public": "public", "creator": "public", "topic": "public",
		"private": "private", "paid": "private",
	} {
		if got := VisibilityOf(ct); got != want {
			t.Fatalf("VisibilityOf(%q) = %q, want %q", ct, got, want)
		}
	}
	for in, want := range map[string]string{"": "", "public": "public", "Private": "private"} {
		got, err := channelTypeForVisibility(in)
		if err != nil || got != want {
			t.Fatalf("channelTypeForVisibility(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := channelTypeForVisibility("secret"); err == nil {
		t.Fatal("visibility=secret accepted")
	}
}

func TestCommunityValidation(t *testing.T) {
	if _, err := validateCommunityName(strings.Repeat("n", 61)); err == nil {
		t.Fatal("61-char name accepted")
	}
	if got, err := validateCommunityName("  Riders  "); err != nil || got != "Riders" {
		t.Fatalf("name trim: %q %v", got, err)
	}
	if _, err := validateCommunityAbout(strings.Repeat("é", 301)); err == nil {
		t.Fatal("301-rune about accepted")
	}
	if _, err := validateCommunityAbout(strings.Repeat("é", 300)); err != nil {
		t.Fatalf("300-rune about rejected: %v", err)
	}
	for _, bad := range []string{"ab", "Has Space", strings.Repeat("a", 31), "dash-ed"} {
		if _, err := normalizeHandle(bad); err == nil {
			t.Fatalf("handle %q accepted", bad)
		}
	}
	if got, err := normalizeHandle("@Riders_2026"); err != nil || got != "riders_2026" {
		t.Fatalf("handle normalize: %q %v", got, err)
	}
}

func TestEventRoundTripThroughMetadata(t *testing.T) {
	start := time.Date(2026, 9, 12, 18, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	e := &EventInfo{Title: " Launch night ", StartsAt: start, EndsAt: &end, Location: "Hyderabad"}
	if err := validateEvent(e); err != nil {
		t.Fatal(err)
	}
	if e.Title != "Launch night" {
		t.Fatalf("title not trimmed: %q", e.Title)
	}
	merged, err := mergeEventMetadata(json.RawMessage(`{"severity":"info"}`), e)
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	if err := json.Unmarshal(merged, &meta); err != nil {
		t.Fatal(err)
	}
	if meta["severity"] != "info" {
		t.Fatalf("existing metadata key lost: %v", meta)
	}
	back := eventFromMetadata(merged)
	if back == nil || back.Title != "Launch night" || !back.StartsAt.Equal(start) || back.EndsAt == nil || !back.EndsAt.Equal(end) || back.Location != "Hyderabad" {
		t.Fatalf("event round trip: %+v", back)
	}
	if eventFromMetadata(nil) != nil || eventFromMetadata(json.RawMessage(`{"x":1}`)) != nil {
		t.Fatal("event parsed from metadata without one")
	}

	// Validation failures.
	earlier := start.Add(-time.Hour)
	for _, bad := range []*EventInfo{
		{Title: "", StartsAt: start},
		{Title: "x"},
		{Title: "x", StartsAt: start, EndsAt: &earlier},
		{Title: strings.Repeat("t", 121), StartsAt: start},
	} {
		if err := validateEvent(bad); err == nil {
			t.Fatalf("event %+v accepted", bad)
		}
	}
	if _, err := mergeEventMetadata(json.RawMessage(`[1,2]`), e); err == nil {
		t.Fatal("array metadata accepted")
	}
}
