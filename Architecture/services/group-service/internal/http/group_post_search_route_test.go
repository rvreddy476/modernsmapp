package http

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/atpost/group-service/internal/service"
	"github.com/gin-gonic/gin"
)

/*
Guards for the in-group post search route.

Two of these are real: they build the router gin will actually serve, and they
run the error mapping a refusal will actually go through. The structural one
pins the handler to passing the service's own type to the encoder, because a
bespoke response struct would bypass the anonymity mask on store.GroupPostV2.
*/

var (
	httpLineComment  = regexp.MustCompile(`(?m)//.*$`)
	httpBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

func codeOnly(src string) string {
	return httpLineComment.ReplaceAllString(httpBlockComment.ReplaceAllString(src, " "), "")
}

func handlerBody(t *testing.T, path, name string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != name || fd.Body == nil {
			continue
		}
		return string(raw)[fset.Position(fd.Body.Pos()).Offset:fset.Position(fd.Body.End()).Offset]
	}
	t.Fatalf("%s: no function named %s", path, name)
	return ""
}

/*
The route exists, is served by the right handler, and does not shadow the
single-post route.

This registers the real routes on a real engine, so it is also the check that
"search" as a static segment beside ":postId" does not make gin panic at
startup — a conflict there would take the whole service down on boot, not just
this endpoint.
*/
func TestGroupPostSearchRouteIsRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(nil).RegisterRoutes(r)

	const searchPath = "/v1/groups/:groupId/posts/v2/search"
	const postPath = "/v1/groups/:groupId/posts/v2/:postId"

	byPath := map[string]string{}
	for _, ri := range r.Routes() {
		if ri.Method == http.MethodGet {
			byPath[ri.Path] = ri.Handler
		}
	}

	h, ok := byPath[searchPath]
	if !ok {
		t.Fatalf("GET %s is not registered — the web group page's search box has nothing to call", searchPath)
	}
	// gin reports a method value as "…(*Handler).Name-fm".
	if !strings.Contains(h, ".SearchGroupPostsV2") {
		t.Errorf("GET %s is served by %s, not SearchGroupPostsV2", searchPath, h)
	}
	// Additive only: the existing single-post read must still be there.
	if _, ok := byPath[postPath]; !ok {
		t.Errorf("GET %s disappeared — the mobile app reads single posts through it", postPath)
	}
}

/*
A refusal is a 404 that says nothing.

service.ErrGroupSearchUnavailable is the single answer for "no such group",
"deleted", and "private and you are not a member". This runs it through the real
error mapping and asserts the caller cannot tell which: same status as a
genuinely missing group, and a body that names no reason.

Without this the collapse is only a claim about a string; here it is the status
code and the bytes.
*/
func TestGroupPostSearchRefusalIsIndistinguishableFromAMissingGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mapErr := func(err error) (int, string) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/groups/x/posts/v2/search?q=book+club", nil)
		handleServiceError(c, err)
		return w.Code, w.Body.String()
	}

	// What a group that genuinely does not exist produces.
	missingStatus, _ := mapErr(errors.New("not found: group not found"))
	if missingStatus != http.StatusNotFound {
		t.Fatalf("a missing group maps to %d, not 404 — this guard's reference point has moved", missingStatus)
	}

	status, body := mapErr(service.ErrGroupSearchUnavailable)
	if status != missingStatus {
		t.Errorf("a refused search maps to %d while a missing group maps to %d — the difference tells any caller that a private group exists at that id and that they are not in it", status, missingStatus)
	}
	for _, leak := range []string{"forbidden", "private", "member", "banned", "FORBIDDEN"} {
		if strings.Contains(body, leak) {
			t.Errorf("the refusal body contains %q: %s", leak, body)
		}
	}
	// And it must not carry the FORBIDDEN code either, which is as good as a 403.
	var decoded struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err == nil && decoded.Error.Code == "FORBIDDEN" {
		t.Errorf("the refusal carries error code FORBIDDEN: %s", body)
	}
}

/*
The handler hands the encoder the service's own type.

store.GroupPostV2.MarshalJSON masks an anonymous post's author. A response
struct built in the handler — even one that simply lists the fields it wants —
bypasses that method and publishes the real author_id of every anonymous post
that matched. So the handler must pass the value it got straight through and
must not name the author at all.
*/
func TestGroupPostSearchHandlerPassesTheMaskingTypeThrough(t *testing.T) {
	body := codeOnly(handlerBody(t, "handler.go", "SearchGroupPostsV2"))

	if !strings.Contains(body, "h.svc.SearchGroupPostsV2(") {
		t.Fatal("the handler does not call the service")
	}
	if !strings.Contains(body, "api.JSON(c.Writer, http.StatusOK, posts, nil)") {
		t.Fatal("the handler does not pass the service's own value to the encoder — anything it rebuilds on the way out bypasses store.GroupPostV2.MarshalJSON and leaks the author of every anonymous post")
	}
	for _, bad := range []string{"AuthorID", "author_id", "struct {", "struct{", "map[string]"} {
		if strings.Contains(body, bad) {
			t.Errorf("the handler contains %q — it must not reshape the response, and it must never touch the author", bad)
		}
	}
	// The mask only applies to store.GroupPostV2, so the empty case must be
	// that type too, not some other empty slice.
	if !strings.Contains(body, "[]store.GroupPostV2{}") {
		t.Error("the handler's empty-result value is not []store.GroupPostV2{}")
	}
}

// An empty q is refused before anything is read, and refused the same way the
// existing group search refuses it.
func TestGroupPostSearchRequiresAQuery(t *testing.T) {
	body := codeOnly(handlerBody(t, "handler.go", "SearchGroupPostsV2"))
	gate := strings.Index(body, `strings.TrimSpace(c.Query("q")) == ""`)
	if gate < 0 {
		t.Fatal(`the handler does not refuse a blank q — a whitespace-only query reaches the store and matches nothing at the cost of a full request`)
	}
	call := strings.Index(body, "h.svc.SearchGroupPostsV2(")
	if call < gate {
		t.Fatal("the blank-q check runs after the service call")
	}
}
