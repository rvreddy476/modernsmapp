package permissions

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/internal/roles"
)

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func mustFor(t *testing.T, role, app string) []string {
	t.Helper()
	perms, err := For(role, app)
	if err != nil {
		t.Fatalf("For(%q,%q): %v", role, app, err)
	}
	return perms
}

func TestForTable(t *testing.T) {
	cases := []struct {
		name    string
		role    string
		app     string
		has     []string
		hasNot  []string
		noOther bool // every permission belongs to app
	}{
		{name: "dating moderator stays in dating", role: roles.Moderator, app: AppDating,
			has:    []string{"dating:reports.act", "dating:photos.review"},
			hasNot: []string{"dating:users.ban", "dating:panic.reveal", "food:reviews.moderate"}, noOther: true},
		// Admin console Wave 2 — the Dating dashboard's stats, panic triage and
		// risk queue. Support sees the counts but cannot triage; a KYC reviewer
		// reveals panic GPS but does not triage or read risk.
		{name: "dating moderator runs the dashboard", role: roles.Moderator, app: AppDating,
			has:    []string{"dating:stats.read", "dating:panic.act", "dating:risk.read"},
			hasNot: []string{"dating:users.ban", "dating:panic.reveal"}, noOther: true},
		{name: "dating support sees counts only", role: roles.Support, app: AppDating,
			has: []string{"dating:stats.read", "dating:reports.read"}, hasNot: []string{"dating:panic.act", "dating:risk.read", "dating:reports.act"}, noOther: true},
		{name: "dating kyc reviewer does not triage", role: roles.KYCReviewer, app: AppDating,
			has: []string{"dating:panic.reveal"}, hasNot: []string{"dating:stats.read", "dating:panic.act", "dating:risk.read"}, noOther: true},
		{name: "dating admin holds the new permissions", role: roles.Admin, app: AppDating,
			has: []string{"dating:stats.read", "dating:panic.act", "dating:risk.read", "dating:users.ban"}, noOther: true},
		{name: "food admin", role: roles.Admin, app: AppFood,
			has: []string{"food:restaurant.approve", "food:refund.issue", "food:audit.read"}, noOther: true},
		{name: "commerce kyc reviewer", role: roles.KYCReviewer, app: AppCommerce,
			has: []string{"commerce:kyc.reveal", "commerce:kyc.verify"}, hasNot: []string{"commerce:seller.approve"}, noOther: true},
		{name: "payments finance", role: roles.Finance, app: AppPayments,
			has: []string{"payments:refund.issue"}, hasNot: []string{"payments:applications.manage"}, noOther: true},
		{name: "monetization finance", role: roles.Finance, app: AppMonetization,
			has: []string{"monetization:fund.settle"}, noOther: true},
		{name: "support has no money or bans", role: roles.Support, app: "",
			has:    []string{"food:orders.read", "platform:users.read"},
			hasNot: []string{"payments:refund.issue", "dating:users.ban", "platform:users.suspend", "commerce:kyc.reveal"}},
		{name: "platform admin reaches every app but not roles.manage", role: roles.Admin, app: "",
			has:    []string{"dating:users.ban", "food:restaurant.approve", "commerce:seller.approve", "wallet:wallet.freeze", "platform:users.suspend"},
			hasNot: []string{"platform:roles.manage", AllAppsAuditRead}},
		{name: "platform moderator reaches every app's moderation", role: roles.Moderator, app: "",
			has:    []string{"dating:reports.act", "food:reviews.moderate", "qa:questions.moderate", "chat:reports.act"},
			hasNot: []string{"food:restaurant.approve", "payments:refund.issue"}},
		{name: "superadmin holds everything", role: roles.Superadmin, app: "",
			has: []string{"platform:roles.manage", AllAppsAuditRead, "commerce:kyc.reveal", "monetization:fund.settle"}},
		{name: "platform auditor", role: roles.Auditor, app: "",
			has: []string{AllAppsAuditRead, "dating:audit.read", "platform:audit.read"}, hasNot: []string{"dating:reports.read"}},
		{name: "ecosystem role holds nothing", role: roles.Seller, app: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			perms := mustFor(t, tc.role, tc.app)
			for _, p := range tc.has {
				if !contains(perms, p) {
					t.Errorf("missing %q in %v", p, perms)
				}
			}
			for _, p := range tc.hasNot {
				if contains(perms, p) {
					t.Errorf("must not hold %q", p)
				}
			}
			if tc.noOther {
				for _, p := range perms {
					if !strings.HasPrefix(p, tc.app+":") {
						t.Errorf("%s/%s leaked %q", tc.role, tc.app, p)
					}
				}
			}
			if tc.role == roles.Seller && len(perms) != 0 {
				t.Errorf("seller holds %v", perms)
			}
		})
	}
}

// TestAppModeratorNeverCrossesApps: for every app-scopable role and every app,
// the permissions granted name only that app.
func TestAppModeratorNeverCrossesApps(t *testing.T) {
	for _, role := range roles.AdminRoles() {
		if role == roles.Superadmin {
			continue
		}
		for _, app := range Apps() {
			perms, err := For(role, app)
			if errors.Is(err, ErrNoPermissionsInApp) {
				continue
			}
			if err != nil {
				t.Fatalf("For(%q,%q): %v", role, app, err)
			}
			for _, p := range perms {
				if !strings.HasPrefix(p, app+":") {
					t.Fatalf("For(%q,%q) contains %q", role, app, p)
				}
			}
		}
	}
	for _, p := range mustFor(t, roles.Moderator, AppDating) {
		if strings.HasPrefix(p, "food:") {
			t.Fatalf("dating moderator holds %q", p)
		}
	}
}

func TestForRefusals(t *testing.T) {
	cases := []struct {
		role, app string
		want      error
	}{
		{"bogus", "", ErrUnknownRole},
		{"customer", AppDating, ErrUnknownRole},
		{roles.Moderator, "casino", ErrUnknownApp},
		{roles.Superadmin, AppDating, ErrPlatformOnly},
		{roles.Finance, AppQA, ErrNoPermissionsInApp},
		{roles.Seller, AppCommerce, ErrNoPermissionsInApp},
	}
	for _, tc := range cases {
		if _, err := For(tc.role, tc.app); !errors.Is(err, tc.want) {
			t.Errorf("For(%q,%q) = %v, want %v", tc.role, tc.app, err, tc.want)
		}
	}
}

var permShape = regexp.MustCompile(`^([a-z_]+|\*):[a-z_]+\.[a-z_]+$`)

func TestCatalogueShape(t *testing.T) {
	if len(catalogue) != len(apps) {
		t.Fatalf("catalogue has %d apps, vocabulary %d", len(catalogue), len(apps))
	}
	all := mustFor(t, roles.Superadmin, "")
	for _, p := range all {
		if !permShape.MatchString(p) {
			t.Errorf("malformed permission %q", p)
		}
	}
	for _, app := range apps {
		seen := map[string]bool{}
		for _, e := range catalogue[app] {
			if seen[e.action] {
				t.Errorf("%s:%s listed twice", app, e.action)
			}
			seen[e.action] = true
			for _, r := range e.roles {
				if !roles.IsAdminRole(r) || r == roles.Admin || r == roles.Superadmin {
					t.Errorf("%s:%s names role %q (admin/superadmin are implicit)", app, e.action, r)
				}
			}
		}
	}
	for _, p := range []string{"dating:reports.act", "food:restaurant.approve", "commerce:seller.approve",
		"commerce:kyc.reveal", "payments:refund.issue", "monetization:fund.settle", "platform:roles.manage"} {
		if !contains(all, p) {
			t.Errorf("catalogue lacks %q", p)
		}
	}
}

func TestResolve(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Second)
	future := now.Add(time.Hour)

	t.Run("expired role grants nothing", func(t *testing.T) {
		got := Resolve([]Grant{{Role: roles.Admin, App: AppFood, ExpiresAt: &past}}, now)
		if len(got.Apps) != 0 || len(got.Platform) != 0 {
			t.Fatalf("expired grant resolved to %+v", got)
		}
		exact := now
		if got := Resolve([]Grant{{Role: roles.Superadmin, ExpiresAt: &exact}}, now); len(got.Platform) != 0 {
			t.Fatalf("grant expiring exactly now resolved to %+v", got)
		}
	})
	t.Run("future expiry is active", func(t *testing.T) {
		got := Resolve([]Grant{{Role: roles.Moderator, App: AppDating, ExpiresAt: &future}}, now)
		if !got.Has("dating:reports.act") {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("app-scoped grants union without crossing", func(t *testing.T) {
		got := Resolve([]Grant{
			{Role: roles.Moderator, App: AppDating},
			{Role: roles.Finance, App: AppPayments},
		}, now)
		if len(got.Apps) != 2 || got.Has("food:reviews.moderate") || !got.Has("payments:refund.issue") {
			t.Fatalf("got %+v", got)
		}
		if len(got.Platform) != 0 {
			t.Fatalf("app grants produced platform permissions %v", got.Platform)
		}
	})
	t.Run("unknown rows add nothing", func(t *testing.T) {
		got := Resolve([]Grant{{Role: "bogus"}, {Role: roles.Moderator, App: "casino"}, {Role: roles.Superadmin, App: AppDating}}, now)
		if len(got.Apps) != 0 || len(got.Platform) != 0 {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("platform permissions separated", func(t *testing.T) {
		got := Resolve([]Grant{{Role: roles.Superadmin}}, now)
		if _, ok := got.Apps[AppPlatform]; ok {
			t.Fatal("platform app must not appear under apps")
		}
		if !contains(got.Platform, "platform:roles.manage") || !contains(got.Platform, AllAppsAuditRead) {
			t.Fatalf("platform=%v", got.Platform)
		}
		if len(got.Apps) != len(apps)-1 {
			t.Fatalf("superadmin apps=%d want %d", len(got.Apps), len(apps)-1)
		}
	})
	t.Run("empty is non-nil", func(t *testing.T) {
		got := Resolve(nil, now)
		if got.Apps == nil || got.Platform == nil {
			t.Fatal("nil maps serialise as null")
		}
	})
}
