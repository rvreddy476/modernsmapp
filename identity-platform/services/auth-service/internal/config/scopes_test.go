package config

import "testing"

func TestScopesForUser(t *testing.T) {
	cfg := &Config{
		ScopeSuperadminUserIDs: map[string]struct{}{"super-1": {}},
		ScopeAdminUserIDs:      map[string]struct{}{"admin-1": {}},
		ScopeModeratorUserIDs:  map[string]struct{}{"mod-1": {}},
	}
	cases := map[string]string{
		"super-1":   "superadmin admin moderator", // superadmin implies admin+moderator
		"admin-1":   "admin moderator",            // admin implies moderator
		"mod-1":     "moderator",
		"nobody":    "", // ordinary user gets no privileged scope
	}
	for userID, want := range cases {
		if got := cfg.ScopesForUser(userID); got != want {
			t.Fatalf("ScopesForUser(%q)=%q want %q", userID, got, want)
		}
	}
}

func TestExpandRoles(t *testing.T) {
	cases := []struct {
		roles []string
		want  string
	}{
		{nil, ""},
		{[]string{"moderator"}, "moderator"},
		{[]string{"admin"}, "admin moderator"},                       // admin implies moderator
		{[]string{"superadmin"}, "superadmin admin moderator"},       // superadmin implies all
		{[]string{"moderator", "admin"}, "admin moderator"},          // union, deduped
		{[]string{"admin", "superadmin"}, "superadmin admin moderator"},
		{[]string{"bogus"}, ""},                                      // unknown role ignored
		{[]string{"moderator", "moderator"}, "moderator"},            // dedupe
	}
	for _, tc := range cases {
		if got := ExpandRoles(tc.roles); got != tc.want {
			t.Fatalf("ExpandRoles(%v)=%q want %q", tc.roles, got, tc.want)
		}
	}
}

func TestEnvRolesForUser(t *testing.T) {
	cfg := &Config{
		ScopeSuperadminUserIDs: map[string]struct{}{"s": {}},
		ScopeAdminUserIDs:      map[string]struct{}{"a": {}},
		ScopeModeratorUserIDs:  map[string]struct{}{"m": {}},
	}
	if got := cfg.EnvRolesForUser("s"); len(got) != 1 || got[0] != "superadmin" {
		t.Fatalf("env roles for s = %v", got)
	}
	if got := cfg.EnvRolesForUser("nobody"); len(got) != 0 {
		t.Fatalf("env roles for nobody = %v want empty", got)
	}
}

func TestSplitToSet(t *testing.T) {
	set := splitToSet(" a , b ,, c ")
	if len(set) != 3 {
		t.Fatalf("len=%d want 3 (%v)", len(set), set)
	}
	for _, k := range []string{"a", "b", "c"} {
		if _, ok := set[k]; !ok {
			t.Fatalf("missing %q in %v", k, set)
		}
	}
}
