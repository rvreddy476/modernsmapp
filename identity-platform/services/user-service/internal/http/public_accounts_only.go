package http

import (
	"strings"
)

// Module 3 M3-P0-5 / SR-5 — launch is PUBLIC ACCOUNTS ONLY.
//
// THE SITUATION THIS RESOLVES
//
// `account_visibility` was settable to "private" or "followers", and the
// setting was stored faithfully. Nothing read it. graph-service's Follow path
// never consulted it — `grep account_visibility` in the graph permission
// resolver returns nothing — so a user who set their account to private was
// still followable by anyone, and their posts still reached the public feed
// and search exactly as before.
//
// That is worse than not having the feature. A privacy control that stores the
// user's choice and changes no behaviour is a false promise, and users make
// real decisions about what to post based on it.
//
// A working private account needs a pending follow-request lifecycle
// (request → accept/decline → edge), enforcement in the follow path, audience
// resolution in feed and search, and client surfaces for the request queue.
// That is not a switch; it is a feature. Rather than ship the switch without
// the feature, launch is public-accounts-only and the control is REMOVED.
//
// WHY REJECT RATHER THAN SILENTLY COERCE
//
// Accepting "private" and storing "public" would leave a client showing a
// toggle that flips back, and a user believing the platform lost their
// setting. A 400 that says the feature does not exist is honest, visible, and
// tells the client author exactly what to remove.

// SupportedAccountVisibility is the only accepted value at launch.
const SupportedAccountVisibility = "public"

// PublicAccountsOnlyMessage is returned when a client asks for a visibility
// the platform cannot actually enforce.
const PublicAccountsOnlyMessage = "Private and followers-only accounts are not available. " +
	"This setting previously stored a value that nothing enforced: the account " +
	"remained followable and its posts still reached feed and search. Only " +
	`"public" is accepted.`

// AccountVisibilityRejected reports whether the requested visibility must be
// refused. An empty or absent value is fine — it means "do not change it".
func AccountVisibilityRejected(requested string) bool {
	v := strings.ToLower(strings.TrimSpace(requested))
	return v != "" && v != SupportedAccountVisibility
}
