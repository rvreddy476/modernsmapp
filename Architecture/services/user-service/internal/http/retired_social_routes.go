package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// Routes retired by the 2026-11-07 platform audit (US-L1, US-L2, US-L4).
//
// No client calls any of them: Android reads and writes About through
// profile-service, and the web's endorsement and compatibility components are
// not mounted on any page. Each was open in a way that mattered:
//
//   - Endorsements (US-L1): anyone, a blocked user included, could put
//     uncapped free text on any profile, the recipient could not remove it,
//     and the list was unbounded and anonymous. Reputation only existed to
//     count them.
//   - Compatibility (US-L2) compared the caller's About sections with the
//     target's PRIVATE ones, so its score was an oracle for what the target
//     had hidden.
//   - About writes (US-L2) stored any visibility string; profile-service is
//     the About writer clients use. The About READS stay, filtered by
//     ViewerAccess, and an unknown visibility now reads as private (canView).
//   - The link-click and QR-scan counters (US-L4) took no identity, so any
//     script could inflate anyone's numbers.
//
// The :userId routes keep requireServableUser in front, so a deactivated
// account still answers the same 404 as a missing one there.

const (
	canonicalAboutUpsertRoute = "PUT /v1/profiles/me/about/{section}"
	canonicalAboutDeleteRoute = "DELETE /v1/profiles/me/about/{section}/{itemId}"
)

const (
	whyEndorsementsRetired  = "Endorsements and reputation are retired: anyone could write on any profile and the recipient could not remove it."
	whyCompatibilityRetired = "Compatibility is retired: it compared private About sections."
	whyAboutWriteMoved      = "About is written through profile-service."
	whyCounterRetired       = "This counter is retired: it accepted anonymous, unbounded increments."
)

// retiredUserRoute answers 410 ROUTE_RETIRED with the reason and, when the
// feature lives on elsewhere, the route that replaces it.
func retiredUserRoute(why, canonical string) gin.HandlerFunc {
	return func(c *gin.Context) {
		msg := "This endpoint has been retired. " + why
		var details map[string]any
		if canonical != "" {
			msg += " Use " + canonical + "."
			details = map[string]any{"canonical_route": canonical}
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusGone, "ROUTE_RETIRED", msg, details)
	}
}
