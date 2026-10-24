package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Mentions and tags (2026-10-24): who_can_mention / who_can_tag take exactly
// everyone | following | no_one; minors and Strict Privacy Mode never hold
// "everyone".

func TestWhoCanMentionAndTagAcceptExactlyThreeValues(t *testing.T) {
	for _, field := range []string{"who_can_mention", "who_can_tag"} {
		set := func(v string) error {
			s := validSettings()
			if field == "who_can_mention" {
				s.WhoCanMention = v
			} else {
				s.WhoCanTag = v
			}
			return prepareSettings(s)
		}
		for _, v := range []string{"everyone", "following", "no_one"} {
			if err := set(v); err != nil {
				t.Errorf("%s=%q should be accepted, got %v", field, v, err)
			}
		}
		for _, v := range []string{"", "friends", "followers", "connections_only", "Everyone", "people_i_follow"} {
			err := set(v)
			var ve *SettingValidationError
			if !errors.As(err, &ve) || ve.Field != field || ve.Code != SettingCodeInvalidValue {
				t.Errorf("%s=%q should be refused as INVALID_VALUE on %s, got %v", field, v, field, err)
			}
			if !errors.Is(err, ErrInvalidPrivacySetting) {
				t.Errorf("%s=%q: error must unwrap to ErrInvalidPrivacySetting (400), got %v", field, v, err)
			}
		}
	}
}

func TestMinorAndStrictModeNarrowEveryoneMentionsAndTags(t *testing.T) {
	cases := []struct {
		name            string
		under18, strict bool
		mention, tag    string
		wantM, wantT    string
	}{
		{"adult keeps everyone", false, false, "everyone", "everyone", "everyone", "everyone"},
		{"minor everyone narrows", true, false, "everyone", "everyone", "following", "following"},
		{"strict everyone narrows", false, true, "everyone", "everyone", "following", "following"},
		{"minor keeps no_one", true, false, "no_one", "no_one", "no_one", "no_one"},
		{"minor keeps following", true, false, "following", "no_one", "following", "no_one"},
		{"minor mixed", true, false, "everyone", "no_one", "following", "no_one"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := validSettings()
			s.Under18Mode, s.StrictPrivacyMode = tc.under18, tc.strict
			s.WhoCanMessage = "people_i_follow"
			s.WhoCanMention, s.WhoCanTag = tc.mention, tc.tag
			if err := prepareSettings(s); err != nil {
				t.Fatal(err)
			}
			if s.WhoCanMention != tc.wantM || s.WhoCanTag != tc.wantT {
				t.Fatalf("got mention=%q tag=%q, want %q %q", s.WhoCanMention, s.WhoCanTag, tc.wantM, tc.wantT)
			}
		})
	}
}

// A minor created with the column defaults (everyone) is narrowed at
// creation, like the rest of the minor profile.
func TestMinorProfileNarrowsMentionsAtCreation(t *testing.T) {
	cur := validSettings()
	next, changed, err := minorProfile(cur)
	if err != nil || !changed {
		t.Fatalf("minorProfile: changed=%v err=%v", changed, err)
	}
	if next.WhoCanMention != "following" || next.WhoCanTag != "following" {
		t.Fatalf("minor at creation: mention=%q tag=%q, want following/following", next.WhoCanMention, next.WhoCanTag)
	}
}

// The settings cache is versioned past v2: a v2 snapshot has no
// who_can_mention / who_can_tag and would unmarshal them as "", which
// validation refuses, so every PUT read-modify-write would 400.
func TestSettingsCacheKeyMovedPastV2(t *testing.T) {
	id := uuid.New()
	if k := settingsCacheKey(id); strings.Contains(k, ":v2:") || !strings.Contains(k, id.String()) {
		t.Fatalf("settingsCacheKey = %q; must not be the v2 key", k)
	}
	if k := v2SettingsCacheKey(id); k != "user:settings:v2:"+id.String() {
		t.Fatalf("v2SettingsCacheKey = %q", k)
	}
}
