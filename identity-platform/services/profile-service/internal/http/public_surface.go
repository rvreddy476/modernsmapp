package http

import (
	"net/url"
	"strings"

	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Module 3 M3-P0-4 / LB-4 — one gate for every public profile surface.
//
// WHAT SR-4 MISSED
//
// SR-4 added block denial to two routes — get-by-id and get-by-username — and
// named the test "on every profile surface". It was not. These are also public
// and were serving unconditionally:
//
//	GET /v1/profiles/:userId/links
//	GET /v1/profiles/:userId/about
//	GET /v1/profiles/:userId/about/:section
//	GET /v1/profiles/:userId/stats
//	GET /v1/profiles/:userId/modules/:module
//	GET /v1/profiles/resolve-handle/:username
//
// So a blocked account could not open the profile page, and could read the
// same person's links, about data, stats and module profiles directly. A block
// that covers the front door and none of the windows is not a block.
//
// Worse, `GetAllAbout` returned EVERY row regardless of `visibility`. About
// items carry a visibility column precisely because some of them are not
// public — employer, education, relationship status, birthday — and all of
// them were being published to anonymous callers.
//
// THE GATE
//
// publicTargetOrDeny is the single place a public per-user surface resolves
// its target and applies block denial. Adding a route without calling it is
// visible in review: the handler has no target id to work with until it does.

// publicTargetOrDeny resolves the :userId path parameter and applies block
// denial, writing the response itself when the request must not proceed.
//
// Returns ok=false when the caller should return immediately.
func (h *Handler) publicTargetOrDeny(c *gin.Context) (uuid.UUID, bool) {
	targetID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		// Not-found rather than bad-request: a malformed id and a non-existent
		// user should be indistinguishable, so probing learns nothing.
		writeProfileNotFound(c)
		return uuid.Nil, false
	}
	if h.deniedByBlock(c, targetID) {
		return uuid.Nil, false
	}
	return targetID, true
}

// ── Visibility filtering ────────────────────────────────────────────────────

// PublicVisibility is the only value publishable to an arbitrary viewer.
//
// Everything else — "connections", "followers", "private", or any value added
// later — is withheld. This is an ALLOWLIST for the same reason PublicProfile
// is: a new visibility level introduced next month must be private until
// someone decides otherwise, not public because nobody remembered to add it to
// a denylist.
const PublicVisibility = "public"

// isPublicRow reports whether a row's visibility may be served to any viewer.
// An EMPTY visibility is treated as public: existing rows predate the column
// and the product has always shown them, so withholding them would be a
// silent data loss rather than a privacy fix.
func isPublicRow(visibility string) bool {
	v := strings.ToLower(strings.TrimSpace(visibility))
	return v == "" || v == PublicVisibility
}

// PublicAboutItem is the publishable shape of an about row.
//
// Written as its own type rather than reusing store.AboutItem so a column
// added to the store type does not become public by default — the same
// allowlist discipline as PublicProfile.
type PublicAboutItem struct {
	Section   string         `json:"section"`
	ItemID    uuid.UUID      `json:"item_id"`
	Data      map[string]any `json:"data"`
	SortOrder int            `json:"sort_order"`
}

// PublicAboutItems keeps only rows a stranger may see.
func PublicAboutItems(items []store.AboutItem) []PublicAboutItem {
	out := make([]PublicAboutItem, 0, len(items))
	for _, item := range items {
		if !isPublicRow(item.Visibility) {
			continue
		}
		out = append(out, PublicAboutItem{
			Section:   item.Section,
			ItemID:    item.ItemID,
			Data:      item.Data,
			SortOrder: item.SortOrder,
		})
	}
	return out
}

// PublicProfileLink is the publishable shape of a profile link.
type PublicProfileLink struct {
	ID        uuid.UUID `json:"id"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	Icon      *string   `json:"icon,omitempty"`
	Category  *string   `json:"category,omitempty"`
	SortOrder int       `json:"sort_order"`
	IsPinned  bool      `json:"is_pinned"`
}

// PublicProfileLinks keeps only public rows, and drops any whose URL is not a
// safe scheme — see SafePublicURL.
func PublicProfileLinks(links []store.ProfileLink) []PublicProfileLink {
	out := make([]PublicProfileLink, 0, len(links))
	for _, l := range links {
		if !isPublicRow(l.Visibility) {
			continue
		}
		if !SafePublicURL(l.URL) {
			continue
		}
		out = append(out, PublicProfileLink{
			ID: l.ID, Title: l.Title, URL: l.URL, Icon: l.Icon,
			Category: l.Category, SortOrder: l.SortOrder, IsPinned: l.IsPinned,
		})
	}
	return out
}

// PublicUserLink is the publishable shape of a legacy user link.
type PublicUserLink struct {
	Platform     string `json:"platform"`
	URL          string `json:"url"`
	DisplayLabel string `json:"display_label"`
	SortOrder    int    `json:"sort_order"`
}

// PublicUserLinks drops links whose URL is not a safe scheme.
func PublicUserLinks(links []store.UserLink) []PublicUserLink {
	out := make([]PublicUserLink, 0, len(links))
	for _, l := range links {
		if !SafePublicURL(l.URL) {
			continue
		}
		out = append(out, PublicUserLink{
			Platform: l.Platform, URL: l.URL,
			DisplayLabel: l.DisplayLabel, SortOrder: l.SortOrder,
		})
	}
	return out
}

// ── URL safety ──────────────────────────────────────────────────────────────

// SafePublicURL reports whether a user-supplied URL may be published.
//
// LB-4: website, intro-media and CTA URLs were copied into the public response
// with no scheme check at all. A profile is a place a user types a URL and
// other people click it, so an unvalidated value is a stored injection vector:
//
//	javascript:...  executes in the viewer's session on any client that
//	                renders the link with an href
//	data:text/html  renders attacker-controlled markup from the profile's
//	                own origin in some webviews
//	file: / intent: / market:  reach outside the browser on mobile
//
// The check is an ALLOWLIST of http and https. A denylist of known-bad schemes
// fails on the next one someone invents, and on case and whitespace tricks
// like "JaVaScRiPt:" or " javascript:".
func SafePublicURL(raw string) bool {
	s := strings.TrimSpace(raw)
	if s == "" {
		return false
	}
	// Reject control characters outright: a newline or NUL inside a URL is
	// never legitimate and defeats naive downstream parsing.
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		// A scheme alone is not enough: "http:" with no host is not a link.
		return u.Host != ""
	default:
		return false
	}
}

// SanitizePublicURLPointer blanks a pointer URL that is not safe to publish.
func SanitizePublicURLPointer(p *string) *string {
	if p == nil {
		return nil
	}
	if !SafePublicURL(*p) {
		return nil
	}
	return p
}

// safePublicURLString blanks a non-pointer URL that is not safe to publish.
// An empty string renders as "no website", which is the correct outcome for a
// value the platform will not stand behind.
func safePublicURLString(raw string) string {
	if raw == "" || !SafePublicURL(raw) {
		return ""
	}
	return raw
}
