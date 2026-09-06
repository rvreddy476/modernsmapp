package reindex

// Rebuilding the product index from commerce-service.
//
// ─── WHY THIS EXISTS ALONGSIDE THE EVENT PATH ───────────────────────────
//
// The event path (commerce.product.published / .unpublished → read-back →
// index) keeps the index current from now on. It cannot populate it from
// the past: every listing approved before that path existed produced no
// event anybody kept, and the index has to start from somewhere.
//
// It is also the answer to a class of failure the event path cannot cover
// on its own — an OpenSearch wipe, a Kafka retention window passed while
// search was down, a mapping change that needs the documents rewritten.
// ReindexUsers exists for exactly the same reasons; this is the same job
// for the catalogue.
//
// ─── WHY IT WALKS COMMERCE AND NOT THE DATABASE ─────────────────────────
//
// cmd/backfill reads commerce's Postgres directly, and that is precisely
// why it set neither category nor price: it hand-wrote a SELECT over
// `products`, and the price lives on the variants and the category chain
// needs a recursive walk. Reproducing commerce's projection in a second
// service is how the two drift, and this one had drifted before it was
// ever run.
//
// So this walks commerce's own /search-docs endpoint and indexes exactly
// what the event path indexes, through exactly the same conversion
// (productindex.Doc). A document written by a reindex and a document
// written by an approval are byte-identical, which is what makes running a
// reindex safe on a live index rather than something to schedule at night.

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/search-service/internal/commerceclient"
	"github.com/atpost/search-service/internal/productindex"
	"github.com/atpost/search-service/internal/store/search"
)

// productPageSize is the keyset page size for the walk.
const productPageSize = 200

// ProductsResult summarizes a run.
//
// Fetched and Indexed are reported separately, and CatalogueTotal beside
// them, because "indexed 4213" alone cannot be checked. Indexed < Fetched
// means OpenSearch rejected documents; Fetched < CatalogueTotal means the
// walk stopped early. Both are silent in a single number.
type ProductsResult struct {
	Fetched        int    `json:"fetched"`
	Indexed        int    `json:"indexed"`
	CatalogueTotal int    `json:"catalogue_total"`
	Pages          int    `json:"pages"`
	AliasTarget    string `json:"alias_target"`
	Duration       string `json:"duration"`
}

// ReindexProducts walks commerce's live catalogue and bulk-indexes it
// through the products alias.
//
// It does NOT move the alias and does not delete anything. A reindex that
// also flipped the alias would make "rebuild the index" and "change which
// index is live" the same button, and the second is an operator decision
// with a rollback attached — see Store.MoveProductsAlias.
//
// Documents already in the index and no longer in the catalogue are left
// alone rather than swept: an unpublished listing is removed by its own
// unpublish event, and a sweep here would need to hold the whole live id
// set in memory to be safe. When a clean rebuild is wanted, the honest way
// is a new index and an alias move — which is what this step's alias is for.
func ReindexProducts(
	ctx context.Context,
	client *commerceclient.Client,
	store *search.Store,
	log *slog.Logger,
) (ProductsResult, error) {
	started := time.Now()
	var res ProductsResult
	if client == nil || !client.Configured() {
		return res, fmt.Errorf("reindex: COMMERCE_SERVICE_URL not configured")
	}
	if target, err := store.ProductsAliasTarget(ctx); err == nil {
		res.AliasTarget = target
	}

	cursor := ""
	for {
		page, err := client.ListProductSearchDocs(ctx, cursor, productPageSize)
		if err != nil {
			return res, fmt.Errorf("reindex: fetch product page (cursor %q): %w", cursor, err)
		}
		res.Pages++
		res.CatalogueTotal = page.VisibleTotal
		if len(page.Items) == 0 {
			break
		}
		res.Fetched += len(page.Items)

		docs := make([]search.ProductV2Doc, 0, len(page.Items))
		for _, item := range page.Items {
			// The endpoint already filters to visible listings, but the
			// document carries the flag and it costs nothing to believe it.
			// A drafts-in-search bug is not worth saving a boolean test.
			if !item.Visible {
				continue
			}
			docs = append(docs, productindex.Doc(item))
		}
		n, err := store.BulkIndexProductV2(ctx, docs)
		if err != nil {
			// Log and continue: a partial reindex is better than none, and
			// the count reported at the end tells the truth about what
			// landed. Returning here would leave the operator with neither
			// the index nor a number.
			log.Warn("reindex: bulk index page failed", "cursor", cursor, "err", err)
		}
		res.Indexed += n

		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}

	// Make the writes searchable before returning, so the count this
	// reports can actually be verified against a query. Without it a
	// caller who checks immediately sees the pre-reindex count and
	// concludes the run did nothing.
	if err := store.RefreshProducts(ctx); err != nil {
		log.Warn("reindex: products refresh failed; documents will become searchable shortly", "err", err)
	}

	res.Duration = time.Since(started).Round(time.Millisecond).String()
	log.Info("reindex: products complete",
		"fetched", res.Fetched, "indexed", res.Indexed,
		"catalogue_total", res.CatalogueTotal, "pages", res.Pages,
		"alias_target", res.AliasTarget, "duration", res.Duration)
	return res, nil
}
