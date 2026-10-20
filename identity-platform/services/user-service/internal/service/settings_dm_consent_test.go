package service

import (
	"errors"
	"testing"

	"github.com/atpost/identity-user-service/internal/store"
)

// DM consent model (founder-approved 2026-10-20): who_can_message has four
// values, legacy values are mapped on write and read, minors are clamped to
// people_i_follow, and who_can_call drops connections_only.

var contractWhoCanMessage = []string{
	"everyone_message_requests", "followers_message_requests", "people_i_follow", "no_one",
}

func TestWhoCanMessageAcceptsExactlyTheFourContractValues(t *testing.T) {
	for _, v := range contractWhoCanMessage {
		s := validSettings()
		s.WhoCanMessage = v
		if err := prepareSettings(s); err != nil {
			t.Errorf("who_can_message=%q should be accepted, got %v", v, err)
		}
		if s.WhoCanMessage != v {
			t.Errorf("who_can_message=%q was rewritten to %q for an adult", v, s.WhoCanMessage)
		}
	}
	if n := len(allowedPrivacyValues["who_can_message"]); n != 4 {
		t.Fatalf("who_can_message vocabulary has %d values, want exactly 4", n)
	}
	for _, v := range []string{"", "everyone", "followers", "People_I_Follow", "mutual_followers", "connections"} {
		s := validSettings()
		s.WhoCanMessage = v
		err := prepareSettings(s)
		var fe *SettingValidationError
		if !errors.As(err, &fe) || fe.Field != "who_can_message" || fe.Code != SettingCodeInvalidValue {
			t.Errorf("who_can_message=%q should be refused with a field error, got %v", v, err)
		}
	}
}

func TestWhoCanMessageLegacyValuesAreMappedOnWrite(t *testing.T) {
	cases := map[string]string{
		"connections_only":                 "people_i_follow",
		"connections_and_mutual_followers": "people_i_follow",
		"friends_of_friends_requests":      "followers_message_requests",
	}
	for legacy, want := range cases {
		s := validSettings()
		s.WhoCanMessage = legacy
		if err := prepareSettings(s); err != nil {
			t.Fatalf("legacy who_can_message=%q should be accepted on write, got %v", legacy, err)
		}
		if s.WhoCanMessage != want {
			t.Errorf("legacy %q stored as %q, want %q", legacy, s.WhoCanMessage, want)
		}
	}
}

func TestReadsOnlyReturnContractValues(t *testing.T) {
	cases := []struct {
		name               string
		stored, storedCall string
		under18, strict    bool
		wantMsg, wantCall  string
	}{
		{"legacy connections_only", "connections_only", "connections_only", false, false, "people_i_follow", "accepted_chats_only"},
		{"legacy mutual", "connections_and_mutual_followers", "no_one", false, false, "people_i_follow", "no_one"},
		{"legacy fof", "friends_of_friends_requests", "accepted_chats_only", false, false, "followers_message_requests", "accepted_chats_only"},
		{"adult everyone untouched", "everyone_message_requests", "accepted_chats_only", false, false, "everyone_message_requests", "accepted_chats_only"},
		{"minor with an open value", "everyone_message_requests", "no_one", true, false, "people_i_follow", "no_one"},
		{"minor with legacy fof", "friends_of_friends_requests", "no_one", true, false, "people_i_follow", "no_one"},
		{"strict adult followers", "followers_message_requests", "no_one", false, true, "people_i_follow", "no_one"},
		{"minor no_one kept", "no_one", "no_one", true, false, "no_one", "no_one"},
	}
	allowed := map[string]bool{}
	for _, v := range contractWhoCanMessage {
		allowed[v] = true
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &store.UserSettings{WhoCanMessage: tc.stored, WhoCanCall: tc.storedCall, Under18Mode: tc.under18, StrictPrivacyMode: tc.strict}
			normaliseForRead(s)
			if s.WhoCanMessage != tc.wantMsg || s.WhoCanCall != tc.wantCall {
				t.Fatalf("read gave (%q, %q), want (%q, %q)", s.WhoCanMessage, s.WhoCanCall, tc.wantMsg, tc.wantCall)
			}
			if !allowed[s.WhoCanMessage] {
				t.Fatalf("read returned non-contract who_can_message %q", s.WhoCanMessage)
			}
		})
	}
}

func TestMinorWritesOfOpenValuesClampToPeopleIFollow(t *testing.T) {
	cases := map[string]string{
		"everyone_message_requests":   "people_i_follow",
		"followers_message_requests":  "people_i_follow",
		"friends_of_friends_requests": "people_i_follow", // legacy -> followers -> clamped
		"connections_only":            "people_i_follow",
		"people_i_follow":             "people_i_follow",
		"no_one":                      "no_one",
	}
	for in, want := range cases {
		for _, mode := range []string{"under18", "strict"} {
			s := validSettings()
			s.Under18Mode = mode == "under18"
			s.StrictPrivacyMode = mode == "strict"
			s.WhoCanMessage = in
			if err := prepareSettings(s); err != nil {
				t.Fatalf("%s write of %q: %v", mode, in, err)
			}
			if s.WhoCanMessage != want {
				t.Errorf("%s write of %q stored %q, want %q", mode, in, s.WhoCanMessage, want)
			}
		}
	}
	// An unknown value is still refused for a minor, not silently rewritten.
	s := validSettings()
	s.Under18Mode = true
	s.WhoCanMessage = "anyone"
	if err := prepareSettings(s); !errors.Is(err, ErrInvalidPrivacySetting) {
		t.Fatalf("minor write of an unknown value should be refused, got %v", err)
	}
}

func TestMinorProfileAtCreation(t *testing.T) {
	// The row auth-service inserts at registration: adult column defaults.
	adultDefaults := validSettings()
	adultDefaults.WhoCanMessage = "everyone_message_requests"
	adultDefaults.WhoCanCall = "accepted_chats_only"
	adultDefaults.DmFilteredKeywords = []string{}

	next, changed, err := minorProfile(adultDefaults)
	if err != nil {
		t.Fatalf("minorProfile: %v", err)
	}
	if !changed {
		t.Fatal("an adult-default row for a minor must be rewritten at creation")
	}
	if !next.Under18Mode || next.WhoCanMessage != "people_i_follow" || next.WhoCanCall != "no_one" ||
		next.WhoCanAddToGroups != "no_one" || next.WhoCanSeeProfilePhoto != "connections_only" ||
		!next.BlockUnknownCalls || !next.DmSafeMode || !next.DmSensitiveMediaFilter {
		t.Fatalf("minor profile not applied at creation: %+v", next)
	}
	if adultDefaults.Under18Mode || adultDefaults.WhoCanMessage != "everyone_message_requests" {
		t.Fatal("minorProfile must not mutate its input")
	}

	// Idempotent: the already-pinned row needs no second write.
	_, changedAgain, err := minorProfile(next)
	if err != nil || changedAgain {
		t.Fatalf("second minorProfile: changed=%v err=%v, want no change", changedAgain, err)
	}

	// A nil keyword slice (as a freshly scanned row never has, but a cached
	// one might) is not a change on its own.
	nilKw := *next
	nilKw.DmFilteredKeywords = nil
	if _, c, _ := minorProfile(&nilKw); c {
		t.Fatal("nil vs empty keywords must not count as a change")
	}
}

func TestAdultDefaultMessagingIsUntouched(t *testing.T) {
	s := validSettings()
	s.WhoCanMessage = "everyone_message_requests"
	if err := prepareSettings(s); err != nil || s.WhoCanMessage != "everyone_message_requests" {
		t.Fatalf("adult everyone_message_requests: got %q, %v", s.WhoCanMessage, err)
	}
}

func TestWhoCanCallVocabularyAndLegacyMapping(t *testing.T) {
	for _, v := range []string{"no_one", "accepted_chats_only"} {
		s := validSettings()
		s.WhoCanCall = v
		if err := prepareSettings(s); err != nil || s.WhoCanCall != v {
			t.Errorf("who_can_call=%q: got %q, %v", v, s.WhoCanCall, err)
		}
	}
	s := validSettings()
	s.WhoCanCall = "connections_only"
	if err := prepareSettings(s); err != nil {
		t.Fatalf("legacy who_can_call=connections_only should be accepted on write, got %v", err)
	}
	if s.WhoCanCall != "accepted_chats_only" {
		t.Fatalf("legacy who_can_call stored as %q, want accepted_chats_only", s.WhoCanCall)
	}
	if allowedPrivacyValues["who_can_call"]["connections_only"] {
		t.Fatal("connections_only must not be a storable who_can_call value")
	}
	s = validSettings()
	s.WhoCanCall = "everyone"
	var fe *SettingValidationError
	if err := prepareSettings(s); !errors.As(err, &fe) || fe.Field != "who_can_call" {
		t.Fatalf("who_can_call=everyone should be refused, got %v", err)
	}
}
