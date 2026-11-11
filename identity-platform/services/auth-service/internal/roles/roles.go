// Package roles is the ONE vocabulary of assignable identity roles.
//
// # WHY THIS PACKAGE EXISTS
//
// Identity is the platform's single sign-on. A person's roles belong here and
// nowhere else. Before this package, identity knew about three roles —
// superadmin, admin, moderator — and every other "role" in the ecosystem was
// really a row in some other service's database, invisible to the access
// token:
//
//	commerce-service  sellers                    (user_id UNIQUE)  -> a seller
//	food-service      food.restaurant_partners   (owner_user_id)   -> a restaurant owner
//	food-service      food.delivery_partners     (user_id UNIQUE)  -> a delivery partner
//	rider-service     rider_partners             (user_id)         -> a rider/fleet partner
//
// Answering "what is this person allowed to be" therefore meant fanning out to
// four services, and no token carried the answer. Those four are now roles in
// auth.user_roles, resolved into the token's `scopes` claim like any other.
// doorstep-service's professionals (service_professional, October 2026) were
// born into this model: identity is that role's only home from day one.
//
// The vocabulary used to be spelled out as string literals in three places
// that had to be kept in agreement by hand — the auth.user_roles CHECK
// constraint, store.ValidRole, and config.ExpandRoles. They now all derive
// from the lists below. TestSetupSQLCheckMatchesVocabulary asserts the SQL
// still agrees, because a CHECK constraint is the one copy Go cannot import.
//
// THERE IS NO "CUSTOMER" ROLE, AND ONE MUST NEVER BE ADDED.
//
// Every account is a customer. That is what an account IS. The absence of a
// role is the customer state, so "customer" is not information — granting it
// would say nothing, and failing to grant it would wrongly imply a person
// cannot shop. Clients that want the flag get `is_customer: true`,
// unconditionally, from /v1/auth/me/capabilities.
package roles

// The privilege ladder. These three imply one another (see Expand) and are
// grantable only by a superadmin, through POST /v1/auth/admin/roles.
const (
	Superadmin = "superadmin"
	Admin      = "admin"
	Moderator  = "moderator"
)

// The ecosystem roles. A service grants these when it approves someone —
// commerce when a seller application passes, food when a restaurant or
// delivery partner is onboarded, rider when a fleet partner is approved,
// doorstep when a home-services professional is created (revoked when the
// professional is rejected or blocked, kept while suspended).
//
// They imply NOTHING and are implied by NOTHING. A superadmin is not
// automatically a seller; a restaurant owner is not a delivery partner. See
// Expand and TestEcosystemRolesImplyNothing.
//
// Each string must stay identical to its shared/identityroles constant, which
// is what the granting services send.
const (
	Seller              = "seller"
	RestaurantOwner     = "restaurant_owner"
	DeliveryPartner     = "delivery_partner"
	RiderPartner        = "rider_partner"
	ServiceProfessional = "service_professional"
)

// The timed roles. A timed role is BOUGHT for a period and must lapse by
// itself even if nobody ever revokes it, so it is held only with an expiry:
// auth.user_roles.expires_at is required for it (service layer and a CHECK
// constraint), and a row past its expiry grants nothing (every read filters
// on expires_at).
//
// Premium is Momentum Premium (short-posts design 3.1, decided 11 Nov 2026):
// monetization-service grants it when a pass is paid for, until the paid
// period ends plus grace, through POST /v1/auth/internal/roles with
// expires_at. It reaches the token's `scopes` (and the gateway's X-Scopes) so
// clients can SHOW premium features; services still DECIDE with a live read.
//
// It is deliberately NOT an ecosystem role: Ecosystem() is the set a service
// may grant WITHOUT an expiry, and adding premium there would let any service
// grant it permanently. See TimedRoleGranter for who may grant it.
//
// The string must stay identical to shared/identityroles.RolePremium.
const (
	Premium = "premium"
)

// The staff roles (admin console). Granted by a superadmin through the same
// route as the ladder, platform-wide or scoped to one application. Their
// meaning is entirely the permission catalogue in internal/permissions.
//
// They are deliberately NOT part of the token's `scopes` claim (Expand drops
// them): every service today reads `admin`/`moderator` from that claim as a
// blanket grant, and a finance or support hat must never be mistaken for one.
// Services resolve staff permissions live through identity instead.
const (
	Finance     = "finance"
	Support     = "support"
	KYCReviewer = "kyc_reviewer"
	Auditor     = "auditor"
)

// platform is the privilege ladder in descending order. The order is also the
// canonical output order of Expand, so a token's `scopes` claim is stable.
var platform = []string{Superadmin, Admin, Moderator}

// staff is the admin-console roles that sit outside the ladder.
var staff = []string{Finance, Support, KYCReviewer, Auditor}

// ecosystem is the set a SERVICE may grant. Deliberately separate from
// platform: the internal grant endpoint is allowed to write only this list, so
// a compromised commerce-service cannot mint an admin. See
// service.GrantEcosystemRole.
var ecosystem = []string{Seller, RestaurantOwner, DeliveryPartner, RiderPartner, ServiceProfessional}

// timed is the set a service may grant only together with a future expiry.
var timed = []string{Premium}

// timedGranters names the ONE calling service allowed to grant or revoke each
// timed role. The internal role API's caller name is self-declared (the
// shared internal key proves only that the caller is inside the cluster), so
// this guards against a wiring bug in another service; it is not
// authentication. No other service has any business handing out a paid role.
var timedGranters = map[string]string{
	Premium: "monetization-service",
}

// Platform returns the privilege-ladder roles in canonical order.
func Platform() []string { return append([]string(nil), platform...) }

// Ecosystem returns the service-grantable roles in canonical order.
func Ecosystem() []string { return append([]string(nil), ecosystem...) }

// Timed returns the roles a service may grant only with an expiry.
func Timed() []string { return append([]string(nil), timed...) }

// IsTimed reports whether r may be held only with an expiry.
func IsTimed(r string) bool {
	for _, known := range timed {
		if r == known {
			return true
		}
	}
	return false
}

// TimedRoleGranter returns the calling-service name allowed to grant and
// revoke the timed role r, or "" when r is not a timed role.
func TimedRoleGranter(r string) string { return timedGranters[r] }

// Staff returns the admin-console staff roles in canonical order.
func Staff() []string { return append([]string(nil), staff...) }

// AdminRoles returns every role an admin grant may name: the ladder, then staff.
func AdminRoles() []string {
	out := make([]string, 0, len(platform)+len(staff))
	out = append(out, platform...)
	out = append(out, staff...)
	return out
}

// IsAdminRole reports whether r is a ladder or staff role.
func IsAdminRole(r string) bool {
	for _, known := range AdminRoles() {
		if r == known {
			return true
		}
	}
	return false
}

// TokenRoles returns the roles that can appear in the `scopes` claim and in
// the capabilities switcher: the ladder, the ecosystem roles and the timed
// roles. Staff roles stay out (see the staff block above).
func TokenRoles() []string {
	out := make([]string, 0, len(platform)+len(ecosystem)+len(timed))
	out = append(out, platform...)
	out = append(out, ecosystem...)
	out = append(out, timed...)
	return out
}

// All returns every assignable role in canonical order. The auth.user_roles
// CHECK constraint must list exactly these values.
func All() []string {
	out := make([]string, 0, len(platform)+len(staff)+len(ecosystem)+len(timed))
	out = append(out, platform...)
	out = append(out, staff...)
	out = append(out, ecosystem...)
	out = append(out, timed...)
	return out
}

// Valid reports whether r is an assignable role.
func Valid(r string) bool {
	for _, known := range All() {
		if r == known {
			return true
		}
	}
	return false
}

// IsEcosystem reports whether r is one of the five roles a service may grant
// over the internal API. This is the security boundary of that endpoint.
func IsEcosystem(r string) bool {
	for _, known := range ecosystem {
		if r == known {
			return true
		}
	}
	return false
}

// IsPlatform reports whether r is one of the three privilege-ladder roles.
func IsPlatform(r string) bool {
	for _, known := range platform {
		if r == known {
			return true
		}
	}
	return false
}

// Expand turns a set of raw roles into the effective role set, applying the
// implication rules, deduped and in canonical order. Unknown roles are dropped.
//
// The ONLY implications are on the privilege ladder:
//
//	superadmin ⊇ admin ⊇ moderator
//
// The five ecosystem roles and the timed role (premium) pass through untouched. They neither imply nor are
// implied by anything, in either direction — being a superadmin does not make
// you a seller, and being a seller does not make you a moderator. Adding an
// implication here would silently hand one product's authority to another
// product's partners.
func Expand(in []string) []string {
	set := make(map[string]struct{}, len(in)+2)
	for _, r := range in {
		switch r {
		case Superadmin:
			set[Superadmin], set[Admin], set[Moderator] = struct{}{}, struct{}{}, struct{}{}
		case Admin:
			set[Admin], set[Moderator] = struct{}{}, struct{}{}
		case Moderator:
			set[Moderator] = struct{}{}
		default:
			// Ecosystem and timed roles (and only those) survive as
			// themselves. A timed role arrives here only from an unexpired
			// row: every store read filters on expires_at.
			if IsEcosystem(r) || IsTimed(r) {
				set[r] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(set))
	for _, r := range All() {
		if _, ok := set[r]; ok {
			out = append(out, r)
		}
	}
	return out
}

// Label is the human wording a role switcher renders. Kept next to the
// vocabulary so a new role cannot be added without someone deciding what it is
// called. Unknown roles return the raw value rather than an empty string — a
// blank row in a switcher is worse than an ugly one.
func Label(r string) string {
	switch r {
	case Superadmin:
		return "Super admin"
	case Admin:
		return "Admin"
	case Moderator:
		return "Moderator"
	case Finance:
		return "Finance"
	case Support:
		return "Support"
	case KYCReviewer:
		return "KYC reviewer"
	case Auditor:
		return "Auditor"
	case Seller:
		return "Seller"
	case RestaurantOwner:
		return "Restaurant"
	case DeliveryPartner:
		return "Delivery partner"
	case RiderPartner:
		return "Rider partner"
	case ServiceProfessional:
		return "Service professional"
	case Premium:
		return "Premium"
	}
	return r
}
