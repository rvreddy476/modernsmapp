package main

import (
	"os"
	"strings"
	"testing"

	"github.com/atpost/search-service/internal/store/search"
)

// Video credits on the posts rebuild (2026-10-23, credits.go): a rebuilt
// long video keeps the credit fields the live events gave it; nothing else
// gains them; a database without migration 070 never references a column
// it does not have.

func TestCreditExprsFor_DegradesPerSource(t *testing.T) {
	none := creditExprsFor(false, false, false, false)
	for _, expr := range []string{none.Names, none.Roles, none.Genres, none.Year, none.House} {
		if strings.Contains(expr, "post_credits") || strings.Contains(expr, "p.") {
			t.Fatalf("pre-070 expression %q references a source that is not there (a parse error, not a fallback)", expr)
		}
	}
	all := creditExprsFor(true, true, true, true)
	for name, want := range map[string]string{
		all.Names:  "array_agg(pc.name ORDER BY pc.position)",
		all.Roles:  "array_agg(pc.role ORDER BY pc.position)",
		all.Genres: "p.genres",
		all.Year:   "p.release_year",
		all.House:  "p.production_house",
	} {
		if !strings.Contains(name, want) {
			t.Fatalf("expression %q lacks %q", name, want)
		}
	}
}

func TestApplyCredits_LongVideosOnly(t *testing.T) {
	row := creditRow{Names: []string{"Asha Rao"}, Roles: []string{"Director"}, Genres: []string{"drama"}, Year: 2024, House: "Blue Hour Films"}
	for _, ct := range []string{"long_video", "video"} {
		var doc search.PostDoc
		applyCredits(&doc, ct, row)
		if len(doc.CreditNames) != 1 || doc.ReleaseYear != 2024 || doc.ProductionHouse != "Blue Hour Films" || len(doc.Genres) != 1 {
			t.Fatalf("%s: credits not rebuilt: %+v", ct, doc)
		}
	}
	for _, ct := range []string{"flick", "post", ""} {
		var doc search.PostDoc
		applyCredits(&doc, ct, row)
		if doc.CreditNames != nil || doc.Genres != nil || doc.ReleaseYear != 0 || doc.ProductionHouse != "" {
			t.Fatalf("%s: a non-long-video gained credits on rebuild: %+v", ct, doc)
		}
	}
}

// Source guard: the posts SELECT scans the five credit expressions.
func TestBackfillPosts_SelectsCredits(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	for _, want := range []string{"creditExprs(ctx, pool)", "credits.Names", "credits.House", "applyCredits(&doc, contentType, credit)"} {
		if !strings.Contains(code, want) {
			t.Fatalf("backfillPosts no longer carries video credits (%s missing); a reindex would wipe them", want)
		}
	}
}
