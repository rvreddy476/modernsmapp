package http

import (
	"github.com/atpost/graph-service/pkg/writesource"
	"net/http"
	"strings"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// Module 3 M3-P0-1 / SR-3 — the source guard on canonical graph writes.
//
// Retiring profile-service's duplicate routes removes the known second writer.
// It does not stop the next one. The failure mode being closed is a service
// growing its own follow/block implementation again, six months from now,
// because that was easier than calling graph-service — and nobody noticing
// until a user reports that blocking did nothing.
//
// So every mutating route records WHICH caller wrote it. Two effects:
//
//   - An unattributed write is refused. A caller that cannot say who it is
//     has not been reviewed as a graph writer.
//   - The attribution is stored with the outbox event, so "who created this
//     edge" is answerable from data rather than from log archaeology.
//
// This is not authentication — RequireInternalKey already does that, and the
// key is shared, so it cannot distinguish callers. This is attribution, and it
// is the part that makes an unauthorised writer visible.

// GraphWriteSourceHeader identifies the calling service on a mutating route.
const GraphWriteSourceHeader = writesource.Header

// allowedGraphWriteSources is the closed list of services permitted to mutate
// the social graph. Adding an entry is a deliberate act: it means that service
// has been reviewed for block safety, because every writer here can create an
// edge that feed, search and chat will honour.
//
// `identity-profile-service` is deliberately ABSENT. Its graph routes were
// retired in SR-3; if it appears here again, the duplicate graph is back.
// allowedGraphWriteSources is the tracked, importable allowlist. It lives in
// pkg/writesource so the CI-only contract module can drive it against the
// gateway.s real stamping function — the two must agree, and did not.
var allowedGraphWriteSources = writesource.Allowed

// RequireCanonicalWriteSource rejects a mutating graph request that does not
// name an allowed caller.
//
// strict=false logs and permits an unknown source instead of refusing. It
// exists for one purpose: rolling this out without an instant outage if a
// caller was missed. It is NOT the production setting, and the startup path
// warns loudly when it is on.
func RequireCanonicalWriteSource(strict bool, warn func(string, ...any)) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isGraphMutation(c.Request.Method) || isReadOnlyPost(c.FullPath()) {
			c.Next()
			return
		}

		source := strings.TrimSpace(c.GetHeader(GraphWriteSourceHeader))
		switch {
		case allowedGraphWriteSources[source]:
			c.Set("graph_write_source", source)
			c.Next()
		case strict:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden,
				"UNRECOGNISED_GRAPH_WRITER",
				"This service is not a recognised writer of the social graph. The graph "+
					"is canonically owned by graph-service; a second implementation "+
					"produces blocks that no other service enforces. Set "+
					GraphWriteSourceHeader+" to an approved caller.",
				map[string]any{"received_source": source})
			c.Abort()
		default:
			if warn != nil {
				warn("graph write from unrecognised source %q on %s %s — this caller "+
					"would be REFUSED once GRAPH_WRITE_SOURCE_STRICT=true",
					source, c.Request.Method, c.Request.URL.Path)
			}
			c.Set("graph_write_source", "unattributed")
			c.Next()
		}
	}
}

func isGraphMutation(method string) bool { return writesource.IsMutation(method) }

// readOnlyPostRoutes are POST routes on /v1/graph that READ rather than mutate.
//
// The guard's premise is "a mutating method names an approved writer", and it
// derives "mutating" from the HTTP method alone. That is right for follow,
// block and mute, and wrong for a batch lookup that happens to use POST
// because its input is a list of ids too long for a query string.
//
// Found on 2026-08-17 by the feed-hydration evidence pass. post-service asks
// this route to resolve relationships while authorizing a page of media; the
// guard refused it with UNRECOGNISED_GRAPH_WRITER, post-service returned
// ErrStoryPolicyUnresolved, media-service returned 503, and every ranked feed
// surface answered 503 FEED_UNAVAILABLE.
//
// The fix is deliberately NOT to add post-service to allowedGraphWriteSources.
// That list is the closed set of services permitted to MUTATE the graph, and
// every entry in it is a reviewed block-safety risk. post-service does not
// write edges; granting it write attribution to fix a read would weaken the
// boundary SR-3 exists to hold.
var readOnlyPostRoutes = map[string]bool{
	"/v1/graph/relationships/batch": true,
}

// isReadOnlyPost matches on the registered route template, not the raw path,
// so a caller cannot slip past the guard with a crafted URL that merely looks
// like one of these.
func isReadOnlyPost(fullPath string) bool { return readOnlyPostRoutes[fullPath] }
