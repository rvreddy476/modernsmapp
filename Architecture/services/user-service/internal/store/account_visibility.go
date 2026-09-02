package store

// Module 3 — private accounts are now a REAL, enforced feature.
//
// This file used to clamp account_visibility to "public" (launch SR-5:
// nothing read the stored value, so storing "private" was a false promise).
// That era is over: the identity user-service validates the value against
// {public, private}, graph-service enforces it in the follow path and
// auto-accepts pending requests on private→public, and the settings-changed
// event carries the new value.
//
// This service's settings route is retired (410 — see
// internal/http/settings_moved.go); its store is no longer the authority.
// The clamp is therefore neutralised rather than left to silently rewrite
// "private" back to "public" on any residual internal write path.

// PublicAccountVisibility is the historical default.
const PublicAccountVisibility = "public"

// ClampAccountVisibility no longer clamps: the requested value is preserved
// so a stored "private" survives any write that still flows through this
// retired store. An empty value falls back to the historical default rather
// than persisting "".
func ClampAccountVisibility(requested string) string {
	if requested == "" {
		return PublicAccountVisibility
	}
	return requested
}
