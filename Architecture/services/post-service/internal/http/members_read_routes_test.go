package http

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Members-only reads on the real router (2026-10-23 audit): the batch every
// feed hydrates from, the comment threads, and PUT /membership. The service
// has no stores behind it; the seams stand in for the reads, and
// monetization-service is an httptest fake reached by the real
// CheckEntitlement path.

type membersRouteRig struct {
	router                *gin.Engine
	author, member, tier  uuid.UUID
	gatedPost, openPost   uuid.UUID
	gatedComment          uuid.UUID
	monetizationQuestions int
	gates                 *fakeMembershipGates
	// tierQuestions counts reads of the public tier list; tiersDown makes
	// the rig's tier list fail.
	tierQuestions int
	tiersDown     bool
}

// rigTierPrice is the rig tier's monthly price, in paise.
const rigTierPrice = 4900

func newMembersRouteRig(t *testing.T, monetizationURL func(r *membersRouteRig) string) *membersRouteRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := &membersRouteRig{
		author: uuid.New(), member: uuid.New(), tier: uuid.New(),
		gatedPost: uuid.New(), openPost: uuid.New(), gatedComment: uuid.New(),
	}
	r.gates = &fakeMembershipGates{authors: map[uuid.UUID]uuid.UUID{r.gatedPost: r.author, r.openPost: r.author}}
	url := ""
	if monetizationURL != nil {
		url = monetizationURL(r)
	}
	post := func(id uuid.UUID) *postgres.Post {
		p := &postgres.Post{ID: id, AuthorID: r.author, Visibility: "public", ContentType: "long_video",
			Title: "Episode 1", Text: "the paid body", Media: []postgres.PostMedia{{MediaID: uuid.New(), Kind: "video"}}}
		if id == r.gatedPost {
			tier := r.tier
			p.TierRequiredID = &tier
		}
		return p
	}
	deps := service.HandlerTestDeps{
		Now:                    func() time.Time { return time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC) },
		ReadGate:               func(context.Context, uuid.UUID, *uuid.UUID) error { return nil },
		MonetizationServiceURL: url,
		BatchPosts: func(_ context.Context, ids []uuid.UUID, _ *uuid.UUID) (map[uuid.UUID]*service.PostDetail, error) {
			out := map[uuid.UUID]*service.PostDetail{}
			for _, id := range ids {
				out[id] = &service.PostDetail{Post: post(id)}
			}
			return out, nil
		},
		PostGate: func(_ context.Context, id uuid.UUID) (uuid.UUID, *uuid.UUID, error) {
			p := post(id)
			return p.AuthorID, p.TierRequiredID, nil
		},
		EngagementPost: func(_ context.Context, id, _ uuid.UUID) (*postgres.Post, error) { return post(id), nil },
		CommentLookup: func(_ context.Context, id uuid.UUID) (*postgres.Comment, error) {
			if id != r.gatedComment {
				return nil, errors.New("COMMENT_NOT_FOUND")
			}
			return &postgres.Comment{ID: id, PostID: r.gatedPost, AuthorID: r.author}, nil
		},
		MembershipGates: r.gates,
	}
	if url == "" {
		// No monetization-service: the tier list comes from the rig (the
		// author's one active tier), or fails while tiersDown is set.
		deps.CreatorTiers = func(_ context.Context, creator uuid.UUID) ([]service.CreatorTierRef, error) {
			if r.tiersDown {
				return nil, errors.New("monetization unreachable")
			}
			if creator != r.author {
				return []service.CreatorTierRef{}, nil
			}
			return []service.CreatorTierRef{{ID: r.tier, CreatorID: r.author, PricePaise: rigTierPrice, IsActive: true}}, nil
		}
	}
	r.router = gin.New()
	r.router.Use(gin.CustomRecovery(func(c *gin.Context, _ any) {
		c.AbortWithStatus(http.StatusInternalServerError)
	}))
	h := New(service.NewForHandlerTests(deps), nil)
	h.RegisterRoutes(r.router)
	h.RegisterReelEngagementRoutes(r.router)
	return r
}

// monetizationFake answers allowed only for the rig's member on the rig's
// author and tier — the real wire contract CheckEntitlement speaks.
func monetizationFake(t *testing.T) func(r *membersRouteRig) string {
	return func(r *membersRouteRig) string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			// The public tier list (members_tiers.go): the author's one
			// active tier, a switched-off one, and nothing for anyone else.
			if strings.HasPrefix(req.URL.Path, "/v1/monetization/creators/") && strings.HasSuffix(req.URL.Path, "/tiers") {
				r.tierQuestions++
				tiers := []map[string]any{}
				if req.URL.Path == "/v1/monetization/creators/"+r.author.String()+"/tiers" {
					tiers = append(tiers, map[string]any{"id": r.tier, "creator_id": r.author, "price_paise": rigTierPrice, "is_active": true})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": tiers})
				return
			}
			r.monetizationQuestions++
			ok := req.URL.Path == "/v1/monetization/entitlements" &&
				req.Header.Get("X-User-Id") == r.member.String() &&
				req.URL.Query().Get("creator_id") == r.author.String() &&
				req.URL.Query().Get("tier_id") == r.tier.String()
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"allowed": ok, "reason": "test"}})
		}))
		t.Cleanup(srv.Close)
		return srv.URL
	}
}

func (r *membersRouteRig) do(method, path string, caller *uuid.UUID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if caller != nil {
		req.Header.Set("X-User-Id", caller.String())
	}
	w := httptest.NewRecorder()
	r.router.ServeHTTP(w, req)
	return w
}

// codeOf is the error code, "" when the body is not an error envelope (a
// recovered 500 past the gate has none).
func codeOf(w *httptest.ResponseRecorder) string {
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return body.Error.Code
}

func batchRows(t *testing.T, r *membersRouteRig, caller *uuid.UUID) map[string]map[string]any {
	t.Helper()
	w := r.do(http.MethodPost, "/v1/posts/batch", caller, `{"ids":["`+r.gatedPost.String()+`","`+r.openPost.String()+`"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("batch: %d %s", w.Code, w.Body.String())
	}
	var env struct {
		Data map[string]map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	return env.Data
}

// POST /v1/posts/batch: per viewer. A non-member and a signed-out caller get
// the redacted row; a member and the author the full one; tier_required_id
// is on the gated row for all of them; the open row is never touched.
func TestBatchRedactsMembersOnlyRowsPerViewer(t *testing.T) {
	stranger := uuid.New()
	r := newMembersRouteRig(t, monetizationFake(t))
	for _, tc := range []struct {
		name   string
		caller *uuid.UUID
		full   bool
	}{
		{"non-member", &stranger, false},
		{"signed out", nil, false},
		{"member", &r.member, true},
		{"author", &r.author, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asked := r.monetizationQuestions
			rows := batchRows(t, r, tc.caller)
			// The author and a signed-out caller are answered without
			// asking monetization-service at all.
			if (tc.caller == nil || *tc.caller == r.author) && r.monetizationQuestions != asked {
				t.Fatalf("monetization was asked %d times for %s", r.monetizationQuestions-asked, tc.name)
			}
			gated, open := rows[r.gatedPost.String()], rows[r.openPost.String()]
			if gated == nil || open == nil {
				t.Fatalf("rows missing: %v", rows)
			}
			if gated["tier_required_id"] != r.tier.String() {
				t.Fatalf("gated row lost tier_required_id: %v", gated)
			}
			// Every gated row says what its tier costs, for every viewer;
			// the open row carries no price key at all.
			if gated["tier_required_price_paise"] != float64(rigTierPrice) {
				t.Fatalf("gated row tier_required_price_paise = %v, want %d: %v", gated["tier_required_price_paise"], rigTierPrice, gated)
			}
			if _, ok := open["tier_required_price_paise"]; ok {
				t.Fatalf("the open row carries a tier price: %v", open)
			}
			if tc.full {
				if gated["text"] != "the paid body" || gated["media"] == nil {
					t.Fatalf("want the full row: %v", gated)
				}
				if _, ok := gated["body_redacted"]; ok {
					t.Fatalf("a full row says body_redacted: %v", gated)
				}
			} else {
				if gated["body_redacted"] != true || gated["text"] != "" || gated["media"] != nil {
					t.Fatalf("want the redacted row: %v", gated)
				}
				if gated["title"] != "Episode 1" {
					t.Fatalf("the redacted row lost its title: %v", gated)
				}
			}
			if open["text"] != "the paid body" || open["body_redacted"] != nil || open["tier_required_id"] != nil {
				t.Fatalf("the open row changed: %v", open)
			}
		})
	}
}

// Monetization down: the member's row is redacted, never served in full.
func TestBatchFailsClosedWhenEntitlementsAreDown(t *testing.T) {
	for name, url := range map[string]func(*membersRouteRig) string{
		"502": func(*membersRouteRig) string {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
			t.Cleanup(srv.Close)
			return srv.URL
		},
		"unreachable": func(*membersRouteRig) string {
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			srv.Close()
			return srv.URL
		},
		"not configured": nil,
	} {
		t.Run(name, func(t *testing.T) {
			r := newMembersRouteRig(t, url)
			gated := batchRows(t, r, &r.member)[r.gatedPost.String()]
			if gated["body_redacted"] != true || gated["text"] != "" {
				t.Fatalf("a member's row was served in full while entitlements were down: %v", gated)
			}
		})
	}
}

// The comment threads of a members-only post: non-members (signed in or
// not) get 403 MEMBERS_ONLY on every comment route; the author and members
// pass the gate (here they then reach the absent store); an open post's
// thread is never MEMBERS_ONLY.
func TestCommentsOnMembersOnlyPostRefuseNonMembers(t *testing.T) {
	stranger := uuid.New()
	r := newMembersRouteRig(t, monetizationFake(t))
	gp, op, cid := r.gatedPost.String(), r.openPost.String(), r.gatedComment.String()
	other := uuid.NewString()
	routes := []struct {
		method, path, body string
		signedIn           bool
	}{
		{"GET", "/v1/posts/" + gp + "/comments", "", false},
		{"GET", "/v1/posts/" + gp + "/comments/around/" + other, "", false},
		{"POST", "/v1/posts/" + gp + "/comments", `{"text":"hi"}`, true},
		{"GET", "/v1/reels/" + gp + "/comments", "", false},
		{"POST", "/v1/reels/" + gp + "/comments", `{"text":"hi"}`, true},
		{"POST", "/v1/comments/" + cid + "/reply", `{"text":"hi"}`, true},
		{"POST", "/v1/comments/" + cid + "/dislike", "", true},
	}
	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			if w := r.do(rt.method, rt.path, &stranger, rt.body); w.Code != http.StatusForbidden || codeOf(w) != "MEMBERS_ONLY" {
				t.Fatalf("non-member: %d %s; want 403 MEMBERS_ONLY", w.Code, w.Body.String())
			}
			if !rt.signedIn {
				if w := r.do(rt.method, rt.path, nil, rt.body); w.Code != http.StatusForbidden || codeOf(w) != "MEMBERS_ONLY" {
					t.Fatalf("signed out: %d %s; want 403 MEMBERS_ONLY", w.Code, w.Body.String())
				}
			}
			for name, who := range map[string]*uuid.UUID{"member": &r.member, "author": &r.author} {
				if w := r.do(rt.method, rt.path, who, rt.body); codeOf(w) == "MEMBERS_ONLY" {
					t.Fatalf("%s was refused: %d %s", name, w.Code, w.Body.String())
				}
			}
			openPath := strings.Replace(rt.path, gp, op, 1)
			if openPath != rt.path {
				if w := r.do(rt.method, openPath, &stranger, rt.body); codeOf(w) == "MEMBERS_ONLY" {
					t.Fatalf("an open post's thread was refused: %d %s", w.Code, w.Body.String())
				}
			}
		})
	}
}

// fakeMembershipGates is the PUT /membership store.
type fakeMembershipGates struct {
	authors map[uuid.UUID]uuid.UUID
	sets    []*uuid.UUID
}

func (f *fakeMembershipGates) GetPostAuthorAndGate(_ context.Context, id uuid.UUID) (uuid.UUID, *uuid.UUID, error) {
	a, ok := f.authors[id]
	if !ok {
		return uuid.Nil, nil, errors.New("no rows in result set")
	}
	return a, nil, nil
}

func (f *fakeMembershipGates) SetPostMembershipGate(_ context.Context, _ uuid.UUID, tier *uuid.UUID) error {
	f.sets = append(f.sets, tier)
	return nil
}

// A malformed body is 400 and changes nothing. It used to be ignored, so any
// of these cleared the gate and made a members-only post public.
func TestMembershipPutRefusesAMalformedBody(t *testing.T) {
	r := newMembersRouteRig(t, nil)
	path := "/v1/posts/" + r.gatedPost.String() + "/membership"
	for name, body := range map[string]string{
		"empty":         "",
		"whitespace":    "   ",
		"not json":      "tier=abc",
		"truncated":     `{"tier_required_id":`,
		"not a uuid":    `{"tier_required_id":"gold"}`,
		"a number":      `{"tier_required_id":42}`,
		"misspelt key":  `{"tier_requried_id":"` + uuid.NewString() + `"}`, //nolint:misspell // the misspelt key is the case under test
		"array":         `[{"tier_required_id":null}]`,
		"bare null":     `null`,
		"trailing data": `{} {}`,
		"nil uuid":      `{"tier_required_id":"00000000-0000-0000-0000-000000000000"}`,
	} {
		t.Run(name, func(t *testing.T) {
			before := len(r.gates.sets)
			w := r.do(http.MethodPut, path, &r.author, body)
			if w.Code != http.StatusBadRequest || codeOf(w) != "INVALID_REQUEST" {
				t.Fatalf("body %q: %d %s; want 400 INVALID_REQUEST", body, w.Code, w.Body.String())
			}
			if len(r.gates.sets) != before {
				t.Fatalf("body %q changed the gate: %v", body, r.gates.sets)
			}
		})
	}
}

// A well-formed body sets or clears the gate, author only.
func TestMembershipPutSetsAndClears(t *testing.T) {
	r := newMembersRouteRig(t, nil)
	path := "/v1/posts/" + r.gatedPost.String() + "/membership"
	tier := r.tier // the author's own active tier
	if w := r.do(http.MethodPut, path, &r.author, `{"tier_required_id":"`+tier.String()+`"}`); w.Code != http.StatusOK {
		t.Fatalf("set: %d %s", w.Code, w.Body.String())
	}
	for _, body := range []string{`{}`, `{"tier_required_id":null}`} {
		if w := r.do(http.MethodPut, path, &r.author, body); w.Code != http.StatusOK {
			t.Fatalf("clear %s: %d %s", body, w.Code, w.Body.String())
		}
	}
	if len(r.gates.sets) != 3 || r.gates.sets[0] == nil || *r.gates.sets[0] != tier || r.gates.sets[1] != nil || r.gates.sets[2] != nil {
		t.Fatalf("store saw %v", r.gates.sets)
	}
	stranger := uuid.New()
	if w := r.do(http.MethodPut, path, &stranger, `{}`); w.Code != http.StatusForbidden {
		t.Fatalf("non-author: %d %s", w.Code, w.Body.String())
	}
	if w := r.do(http.MethodPut, path, nil, `{}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("signed out: %d %s", w.Code, w.Body.String())
	}
	if len(r.gates.sets) != 3 {
		t.Fatalf("a refused PUT changed the gate: %v", r.gates.sets)
	}
}

// PUT /membership names one of the author's ACTIVE tiers: another
// creator's, an unknown or a switched-off tier is 400 INVALID_TIER, and a
// tier list monetization-service cannot answer is 503 — the gate unchanged
// either way. Clearing needs no lookup, so it works while tiers are down.
func TestMembershipPutChecksTheTier(t *testing.T) {
	r := newMembersRouteRig(t, nil)
	path := "/v1/posts/" + r.gatedPost.String() + "/membership"
	for name, tier := range map[string]string{"unknown or another creator's": uuid.NewString()} {
		w := r.do(http.MethodPut, path, &r.author, `{"tier_required_id":"`+tier+`"}`)
		if w.Code != http.StatusBadRequest || codeOf(w) != "INVALID_TIER" {
			t.Fatalf("%s: %d %s; want 400 INVALID_TIER", name, w.Code, w.Body.String())
		}
	}
	r.tiersDown = true
	w := r.do(http.MethodPut, path, &r.author, `{"tier_required_id":"`+r.tier.String()+`"}`)
	if w.Code != http.StatusServiceUnavailable || codeOf(w) != "MEMBERSHIP_TIERS_UNAVAILABLE" {
		t.Fatalf("tiers down: %d %s; want 503 MEMBERSHIP_TIERS_UNAVAILABLE", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "unreachable") {
		t.Fatalf("the lookup error leaked into the body: %s", w.Body.String())
	}
	if len(r.gates.sets) != 0 {
		t.Fatalf("a refused tier changed the gate: %v", r.gates.sets)
	}
	if w := r.do(http.MethodPut, path, &r.author, `{}`); w.Code != http.StatusOK || len(r.gates.sets) != 1 || r.gates.sets[0] != nil {
		t.Fatalf("clear while tiers down: %d %s, store saw %v", w.Code, w.Body.String(), r.gates.sets)
	}
}

// A non-member refused on a members-only thread is told which gate: details
// {creator_id, tier_required_id}, so the Join sheet opens on the right
// creator and level.
func TestMembersOnlyRefusalNamesTheGate(t *testing.T) {
	stranger := uuid.New()
	r := newMembersRouteRig(t, monetizationFake(t))
	w := r.do(http.MethodGet, "/v1/posts/"+r.gatedPost.String()+"/comments", &stranger, "")
	if w.Code != http.StatusForbidden || codeOf(w) != "MEMBERS_ONLY" {
		t.Fatalf("%d %s; want 403 MEMBERS_ONLY", w.Code, w.Body.String())
	}
	var body struct {
		Error struct {
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Details["creator_id"] != r.author.String() || body.Error.Details["tier_required_id"] != r.tier.String() {
		t.Fatalf("details = %v, want creator_id %s and tier_required_id %s", body.Error.Details, r.author, r.tier)
	}
}

// GET /v1/posts/by-author/:id?members_only takes true / false; anything
// else is 400 before any read.
func TestByAuthorMembersOnlyParam(t *testing.T) {
	for raw, want := range map[string]bool{"": false, "false": false, "true": true, "TRUE": true, "1": true, "0": false} {
		got, ok := parseMembersOnlyParam(raw)
		if !ok || got != want {
			t.Errorf("%q: got %v ok=%v, want %v", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"yes", "maybe", "2"} {
		if _, ok := parseMembersOnlyParam(raw); ok {
			t.Errorf("%q accepted", raw)
		}
	}
	r := newMembersRouteRig(t, nil)
	w := r.do(http.MethodGet, "/v1/posts/by-author/"+r.author.String()+"?members_only=yes", &r.author, "")
	if w.Code != http.StatusBadRequest || codeOf(w) != "INVALID_REQUEST" {
		t.Fatalf("members_only=yes: %d %s; want 400", w.Code, w.Body.String())
	}
}

// Every handler that serialises a row it read with svc.GetPost runs the
// members-only rule on it first (the service's inventory exempts GetPost
// because these handlers redact).
func TestHandlersReadingGetPostRedact(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	found := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			readsPost, redacts := false, false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "svc" {
					switch sel.Sel.Name {
					case "GetPost":
						readsPost = true
					case "RedactDetailForViewer":
						redacts = true
					}
				}
				return true
			})
			if readsPost {
				found++
				if !redacts {
					t.Errorf("%s (%s) serialises svc.GetPost's row without h.svc.RedactDetailForViewer: a members-only "+
						"body would reach a non-member through it", fn.Name.Name, f)
				}
			}
		}
	}
	if found == 0 {
		t.Fatal("no handler reads svc.GetPost; the scan is looking at the wrong thing")
	}
}
