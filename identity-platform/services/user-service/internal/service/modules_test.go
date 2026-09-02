package service

import (
	"errors"
	"reflect"
	"testing"

	"github.com/atpost/identity-user-service/internal/store"
	"github.com/google/uuid"
)

func TestNormalizeModulePreferences(t *testing.T) {
	cases := []struct {
		name        string
		modules     []string
		home        string
		wantModules []string
		wantHome    string
		wantErr     error
	}{
		{
			name:    "unknown module is refused",
			modules: []string{"reels", "banana"},
			home:    "feed",
			wantErr: ErrInvalidModule,
		},
		{
			name:        "dedupe preserves first-occurrence order",
			modules:     []string{"chat", "reels", "chat", "reels", "qa"},
			home:        "feed",
			wantModules: []string{"chat", "reels", "qa"},
			wantHome:    "feed",
		},
		{
			name:        "case and whitespace are normalised",
			modules:     []string{" Reels ", "CHAT"},
			home:        " REELS ",
			wantModules: []string{"reels", "chat"},
			wantHome:    "reels",
		},
		{
			name:    "home must be feed or a chosen module",
			modules: []string{"reels"},
			home:    "chat", // known module, but not chosen
			wantErr: ErrInvalidHomeModule,
		},
		{
			name:    "home outside the vocabulary is refused",
			modules: []string{"reels"},
			home:    "banana",
			wantErr: ErrInvalidHomeModule,
		},
		{
			name:        "empty home defaults to feed",
			modules:     []string{"reels"},
			home:        "",
			wantModules: []string{"reels"},
			wantHome:    "feed",
		},
		{
			name:        "feed is always a valid home, even with no modules",
			modules:     []string{},
			home:        "feed",
			wantModules: []string{},
			wantHome:    "feed",
		},
		{
			name:        "all seven modules are accepted",
			modules:     []string{"reels", "commerce", "chat", "dating", "food", "qa", "posttube"},
			home:        "posttube",
			wantModules: []string{"reels", "commerce", "chat", "dating", "food", "qa", "posttube"},
			wantHome:    "posttube",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotModules, gotHome, err := normalizeModulePreferences(tc.modules, tc.home)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(gotModules, tc.wantModules) {
				t.Errorf("modules = %v, want %v", gotModules, tc.wantModules)
			}
			if gotHome != tc.wantHome {
				t.Errorf("home = %q, want %q", gotHome, tc.wantHome)
			}
		})
	}
}

func TestDefaultModulePreferences(t *testing.T) {
	p := defaultModulePreferences(uuid.New())
	if !reflect.DeepEqual(p.Modules, []string{"reels", "commerce", "chat", "dating", "food", "qa", "posttube"}) {
		t.Errorf("default modules = %v", p.Modules)
	}
	if p.HomeModule != "feed" {
		t.Errorf("default home = %q, want feed", p.HomeModule)
	}
	if p.OnboardingCompletedAt != nil {
		t.Errorf("default onboarding_completed_at must be nil, got %v", p.OnboardingCompletedAt)
	}
	if p.UpdatedAt.IsZero() {
		t.Error("default updated_at must not be the zero time")
	}
}

func TestValidRegionCode(t *testing.T) {
	for _, ok := range []string{"IN", "in", "Us", "gb"} {
		if !validRegionCode(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "I", "IND", "1N", "I-", "  ", "İN"} {
		if validRegionCode(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

// validSettings returns a settings row where every validated enum carries an
// allowed value, so each test flips exactly one field.
func validSettings() *store.UserSettings {
	return &store.UserSettings{
		AccountVisibility:           "public",
		AllowCommentsFrom:           "everyone",
		WhoCanMessage:               "connections_only",
		WhoCanSendConnectionRequest: "everyone",
		WhoCanCall:                  "connections_only",
		WhoCanAddToGroups:           "connections_only",
		WhoCanSeeOnlineStatus:       "everyone",
		WhoCanSeeReadReceipts:       "everyone",
		WhoCanSeeLastSeen:           "everyone",
		WhoCanSeeProfilePhoto:       "everyone",
		ChatAvailability:            "enabled",
	}
}

// Module 3 — the validator ACCEPTS 'private' (and 'public'), and refuses
// everything else. This is the enforcement the launch-era public-accounts-only
// refusal was replaced by.
func TestAccountVisibilityValidationAcceptsPrivate(t *testing.T) {
	for _, v := range []string{"public", "private"} {
		s := validSettings()
		s.AccountVisibility = v
		if err := validatePrivacySettings(s); err != nil {
			t.Errorf("account_visibility=%q should be accepted, got %v", v, err)
		}
	}
	for _, v := range []string{"", "followers", "PRIVATE", "friends_only", "hidden"} {
		s := validSettings()
		s.AccountVisibility = v
		if err := validatePrivacySettings(s); !errors.Is(err, ErrInvalidPrivacySetting) {
			t.Errorf("account_visibility=%q should be refused, got %v", v, err)
		}
	}
}

func TestAllowCommentsFromValidation(t *testing.T) {
	for _, v := range []string{"everyone", "friends"} {
		s := validSettings()
		s.AllowCommentsFrom = v
		if err := validatePrivacySettings(s); err != nil {
			t.Errorf("allow_comments_from=%q should be accepted, got %v", v, err)
		}
	}
	for _, v := range []string{"", "no_one", "connections", "EVERYONE"} {
		s := validSettings()
		s.AllowCommentsFrom = v
		if err := validatePrivacySettings(s); !errors.Is(err, ErrInvalidPrivacySetting) {
			t.Errorf("allow_comments_from=%q should be refused, got %v", v, err)
		}
	}
}
