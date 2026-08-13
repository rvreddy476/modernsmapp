package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// Module 3 M3-P0-1 / SR-3 — one writer for profile fields.
//
// THE DEFECT
//
// `PUT /v1/users/me` and `PUT /v1/profiles/me` both wrote display name, bio,
// avatar, cover, first/last name, gender, username, category, profession,
// website, location and date of birth — into two different stores, with
// nothing reconciling them.
//
// Two writers with no merge rule means last-write-wins per store, not per
// field. A user who edits their bio in one surface and their avatar in another
// ends up with two profiles that disagree, and which one a reader sees depends
// on which service that reader happens to query. Worse for the fields that
// carry privacy weight: date of birth drives the 18+ gate, and username drives
// impersonation checks. A value that can be set through an unaudited second
// path is not a value any policy can rely on.
//
// THE FIX
//
// profile-service is the canonical writer. This path answers 410 and names the
// replacement, so a deployed client fails visibly instead of writing to a
// store that readers may never consult.
//
// The GET stays: reads are a projection and retiring them would break callers
// for no safety gain. Only the WRITE is retired, because the write is what
// creates the divergence.

const canonicalProfileWriteRoute = "PUT /v1/profiles/me"

func retiredProfileWrite(c *gin.Context) {
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusGone, "ROUTE_RETIRED",
		"This endpoint has been retired. Profile fields are owned by "+
			"profile-service; writing them here produced a second, unreconciled "+
			"copy — including date of birth, which the 18+ gate depends on. "+
			"Use "+canonicalProfileWriteRoute+".",
		map[string]any{"canonical_route": canonicalProfileWriteRoute})
}
