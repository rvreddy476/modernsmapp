package adminauth

import (
	"sort"
	"strings"
)

// The admin-console catalogue the Access page offers choices from: which
// roles exist, which applications, and which permissions each role holds in
// each application.
//
// Identity owns this table (identity-platform/services/auth-service/internal/
// permissions/catalogue.go, roles in internal/roles/roles.go) and resolves
// every grant from it, but exposes no route that returns it. This is a
// mirror, kept in the same shape so a diff between the two files is
// line-for-line; it is used ONLY to populate the page's pickers and to
// sanity-check the route tables (TestCatalogue_CoversEveryDeclaredPermission).
// It never decides access: the gate enforces what identity resolved, and
// identity refuses a grant its own table does not allow (INVALID_APP,
// ROLE_NOT_SCOPABLE), so a stale mirror can only offer a choice identity
// then rejects, never widen anything.
//
// When identity's catalogue changes, change this file the same way.

// Roles an admin grant may name (identity roles.AdminRoles()).
const (
	RoleSuperadmin  = "superadmin"
	RoleAdmin       = "admin"
	RoleModerator   = "moderator"
	RoleFinance     = "finance"
	RoleSupport     = "support"
	RoleKYCReviewer = "kyc_reviewer"
	RoleAuditor     = "auditor"
)

// Applications, in identity's canonical order. "platform" is identity itself.
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

// AllAppsAuditRead is the cross-application audit permission, held only
// platform-wide by superadmin and auditor.
const AllAppsAuditRead = "*:audit.read"

// RoleInfo is one role as the page renders it.
type RoleInfo struct {
	Role  string `json:"role"`
	Label string `json:"label"`
	// PlatformOnly: the role cannot be scoped to one application (superadmin).
	PlatformOnly bool `json:"platform_only"`
}

// PermissionInfo is one permission of an app and every role that holds it,
// the implicit admin and superadmin included.
type PermissionInfo struct {
	Permission string   `json:"permission"`
	Roles      []string `json:"roles"`
}

// CatalogueResponse is GET /v1/admin/access/catalogue.
type CatalogueResponse struct {
	// Source says where this came from: admin-service's mirror of identity's
	// catalogue, not identity itself.
	Source string     `json:"source"`
	Roles  []RoleInfo `json:"roles"`
	Apps   []string   `json:"apps"`
	// Permissions lists every permission per app with its holders.
	Permissions map[string][]PermissionInfo `json:"permissions"`
	// RolePermissions is role → app → the permissions an app-scoped grant of
	// that role resolves to; only apps where the role holds something appear.
	// A platform-wide grant is the union over every app (plus *:audit.read
	// for superadmin and auditor).
	RolePermissions map[string]map[string][]string `json:"role_permissions"`
}

var adminRoles = []RoleInfo{
	{Role: RoleSuperadmin, Label: "Super admin", PlatformOnly: true},
	{Role: RoleAdmin, Label: "Admin"},
	{Role: RoleModerator, Label: "Moderator"},
	{Role: RoleFinance, Label: "Finance"},
	{Role: RoleSupport, Label: "Support"},
	{Role: RoleKYCReviewer, Label: "KYC reviewer"},
	{Role: RoleAuditor, Label: "Auditor"},
}

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
	mod  = RoleModerator
	fin  = RoleFinance
	sup  = RoleSupport
	kyc  = RoleKYCReviewer
	audr = RoleAuditor
)

// confinedPayments mirrors identity's: the payments view of one product
// application, held under that product app.
func confinedPayments(withFinance bool) []entry {
	f := func(holders ...string) []string {
		if withFinance {
			return holders
		}
		out := []string{}
		for _, r := range holders {
			if r != fin {
				out = append(out, r)
			}
		}
		return out
	}
	return []entry{
		p("payments_stats.read", f(fin)...),
		p("payments_intents.read", f(fin, sup)...),
		p("payments_reconciliation.read", f(fin)...),
		p("payments_applications.read", f(fin)...),
		p("payments_refunds.read", f(fin, sup)...),
		p("payments_refund.issue", f(fin)...),
		{action: "payments_applications.manage", superOnly: true},
		p("payments_audit.read", audr),
	}
}

// catalogue mirrors identity's table entry for entry.
var catalogue = map[string][]entry{
	AppDating: append([]entry{
		p("stats.read", mod, sup),
		p("reports.read", mod, sup),
		p("reports.act", mod),
		p("photos.review", mod),
		p("selfie.review", mod, kyc),
		p("verification.review", mod, kyc),
		p("panic.read", mod),
		p("panic.act", mod),
		p("panic.reveal", kyc),
		p("risk.read", mod),
		p("users.ban"),
		p("audit.read", audr),
	}, confinedPayments(false)...),
	AppFood: append([]entry{
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
		p("reports.read", fin, sup),
		p("fraud.read"),
		p("audit.read", audr),
	}, confinedPayments(true)...),
	AppCommerce: append([]entry{
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
	}, confinedPayments(true)...),
	AppMonetization: {
		p("stats.read", fin, sup),
		p("fraud.review", fin),
		p("wallet.freeze"),
		p("wallet.unfreeze"),
		p("wallet.rebuild"),
		p("fund.read", fin),
		p("fund.rates", fin),
		p("fund.budget", fin),
		p("fund.settle", fin),
		p("fund.reverse", fin),
		p("creators.read", fin, sup),
		p("creators.suspend"),
		p("disputes.read", fin, sup),
		p("disputes.act", fin),
		p("refund.issue", fin),
		p("payouts.read", fin, sup),
		p("audit.read", audr),
	},
	AppPayments: {
		p("stats.read", fin),
		p("intents.read", fin, sup),
		p("reconciliation.read", fin),
		p("applications.read", fin),
		p("refunds.read", fin, sup),
		p("refund.issue", fin),
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
		p("stats.read", mod, sup),
		p("posts.moderate", mod),
		p("posts.remove", mod),
		p("reels.moderate", mod),
		p("reels.remove", mod),
		p("comments.moderate", mod),
		p("comments.remove", mod),
		p("reports.act", mod),
		p("pages.moderate", mod),
		p("pages.suspend"),
		p("pages.disable"),
		p("documents.review", kyc),
		p("users.read", mod, sup),
		p("audit.read", audr),
	},
	AppTube: {
		p("stats.read", mod, sup),
		p("videos.moderate", mod),
		p("videos.remove", mod),
		p("channels.moderate", mod),
		p("comments.moderate", mod),
		p("reports.act", mod),
		p("audit.read", audr),
	},
	AppQA: {
		p("stats.read", mod, sup),
		p("reports.read", mod, sup),
		p("reports.act", mod),
		p("questions.moderate", mod),
		p("questions.merge", mod),
		p("answers.moderate", mod),
		p("comments.moderate", mod),
		p("audit.read", audr),
	},
	AppChat: {
		p("stats.read", mod, sup),
		p("reports.read", mod, sup),
		p("reports.act", mod),
		p("channels.moderate", mod),
		p("groups.moderate", mod),
		p("audit.read", audr),
	},
	AppRider: {
		p("stats.read", fin, sup),
		p("partners.read", mod, sup),
		p("partners.approve"),
		p("partners.suspend"),
		p("documents.review", kyc),
		p("kyc.reveal", kyc),
		p("vehicles.review", kyc),
		p("payments.read", fin, sup),
		p("payments.settle", fin),
		p("payments.reject", fin),
		p("rides.read", mod, fin, sup),
		p("rides.cancel"),
		p("ratings.moderate", mod),
		p("complaints.act", mod, sup),
		p("incidents.read", mod, sup),
		p("incidents.act", mod),
		p("incidents.reveal", kyc),
		p("cities.manage"),
		p("fares.manage"),
		p("reports.read", fin),
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
		p("verification.review", kyc),
		p("media_labels.read", mod),
		p("keyword_filters.read", mod),
		p("audit.read", audr),
	},
	AppPlatform: {
		p("users.read", mod, sup),
		p("users.search", mod, sup),
		p("users.suspend"),
		p("sessions.revoke"),
		p("roles.read", audr),
		{action: "roles.manage", superOnly: true},
		p("audit.read", audr),
	},
}

func holds(role string, e entry) bool {
	switch role {
	case RoleSuperadmin:
		return true
	case RoleAdmin:
		return !e.superOnly
	}
	for _, r := range e.roles {
		if r == role {
			return true
		}
	}
	return false
}

// holders lists every role holding e, the implicit two first.
func holders(e entry) []string {
	out := []string{RoleSuperadmin}
	if !e.superOnly {
		out = append(out, RoleAdmin)
	}
	return append(out, e.roles...)
}

// Apps returns the application vocabulary in canonical order.
func Apps() []string { return append([]string(nil), apps...) }

// AdminRoles returns the grantable roles in canonical order.
func AdminRoles() []RoleInfo { return append([]RoleInfo(nil), adminRoles...) }

// KnownPermission reports whether perm is in the catalogue (or is the
// cross-app audit permission).
func KnownPermission(perm string) bool {
	if perm == AllAppsAuditRead {
		return true
	}
	i := strings.IndexByte(perm, ':')
	if i <= 0 {
		return false
	}
	for _, e := range catalogue[perm[:i]] {
		if e.action == perm[i+1:] {
			return true
		}
	}
	return false
}

// Catalogue builds the page's catalogue from the mirror.
func Catalogue() CatalogueResponse {
	out := CatalogueResponse{
		Source:          "admin-service mirror of identity permissions/catalogue.go",
		Roles:           AdminRoles(),
		Apps:            Apps(),
		Permissions:     map[string][]PermissionInfo{},
		RolePermissions: map[string]map[string][]string{},
	}
	for _, app := range apps {
		list := make([]PermissionInfo, 0, len(catalogue[app]))
		for _, e := range catalogue[app] {
			list = append(list, PermissionInfo{Permission: app + ":" + e.action, Roles: holders(e)})
		}
		out.Permissions[app] = list
	}
	for _, r := range adminRoles {
		byApp := map[string][]string{}
		for _, app := range apps {
			var perms []string
			for _, e := range catalogue[app] {
				if holds(r.Role, e) {
					perms = append(perms, app+":"+e.action)
				}
			}
			if len(perms) > 0 {
				sort.Strings(perms)
				byApp[app] = perms
			}
		}
		out.RolePermissions[r.Role] = byApp
	}
	return out
}
