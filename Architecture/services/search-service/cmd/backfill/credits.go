package main

// Video credits on the posts rebuild (2026-10-23).
//
// The rebuild replaces each document whole, so it must carry the cast &
// crew fields the live events put on a long video (credit_names,
// credit_roles, genres, release_year, production_house) or a reindex wipes
// them. They come from post-service migration 070: the post_credits table
// and three posts columns. Each source is presence-checked like the MTube
// filter columns (videoFilterExprs), so the run still works on a database
// without the migration and simply indexes no credits there.

import (
	"context"
	"log/slog"
	"strings"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/jackc/pgx/v5/pgxpool"
)

// creditSelect is the five SELECT expressions, in scan order.
type creditSelect struct {
	Names, Roles, Genres, Year, House string
}

// creditExprsFor builds the expressions from what the database has. Pure,
// for the tests. Names and roles are aggregated in credit order (position)
// so the two arrays stay parallel, as on the events.
func creditExprsFor(hasCredits, hasGenres, hasYear, hasHouse bool) creditSelect {
	out := creditSelect{
		Names:  `ARRAY[]::text[]`,
		Roles:  `ARRAY[]::text[]`,
		Genres: `ARRAY[]::text[]`,
		Year:   `0::int`,
		House:  `''`,
	}
	if hasCredits {
		out.Names = `COALESCE((SELECT array_agg(pc.name ORDER BY pc.position) FROM post_credits pc WHERE pc.post_id = p.id), ARRAY[]::text[])`
		out.Roles = `COALESCE((SELECT array_agg(pc.role ORDER BY pc.position) FROM post_credits pc WHERE pc.post_id = p.id), ARRAY[]::text[])`
	}
	if hasGenres {
		out.Genres = `COALESCE(p.genres, ARRAY[]::text[])`
	}
	if hasYear {
		out.Year = `COALESCE(p.release_year, 0)::int`
	}
	if hasHouse {
		out.House = `COALESCE(p.production_house, '')`
	}
	return out
}

// creditExprs detects migration 070 and returns the expressions.
func creditExprs(ctx context.Context, pool *pgxpool.Pool) creditSelect {
	hasCredits := tableExists(ctx, pool, "post_credits")
	hasGenres := columnExists(ctx, pool, "posts", "genres")
	hasYear := columnExists(ctx, pool, "posts", "release_year")
	hasHouse := columnExists(ctx, pool, "posts", "production_house")
	if !hasCredits || !hasGenres || !hasYear || !hasHouse {
		slog.Warn("backfill posts: video credits not (fully) on this database (post-service migration 070); missing parts indexed empty",
			"post_credits", hasCredits, "genres", hasGenres, "release_year", hasYear, "production_house", hasHouse)
	}
	return creditExprsFor(hasCredits, hasGenres, hasYear, hasHouse)
}

// creditRow is what one scanned row carries.
type creditRow struct {
	Names, Roles, Genres []string
	Year                 int
	House                string
}

// applyCredits puts the credit fields on a rebuilt document, for a long
// video only — the same rule as the live consumer (internal/events
// credits.go), so a rebuild can never index credits the live path would not.
func applyCredits(doc *search.PostDoc, contentType string, r creditRow) {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "long_video", "video":
	default:
		return
	}
	if len(r.Names) > 0 {
		doc.CreditNames = r.Names
	}
	if len(r.Roles) > 0 {
		doc.CreditRoles = r.Roles
	}
	if len(r.Genres) > 0 {
		doc.Genres = r.Genres
	}
	doc.ReleaseYear = r.Year
	doc.ProductionHouse = r.House
}
