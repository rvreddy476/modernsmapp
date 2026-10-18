package service

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Chat parity (CONTRACT §7): validation of the four inbox keys.

func TestChatBubbleStyleValidation(t *testing.T) {
	for _, v := range []string{"default", "ocean", "ember", "forest", "violet", "mono"} {
		s := validSettings()
		s.ChatBubbleStyle = v
		if err := prepareSettings(s); err != nil {
			t.Errorf("chat_bubble_style=%q should be accepted, got %v", v, err)
		}
	}
	for _, v := range []string{"", "Ocean", "red", "#ff0000", "default ", "sunset"} {
		s := validSettings()
		s.ChatBubbleStyle = v
		err := prepareSettings(s)
		var fe *SettingValidationError
		if !errors.As(err, &fe) || !errors.Is(err, ErrInvalidPrivacySetting) {
			t.Fatalf("chat_bubble_style=%q should be refused with a field error, got %v", v, err)
		}
		if fe.Field != "chat_bubble_style" || fe.Code != SettingCodeInvalidValue {
			t.Errorf("chat_bubble_style=%q: got field=%q code=%q", v, fe.Field, fe.Code)
		}
	}
}

func TestFilteredKeywordsTable(t *testing.T) {
	long31 := strings.Repeat("a", 31)
	exact30 := strings.Repeat("é", 30) // 30 runes, 60 bytes: length is characters, not bytes
	hundred := make([]string, 100)
	for i := range hundred {
		hundred[i] = fmt.Sprintf("word%d", i)
	}
	hundredOne := append(append([]string{}, hundred...), "extra")
	// 101 entries that dedupe to one are still refused: the cap is on input.
	hundredOneDupes := make([]string, 101)
	for i := range hundredOneDupes {
		hundredOneDupes[i] = "same"
	}

	cases := []struct {
		name     string
		in       []string
		want     []string
		wantCode string
	}{
		{"nil becomes empty", nil, []string{}, ""},
		{"empty stays empty", []string{}, []string{}, ""},
		{"trim and lower-case", []string{"  Spam  ", "CRYPTO"}, []string{"spam", "crypto"}, ""},
		{"inner whitespace collapses", []string{"free \t  money"}, []string{"free money"}, ""},
		{"dedupe case-insensitively keeps first position", []string{"Loan", "spam", "LOAN", " loan "}, []string{"loan", "spam"}, ""},
		{"exactly 30 characters ok", []string{exact30}, []string{exact30}, ""},
		{"exactly 100 ok", hundred, hundred, ""},
		{"31 characters refused", []string{long31}, nil, SettingCodeKeywordLength},
		{"blank refused", []string{"ok", "   "}, nil, SettingCodeKeywordLength},
		{"empty string refused", []string{""}, nil, SettingCodeKeywordLength},
		{"101 refused", hundredOne, nil, SettingCodeTooManyKeywords},
		{"101 duplicates refused", hundredOneDupes, nil, SettingCodeTooManyKeywords},
		{"control character refused", []string{"bad\x00word"}, nil, SettingCodeKeywordInvalid},
		{"invalid utf-8 refused", []string{"bad\xffword"}, nil, SettingCodeKeywordInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := validSettings()
			s.DmFilteredKeywords = tc.in
			err := prepareSettings(s)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !reflect.DeepEqual(s.DmFilteredKeywords, tc.want) {
					t.Fatalf("got %q, want %q", s.DmFilteredKeywords, tc.want)
				}
				return
			}
			var fe *SettingValidationError
			if !errors.As(err, &fe) || !errors.Is(err, ErrInvalidPrivacySetting) {
				t.Fatalf("want field error %s, got %v", tc.wantCode, err)
			}
			if fe.Field != "dm_filtered_keywords" || fe.Code != tc.wantCode {
				t.Fatalf("got field=%q code=%q, want dm_filtered_keywords/%s", fe.Field, fe.Code, tc.wantCode)
			}
		})
	}
}

// Normalising is idempotent: the stored list re-validates to itself, which
// matters because every PUT re-runs it over the merged row.
func TestFilteredKeywordsIdempotent(t *testing.T) {
	first, err := NormaliseFilteredKeywords([]string{" A  b ", "c", "A B"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NormaliseFilteredKeywords(first)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || !reflect.DeepEqual(first, []string{"a b", "c"}) {
		t.Fatalf("first=%q second=%q", first, second)
	}
}

// Existing enum errors now name their field too (visibility shares a vocab
// but each column is reported by its own JSON name).
func TestEnumErrorsNameTheField(t *testing.T) {
	s := validSettings()
	s.WhoCanSeeReadReceipts = "friends"
	var fe *SettingValidationError
	if err := prepareSettings(s); !errors.As(err, &fe) || fe.Field != "who_can_see_read_receipts" {
		t.Fatalf("want who_can_see_read_receipts field error, got %v", err)
	}
}

// Minors and Strict Privacy Mode keep safe requests and media blur on.
func TestStrictModeForcesDMSafety(t *testing.T) {
	for _, minor := range []bool{true, false} {
		s := validSettings()
		s.Under18Mode = minor
		s.StrictPrivacyMode = !minor
		s.DmSafeMode = false
		s.DmSensitiveMediaFilter = false
		if err := prepareSettings(s); err != nil {
			t.Fatal(err)
		}
		if !s.DmSafeMode || !s.DmSensitiveMediaFilter {
			t.Errorf("minor=%v: dm_safe_mode=%v dm_sensitive_media_filter=%v, want both true",
				minor, s.DmSafeMode, s.DmSensitiveMediaFilter)
		}
	}
	s := validSettings()
	s.DmSafeMode = false
	if err := prepareSettings(s); err != nil || s.DmSafeMode {
		t.Fatalf("adult without strict mode may turn dm_safe_mode off: err=%v value=%v", err, s.DmSafeMode)
	}
}

// The cache key is versioned so a snapshot cached before these fields
// existed is never read back with dm_safe_mode=false / bubble "".
func TestSettingsCacheKeyIsVersioned(t *testing.T) {
	id := validSettings().UserID
	if settingsCacheKey(id) == legacySettingsCacheKey(id) {
		t.Fatal("settings cache key must differ from the pre-v2 key")
	}
}
