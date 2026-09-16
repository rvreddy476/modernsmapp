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
		// Admin console Wave 2 — food, commerce and trust-safety dashboards.
		{name: "food admin holds the console permissions", role: roles.Admin, app: AppFood,
			has: []string{"food:stats.read", "food:restaurant.suspend", "food:delivery_partner.suspend", "food:payout_accounts.read",
				"food:settlement.generate", "food:coupons.manage", "food:service_areas.manage", "food:fraud.read", "food:reports.read"}, noOther: true},
		{name: "food finance runs settlements", role: roles.Finance, app: AppFood,
			has: []string{"food:stats.read", "food:refunds.read", "food:settlement.read", "food:settlement.generate",
				"food:settlement.mark_paid", "food:payout_accounts.read", "food:reports.read", "food:refund.issue"},
			hasNot: []string{"food:restaurant.suspend", "food:coupons.manage", "food:fraud.read"}, noOther: true},
		{name: "food moderator moderates, sees no money", role: roles.Moderator, app: AppFood,
			has:    []string{"food:reviews.moderate"},
			hasNot: []string{"food:stats.read", "food:settlement.generate", "food:payout_accounts.read", "food:restaurant.suspend", "food:reports.read", "food:refunds.read"}, noOther: true},
		{name: "food support reads only", role: roles.Support, app: AppFood,
			has: []string{"food:stats.read", "food:refunds.read", "food:reports.read"}, hasNot: []string{"food:refund.issue", "food:settlement.read", "food:coupons.manage"}, noOther: true},
		{name: "commerce admin holds the console permissions", role: roles.Admin, app: AppCommerce,
			has: []string{"commerce:stats.read", "commerce:sellers.read", "commerce:banners.edit", "commerce:jobs.read", "commerce:compliance.read", "commerce:compliance.sweep"}, noOther: true},
		{name: "commerce moderator reads the seller queue", role: roles.Moderator, app: AppCommerce,
			has: []string{"commerce:sellers.read"}, hasNot: []string{"commerce:stats.read", "commerce:compliance.sweep", "commerce:seller.approve", "commerce:banners.edit"}, noOther: true},
		{name: "commerce finance sees stats", role: roles.Finance, app: AppCommerce,
			has: []string{"commerce:stats.read", "commerce:cod.settle", "commerce:refund.issue"}, hasNot: []string{"commerce:sellers.read", "commerce:compliance.read"}, noOther: true},
		{name: "commerce support", role: roles.Support, app: AppCommerce,
			has: []string{"commerce:stats.read", "commerce:sellers.read"}, hasNot: []string{"commerce:banners.edit", "commerce:jobs.read"}, noOther: true},
		{name: "trust_safety moderator reads its queues", role: roles.Moderator, app: AppTrustSafety,
			has: []string{"trust_safety:stats.read", "trust_safety:appeals.read", "trust_safety:grievances.read", "trust_safety:strikes.read",
				"trust_safety:media_labels.read", "trust_safety:keyword_filters.read"},
			hasNot: []string{"trust_safety:verification.review", "trust_safety:strikes.manage"}, noOther: true},
		{name: "trust_safety kyc reviewer reviews verification", role: roles.KYCReviewer, app: AppTrustSafety,
			has: []string{"trust_safety:verification.review"}, hasNot: []string{"trust_safety:appeals.read", "trust_safety:stats.read"}, noOther: true},
		{name: "trust_safety support", role: roles.Support, app: AppTrustSafety,
			has:    []string{"trust_safety:grievances.read", "trust_safety:appeals.read", "trust_safety:reports.read"},
			hasNot: []string{"trust_safety:grievances.act", "trust_safety:appeals.act", "trust_safety:verification.review"}, noOther: true},
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

// consolePermissionHolders is every permission the food, commerce and
// trust-safety admin token routes check that the catalogue gained for the admin
// console, with EXACTLY the roles (besides implicit admin and superadmin) that
// must hold it. The strings mirror each service's AdminPermissions list.
var consolePermissionHolders = map[string][]string{
	"food:stats.read":                   {roles.Finance, roles.Support},
	"food:restaurant.suspend":           nil,
	"food:delivery_partner.suspend":     nil,
	"food:payout_accounts.read":         {roles.Finance},
	"food:refunds.read":                 {roles.Finance, roles.Support},
	"food:settlement.read":              {roles.Finance},
	"food:settlement.generate":          {roles.Finance},
	"food:coupons.manage":               nil,
	"food:service_areas.manage":         nil,
	"food:reports.read":                 {roles.Finance, roles.Support},
	"food:fraud.read":                   nil,
	"commerce:stats.read":               {roles.Finance, roles.Support},
	"commerce:sellers.read":             {roles.Moderator, roles.Support},
	"commerce:banners.edit":             nil,
	"commerce:jobs.read":                nil,
	"commerce:compliance.read":          nil,
	"commerce:compliance.sweep":         nil,
	"trust_safety:stats.read":           {roles.Moderator},
	"trust_safety:appeals.read":         {roles.Moderator, roles.Support},
	"trust_safety:grievances.read":      {roles.Moderator, roles.Support},
	"trust_safety:strikes.read":         {roles.Moderator},
	"trust_safety:verification.review":  {roles.KYCReviewer},
	"trust_safety:media_labels.read":    {roles.Moderator},
	"trust_safety:keyword_filters.read": {roles.Moderator},
}

// TestConsolePermissionsExactHolders: each new permission resolves for its
// intended roles (app-scoped and platform-wide), for admin and superadmin,
// and for no other role.
func TestConsolePermissionsExactHolders(t *testing.T) {
	for perm, holders := range consolePermissionHolders {
		app := appOf(perm)
		want := map[string]bool{roles.Admin: true, roles.Superadmin: true}
		for _, r := range holders {
			want[r] = true
		}
		for _, role := range roles.AdminRoles() {
			platform := mustFor(t, role, "")
			if got := contains(platform, perm); got != want[role] {
				t.Errorf("platform-wide %s holds %s = %v, want %v", role, perm, got, want[role])
			}
			if role == roles.Superadmin {
				continue
			}
			scoped, err := For(role, app)
			if err != nil && !errors.Is(err, ErrNoPermissionsInApp) {
				t.Fatalf("For(%q,%q): %v", role, app, err)
			}
			if got := contains(scoped, perm); got != want[role] {
				t.Errorf("%s scoped to %s holds %s = %v, want %v", role, app, perm, got, want[role])
			}
		}
	}
}

// TestModeratorNeverHoldsMoneyOrReview: moderator, at any scope, never holds
// settlements, payout accounts, suspensions, identity verification or a
// compliance sweep.
func TestModeratorNeverHoldsMoneyOrReview(t *testing.T) {
	forbidden := []string{
		"food:settlement.generate", "food:settlement.read", "food:settlement.mark_paid",
		"food:payout_accounts.read", "food:refunds.read", "food:refund.issue", "food:reports.read",
		"food:restaurant.suspend", "food:delivery_partner.suspend",
		"trust_safety:verification.review", "commerce:compliance.sweep", "commerce:stats.read",
		"commerce:seller.suspend", "commerce:payouts.read",
	}
	scopes := [][]string{mustFor(t, roles.Moderator, "")}
	for _, app := range []string{AppFood, AppCommerce, AppTrustSafety} {
		scopes = append(scopes, mustFor(t, roles.Moderator, app))
	}
	for _, perms := range scopes {
		for _, p := range forbidden {
			if contains(perms, p) {
				t.Errorf("moderator holds %q", p)
			}
		}
	}
}

// TestSupportHoldsNoWrites: support's only non-read permissions are the two
// ticket/complaint handling actions it already had; nothing new is a write.
func TestSupportHoldsNoWrites(t *testing.T) {
	allowedActs := map[string]bool{"food:tickets.act": true, "rider:complaints.act": true}
	for _, p := range mustFor(t, roles.Support, "") {
		if strings.HasSuffix(p, ".read") || allowedActs[p] {
			continue
		}
		t.Errorf("support holds write permission %q", p)
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
