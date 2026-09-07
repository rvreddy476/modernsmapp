package roles

import (
	"reflect"
	"testing"
)

// TestVocabulary pins the exact set of assignable roles.
//
// Seven, and only seven. If someone adds an eighth this fails, which is the
// point: the CHECK constraint in database/setup.sql is a copy of this list that
// Go cannot import, and the two must not drift.
func TestVocabulary(t *testing.T) {
	want := []string{
		"superadmin", "admin", "moderator",
		"seller", "restaurant_owner", "delivery_partner", "rider_partner",
	}
	if got := All(); !reflect.DeepEqual(got, want) {
		t.Fatalf("All() = %v, want %v", got, want)
	}
	for _, r := range want {
		if !Valid(r) {
			t.Fatalf("Valid(%q) = false, want true", r)
		}
	}
}

// TestUnknownRolesRejected covers the near-misses that a hand-typed grant
// produces, plus the one that must never become real.
func TestUnknownRolesRejected(t *testing.T) {
	for _, r := range []string{
		"", " ", "bogus", "SELLER", "Seller", "seller ", "super-admin",
		"restaurantowner", "restaurant-owner", "rider", "partner",
		// "customer" is NOT a role and must never become one. Every account is
		// a customer; the ABSENCE of a role is the customer state. Granting it
		// would say nothing, and failing to grant it would wrongly imply a
		// person cannot shop.
		"customer",
	} {
		if Valid(r) {
			t.Fatalf("Valid(%q) = true, want false", r)
		}
	}
}

// TestEcosystemSet is the security boundary of the internal grant endpoint:
// exactly these four are grantable by a service, and the privilege ladder is
// not.
func TestEcosystemSet(t *testing.T) {
	want := []string{"seller", "restaurant_owner", "delivery_partner", "rider_partner"}
	if got := Ecosystem(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Ecosystem() = %v, want %v", got, want)
	}
	for _, r := range want {
		if !IsEcosystem(r) {
			t.Fatalf("IsEcosystem(%q) = false, want true", r)
		}
		if IsPlatform(r) {
			t.Fatalf("IsPlatform(%q) = true — an ecosystem role must never be a platform role", r)
		}
	}
	for _, r := range []string{"superadmin", "admin", "moderator", "customer", "bogus", ""} {
		if IsEcosystem(r) {
			t.Fatalf("IsEcosystem(%q) = true — a service could then mint it", r)
		}
	}
}

// TestExpandPrivilegeLadder is the pre-existing behaviour, restated here now
// that the rules live in this package.
func TestExpandPrivilegeLadder(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{nil, []string{}},
		{[]string{"moderator"}, []string{"moderator"}},
		{[]string{"admin"}, []string{"admin", "moderator"}},
		{[]string{"superadmin"}, []string{"superadmin", "admin", "moderator"}},
		{[]string{"moderator", "admin"}, []string{"admin", "moderator"}},
		{[]string{"bogus"}, []string{}},
		{[]string{"moderator", "moderator"}, []string{"moderator"}},
	}
	for _, tc := range cases {
		if got := Expand(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("Expand(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestEcosystemRolesImplyNothing is the assertion a later edit gets wrong.
//
// The four ecosystem roles neither imply anything nor are implied by anything,
// in EITHER direction:
//
//   - a superadmin is not automatically a seller. Someone who can moderate the
//     platform has not been onboarded as a merchant, has no payout account, and
//     must not appear in a seller listing;
//   - a seller is not a moderator. The blast radius of a compromised
//     commerce-service is "makes someone a seller", and an implication edge
//     from seller to any platform role would quietly destroy that;
//   - the four do not imply each other. A restaurant owner is not a delivery
//     partner, however convenient that would be for the food app.
func TestEcosystemRolesImplyNothing(t *testing.T) {
	// Downward: no platform role produces any ecosystem role.
	for _, p := range Platform() {
		for _, e := range Ecosystem() {
			if containsRole(Expand([]string{p}), e) {
				t.Fatalf("Expand([%q]) contains %q — a platform role must not imply an ecosystem role", p, e)
			}
		}
	}
	// Upward: no ecosystem role produces any platform role, or any other
	// ecosystem role.
	for _, e := range Ecosystem() {
		got := Expand([]string{e})
		if !reflect.DeepEqual(got, []string{e}) {
			t.Fatalf("Expand([%q]) = %v, want exactly [%q] — ecosystem roles expand to themselves and nothing else", e, got, e)
		}
	}
	// A superadmin who was ALSO granted seller keeps both, and gains no other
	// ecosystem role from the superadmin half.
	got := Expand([]string{"superadmin", "seller"})
	want := []string{"superadmin", "admin", "moderator", "seller"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Expand([superadmin seller]) = %v, want %v", got, want)
	}
}

// TestExpandOrderIsCanonical — the output feeds the access token's `scopes`
// claim, so it has to be deterministic regardless of input order.
func TestExpandOrderIsCanonical(t *testing.T) {
	a := Expand([]string{"rider_partner", "seller", "admin", "restaurant_owner"})
	b := Expand([]string{"restaurant_owner", "admin", "rider_partner", "seller"})
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Expand is order-dependent: %v vs %v", a, b)
	}
	want := []string{"admin", "moderator", "seller", "restaurant_owner", "rider_partner"}
	if !reflect.DeepEqual(a, want) {
		t.Fatalf("Expand = %v, want %v", a, want)
	}
}

func TestLabelCoversEveryRole(t *testing.T) {
	for _, r := range All() {
		if Label(r) == "" || Label(r) == r {
			t.Fatalf("Label(%q) = %q — every role needs human wording for the switcher", r, Label(r))
		}
	}
}

func containsRole(list []string, want string) bool {
	for _, r := range list {
		if r == want {
			return true
		}
	}
	return false
}
