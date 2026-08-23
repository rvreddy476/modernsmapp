package postgres

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Slice C / C-P0-4 — which media may be reclaimed, and which must never be.
//
// # WHY THE PREVIOUS VERSION WAS UNSAFE
//
// It declared a short list of FOREIGN KEYS into `media_assets` and swept every
// old asset that matched none of them. Two independent problems, both of which
// end in deleting content somebody is still using:
//
//  1. This is one shared `app` database. Most services reference media by a
//     plain UUID column with NO foreign key — `users.avatar_media_id`,
//     `channels.banner_media_id`, `posts.cover_media_id`, `stories.media_id`,
//     `portfolio_items.media_id`, and UUID ARRAYS elsewhere. A foreign-key walk
//     cannot see any of them, so the guard's "a new reference authority cannot
//     appear silently" claim was false.
//  2. The sweep had no notion of WHOSE upload it was. It was described as a
//     collector for abandoned composer uploads and was in fact a global
//     collector for every old, apparently-unreferenced ready asset.
//
// # WHAT REPLACES IT
//
// Three mechanisms, together:
//
//   - a DECLARED classification of every media reference in the shared schema,
//     live or derived, including non-FK columns and arrays;
//   - RUNTIME RESOLUTION against the live catalog, so the predicate only names
//     tables that actually exist in this deployment (services are optional) and
//     so a column that exists but is NOT classified makes the sweep REFUSE TO
//     RUN rather than delete something unknown;
//   - a COMPOSER LEASE (`media_assets.upload_purpose`), so confirmed-media
//     reclamation is scoped to assets this product created and abandoned,
//     never to an avatar or a channel banner that merely looks unreferenced.
//
// Retaining a blob costs storage. Deleting a live avatar is irreversible.

// MediaReference is one table/column that can hold a live claim on media.
type MediaReference struct {
	Table  string
	Column string
	// Array is true when Column is UUID[] and must be matched with `= ANY(...)`.
	Array bool
	// Predicate optionally restricts which rows COUNT as live. Drafts use it: a
	// soft-deleted draft keeps its rows but is not a live claim.
	Predicate string
	// Why records what breaks if this is dropped. Reviewable by construction.
	Why string
}

// LiveMediaReferences is every canonical claim that blocks reclamation.
//
// Tables absent from a given deployment are skipped at resolution time; a
// column present but unclassified stops the sweep entirely.
var LiveMediaReferences = []MediaReference{
	// ── post-service ────────────────────────────────────────────────────
	{
		Table: "post_media", Column: "media_id",
		Why: "published post attachments; deleting one blanks an image in a live post",
	},
	{
		Table: "post_draft_media", Column: "media_id",
		Predicate: "EXISTS (SELECT 1 FROM post_drafts d " +
			"WHERE d.id = post_draft_media.draft_id AND d.status <> 'deleted')",
		Why: "unpublished drafts the user still intends to post",
	},
	{
		Table: "posts", Column: "cover_media_id",
		Why: "post/video cover frames; the post survives but loses its cover",
	},
	{
		Table: "stories", Column: "media_id",
		Why: "canonical story media (post-service migration 032)",
	},
	{
		Table: "video_metadata", Column: "media_asset_id",
		Why: "the canonical video record behind a PostTube/Flick post",
	},
	{
		Table: "reel_drafts", Column: "media_id",
		Why: "an unpublished reel's source asset",
	},
	{
		Table: "reel_drafts", Column: "cover_media_id",
		Why: "an unpublished reel's chosen cover frame",
	},

	// ── media-service ───────────────────────────────────────────────────
	{
		Table: "owner_media_slots", Column: "media_asset_id",
		// The dangerous one: ON DELETE CASCADE, so a wrong decision here is
		// SILENT. The avatar disappears and no constraint objects.
		Why: "profile avatars/covers and other owner slots — ON DELETE CASCADE, " +
			"so omitting this deletes a live avatar with no error at all",
	},
	{
		Table: "media_chat_attachment_reservations", Column: "media_id",
		Why: "media reserved for a chat message; reclaiming breaks a sent attachment",
	},
	{
		Table: "owner_media_resolved", Column: "media_asset_id",
		// The READ side of owner_media_slots: the denormalised row every
		// surface actually renders an avatar or cover from. Classifying only
		// the slot table would leave this one unclassified, and the sweep
		// would refuse — which is what the live catalog did.
		Why: "the resolved avatar/cover projection surfaces render from",
	},

	// ── other products that reference media by a plain UUID column ──────
	//
	// None of these has a foreign key. They were found by running the
	// resolution against the REAL schema, which is the only reason they are
	// here: reading the migrations of one service could not have found them.
	{
		Table: "events", Column: "cover_media_id",
		Why: "an event's cover image; reclaiming it blanks a live event page",
	},
	{
		Table: "video_series", Column: "cover_media_id",
		Why: "a series cover; reclaiming it blanks the series everywhere it is listed",
	},
	{
		Table: "audio_tracks", Column: "source_media_id",
		Why: "the asset an audio track was extracted from",
	},
	{
		Table: "audio_tracks", Column: "media_id",
		Why: "the audio asset a track plays; added by ALTER, so it carries no FK",
	},
	{
		// MOVED FROM `derived` — C-P0-4. media_clips carries a live `post_id`
		// and the ordered trim ranges that ARE the Flick edit/playback plan
		// (`clips.go` reads them as canonical). Deleting the parent asset
		// destroys a published edit, so this is a live claim, not a by-product.
		Table: "media_clips", Column: "media_asset_id",
		Why: "the canonical Flick edit/playback plan; cascades, so losing it " +
			"silently destroys a published edit",
	},

	// ── identity / social surfaces (no FK; plain UUID columns) ───────────
	{
		Table: "users", Column: "avatar_media_id",
		Why: "a user's current avatar",
	},
	{
		Table: "users", Column: "cover_media_id",
		Why: "a user's current profile cover",
	},
	{
		Table: "channels", Column: "avatar_media_id",
		Why: "a channel's avatar",
	},
	{
		Table: "channels", Column: "banner_media_id",
		Why: "a channel's banner",
	},
	{
		Table: "portfolio_items", Column: "media_id",
		Why: "a portfolio entry's image",
	},
	{
		Table: "business_pages", Column: "avatar_media_id",
		// TEXT, not UUID, in this schema. Resolution casts it.
		Why: "a business page avatar",
	},
	{
		Table: "business_pages", Column: "cover_media_id",
		Why: "a business page cover",
	},
}

// DerivedMediaTables reference media_assets but are NOT independent claims:
// every row exists only because the asset does and is removed with it.
//
// `media_clips` is deliberately NOT here any more — see above.
var DerivedMediaTables = []string{
	"media_variants",     // encoded renditions of the same asset
	"media_renditions",   // ditto, older name
	"media_subtitles",    // generated captions; asset-dependent
	"media_caption_jobs", // caption work records
	"transcoding_jobs",   // processing work records
	"resumable_uploads",  // in-flight upload bookkeeping
	"media_blob_reclaim", // the reclaim ledger itself

	// media-service's own event plumbing. These are records ABOUT an asset,
	// not claims ON it: the outbox row is this service's copy of "something
	// happened", and the inbox row is its record of having applied a transcode
	// result. Neither is a surface anyone sees, and both are removed with the
	// asset in the same transaction.
	//
	// Classified as derived rather than live deliberately. Treating the outbox
	// as a live reference would mean any retained publication record pins its
	// asset forever, which turns an unrelated retention decision into an
	// unbounded storage cost.
	"media_event_outbox",    // this service's own published-event ledger
	"media_transcode_inbox", // its record of applied transcode outcomes
}

// UploadPurposeComposer is the lease written by the social composer's `init`.
//
// Recorded on every composer upload and still the intended scope for confirmed
// reclamation — but confirmed reclamation itself is DISABLED at launch
// (C-CLB-1, see ErrMediaConfirmed). The lease keeps being written so the data
// needed by a future safe sweeper accumulates from day one rather than starting
// empty on the day someone builds it.
const UploadPurposeComposer = "composer"

// ProcessingStatusPendingUpload is the only reclaimable state — Slice C,
// C-CLB-1.
//
// An asset in this state has a row and no bytes: `init` created it, the
// presigned PUT never completed, and `confirm` never ran. Nothing can be
// referencing it, because nothing can reference an asset that was never
// confirmed, so reclaiming it races with nobody.
//
// Every other state means the upload finished, and a finished asset can be
// claimed by writers that do not serialize against the reclaim transaction.
// See ErrMediaConfirmed.
const ProcessingStatusPendingUpload = "pending_upload"

// ── Runtime resolution ──────────────────────────────────────────────────

// ErrUnclassifiedMediaReference means the live schema holds a media-referencing
// column that reclaim_policy.go does not classify.
//
// The sweep REFUSES rather than guessing. A column nobody has classified is, by
// definition, one nobody has decided is safe to ignore.
type ErrUnclassifiedMediaReference struct{ Columns []string }

func (e ErrUnclassifiedMediaReference) Error() string {
	return "media reclamation refused: unclassified media reference(s) " +
		strings.Join(e.Columns, ", ") +
		" — classify them in reclaim_policy.go before any sweep runs"
}

// resolvedReference is a live reference confirmed to exist in this database.
type resolvedReference struct {
	ref    MediaReference
	isUUID bool
}

// ResolveLiveReferences checks the live catalog and returns the references that
// exist here, or an error naming anything unclassified.
//
// Both halves matter. Skipping absent tables means a deployment without, say,
// channel-service can still sweep. Refusing on unclassified columns means a new
// migration that adds `something.media_id` stops reclamation instead of
// silently having its rows deleted.
func ResolveLiveReferences(ctx context.Context, q Querier) ([]resolvedReference, error) {
	present, err := mediaReferencingColumns(ctx, q)
	if err != nil {
		return nil, err
	}

	classified := map[string]bool{}
	for _, ref := range LiveMediaReferences {
		classified[ref.Table+"."+ref.Column] = true
	}
	derived := map[string]bool{}
	for _, t := range DerivedMediaTables {
		derived[t] = true
	}

	var unknown []string
	for key, col := range present {
		if classified[key] || derived[col.table] {
			continue
		}
		unknown = append(unknown, key+" ("+col.udt+")")
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, ErrUnclassifiedMediaReference{Columns: unknown}
	}

	var out []resolvedReference
	for _, ref := range LiveMediaReferences {
		col, ok := present[ref.Table+"."+ref.Column]
		if !ok {
			// Not deployed here. Skipping is safe: a table that does not exist
			// holds no claims.
			continue
		}
		out = append(out, resolvedReference{ref: ref, isUUID: col.udt == "uuid" || col.udt == "_uuid"})
	}
	return out, nil
}

type catalogColumn struct {
	table string
	udt   string
}

// mediaReferencingColumns finds every column that could hold a media id.
//
// Matched by NAME as well as type, because these references carry no foreign
// key. A UUID column called `media_id` is a media reference whether or not the
// schema says so, and that is exactly the class the FK-only walk missed.
func mediaReferencingColumns(ctx context.Context, q Querier) (map[string]catalogColumn, error) {
	rows, err := q.Query(ctx, `
		SELECT table_name, column_name, udt_name
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name <> 'media_assets'
		  AND (
		        column_name ~ '(^|_)media_id$'
		     OR column_name ~ '(^|_)media_ids$'
		     OR column_name ~ '(^|_)media_asset_id$'
		     OR column_name ~ '(^|_)media_asset_ids$'
		  )`)
	if err != nil {
		return nil, fmt.Errorf("scan media-referencing columns: %w", err)
	}
	defer rows.Close()

	out := map[string]catalogColumn{}
	for rows.Next() {
		var table, column, udt string
		if err := rows.Scan(&table, &column, &udt); err != nil {
			return nil, err
		}
		out[table+"."+column] = catalogColumn{table: table, udt: udt}
	}
	return out, rows.Err()
}

// liveReferenceSQL builds the OR-ed EXISTS predicate from resolved references.
//
// Built once and shared by the candidate scan and the atomic delete, so the
// query that FINDS orphans and the query that CONFIRMS them cannot disagree.
func liveReferenceSQL(refs []resolvedReference, param string) string {
	if len(refs) == 0 {
		// No known claim can exist. `false` would mean "reclaim everything",
		// which is the opposite of safe, so this returns TRUE — nothing is
		// reclaimable until at least one reference table is resolvable.
		return "TRUE"
	}
	parts := make([]string, 0, len(refs))
	for _, r := range refs {
		qualified := r.ref.Table + "." + r.ref.Column
		var match string
		switch {
		case r.ref.Array:
			match = param + " = ANY(" + qualified + ")"
		case r.isUUID:
			match = qualified + " = " + param
		default:
			// TEXT columns holding a media id (business_pages) need a cast, and
			// a malformed value must not abort the whole sweep.
			match = qualified + " = " + param + "::text"
		}
		clause := "EXISTS (SELECT 1 FROM " + r.ref.Table + " WHERE " + match
		if r.ref.Predicate != "" {
			clause += " AND " + r.ref.Predicate
		}
		out := clause + ")"
		parts = append(parts, out)
	}
	return strings.Join(parts, "\n            OR ")
}

// LiveReferenceSQLForTest exposes the composed predicate to tests.
func LiveReferenceSQLForTest(refs []resolvedReference, param string) string {
	return liveReferenceSQL(refs, param)
}

// Querier is the read surface ResolveLiveReferences needs.
//
// An interface rather than *pgxpool.Pool so the resolution can also run inside
// the transaction that performs the delete — the check and the delete must see
// the same catalog.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}
