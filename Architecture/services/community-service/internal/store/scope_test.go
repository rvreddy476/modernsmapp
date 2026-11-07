package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// storeFunc returns the source of one *Store method in file.
func storeFunc(t *testing.T, file, name string) string {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv != nil && fd.Name.Name == name {
			return string(src[fset.Position(fd.Pos()).Offset:fset.Position(fd.End()).Offset])
		}
	}
	t.Fatalf("%s not found in %s", name, file)
	return ""
}

/*
Every write and list that names a post, space or wiki page by id is scoped to
the PATH community in SQL, so a moderator of one community cannot act on
another's content by pairing two ids (K-M2), and a space's posts are not
readable under the wrong community (K-M1).
*/
func TestContentStatementsAreScopedToTheCommunity(t *testing.T) {
	cases := []struct {
		file, fn string
		must     []string
	}{
		{"community_posts.go", "ListSpacePosts", []string{"space_id = $1 AND community_id = $4", "status = 'published'"}},
		{"community_posts.go", "DeleteCommunityPost", []string{"WHERE id = $1 AND community_id = $2", "s.execOnePost("}},
		{"community_posts.go", "MarkFeatured", []string{"WHERE id = $1 AND community_id = $2", "s.execOnePost("}},
		{"community_posts.go", "PinCommunityPost", []string{"WHERE id = $1 AND community_id = $2", "s.execOnePost("}},
		{"community_posts.go", "AcceptAnswer", []string{"WHERE id = $1 AND community_id = $2", "a.parent_post_id = $1 AND a.community_id = $2 AND a.status = 'published'"}},
		{"community_posts.go", "UpdateWikiPage", []string{"WHERE id=$1 AND community_id=$6", "RowsAffected() == 0"}},
		{"community_posts.go", "ListWikiPages", []string{"WHERE community_id = $1", "LIMIT $2 OFFSET $3"}},
		{"community.go", "ListEvents", []string{"LIMIT $2 OFFSET $3"}},
	}
	for _, c := range cases {
		body := storeFunc(t, c.file, c.fn)
		for _, m := range c.must {
			if !strings.Contains(body, m) {
				t.Errorf("%s lost %q", c.fn, m)
			}
		}
	}
	if !strings.Contains(storeFunc(t, "community_posts.go", "execOnePost"), "RowsAffected() == 0") {
		t.Fatal("execOnePost treats a write that matched nothing as success")
	}
}

// A second ban must not rewrite banned_by (K-L1).
func TestBanDoesNotRebanABannedRow(t *testing.T) {
	body := storeFunc(t, "community.go", "BanMember")
	for _, m := range []string{"role NOT IN ('banned', 'suspended')", "RowsAffected() == 0", `"already banned"`} {
		if !strings.Contains(body, m) {
			t.Errorf("BanMember lost %q", m)
		}
	}
}

// Discover lists only the public set, and only active communities (K-L2).
func TestDiscoverListsOnlyThePublicSet(t *testing.T) {
	body := storeFunc(t, "community.go", "DiscoverCommunities")
	for _, m := range []string{"status = 'active'", "community_type = ANY($3)", "PublicCommunityTypes"} {
		if !strings.Contains(body, m) {
			t.Errorf("DiscoverCommunities lost %q", m)
		}
	}
	for _, ct := range []string{"private", "invite", "fan", "local"} {
		if IsPublicCommunityType(ct) {
			t.Errorf("%q is listed as public", ct)
		}
	}
}
