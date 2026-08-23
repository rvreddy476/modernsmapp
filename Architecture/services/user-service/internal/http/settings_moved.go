package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// Slice B-Closure B-LB-2 — ONE AUTHORITY FOR PRIVACY SETTINGS.
//
// Two services answered `/v1/users/me/settings`, and they did not store the
// same thing:
//
//   - this service's UserSettings has account_visibility, allow_messages_from
//     and allow_comments_from, and nothing else;
//   - identity user-service owns the full privacy matrix, including
//     who_can_message;
//   - graph-service reads messaging privacy from the identity service when it
//     decides whether one user may open a direct conversation with another.
//
// api-gateway routed `/v1/users` here, so the only settings endpoint a client
// could reach was the one WITHOUT who_can_message. Gin bound the request body
// into a struct that has no such field, dropped it, saved the rest, and
// returned 200. The user changed who could message them, saw it succeed, and
// graph-service's decision never moved — the field was never stored anywhere
// that anything read.
//
// A privacy control that reports success and enforces nothing is worse than
// one that fails: people decide what to expose based on it.
//
// The fix is the routing change in api-gateway (`/v1/users/me/settings` now
// resolves to identity-user:8110) plus this refusal. The route is retired here
// rather than deleted so that a deployment which loses the gateway rule fails
// visibly with 410 and this explanation, instead of quietly reverting to
// accept-and-drop. That failure mode is invisible by construction, and it is
// the reason this file exists rather than a commit that just removes two
// handlers.
//
// Retire this file only when this service no longer serves any `/v1/users`
// route that a client could mistake for the settings endpoint.
const settingsAuthorityMessage = "Privacy settings are served by the identity " +
	"user-service. This endpoint stored a partial copy that nothing enforced: " +
	"who_can_message was silently discarded while the response reported success, " +
	"so the permission check that gates direct messages never saw the change. " +
	"Route /v1/users/me/settings to the identity user-service."

// settingsMoved refuses the request loudly.
//
// 410 Gone, not 404: the path is real and used to work here, and a client
// author debugging a 404 looks for a typo. 410 with a reason names the actual
// problem, which is a misrouted deployment.
func settingsMoved(c *gin.Context) {
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusGone,
		"SETTINGS_AUTHORITY_MOVED", settingsAuthorityMessage,
		map[string]any{"authority": "identity-user-service"})
}
