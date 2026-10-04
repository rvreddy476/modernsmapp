package adminauth

import (
	"sort"
	"strings"
	"testing"
)

// doorstepHolders is identity's doorstep table (auth-service
// internal/permissions catalogue_test.go consolePermissionHolders, the
// doorstep:* rows): every permission the Doorstep contract pins, with EXACTLY
// the roles besides the implicit admin and superadmin that hold it. The mirror
// must match it row for row.
var doorstepHolders = map[string][]string{
	"doorstep:catalogue.read":      {RoleModerator, RoleFinance, RoleSupport},
	"doorstep:catalogue.write":     nil,
	"doorstep:config.write":        nil,
	"doorstep:pros.read":           {RoleModerator, RoleSupport, RoleKYCReviewer},
	"doorstep:pros.approve":        nil,
	"doorstep:pros.suspend":        nil,
	"doorstep:documents.review":    {RoleKYCReviewer},
	"doorstep:prices.review":       {RoleKYCReviewer},
	"doorstep:bookings.read":       {RoleModerator, RoleFinance, RoleSupport},
	"doorstep:bookings.cancel":     nil,
	"doorstep:bookings.redispatch": {RoleSupport},
	"doorstep:refunds.issue":       {RoleFinance},
	"doorstep:incidents.read":      {RoleModerator, RoleSupport},
	"doorstep:incidents.act":       {RoleModerator, RoleSupport},
	"doorstep:tickets.act":         {RoleModerator, RoleSupport},
	"doorstep:ratings.moderate":    {RoleModerator},
	"doorstep:settlements.read":    {RoleFinance},
	"doorstep:stats.read":          {RoleFinance, RoleSupport},
	"doorstep:audit.read":          {RoleAuditor},
}

// TestCatalogue_DoorstepMirrorsIdentity: the doorstep app is in the
// vocabulary between rider and trust_safety, holds exactly the contract's
// nineteen permissions, and each one names exactly identity's holders.
func TestCatalogue_DoorstepMirrorsIdentity(t *testing.T) {
	cat := Catalogue()
	idx := map[string]int{}
	for i, a := range cat.Apps {
		idx[a] = i
	}
	if _, ok := idx[AppDoorstep]; !ok || idx[AppDoorstep] != idx[AppRider]+1 || idx[AppTrustSafety] != idx[AppDoorstep]+1 {
		t.Fatalf("doorstep must sit between rider and trust_safety in identity's order: %v", cat.Apps)
	}
	perms := cat.Permissions[AppDoorstep]
	if len(perms) != len(doorstepHolders) {
		t.Fatalf("doorstep has %d permissions, identity %d", len(perms), len(doorstepHolders))
	}
	for _, pi := range perms {
		extra, ok := doorstepHolders[pi.Permission]
		if !ok {
			t.Errorf("mirror has %s, identity does not", pi.Permission)
			continue
		}
		want := append([]string{RoleSuperadmin, RoleAdmin}, extra...)
		if strings.Join(pi.Roles, ",") != strings.Join(want, ",") {
			t.Errorf("%s holders %v, want %v", pi.Permission, pi.Roles, want)
		}
		if !KnownPermission(pi.Permission) {
			t.Errorf("KnownPermission(%s) = false", pi.Permission)
		}
	}
}

// TestCatalogue_DoorstepRoleViews: what each role's doorstep-scoped grant
// resolves to on the Access page, stated whole.
func TestCatalogue_DoorstepRoleViews(t *testing.T) {
	cat := Catalogue()
	view := func(role string) string {
		got := append([]string(nil), cat.RolePermissions[role][AppDoorstep]...)
		sort.Strings(got)
		return strings.Join(got, ",")
	}
	for role, want := range map[string]string{
		RoleModerator: "doorstep:bookings.read,doorstep:catalogue.read,doorstep:incidents.act,doorstep:incidents.read," +
			"doorstep:pros.read,doorstep:ratings.moderate,doorstep:tickets.act",
		RoleSupport: "doorstep:bookings.read,doorstep:bookings.redispatch,doorstep:catalogue.read,doorstep:incidents.act," +
			"doorstep:incidents.read,doorstep:pros.read,doorstep:stats.read,doorstep:tickets.act",
		RoleFinance: "doorstep:bookings.read,doorstep:catalogue.read,doorstep:refunds.issue,doorstep:settlements.read," +
			"doorstep:stats.read",
		RoleKYCReviewer: "doorstep:documents.review,doorstep:prices.review,doorstep:pros.read",
		RoleAuditor:     "doorstep:audit.read",
	} {
		if got := view(role); got != want {
			t.Errorf("%s doorstep view\n got %s\nwant %s", role, got, want)
		}
	}
	if n := len(cat.RolePermissions[RoleAdmin][AppDoorstep]); n != len(doorstepHolders) {
		t.Errorf("admin holds %d doorstep permissions, want all %d", n, len(doorstepHolders))
	}
}
