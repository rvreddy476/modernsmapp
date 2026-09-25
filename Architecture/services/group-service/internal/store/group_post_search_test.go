package store

import (
	"regexp"
	"strings"
	"testing"

	"github.com/atpost/group-service/database"
)

/*
Guards for in-group post search, at the store layer. No database.

What is pinned here, and why each one is worth a test:

  - The query reuses the shared projection and its scanner. A hand-rolled
    SELECT compiles and then 500s the next time a column is added.

  - No search path calls to_tsquery. It raises a Postgres syntax error on
    ordinary two-word input, which reaches the client as a 500.

  - Only published posts are searchable, in the feed's own words.

  - Nothing in the query touches author_id, because searching by author
    de-anonymises anonymous posts.

  - The GIN index's expression matches the query's. A mismatch is not a wrong
    answer, it is a silent sequential scan, which no other test can see.

EVERY assertion below runs against codeOnly() output. Three guards in this tree
have already passed for the wrong reason by matching a function name that also
appeared inside their own explanatory comment, so comments are removed before
matching rather than merely avoided.
*/

var (
	goLineComment  = regexp.MustCompile(`(?m)//.*$`)
	goBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

// codeOnly strips Go comments from source text so an assertion can only ever
// match real code.
func codeOnly(src string) string {
	return goLineComment.ReplaceAllString(goBlockComment.ReplaceAllString(src, " "), "")
}

// rawConst returns the contents of a raw-string constant's literal.
func rawConst(t *testing.T, src, name string) string {
	t.Helper()
	decl := "const " + name + " = "
	i := strings.Index(src, decl)
	if i < 0 {
		t.Fatalf("declaration not found: %s", decl)
	}
	rest := src[i+len(decl):]
	if rest[0] != '`' {
		t.Fatalf("%s is not a raw string literal", name)
	}
	end := strings.IndexByte(rest[1:], '`')
	if end < 0 {
		t.Fatalf("unterminated literal for %s", name)
	}
	return rest[1 : 1+end]
}

/*
Search selects the SAME projection the feed selects, and scans it with the
scanner built for it.

The arity of that pair is already pinned (projection_scanner_test.go). Reusing
the pair is what puts this query behind that guard; a private SELECT listing
the same columns by hand is outside it, and the next column added to the
projection turns the search endpoint into a 500 with an arity error naming no
column.
*/
func TestGroupPostSearchReusesTheSharedProjectionAndScanner(t *testing.T) {
	body := codeOnly(funcBody(t, "group_post_search.go", "SearchGroupPostsV2"))

	// Concatenation forms, not bare identifiers: `+name+` cannot occur in prose.
	cols := strings.Index(body, "+groupPostV2ColumnsP+")
	if cols < 0 {
		t.Fatal("SearchGroupPostsV2 does not concatenate groupPostV2ColumnsP — a hand-rolled column list sits outside the arity guard, and the first column added to the shared projection makes this endpoint 500")
	}
	viewer := strings.Index(body, "+viewerEngagementColumns+")
	if viewer < 0 {
		t.Fatal("SearchGroupPostsV2 does not concatenate viewerEngagementColumns — the viewer_* flags would be missing and the scanner's arity would not match")
	}
	if viewer < cols {
		t.Fatal("viewerEngagementColumns is selected BEFORE groupPostV2ColumnsP — scanGroupPostV2WithViewer reads them in the other order, so every row scans the wrong column into the wrong field")
	}
	if !strings.Contains(body, "viewerEngagementJoins(") {
		t.Fatal("SearchGroupPostsV2 selects the viewer_* columns without viewerEngagementJoins — vs/ve/vt are not in scope and the query does not parse")
	}
	if !strings.Contains(body, "scanGroupPostV2WithViewer(") {
		t.Fatal("SearchGroupPostsV2 does not use scanGroupPostV2WithViewer — the scanner paired with this projection")
	}
	// The no-viewer scanner reads three fewer columns. `scanGroupPostV2(` does
	// not match `scanGroupPostV2WithViewer(`, so this is an exact check.
	if strings.Contains(body, "scanGroupPostV2(") {
		t.Fatal("SearchGroupPostsV2 uses scanGroupPostV2, which scans three columns fewer than this projection selects")
	}
	// A hand-rolled projection would spell the columns out.
	for _, literal := range []string{"p.group_id,", "p.author_id,", "p.spark_count,"} {
		if strings.Contains(body, literal) {
			t.Fatalf("SearchGroupPostsV2 spells out %q — the projection must come from the shared constant, not be restated here", literal)
		}
	}
}

// A bare to_tsquery call. The negative class in front is what stops this
// matching websearch_to_tsquery / plainto_tsquery / phraseto_tsquery, and the
// required open paren is what stops it matching prose.
var bareToTsquery = regexp.MustCompile(`(^|[^A-Za-z0-9_])to_tsquery\s*\(`)

/*
No search path calls to_tsquery.

to_tsquery parses an OPERATOR expression, so `book & club` is valid and
`book club` — what a search box actually produces — raises

	syntax error in tsquery: "book club"

which arrives in Go as an ordinary error and leaves the handler with nothing to
return but a 500. A lone `&` or `!` does the same. This covers the new endpoint
AND SearchGroups, which carried the bug from the start and was fixed alongside.
*/
func TestNoSearchPathUsesToTsquery(t *testing.T) {
	for _, file := range []string{"group.go", "group_post_search.go"} {
		src := codeOnly(readFile(t, file))
		if loc := bareToTsquery.FindStringIndex(src); loc != nil {
			line := 1 + strings.Count(src[:loc[0]], "\n")
			t.Errorf("%s calls to_tsquery near line %d of its comment-stripped source — it raises a Postgres syntax error on ordinary multi-word input such as `book club`, which the handler can only turn into a 500. Use websearch_to_tsquery.", file, line)
		}
	}
}

// And the replacement is actually in place, on both paths.
func TestSearchPathsUseWebsearchToTsquery(t *testing.T) {
	q := rawConst(t, readFile(t, "group_post_search.go"), "groupPostSearchQuery")
	if !strings.Contains(q, "websearch_to_tsquery(") {
		t.Errorf("groupPostSearchQuery = %q — it must call websearch_to_tsquery, which never raises on user input", q)
	}
	body := codeOnly(funcBody(t, "group_post_search.go", "SearchGroupPostsV2"))
	if !strings.Contains(body, "+groupPostSearchQuery") {
		t.Error("SearchGroupPostsV2 does not use the groupPostSearchQuery constant, so the constant no longer describes what the query does")
	}

	groups := codeOnly(funcBody(t, "group.go", "SearchGroups"))
	if !strings.Contains(groups, "websearch_to_tsquery('english', $1)") {
		t.Error("SearchGroups no longer calls websearch_to_tsquery — searching for any multi-word group name 500s again")
	}
}

/*
Deleted and pending posts are not searchable.

DeleteGroupPostV2 is a SOFT delete: the row stays with status = 'deleted'. A
search that filtered on anything looser than the feed's own predicate would
serve posts whose authors and moderators believe them gone, and posts still
waiting for a moderator's decision. The assertion is parity with the feed
rather than a value of its own, so the two cannot drift.
*/
func TestGroupPostSearchExcludesDeletedAndPendingPosts(t *testing.T) {
	const published = "p.status = 'published'"

	feed := codeOnly(funcBody(t, "group.go", "ListGroupPostsV2"))
	if !strings.Contains(feed, published) {
		t.Fatalf("the feed no longer filters on %q — this guard compares search against the feed and has lost its reference point", published)
	}

	search := codeOnly(funcBody(t, "group_post_search.go", "SearchGroupPostsV2"))
	if !strings.Contains(search, published) {
		t.Fatalf("SearchGroupPostsV2 does not filter on %q — a soft-deleted post (status = 'deleted') or one awaiting approval would be readable through search although the feed refuses it", published)
	}
}

/*
Nothing in search touches the author.

The row keeps the real author_id on purpose (migration 013). That makes an
author filter, or an author-name match, a direct de-anonymisation route: find
"posts by X", read the ones that came back masked, and the mask is worthless.
The only searchable text is the post's own title and body.
*/
func TestGroupPostSearchNeverMatchesOnTheAuthor(t *testing.T) {
	vector := rawConst(t, readFile(t, "group_post_search.go"), "groupPostSearchVector")
	for _, col := range []string{"p.title", "p.body"} {
		if !strings.Contains(vector, col) {
			t.Errorf("the search vector does not include %s", col)
		}
	}
	if strings.Contains(vector, "author") {
		t.Errorf("the search vector mentions the author: %q — searching by author de-anonymises every anonymous post it returns", vector)
	}

	body := codeOnly(funcBody(t, "group_post_search.go", "SearchGroupPostsV2"))
	if strings.Contains(body, "author") {
		t.Error("SearchGroupPostsV2 mentions the author — there must be no author filter and no author search on this endpoint")
	}
	if strings.Contains(body, "anon_alias") || strings.Contains(body, "AnonAlias") {
		t.Error("SearchGroupPostsV2 refers to the anonymity alias — matching on it would let a member correlate an author's anonymous posts")
	}
}

/*
The index's expression matches the query's, byte for byte.

A GIN expression index is only used when the query's expression is identical to
the indexed one. Get it wrong and nothing fails: the endpoint returns the right
rows, from a sequential scan of group_posts, for ever. This is the only test
that can see that, so it checks both the migration and the fresh-install
schema.
*/
func TestSearchVectorExpressionMatchesTheMigration(t *testing.T) {
	vector := rawConst(t, readFile(t, "group_post_search.go"), "groupPostSearchVector")
	// The index is on the table, so it carries no `p` alias.
	indexed := strings.ReplaceAll(vector, "p.", "")

	mig, err := database.Migrations.ReadFile("migrations/015_group_post_search.sql")
	if err != nil {
		t.Fatalf("migration 015 is missing: %v", err)
	}
	if !strings.Contains(string(mig), indexed) {
		t.Errorf("migration 015 does not index %s — an expression index that does not match the query is silently never used, and in-group search becomes a sequential scan of group_posts per request", indexed)
	}
	if !strings.Contains(string(mig), "CREATE INDEX IF NOT EXISTS") {
		t.Error("migration 015 is not idempotent — re-running it against a patched database fails")
	}

	if !strings.Contains(database.SetupSQL, indexed) {
		t.Errorf("setup.sql does not index %s — a freshly created database gets no search index at all", indexed)
	}
}
