package service

// Product visibility events, and the read-back that answers them.
//
// ─── THE BUG THIS FILE CLOSES ───────────────────────────────────────────
//
// commerce-service published "commerce.product.created". search-service
// listened for events.ProductListed — the literal string "ProductListed".
// The two never matched, so in the entire life of this system not one
// product has reached the search index from a live event. The only thing
// that ever populated products_v1 was an offline backfill
// (search-service cmd/backfill -entity products), which read title,
// description and counters and set NEITHER category NOR price — so even
// the rows that were there could not be filtered or sorted.
//
// The name is only half of it. "created" is the wrong MOMENT. Every create
// path in this service writes status='draft', approval_status='draft'
// (Service.CreateProduct, and the bulk importer) — a product is invisible
// to buyers until a moderator approves it. An index driven by creation
// would have indexed drafts and rejected listings and would never have
// heard about an approved listing being taken down.
//
// So the event is not "a product exists". It is "this listing became
// visible" / "stopped being visible", fired from the transitions that
// actually move the two columns the shopper-facing rule reads.
//
// ─── WHERE THEY FIRE ────────────────────────────────────────────────────
//
// Every service-layer path that can change a product's visibility calls
// publishProductVisibility, and there are five:
//
//	AdminApproveProduct          → active + approved      → published
//	AdminRejectProduct           → approval_status=rejected → unpublished
//	AdminRequestProductChanges   → changes_requested       → unpublished
//	SubmitProduct                → submitted               → unpublished
//	UpdateProduct                → the revalidation bounce (approved listing
//	                               edited substantively: status='draft',
//	                               approval_status='submitted') → unpublished;
//	                               and any other edit to a listing that is
//	                               still live → published, which re-reads the
//	                               document so a retitled listing is
//	                               searchable under its new title.
//
// In the store those five converge on syncOfferLifecycleTx (see
// productoffers.go), which is where a SIXTH transition added tomorrow would
// converge too. This function is the same convergence one layer up: a new
// transition has exactly one thing to remember, and it is the same one.
//
// ─── WHY IT ASKS THE DATABASE WHICH EVENT TO SEND ───────────────────────
//
// publishProductVisibility does not take a boolean. It reads the product's
// lifecycle columns back after the transition committed and lets
// ProductLifecycle.Visible() decide. A caller passing its own idea of the
// resulting state is a caller that can be wrong — and the case where it
// would be wrong is the one that matters: a patch that does NOT trip
// revalidation leaves an approved listing live, and a call site guessing
// "an edit means unpublish" would take every edited listing out of search.

import (
	"context"
	"log/slog"
	"time"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// publishProductVisibility emits ProductPublished or ProductUnpublished for
// one product, choosing between them from the product's state as stored.
//
// Best-effort by design, like every other publish in this service: the
// transition has already committed, and failing an approval because an
// event could not be enqueued would take a successful moderation decision
// away over a projection. The outbox behind s.publish is at-least-once, and
// a genuinely lost event is recovered by the reindex — which exists for
// exactly that, and which is honest about it rather than pretending the
// stream is lossless.
func (s *Service) publishProductVisibility(ctx context.Context, productID uuid.UUID) {
	life, err := s.store.GetProductLifecycle(ctx, productID)
	if err != nil {
		slog.WarnContext(ctx, "commerce: could not resolve product visibility for the search event",
			"product_id", productID, "error", err)
		return
	}
	payload := events.ProductVisibilityPayload{
		ProductID:      life.ProductID.String(),
		SellerID:       life.SellerID.String(),
		Status:         life.Status,
		ApprovalStatus: life.ApprovalStatus,
		OccurredAt:     time.Now().UTC(),
	}
	eventType := events.ProductUnpublished
	if life.Visible() {
		eventType = events.ProductPublished
	}
	// No idempotency key, deliberately — and this is the one place in this
	// service where that is a decision rather than an omission.
	//
	// publishWithIdempotency's UNIQUE index is forever, not per-window. A
	// key of "this product, this resulting state" would let a listing be
	// approved, rejected, and approved again — a completely ordinary
	// moderation sequence — and SILENTLY DROP the second approval, leaving
	// the listing live in the catalogue and absent from search with nothing
	// in either service saying why.
	//
	// A duplicate here costs nothing: both copies trigger the same
	// read-back and write the same document. That is what "already
	// idempotent upstream" means on publishWithIdempotency's doc comment,
	// and it is true of these two events in a way it is not true of a
	// payment capture.
	s.publish(ctx, eventType, payload)
}

// ─── The read-back ──────────────────────────────────────────────────────

// ProductSearchDoc is what search-service fetches after an event wakes it.
// See internal/store/postgres/searchdoc.go for what is in it and why.
func (s *Service) ProductSearchDoc(ctx context.Context, productID uuid.UUID) (*postgres.SearchDoc, error) {
	return s.store.ProductSearchDoc(ctx, productID)
}

// ProductSearchDocPage is one keyset page of the reindex walk.
type ProductSearchDocPage struct {
	Items []*postgres.SearchDoc `json:"items"`
	// NextCursor is empty on the last page. It is opaque to the caller and
	// encodes (created_at, id) — see parseSearchDocCursor.
	NextCursor string `json:"next_cursor,omitempty"`
}

// ListProductSearchDocs walks the catalogue for a reindex.
//
// visibleOnly defaults to true at the HTTP layer: the index holds what
// buyers can see, and a reindex that pulled drafts would put every
// unapproved listing into search — which is the failure the created-event
// would have produced, arriving by another door.
func (s *Service) ListProductSearchDocs(
	ctx context.Context, visibleOnly bool, cursor string, limit int,
) (*ProductSearchDocPage, error) {
	afterAt, afterID, err := parseSearchDocCursor(cursor)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	items, err := s.store.ListProductSearchDocs(ctx, visibleOnly, afterAt, afterID, limit)
	if err != nil {
		return nil, err
	}
	page := &ProductSearchDocPage{Items: items}
	if len(items) == limit {
		last := items[len(items)-1]
		page.NextCursor = formatSearchDocCursor(last.CreatedAt, last.ProductID)
	}
	return page, nil
}

// CountVisibleProducts is the figure a reindex reports against — how many
// live listings the catalogue holds, so "indexed N" can be read as complete
// or not.
func (s *Service) CountVisibleProducts(ctx context.Context) (int, error) {
	return s.store.CountVisibleProducts(ctx)
}

// FacetDefinitions returns the filterable attribute definitions, with
// options, that a facet response is built from.
func (s *Service) FacetDefinitions(ctx context.Context) ([]*postgres.FacetDefinition, error) {
	return s.store.FacetDefinitions(ctx)
}

// ─── Cursor ─────────────────────────────────────────────────────────────

// searchDocCursorSep separates the two halves. A '|' cannot occur in either
// an RFC3339 timestamp or a UUID, so no escaping is needed and a malformed
// cursor is detectable rather than silently truncating the walk.
const searchDocCursorSep = "|"

func formatSearchDocCursor(at time.Time, id uuid.UUID) string {
	return at.UTC().Format(time.RFC3339Nano) + searchDocCursorSep + id.String()
}

// parseSearchDocCursor returns (nil, nil, nil) for an empty cursor — the
// first page — and an error for a malformed one.
//
// An error rather than "start from the beginning": a client paging through
// a reindex with a corrupted cursor would silently restart and re-index the
// front of the catalogue forever, which looks like progress and is not.
func parseSearchDocCursor(cursor string) (*time.Time, *uuid.UUID, error) {
	if cursor == "" {
		return nil, nil, nil
	}
	idx := indexOfSep(cursor)
	if idx < 0 {
		return nil, nil, ErrInvalidSearchDocCursor
	}
	at, err := time.Parse(time.RFC3339Nano, cursor[:idx])
	if err != nil {
		return nil, nil, ErrInvalidSearchDocCursor
	}
	id, err := uuid.Parse(cursor[idx+1:])
	if err != nil {
		return nil, nil, ErrInvalidSearchDocCursor
	}
	return &at, &id, nil
}

func indexOfSep(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == searchDocCursorSep[0] {
			return i
		}
	}
	return -1
}
