package media

// Who is looking.
//
// ─── THE DEFECT ─────────────────────────────────────────────────────────
//
// ResolveURLs sent media-service the internal service key and NOTHING else —
// no `X-User-Id`. media-service reads the viewer from that header
// (`deliveryViewer`), so every batch commerce ever made arrived as the
// all-zeroes anonymous viewer, and its delivery gate refused every protected
// asset:
//
//	media batch: asset denied for viewer viewer_id=00000000-0000-…
//
// The result was an empty `variants` map for every product, which the
// fail-soft resolver correctly turned into "no image URL" — so the catalogue
// rendered as grey boxes and every layer looked innocent. commerce logged
// nothing (a denial is a valid answer), media-service logged a denial for a
// viewer nobody recognised, and the phone showed placeholders.
//
// ─── WHY A CONTEXT VALUE AND NOT A PARAMETER ────────────────────────────
//
// The viewer is request-scoped identity that only ONE leaf consumer needs:
// the media resolver, on the read path, for a decoration. Threading it
// through twelve service signatures — ListProducts, ListProductsFiltered,
// GetProduct, ListSellerProducts, CartView, GetOrderDetail and the rest —
// would put a parameter on several money paths that have no business
// knowing about image rendering, and every future read surface would have to
// remember to pass it. One middleware at the edge, one reader in the client,
// and no surface can forget.
//
// The value is used ONLY to ask media-service what this viewer may see. It
// authorises nothing here: every commerce authorisation decision still reads
// the header directly, at the handler, as it always has.

import (
	"context"

	"github.com/google/uuid"
)

type viewerKey struct{}

// WithViewer records who a request is being served for.
//
// uuid.Nil is a legitimate value and means "anonymous" — a shopper who has
// not signed in is a real audience for a product photograph, because a
// product page is public.
func WithViewer(ctx context.Context, viewerID uuid.UUID) context.Context {
	return context.WithValue(ctx, viewerKey{}, viewerID)
}

// ViewerFrom returns the viewer recorded by WithViewer, or uuid.Nil.
func ViewerFrom(ctx context.Context) uuid.UUID {
	if id, ok := ctx.Value(viewerKey{}).(uuid.UUID); ok {
		return id
	}
	return uuid.Nil
}
