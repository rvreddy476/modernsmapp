package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Module 3 M3-P0-4 / LB-4 — EVERY public profile surface, enumerated.
//
// SR-4 named a test "BlockedViewerGetsNotFoundOnEveryProfileSurface" and
// covered two routes. Six others were public and served unconditionally, so a
// blocked account could not open the profile page and could read the same
// person's links, about data, stats and module profiles directly.
//
// This table is the fix for the test as well as the code: it lists every
// public per-user route, and TestPublicRouteTableIsComplete below fails if a
// route is registered that this table does not mention.

// Module 3 CLB-2 — the inventory classifies routes by WHAT THEY RESOLVE, not
// by how their path is spelled.
//
// The previous version of this table listed only patterns containing :userId,
// and TestPublicRouteTableIsComplete filtered on that same substring. A route
// that resolves a target by USERNAME was therefore invisible to it — which is
// exactly how GET /v1/profiles/resolve-handle/:username shipped ungated,
// handing a blocked viewer the current username and user id of the account
// that blocked them. A completeness check that cannot see a whole class of
// resolvers is a false-negative inventory, not an inventory.
//
// So every public route is now classified, and an unclassified one fails the
// suite. The classification says what the route resolves; the behavioural
// tests below then hold each single-target route to the same denial contract.

type routePolicy string

const (
	// policySingleTarget: resolves exactly one account, by ANY identifier —
	// uuid, current username, or historical handle. Must apply the symmetric
	// block gate, return a non-enumerating 404 in both directions, and fail
	// closed when the graph is unreachable.
	policySingleTarget routePolicy = "single-target"
	// policyListSurface: returns many accounts. Blocked entries are omitted
	// rather than denied, and an error empties the list.
	policyListSurface routePolicy = "list-surface"
	// policyNoTarget: resolves no account at all.
	policyNoTarget routePolicy = "no-target"
	// policySelfOnly: the authenticated caller IS the target.
	policySelfOnly routePolicy = "self-only"
)

// targetToken carries every identifier form a single-target route might take,
// so one table entry can build a path regardless of which one it uses.
type targetToken struct {
	id          string
	username    string
	oldUsername string
}

// publicTargetResolvers is every public route that resolves ONE account.
var publicTargetResolvers = []struct {
	name string
	// registered is gin's route pattern, used by the completeness check.
	registered string
	path       func(targetToken) string
}{
	{"profile by id", "/v1/profiles/:userId",
		func(t targetToken) string { return "/v1/profiles/" + t.id }},
	{"links", "/v1/profiles/:userId/links",
		func(t targetToken) string { return "/v1/profiles/" + t.id + "/links" }},
	{"about (all)", "/v1/profiles/:userId/about",
		func(t targetToken) string { return "/v1/profiles/" + t.id + "/about" }},
	{"about (section)", "/v1/profiles/:userId/about/:section",
		func(t targetToken) string { return "/v1/profiles/" + t.id + "/about/work" }},
	{"stats", "/v1/profiles/:userId/stats",
		func(t targetToken) string { return "/v1/profiles/" + t.id + "/stats" }},
	{"module profile", "/v1/profiles/:userId/modules/:module",
		func(t targetToken) string { return "/v1/profiles/" + t.id + "/modules/dating" }},
	// CLB-2: the two routes the :userId filter could not see.
	{"profile by username", "/v1/profiles/by-username/:username",
		func(t targetToken) string { return "/v1/profiles/by-username/" + t.username }},
	{"handle redirect", "/v1/profiles/resolve-handle/:username",
		func(t targetToken) string { return "/v1/profiles/resolve-handle/" + t.oldUsername }},
}

// publicRoutePolicy classifies EVERY route this service registers. A route
// with no entry fails TestEveryRegisteredRouteHasAPolicy, which is what stops
// another unclassified resolver from shipping.
var publicRoutePolicy = map[string]routePolicy{
	// No target.
	"/v1/profiles/health": policyNoTarget,
	"/v1/links/:id/click": policyNoTarget,

	// Many targets.
	"/v1/profiles/discover": policyListSurface,
	"/v1/profiles/batch":    policyListSurface,

	// One target.
	"/v1/profiles/:userId":                  policySingleTarget,
	"/v1/profiles/:userId/links":            policySingleTarget,
	"/v1/profiles/:userId/about":            policySingleTarget,
	"/v1/profiles/:userId/about/:section":   policySingleTarget,
	"/v1/profiles/:userId/stats":            policySingleTarget,
	"/v1/profiles/:userId/modules/:module":  policySingleTarget,
	"/v1/profiles/by-username/:username":    policySingleTarget,
	"/v1/profiles/resolve-handle/:username": policySingleTarget,
}

// aboutSentinel values appear ONLY on non-public rows, so finding one in a
// response is proof of a visibility leak rather than a guess.
const (
	privateAboutSentinel     = "PRIVATE-EMPLOYER-SENTINEL"
	connectionsAboutSentinel = "CONNECTIONS-ONLY-SENTINEL"
	publicAboutSentinel      = "PUBLIC-ABOUT-SENTINEL"
	unsafeLinkSentinel       = "javascript:alert(document.domain)"
	safeLinkSentinel         = "https://example.com/real"
)

// surfaceService serves every public surface with a mixture of public and
// non-public rows.
type surfaceService struct {
	stubProfileService
}

// The identifiers the target account answers to. resolvedHandleSentinel is the
// value a handle redirect leaks: if a blocked viewer ever sees it, the account
// that renamed to get away from them has just been re-linked to its new name.
const (
	targetUsername          = "target-handle"
	targetOldUsername       = "target-old-handle"
	resolvedHandleSentinel  = "target-handle"
	resolvedUserIDIsLeaking = "user_id"
)

func (s *surfaceService) GetProfile(_ context.Context, id uuid.UUID) (*store.Profile, error) {
	p := fullProfile()
	p.UserID = id
	p.Website = unsafeLinkSentinel
	intro := unsafeLinkSentinel
	cta := "DATA:text/html;base64,PHNjcmlwdD4="
	p.IntroMediaURL = &intro
	p.CTAURL = &cta
	return p, nil
}

func (s *surfaceService) GetProfileByUsername(_ context.Context, username string) (*store.Profile, error) {
	if username != targetUsername {
		return nil, nil
	}
	p := fullProfile()
	p.UserID = testTargetID
	return p, nil
}

// ResolveHandle answers as the real service does for a handle the target
// account used to hold: the account's id and its CURRENT username.
func (s *surfaceService) ResolveHandle(_ context.Context, username string) (*uuid.UUID, *string, error) {
	if username != targetOldUsername {
		return nil, nil, nil
	}
	id := testTargetID
	current := targetUsername
	return &id, &current, nil
}

func (s *surfaceService) GetUserLinks(_ context.Context, _ uuid.UUID) ([]store.UserLink, error) {
	return []store.UserLink{
		{Platform: "web", URL: safeLinkSentinel, DisplayLabel: "site"},
		{Platform: "evil", URL: unsafeLinkSentinel, DisplayLabel: "click me"},
	}, nil
}

func aboutFixture() []store.AboutItem {
	return []store.AboutItem{
		{Section: "work", ItemID: uuid.New(), Visibility: "public",
			Data: map[string]any{"title": publicAboutSentinel}},
		{Section: "work", ItemID: uuid.New(), Visibility: "private",
			Data: map[string]any{"employer": privateAboutSentinel}},
		{Section: "work", ItemID: uuid.New(), Visibility: "connections",
			Data: map[string]any{"salary": connectionsAboutSentinel}},
	}
}

func (s *surfaceService) GetAllAbout(_ context.Context, _ uuid.UUID) ([]store.AboutItem, error) {
	return aboutFixture(), nil
}

func (s *surfaceService) GetAboutBySection(_ context.Context, _ uuid.UUID, _ string) ([]store.AboutItem, error) {
	return aboutFixture(), nil
}

func (s *surfaceService) GetProfileStats(_ context.Context, _ uuid.UUID) (*store.ProfileStats, error) {
	return &store.ProfileStats{}, nil
}

func (s *surfaceService) GetModuleProfile(_ context.Context, _ uuid.UUID, _ string) (*store.ModuleProfile, error) {
	return &store.ModuleProfile{Module: "dating"}, nil
}

// reverseBlockChecker models the direction the viewer cannot see: the TARGET
// blocked the VIEWER. It answers true only for that ordered pair, so a gate
// that consulted just "did the viewer block the target" would fail against it.
//
// The production checker asks graph-service for a symmetric set, but the whole
// point of CLB-2 is that untested directions are how gaps ship — so the
// direction gets its own checker rather than being assumed.
type reverseBlockChecker struct {
	blocker uuid.UUID // the account that pressed block
	blocked uuid.UUID // the account it was pressed on
}

func (r reverseBlockChecker) BlockedEitherWay(_ context.Context, viewerID, targetID uuid.UUID) (bool, error) {
	return viewerID == r.blocked && targetID == r.blocker, nil
}

func surfaceRouter(t *testing.T, checker BlockChecker) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&surfaceService{}, nil)
	if checker != nil {
		h.WithBlockChecker(checker)
	}
	h.RegisterRoutes(r, func(c *gin.Context) { c.Next() }, func(c *gin.Context) { c.Next() })
	return r
}

func getAs(t *testing.T, r *gin.Engine, path, viewerID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if viewerID != "" {
		req.Header.Set("X-User-Id", viewerID)
	}
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	return resp
}

// theTarget is the account under test, in every identifier form.
func theTarget() targetToken {
	return targetToken{
		id:          testTargetID.String(),
		username:    targetUsername,
		oldUsername: targetOldUsername,
	}
}

// THE CLOSURE PROOF: both block directions, every target-resolving public
// surface, the non-enumerating result.
func TestEveryPublicSurfaceDeniesABlockedViewerInBothDirections(t *testing.T) {
	// Both directions are exercised explicitly. graph-service's
	// blocked-and-muted set is symmetric — it contains who the viewer blocked
	// AND who blocked the viewer — but a gate that consulted only one
	// direction would still pass a single-direction test, so each is named.
	for _, direction := range []struct {
		name    string
		checker BlockChecker
	}{
		{"viewer blocked the target", staticBlockChecker{blocked: true}},
		{"target blocked the viewer", reverseBlockChecker{blocker: testTargetID, blocked: testViewerID}},
	} {
		t.Run(direction.name, func(t *testing.T) {
			r := surfaceRouter(t, direction.checker)
			for _, route := range publicTargetResolvers {
				t.Run(route.name, func(t *testing.T) {
					path := route.path(theTarget())
					resp := getAs(t, r, path, testViewerID.String())

					if resp.Code != http.StatusNotFound {
						t.Fatalf("%s: status %d, want 404. A blocked viewer could read this "+
							"surface even though the profile page itself is denied — a block "+
							"that covers the front door and none of the windows.",
							path, resp.Code)
					}
					// Must not reveal that a block specifically is the reason.
					body := resp.Body.String()
					upper := strings.ToUpper(body)
					if strings.Contains(upper, "BLOCK") || strings.Contains(upper, "FORBIDDEN") {
						t.Errorf("%s: the response reveals a block exists: %s", path, body)
					}
					// CLB-2: neither identifier may appear. For the handle
					// resolver these ARE the payload, so this is the assertion
					// that the leak is closed rather than merely re-worded.
					if strings.Contains(body, testTargetID.String()) {
						t.Errorf("%s: the denial leaks the target's user id: %s", path, body)
					}
					if strings.Contains(body, resolvedHandleSentinel) {
						t.Errorf("%s: the denial leaks the target's current username: %s", path, body)
					}
				})
			}
		})
	}
}

// A blocked handle redirect must be indistinguishable from a handle nobody
// ever used. If the two answers differ, the block is still an oracle: the
// harasser learns "this handle belongs to someone who blocked me", which is
// precisely the fact the 404 exists to hide.
func TestBlockedHandleRedirectIsIdenticalToAnUnusedHandle(t *testing.T) {
	blockedResp := getAs(t,
		surfaceRouter(t, staticBlockChecker{blocked: true}),
		"/v1/profiles/resolve-handle/"+targetOldUsername, testViewerID.String())

	unusedResp := getAs(t,
		surfaceRouter(t, staticBlockChecker{}),
		"/v1/profiles/resolve-handle/never-existed", testViewerID.String())

	if blockedResp.Code != unusedResp.Code {
		t.Fatalf("blocked handle answered %d, unused handle answered %d; the "+
			"difference tells a blocked viewer their target exists",
			blockedResp.Code, unusedResp.Code)
	}
	if blockedResp.Body.String() != unusedResp.Body.String() {
		t.Fatalf("the two 404 bodies differ, which is an enumeration oracle:\n"+
			"blocked: %s\nunused:  %s", blockedResp.Body.String(), unusedResp.Body.String())
	}
}

// Fail-closed: an unreachable graph-service must deny every target-resolving
// surface, not just the two SR-4 covered.
func TestEveryPublicSurfaceFailsClosedOnBlockCheckError(t *testing.T) {
	r := surfaceRouter(t, staticBlockChecker{err: context.DeadlineExceeded})

	for _, route := range publicTargetResolvers {
		t.Run(route.name, func(t *testing.T) {
			path := route.path(theTarget())
			resp := getAs(t, r, path, testViewerID.String())
			if resp.Code == http.StatusOK {
				t.Fatalf("%s served while the block check was FAILING", path)
			}
			if strings.Contains(resp.Body.String(), testTargetID.String()) {
				t.Errorf("%s leaked the target's user id while the block state was "+
					"unknown: %s", path, resp.Body.String())
			}
		})
	}
}

// The same, with no block checker configured at all — a misconfigured
// deployment must not become an open one.
func TestEveryPublicSurfaceFailsClosedWithNoBlockChecker(t *testing.T) {
	r := surfaceRouter(t, nil)

	for _, route := range publicTargetResolvers {
		t.Run(route.name, func(t *testing.T) {
			path := route.path(theTarget())
			resp := getAs(t, r, path, testViewerID.String())
			if resp.Code == http.StatusOK {
				t.Fatalf("%s served with NO block checker configured", path)
			}
		})
	}
}

// An unblocked viewer must still get everything. A denial that is too broad is
// an outage, not a fix.
func TestUnblockedViewerStillSeesEveryPublicSurface(t *testing.T) {
	r := surfaceRouter(t, staticBlockChecker{})

	for _, route := range publicTargetResolvers {
		t.Run(route.name, func(t *testing.T) {
			path := route.path(theTarget())
			resp := getAs(t, r, path, testViewerID.String())
			if resp.Code != http.StatusOK {
				t.Fatalf("%s: status %d for an UNBLOCKED viewer: %s",
					path, resp.Code, resp.Body.String())
			}
		})
	}

	// And the redirect must actually redirect, or the gate has broken the
	// feature instead of securing it.
	resp := getAs(t, r, "/v1/profiles/resolve-handle/"+targetOldUsername, testViewerID.String())
	body := resp.Body.String()
	if !strings.Contains(body, resolvedHandleSentinel) || !strings.Contains(body, testTargetID.String()) {
		t.Fatalf("an unblocked viewer did not receive the handle redirect: %s", body)
	}
}

// ── Visibility ──────────────────────────────────────────────────────────────

// About rows carry a visibility column precisely because some of them are not
// public. Every row was being published.
func TestPrivateAboutRowsNeverAppearOnPublicSurfaces(t *testing.T) {
	r := surfaceRouter(t, staticBlockChecker{})
	target := testTargetID.String()

	for _, path := range []string{
		"/v1/profiles/" + target + "/about",
		"/v1/profiles/" + target + "/about/work",
	} {
		t.Run(path, func(t *testing.T) {
			resp := getAs(t, r, path, "")
			if resp.Code != http.StatusOK {
				t.Fatalf("status %d: %s", resp.Code, resp.Body.String())
			}
			body := resp.Body.String()

			for _, sentinel := range []string{privateAboutSentinel, connectionsAboutSentinel} {
				if strings.Contains(body, sentinel) {
					t.Errorf("a non-public about row was published (%s):\n%s", sentinel, body)
				}
			}
			// The public row must still be there, or the filter is not
			// filtering — it is emptying.
			if !strings.Contains(body, publicAboutSentinel) {
				t.Errorf("the PUBLIC about row was withheld too; the filter is over-broad:\n%s", body)
			}
		})
	}
}

// ── URL safety ──────────────────────────────────────────────────────────────

func TestUnsafeURLSchemesAreNeverPublished(t *testing.T) {
	r := surfaceRouter(t, staticBlockChecker{})
	target := testTargetID.String()

	for _, path := range []string{
		"/v1/profiles/" + target,
		"/v1/profiles/" + target + "/links",
	} {
		t.Run(path, func(t *testing.T) {
			resp := getAs(t, r, path, "")
			body := resp.Body.String()

			for _, bad := range []string{"javascript:", "JaVaScRiPt:", "data:text/html", "DATA:text/html"} {
				if strings.Contains(strings.ToLower(body), strings.ToLower(bad)) {
					t.Errorf("an unsafe URL scheme (%s) was published — a stored "+
						"injection vector on a page other users click:\n%s", bad, body)
				}
			}
		})
	}

	// The safe link must survive; dropping everything is not a fix.
	resp := getAs(t, r, "/v1/profiles/"+target+"/links", "")
	if !strings.Contains(resp.Body.String(), safeLinkSentinel) {
		t.Errorf("the safe https link was dropped too: %s", resp.Body.String())
	}
}

func TestSafePublicURL(t *testing.T) {
	safe := []string{
		"https://example.com", "http://example.com/path?q=1",
		"HTTPS://EXAMPLE.COM", "https://sub.example.co.uk/a/b#frag",
	}
	for _, u := range safe {
		if !SafePublicURL(u) {
			t.Errorf("%q should be publishable", u)
		}
	}

	unsafe := []string{
		"", "   ",
		"javascript:alert(1)",
		"JaVaScRiPt:alert(1)",
		"  javascript:alert(1)",
		"data:text/html;base64,PHNjcmlwdD4=",
		"file:///etc/passwd",
		"intent://scan#Intent;scheme=zxing;end",
		"market://details?id=com.evil",
		"vbscript:msgbox(1)",
		"about:blank",
		"//example.com",           // scheme-relative: no scheme at all
		"http:",                   // scheme with no host
		"https://",                // no host
		"http://exa\nmple.com",    // embedded newline
		"https://example.com\x00", // NUL
	}
	for _, u := range unsafe {
		if SafePublicURL(u) {
			t.Errorf("%q must NOT be publishable", u)
		}
	}
}

// ── The table itself ────────────────────────────────────────────────────────

// isSelfRoute reports whether a path sits under the authenticated /me group,
// where the caller is the target and there is no third party to block.
func isSelfRoute(path string) bool {
	for _, seg := range strings.Split(path, "/") {
		if seg == "me" {
			return true
		}
	}
	return false
}

// CLB-2 — EVERY registered route must carry a policy classification.
//
// This replaces a check that filtered on the substring ":userId". That filter
// was the bug: it could not see resolve-handle or by-username, so neither
// could anything downstream of it. Classifying by what a route RESOLVES means
// a new resolver cannot be added without someone deciding, in this file, which
// contract it is held to.
func TestEveryRegisteredRouteHasAPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&surfaceService{}, nil)
	h.WithBlockChecker(staticBlockChecker{})
	h.RegisterRoutes(r, func(c *gin.Context) { c.Next() }, func(c *gin.Context) { c.Next() })

	routes := r.Routes()
	if len(routes) == 0 {
		t.Fatal("no routes found; the enumeration is broken")
	}

	seen := 0
	for _, ri := range routes {
		if isSelfRoute(ri.Path) {
			continue // policySelfOnly by construction
		}
		// Retired routes answer 410 and never touch user data.
		probe := strings.NewReplacer(
			":userId", testTargetID.String(),
			":username", targetUsername,
			":section", "work",
			":module", "dating",
			":id", uuid.NewString(),
			":linkId", uuid.NewString(),
			":itemId", uuid.NewString(),
		).Replace(ri.Path)
		req := httptest.NewRequest(ri.Method, probe, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		r.ServeHTTP(resp, req)
		if resp.Code == http.StatusGone {
			continue
		}
		seen++
		if _, ok := publicRoutePolicy[ri.Path]; !ok {
			t.Errorf("route %s %s has no entry in publicRoutePolicy. Classify it: a "+
				"route that resolves ONE account must apply the symmetric block gate, "+
				"and nothing checks that until it is listed here. This is the check "+
				"that resolve-handle slipped past.", ri.Method, ri.Path)
		}
	}
	if seen == 0 {
		t.Fatal("every route was skipped; the enumeration is broken")
	}
}

// Every route classified as single-target must be covered by the behavioural
// table above. Classification without a test is a comment.
func TestEverySingleTargetRouteIsBehaviourallyCovered(t *testing.T) {
	covered := map[string]bool{}
	for _, route := range publicTargetResolvers {
		covered[route.registered] = true
	}
	for path, policy := range publicRoutePolicy {
		if policy != policySingleTarget {
			continue
		}
		if !covered[path] {
			t.Errorf("%s is classified %q but is not in publicTargetResolvers, so no "+
				"test checks that it denies a blocked viewer in both directions",
				path, policy)
		}
	}
	// And the reverse: nothing may be behaviourally tested as a target
	// resolver without being classified as one.
	for _, route := range publicTargetResolvers {
		if publicRoutePolicy[route.registered] != policySingleTarget {
			t.Errorf("%s is tested as a target resolver but is classified %q",
				route.registered, publicRoutePolicy[route.registered])
		}
	}
}

// A classification that names a route the service does not register is stale
// and would silently stop protecting anything.
func TestPolicyTableHasNoStaleEntries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&surfaceService{}, nil)
	h.WithBlockChecker(staticBlockChecker{})
	h.RegisterRoutes(r, func(c *gin.Context) { c.Next() }, func(c *gin.Context) { c.Next() })

	registered := map[string]bool{}
	for _, ri := range r.Routes() {
		registered[ri.Path] = true
	}
	for path := range publicRoutePolicy {
		if !registered[path] {
			t.Errorf("publicRoutePolicy lists %s, which this service does not register", path)
		}
	}
}
