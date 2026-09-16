package adminsession

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsAdminPath(t *testing.T) {
	for p, want := range map[string]bool{
		"/v1/admin":                     true,
		"/v1/admin/":                    true,
		"/v1/admin/commerce/x":          true,
		"/V1/Admin/x":                   true,
		"/v1/admin;p=1/x":               true,
		"/v1/auth/admin-session/login":  false,
		"/v1/admin-session":             false,
		"/v1/administrator":             false,
		"/v1/commerce/admin/x":          false,
		"/v2/admin/x":                   false,
		"v1/admin/x":                    false,
		"":                              false,
		"/":                             false,
		"/v1":                           false,
		"/v1/adminx":                    false,
		"/v1/auth/admin-session/me/../": false,
	} {
		if got := IsAdminPath(p); got != want {
			t.Errorf("IsAdminPath(%q) = %t, want %t", p, got, want)
		}
	}
}

func TestIsAdminRequestUsesEveryInterpretation(t *testing.T) {
	for uri, want := range map[string]bool{
		"/v1/admin/x":                    true,
		"/v1/feed/../admin/x":            true,
		"/v1//admin/x":                   true,
		"/v1/%61dmin/x":                  true,
		"/v1%2Fadmin/x":                  true,
		"/v1/auth/admin-session/refresh": false,
		"/v1/feed/home":                  false,
	} {
		req := httptest.NewRequest(http.MethodGet, "http://gw"+uri, nil)
		req.RequestURI = uri
		if got := IsAdminRequest(req); got != want {
			t.Errorf("IsAdminRequest(%q) = %t, want %t", uri, got, want)
		}
	}
}

func TestCSRFValid(t *testing.T) {
	mk := func(cookie, header string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/v1/admin/x", nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: CSRFCookie, Value: cookie})
		}
		if header != "" {
			r.Header.Set(CSRFHeader, header)
		}
		return r
	}
	cases := []struct {
		cookie, header string
		want           bool
	}{
		{"abc", "abc", true},
		{"abc", "abd", false},
		{"abc", "ab", false},
		{"abc", "", false},
		{"", "abc", false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := CSRFValid(mk(c.cookie, c.header)); got != c.want {
			t.Errorf("CSRFValid(cookie=%q, header=%q) = %t, want %t", c.cookie, c.header, got, c.want)
		}
	}
}

func TestNeedsCSRF(t *testing.T) {
	for m, want := range map[string]bool{
		http.MethodGet: false, http.MethodHead: false, http.MethodOptions: false,
		http.MethodPost: true, http.MethodPut: true, http.MethodPatch: true, http.MethodDelete: true,
	} {
		if got := NeedsCSRF(m); got != want {
			t.Errorf("NeedsCSRF(%s) = %t, want %t", m, got, want)
		}
	}
}

// A plain `==` between the header and the cookie gives the same answers as a
// constant-time compare, so no behavioural test can tell them apart (and a
// timing test would be flaky). This pins the implementation instead: CSRFValid
// must call subtle.ConstantTimeCompare, and must not compare two non-literal
// operands with == or !=.
func TestCSRFValidComparesInConstantTime(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "adminsession.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var fn *ast.FuncDecl
	for _, d := range file.Decls {
		if f, ok := d.(*ast.FuncDecl); ok && f.Name.Name == "CSRFValid" {
			fn = f
		}
	}
	if fn == nil {
		t.Fatal("CSRFValid not found")
	}
	literal := func(e ast.Expr) bool {
		switch v := e.(type) {
		case *ast.BasicLit:
			return true
		case *ast.Ident:
			return v.Name == "nil"
		}
		return false
	}
	usesSubtle := false
	ast.Inspect(fn, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CallExpr:
			if sel, ok := v.Fun.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "subtle" && sel.Sel.Name == "ConstantTimeCompare" {
					usesSubtle = true
				}
				if sel.Sel.Name == "EqualFold" || sel.Sel.Name == "Compare" || sel.Sel.Name == "Equal" {
					t.Errorf("%s: CSRFValid uses %s, which is not constant time", fset.Position(v.Pos()), sel.Sel.Name)
				}
			}
		case *ast.BinaryExpr:
			if (v.Op == token.EQL || v.Op == token.NEQ) && !literal(v.X) && !literal(v.Y) {
				t.Errorf("%s: CSRFValid compares two values with %s; use subtle.ConstantTimeCompare", fset.Position(v.Pos()), v.Op)
			}
		}
		return true
	})
	if !usesSubtle {
		t.Error("CSRFValid does not call subtle.ConstantTimeCompare")
	}
}
