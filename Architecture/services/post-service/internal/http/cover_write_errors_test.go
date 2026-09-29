package http

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/post-service/internal/service"
	"github.com/gin-gonic/gin"
)

// A cover refused on create, on a draft publish or on cover-frame answers
// exactly as PATCH /v1/posts/:id does (2026-09-29, service/cover_guard.go).
func TestWriteCoverMediaErrorAnswersLikeThePatchRoute(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{service.ErrMediaNotOwned, 403, "MEDIA_NOT_OWNED"},
		{service.ErrMediaNotFound, 422, "MEDIA_NOT_FOUND"},
		{service.ErrMediaNotReady, 422, "MEDIA_NOT_READY"},
		{service.ErrMediaTypeMismatch, 422, "MEDIA_TYPE_MISMATCH"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			cover := fmt.Errorf("create: %w", &service.CoverMediaError{Err: fmt.Errorf("%w: detail", tc.err)})

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/posts", nil)
			if !writeCoverMediaError(c, cover) {
				t.Fatal("a refused cover was not answered")
			}
			if rec.Code != tc.status || errorCode(t, rec) != tc.code {
				t.Fatalf("got %d %s want %d %s", rec.Code, errorCode(t, rec), tc.status, tc.code)
			}

			patch := httptest.NewRecorder()
			pc, _ := gin.CreateTestContext(patch)
			pc.Request = httptest.NewRequest(http.MethodPatch, "/v1/posts/x", nil)
			writePostEditError(pc, errors.Unwrap(cover))
			if rec.Code != patch.Code || errorCode(t, rec) != errorCode(t, patch) {
				t.Fatalf("cover answer %d %s differs from PATCH %d %s", rec.Code, rec.Body.String(), patch.Code, patch.Body.String())
			}

			// The same sentinel about an ATTACHMENT is not this mapper's:
			// the create guards keep their own statuses for it.
			plain := httptest.NewRecorder()
			ac, _ := gin.CreateTestContext(plain)
			ac.Request = httptest.NewRequest(http.MethodPost, "/v1/posts", nil)
			if writeCoverMediaError(ac, tc.err) || plain.Body.Len() != 0 {
				t.Fatal("an attachment refusal was answered as a cover refusal")
			}
		})
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/posts", nil)
	if writeCoverMediaError(c, errors.New("boom")) || rec.Body.Len() != 0 {
		t.Fatal("an unrelated error was answered as a cover refusal")
	}
}

// Every route a cover can arrive on maps its refusal: the mapper only helps
// where it is called. Read from the source, so dropping a call fails here.
func TestCoverRefusalIsMappedOnEveryWritePath(t *testing.T) {
	for file, funcs := range map[string][]string{
		"handler.go":     {"CreatePost", "SetCoverFrame"},
		"drafts.go":      {"PublishDraft"},
		"post_drafts.go": {"writePostDraftError"},
	} {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range funcs {
			found, calls := false, false
			for _, decl := range parsed.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name.Name != name || fn.Body == nil {
					continue
				}
				found = true
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					if call, ok := n.(*ast.CallExpr); ok {
						if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "writeCoverMediaError" {
							calls = true
						}
					}
					return true
				})
			}
			if !found {
				t.Fatalf("%s: func %s not found", file, name)
			}
			if !calls {
				t.Fatalf("%s: %s does not map a refused cover (writeCoverMediaError)", file, name)
			}
		}
	}
}
