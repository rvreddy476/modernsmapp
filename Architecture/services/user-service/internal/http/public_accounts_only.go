package http

import "strings"

// Module 3 M3-P0-5 / SR-5 — launch is PUBLIC ACCOUNTS ONLY.
//
// `account_visibility` accepted "private" and "followers", stored them
// faithfully, and nothing read them. graph-service's follow path never
// consulted the setting, so a "private" account was still followable by
// anyone and its posts still reached feed and search unchanged.
//
// A privacy control that records the user's choice and changes no behaviour is
// a false promise, and people decide what to post based on it. A working
// private account needs a pending follow-request lifecycle, enforcement in the
// follow path, audience resolution in feed and search, and client surfaces for
// the request queue — a feature, not a switch. Until that exists the control
// is refused, loudly, rather than silently coerced: a client that shows a
// toggle which flips back looks like a platform that lost the setting.
//
// This constant and check are duplicated in
// identity-platform/services/user-service because the two services are
// separate modules with separate settings stores. Both accept the field, so
// both must refuse it — enforcing in one would leave the other open.

// SupportedAccountVisibility is the only accepted value at launch.
const SupportedAccountVisibility = "public"

// PublicAccountsOnlyMessage explains the refusal to the client author.
const PublicAccountsOnlyMessage = "Private and followers-only accounts are not available. " +
	"This setting previously stored a value that nothing enforced: the account " +
	"remained followable and its posts still reached feed and search. Only " +
	`"public" is accepted.`

// AccountVisibilityRejected reports whether the requested visibility must be
// refused. Empty means "unchanged" and is always fine.
func AccountVisibilityRejected(requested string) bool {
	v := strings.ToLower(strings.TrimSpace(requested))
	return v != "" && v != SupportedAccountVisibility
}
