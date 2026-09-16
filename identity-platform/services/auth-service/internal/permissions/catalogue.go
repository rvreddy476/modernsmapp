// Package permissions is the admin-console permission catalogue: which
// applications exist, which permissions each one has, and which role holds
// which permission.
//
// A permission is "<app>:<resource>.<action>", e.g. "dating:reports.act".
// One special form, "*:audit.read", means "read the audit trail of every
// application" and is held only platform-wide.
//
// # HOW A ROLE MAPS TO PERMISSIONS
//
//   - superadmin (platform-wide only): every permission, including
//     platform:roles.manage and *:audit.read.
//   - admin: every permission of the app EXCEPT the superadmin-only ones.
//   - any other role: exactly the permissions whose entry names it.
//   - platform-wide (app ""): the role's permissions in EVERY app, so a
//     platform-wide admin or moderator keeps today's all-applications reach.
//   - app-scoped: the role's permissions in that one app and nowhere else.
//   - ecosystem roles (seller, …) hold no admin permission at all.
//
// # ADDING A PERMISSION
//
// Add one entry to catalogue below, naming the roles (other than admin and
// superadmin, which are implicit) that hold it. Nothing else changes.
package permissions

import (
	"errors"
	"sort"
	"time"

	"github.com/atpost/identity-auth-service/internal/roles"
)

// Applications. "platform" is identity itself: roles, platform-wide users.
const (
	AppDating       = "dating"
	AppFood         = "food"
	AppCommerce     = "commerce"
	AppMonetization = "monetization"
	AppPayments     = "payments"
	AppWallet       = "wallet"
	AppSocial       = "social"
	AppTube         = "tube"
	AppQA           = "qa"
	AppChat         = "chat"
	AppRider        = "rider"
	AppTrustSafety  = "trust_safety"
	AppPlatform     = "platform"
)

// AllAppsAuditRead is the cross-application audit permission.
const AllAppsAuditRead = "*:audit.read"

// apps is the canonical order. The auth.user_roles app CHECK lists exactly these.
var apps = []string{
	AppDating, AppFood, AppCommerce, AppMonetization, AppPayments, AppWallet,
	AppSocial, AppTube, AppQA, AppChat, AppRider, AppTrustSafety, AppPlatform,
}

// entry is one permission of an app and the roles that hold it besides the
// implicit admin and superadmin. superOnly withholds it from admin.
type entry struct {
	action    string
	roles     []string
	superOnly bool
}

func p(action string, holders ...string) entry { return entry{action: action, roles: holders} }

const (
	mod  = roles.Moderator
	fin  = roles.Finance
	sup  = roles.Support
	kyc  = roles.KYCReviewer
	audr = roles.Auditor
)

// catalogue is the whole permission table. Keep it small; later lanes add
// entries here and nowhere else.
var catalogue = map[string][]entry{
	AppDating: {
		// stats.read: the dashboard's counts (no personal data).
		p("stats.read", mod, sup),
		p("reports.read", mod, sup),
		p("reports.act", mod),
		p("photos.review", mod),
		p("selfie.review", mod, kyc),
		p("verification.review", mod, kyc),
		p("panic.read", mod),
		// panic.act: acknowledge and resolve an incident (no coordinates).
		p("panic.act", mod),
		p("panic.reveal", kyc),
		// risk.read: the fake-account risk queue.
		p("risk.read", mod),
		p("users.ban"),
		p("audit.read", audr),
	},
	AppFood: {
		// stats.read includes today's GMV and unpaid settlement totals: not moderator,
		// matching commerce.
		p("stats.read", fin, sup),
		p("restaurant.approve"),
		p("restaurant.suspend"),
		p("delivery_partner.approve"),
		p("delivery_partner.suspend"),
		p("documents.review", kyc),
		p("kyc.reveal", kyc),
		p("payout_accounts.read", fin),
		p("orders.read", mod, fin, sup),
		p("orders.cancel"),
		p("refund.issue", fin),
		p("refunds.read", fin, sup),
		p("settlement.read", fin),
		p("settlement.generate", fin),
		p("settlement.mark_paid", fin),
		p("tickets.act", mod, sup),
		p("reviews.moderate", mod),
		p("menu.moderate", mod),
		p("coupons.manage"),
		p("service_areas.manage"),
		// reports.read covers payment-recon, refunds and revenue reports, so it
		// is money: finance and support, never moderator.
		p("reports.read", fin, sup),
		p("fraud.read"),
		p("audit.read", audr),
	},
	AppCommerce: {
		// stats.read includes pending payout amounts and GMV: not moderator.
		p("stats.read", fin, sup),
		p("sellers.read", mod, sup),
		p("seller.approve"),
		p("seller.suspend"),
		p("products.moderate", mod),
		p("kyc.verify", kyc),
		p("kyc.reveal", kyc),
		p("orders.read", mod, fin, sup),
		p("payouts.read", fin),
		p("cod.settle", fin),
		p("refund.issue", fin),
		p("catalogue.edit"),
		p("banners.edit"),
		p("jobs.read"),
		p("compliance.read"),
		p("compliance.sweep"),
		p("audit.read", audr),
	},
	// Money dashboard: every monetization and payments permission is money, so
	// moderator holds none of them and support holds reads only.
	AppMonetization: {
		// stats.read includes fund and payout totals: finance and support.
		p("stats.read", fin, sup),
		p("fraud.review", fin),
		// wallet.freeze / unfreeze / rebuild and creators.suspend are account-safety
		// actions on a creator, like food restaurant.suspend and commerce
		// seller.suspend: admin only, not finance.
		p("wallet.freeze"),
		p("wallet.unfreeze"),
		p("wallet.rebuild"),
		p("fund.read", fin),
		p("fund.rates", fin),
		p("fund.budget", fin),
		p("fund.settle", fin),
		p("fund.reverse", fin),
		// creators.read: no monetization route checks it today.
		p("creators.read", fin, sup),
		p("creators.suspend"),
		p("disputes.read", fin, sup),
		p("disputes.act", fin),
		p("refund.issue", fin),
		p("payouts.read", fin, sup),
		p("audit.read", audr),
	},
	AppPayments: {
		// stats.read and reconciliation.read are money totals across applications:
		// finance only.
		p("stats.read", fin),
		p("intents.read", fin, sup),
		p("reconciliation.read", fin),
		p("applications.read", fin),
		p("refunds.read", fin, sup),
		p("refund.issue", fin),
		// disputes.act and payouts.approve: no payments admin route checks them today.
		p("disputes.act", fin),
		p("payouts.approve", fin),
		p("applications.manage"),
		p("audit.read", audr),
	},
	AppWallet: {
		p("wallets.read", fin, sup),
		p("wallet.freeze", fin),
		p("wallet.unfreeze", fin),
		p("disputes.act", fin),
		p("audit.read", audr),
	},
	AppSocial: {
		p("posts.moderate", mod),
		p("reels.moderate", mod),
		p("comments.moderate", mod),
		p("reports.act", mod),
		p("pages.moderate", mod),
		p("users.read", mod, sup),
		p("audit.read", audr),
	},
	AppTube: {
		p("videos.moderate", mod),
		p("channels.moderate", mod),
		p("comments.moderate", mod),
		p("reports.act", mod),
		p("audit.read", audr),
	},
	AppQA: {
		p("questions.moderate", mod),
		p("answers.moderate", mod),
		p("reports.act", mod),
		p("audit.read", audr),
	},
	AppChat: {
		p("channels.moderate", mod),
		p("groups.moderate", mod),
		p("reports.act", mod),
		p("audit.read", audr),
	},
	AppRider: {
		p("partners.approve"),
		p("documents.review", kyc),
		p("kyc.reveal", kyc),
		p("rides.read", mod, fin, sup),
		p("complaints.act", mod, sup),
		p("incidents.read", mod),
		p("incidents.reveal", kyc),
		p("payments.settle", fin),
		p("fares.manage"),
		p("audit.read", audr),
	},
	AppTrustSafety: {
		p("stats.read", mod),
		p("reports.read", mod, sup),
		p("reports.act", mod),
		p("appeals.read", mod, sup),
		p("appeals.act", mod),
		p("grievances.read", mod, sup),
		p("grievances.act", mod),
		p("strikes.read", mod),
		p("strikes.manage"),
		// verification.review: identity documents, so KYC reviewer, never moderator.
		p("verification.review", kyc),
		p("media_labels.read", mod),
		p("keyword_filters.read", mod),
		p("audit.read", audr),
	},
	AppPlatform: {
		p("users.read", mod, sup),
		p("users.suspend"),
		p("sessions.revoke"),
		p("roles.read", audr),
		{action: "roles.manage", superOnly: true},
		p("audit.read", audr),
	},
}

// Errors for an unknown role or app, or a role that cannot hold that scope.
var (
	ErrUnknownRole = errors.New("unknown admin role")
	ErrUnknownApp  = errors.New("unknown application")
	// ErrPlatformOnly: superadmin cannot be scoped to one application.
	ErrPlatformOnly = errors.New("superadmin is platform-wide and cannot be scoped to an application")
	// ErrNoPermissionsInApp: the role holds nothing in that app (e.g. finance
	// on qa), so a grant would be a row that does nothing.
	ErrNoPermissionsInApp = errors.New("this role has no permissions in that application")
)

// Apps returns the application vocabulary in canonical order.
func Apps() []string { return append([]string(nil), apps...) }

// ValidApp reports whether a is a known application.
func ValidApp(a string) bool {
	_, ok := catalogue[a]
	return ok
}

// For is the pure (role, app) -> permissions mapping. app "" means
// platform-wide. The result is sorted and deduplicated. Ecosystem roles return
// an empty set without error; a role or app outside the vocabulary errors.
func For(role, app string) ([]string, error) {
	if app != "" && !ValidApp(app) {
		return nil, ErrUnknownApp
	}
	if roles.IsEcosystem(role) {
		if app != "" {
			return nil, ErrNoPermissionsInApp
		}
		return []string{}, nil
	}
	if !roles.IsAdminRole(role) {
		return nil, ErrUnknownRole
	}
	if role == roles.Superadmin && app != "" {
		return nil, ErrPlatformOnly
	}

	scope := apps
	if app != "" {
		scope = []string{app}
	}
	set := map[string]struct{}{}
	for _, a := range scope {
		for _, e := range catalogue[a] {
			if holds(role, e) {
				set[a+":"+e.action] = struct{}{}
			}
		}
	}
	if app == "" && (role == roles.Superadmin || role == roles.Auditor) {
		set[AllAppsAuditRead] = struct{}{}
	}
	if app != "" && len(set) == 0 {
		return nil, ErrNoPermissionsInApp
	}
	out := make([]string, 0, len(set))
	for perm := range set {
		out = append(out, perm)
	}
	sort.Strings(out)
	return out, nil
}

func holds(role string, e entry) bool {
	switch role {
	case roles.Superadmin:
		return true
	case roles.Admin:
		return !e.superOnly
	}
	for _, r := range e.roles {
		if r == role {
			return true
		}
	}
	return false
}

// Grant is one role row as the resolver sees it. App "" is platform-wide.
type Grant struct {
	Role      string
	App       string
	ExpiresAt *time.Time
}

// Active reports whether a grant is in force at now. An expiry at or before
// now grants nothing.
func (g Grant) Active(now time.Time) bool {
	return g.ExpiresAt == nil || g.ExpiresAt.After(now)
}

// Admin is the resolved permission map a client renders navigation from and a
// service enforces. Apps holds "<app>:..." permissions keyed by app (never the
// platform app); Platform holds "platform:..." and "*:..." permissions. Both
// are always non-nil so the JSON is {} and [] rather than null.
type Admin struct {
	Apps     map[string][]string `json:"apps"`
	Platform []string            `json:"platform"`
}

// Resolve folds a user's grants into one Admin map. Expired grants, unknown
// roles and unknown apps contribute nothing — a bad row never widens access.
func Resolve(grants []Grant, now time.Time) Admin {
	appSets := map[string]map[string]struct{}{}
	platformSet := map[string]struct{}{}
	for _, g := range grants {
		if !g.Active(now) {
			continue
		}
		perms, err := For(g.Role, g.App)
		if err != nil {
			continue
		}
		for _, perm := range perms {
			a := appOf(perm)
			if a == AppPlatform || a == "*" {
				platformSet[perm] = struct{}{}
				continue
			}
			if appSets[a] == nil {
				appSets[a] = map[string]struct{}{}
			}
			appSets[a][perm] = struct{}{}
		}
	}
	out := Admin{Apps: map[string][]string{}, Platform: sortedKeys(platformSet)}
	for a, set := range appSets {
		out.Apps[a] = sortedKeys(set)
	}
	return out
}

// Has reports whether the map holds perm exactly. (A platform-wide auditor or
// superadmin already holds every "<app>:audit.read" individually.)
func (a Admin) Has(perm string) bool {
	for _, p := range a.Platform {
		if p == perm {
			return true
		}
	}
	for _, p := range a.Apps[appOf(perm)] {
		if p == perm {
			return true
		}
	}
	return false
}

func appOf(perm string) string {
	for i := 0; i < len(perm); i++ {
		if perm[i] == ':' {
			return perm[:i]
		}
	}
	return ""
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
