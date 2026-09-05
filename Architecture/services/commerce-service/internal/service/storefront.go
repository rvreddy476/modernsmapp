package service

// The shop front: a product's gallery, a landing page, favourites, and the
// category strip.
//
// ─── WHAT THIS IS FOR ───────────────────────────────────────────────────
//
// The founder opened the shop on a phone and said it "is not looking a proper
// ecommerce application". Two concrete facts were behind that:
//
//   - `product_media` had ZERO rows and no write route that could put one
//     there in display order, so nothing in the catalogue had a picture;
//   - the app's first screen was `GET /v1/commerce/products` — a bare grid of
//     whatever was created last, with no banners, no deals, no categories.
//
// Both are server-side gaps, not client ones. A marketplace home screen is
// merchandising, and merchandising is a server decision the client renders.
//
// ─── THE HYDRATION RULE ─────────────────────────────────────────────────
//
// Every response that carries products resolves its media ids in ONE batch to
// media-service, never per row. `hydrateHome` collects the ids from all of a
// home page's sections AND its banners before making a single call, because a
// page of five sections resolving separately is five sequential HTTP calls on
// the critical path of the app's first screen.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/commerce-service/internal/media"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

var (
	// ErrTooManyMedia means the gallery would exceed postgres.MaxProductMedia.
	ErrTooManyMedia = fmt.Errorf("commerce: a product may carry at most %d images", postgres.MaxProductMedia)
	// ErrNoMedia means the caller sent an empty or all-blank id list to a
	// route whose whole job is to set one.
	ErrNoMedia = errors.New("commerce: no media ids supplied")
	// ErrDuplicateMedia means the same asset appeared twice in one gallery.
	// Silently de-duplicating would reorder the rest of the caller's list.
	ErrDuplicateMedia = errors.New("commerce: the same media appears more than once")
	// ErrMediaNotOnProduct means the id to remove or reorder is not in this
	// product's gallery.
	ErrMediaNotOnProduct = errors.New("commerce: that media is not on this product")
	// ErrProductNotFound is the storefront's 404.
	ErrProductNotFound = errors.New("commerce: no such product")
	// ErrBannerNotFound is the admin banner 404.
	ErrBannerNotFound = errors.New("commerce: no such banner")
	// ErrInvalidBanner means the banner body describes a card that cannot be
	// rendered or cannot be opened.
	ErrInvalidBanner = errors.New("commerce: the banner is not valid")
)

// ─── Product gallery ────────────────────────────────────────────────────

// ProductMediaItem is one gallery entry as a client reads it: the id, its
// place in the order, and the URLs to draw.
type ProductMediaItem struct {
	MediaID      uuid.UUID `json:"media_id"`
	MediaType    string    `json:"media_type"`
	SortOrder    int       `json:"sort_order"`
	ImageURL     string    `json:"image_url,omitempty"`
	ThumbnailURL string    `json:"thumbnail_url,omitempty"`
	Blurhash     *string   `json:"blurhash,omitempty"`
	// IsCover marks the first entry — the image the grid, the cart and the
	// order line all use. Making it explicit means the client does not have
	// to know that "index 0" is the rule.
	IsCover bool `json:"is_cover"`
}

// normaliseMediaIDs validates a submitted gallery and returns it in order.
//
// Pure, so the cap and the duplicate rule are testable without a database or
// a media-service — which matters because these are the two conditions a
// client is most likely to send by accident (a double-tap on "add", a retry
// that re-posts the whole list).
func normaliseMediaIDs(ids []uuid.UUID) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(ids))
	seen := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			continue
		}
		if seen[id] {
			return nil, ErrDuplicateMedia
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, ErrNoMedia
	}
	if len(out) > postgres.MaxProductMedia {
		return nil, ErrTooManyMedia
	}
	return out, nil
}

// SetProductMedia replaces a product's gallery.
//
// Two separate permissions, both required, in this order:
//
//  1. the caller must be the seller who OWNS the product — otherwise any
//     seller could restyle a competitor's listing;
//  2. the caller must own every media asset, which is the check
//     internal/media exists for. Owning the product is not owning the
//     photograph: without (2) a seller reads a competitor's media id out of
//     their public product JSON and hangs it on their own listing.
//
// Media verification happens BEFORE the write, and a single refusal aborts
// the whole batch, so a half-applied gallery is not a state this can produce.
func (s *Service) SetProductMedia(ctx context.Context, productID, actorUserID uuid.UUID, mediaIDs []uuid.UUID) ([]ProductMediaItem, error) {
	ordered, err := normaliseMediaIDs(mediaIDs)
	if err != nil {
		return nil, err
	}
	if err := s.assertProductSeller(ctx, productID, actorUserID); err != nil {
		return nil, err
	}
	ptrs := make([]*uuid.UUID, len(ordered))
	for i := range ordered {
		ptrs[i] = &ordered[i]
	}
	if err := s.verifyMedia(ctx, actorUserID, media.KindImage, ptrs...); err != nil {
		return nil, err
	}
	if err := s.store.SetProductMedia(ctx, productID, ordered); err != nil {
		return nil, err
	}
	return s.ProductMedia(ctx, productID)
}

// ReorderProductMedia changes display order without re-uploading.
//
// The submitted list must be a permutation of the gallery the product already
// has. Accepting a partial list would make "reorder" quietly delete whatever
// the client forgot to send — the failure mode of a client that pages its
// gallery editor.
func (s *Service) ReorderProductMedia(ctx context.Context, productID, actorUserID uuid.UUID, mediaIDs []uuid.UUID) ([]ProductMediaItem, error) {
	ordered, err := normaliseMediaIDs(mediaIDs)
	if err != nil {
		return nil, err
	}
	if err := s.assertProductSeller(ctx, productID, actorUserID); err != nil {
		return nil, err
	}
	existing, err := s.store.ProductMediaIDs(ctx, productID)
	if err != nil {
		return nil, err
	}
	if len(existing) != len(ordered) {
		return nil, ErrMediaNotOnProduct
	}
	have := make(map[uuid.UUID]bool, len(existing))
	for _, id := range existing {
		have[id] = true
	}
	for _, id := range ordered {
		if !have[id] {
			return nil, ErrMediaNotOnProduct
		}
	}
	// No media-service round trip: these ids are already on the product, so
	// they were verified when they were attached. Re-verifying would make
	// dragging a thumbnail depend on media-service being up.
	if err := s.store.SetProductMedia(ctx, productID, ordered); err != nil {
		return nil, err
	}
	return s.ProductMedia(ctx, productID)
}

// DeleteProductMedia removes one asset from a product's gallery.
func (s *Service) DeleteProductMedia(ctx context.Context, productID, actorUserID, mediaID uuid.UUID) ([]ProductMediaItem, error) {
	if err := s.assertProductSeller(ctx, productID, actorUserID); err != nil {
		return nil, err
	}
	removed, err := s.store.DeleteProductMediaByMediaID(ctx, productID, mediaID)
	if err != nil {
		return nil, err
	}
	if !removed {
		return nil, ErrMediaNotOnProduct
	}
	return s.ProductMedia(ctx, productID)
}

// ProductMedia is the public gallery read, with URLs resolved.
//
// One batch call for the whole gallery — a product with eight images used to
// hand the client eight bare UUIDs and nothing else, which is why
// `GET …/media` was a route no screen could use.
func (s *Service) ProductMedia(ctx context.Context, productID uuid.UUID) ([]ProductMediaItem, error) {
	rows, err := s.store.ListProductMedia(ctx, productID)
	if err != nil {
		return nil, err
	}
	out := make([]ProductMediaItem, 0, len(rows))
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.MediaID)
	}
	var resolved map[uuid.UUID]media.Resolved
	if s.media != nil && len(ids) > 0 {
		resolved = s.media.ResolveURLs(ctx, ids)
	}
	for i, r := range rows {
		item := ProductMediaItem{
			MediaID: r.MediaID, MediaType: r.MediaType,
			SortOrder: r.SortOrder, IsCover: i == 0,
		}
		if v, ok := resolved[r.MediaID]; ok {
			item.ImageURL = v.URL()
			item.ThumbnailURL = v.Thumbnail()
			item.Blurhash = v.Blurhash
		}
		out = append(out, item)
	}
	return out, nil
}

// ─── Favourites ─────────────────────────────────────────────────────────

// AddFavourite is idempotent — see the store's ON CONFLICT DO NOTHING and
// migration 024's composite primary key. The product must exist and be live:
// hearting something a seller has withdrawn would put a dead card in the
// shopper's list forever.
func (s *Service) AddFavourite(ctx context.Context, userID, productID uuid.UUID) error {
	p, err := s.store.GetProductByID(ctx, productID)
	if err != nil {
		if errors.Is(err, postgres.ErrProductNotFound) {
			return ErrProductNotFound
		}
		return err
	}
	if p.Status != "active" || p.ApprovalStatus != "approved" {
		return ErrProductNotFound
	}
	return s.store.AddFavourite(ctx, userID, productID)
}

// RemoveFavourite is idempotent in the other direction: removing one that is
// not there succeeds. See the store.
func (s *Service) RemoveFavourite(ctx context.Context, userID, productID uuid.UUID) error {
	return s.store.RemoveFavourite(ctx, userID, productID)
}

// FavouritesPage is the cursor-paged favourites list.
type FavouritesPage struct {
	Items      []*postgres.Product `json:"items"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

// ListFavourites returns the user's hearted products as full summaries, with
// images resolved in one batch.
func (s *Service) ListFavourites(ctx context.Context, userID uuid.UUID, limit int, cursor string) (*FavouritesPage, error) {
	items, next, err := s.store.ListFavourites(ctx, userID, limit, cursor)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []*postgres.Product{}
	}
	s.hydrateProductImages(ctx, items)
	// No favourite lookup needed: every row in this list is, by definition,
	// a favourite. The store sets it.
	return &FavouritesPage{Items: items, NextCursor: next}, nil
}

// MarkFavourites fills `is_favourite` on a page of products in ONE query.
//
// A no-op for an anonymous caller, which leaves the field nil rather than
// false — see the model's note on why those are different answers.
func (s *Service) MarkFavourites(ctx context.Context, userID uuid.UUID, products []*postgres.Product) {
	if userID == uuid.Nil || len(products) == 0 {
		return
	}
	ids := make([]uuid.UUID, 0, len(products))
	for _, p := range products {
		if p != nil {
			ids = append(ids, p.ID)
		}
	}
	set, err := s.store.FavouriteSet(ctx, userID, ids)
	if err != nil {
		// Fail soft, like image hydration: a favourites lookup that fails
		// must leave the grid renderable rather than 500 the browse screen.
		return
	}
	for _, p := range products {
		if p == nil {
			continue
		}
		fav := set[p.ID]
		p.IsFavourite = &fav
	}
}

// ─── Category strip ─────────────────────────────────────────────────────

// ListCategoryCards returns the strip: the taxonomy with live product counts
// and resolved tile images, in one media batch.
func (s *Service) ListCategoryCards(ctx context.Context) ([]*postgres.CategoryCard, error) {
	cards, err := s.store.ListCategoryCards(ctx)
	if err != nil {
		return nil, err
	}
	if cards == nil {
		cards = []*postgres.CategoryCard{}
	}
	ids := make([]uuid.UUID, 0, len(cards))
	for _, c := range cards {
		if c.ImageMediaID != nil {
			ids = append(ids, *c.ImageMediaID)
		}
	}
	if s.media != nil && len(ids) > 0 {
		resolved := s.media.ResolveURLs(ctx, ids)
		for _, c := range cards {
			if c.ImageMediaID == nil {
				continue
			}
			if v, ok := resolved[*c.ImageMediaID]; ok {
				c.ImageURL = v.URL()
				c.ThumbnailURL = v.Thumbnail()
			}
		}
	}
	return cards, nil
}

// ─── The landing page ───────────────────────────────────────────────────

// HomeSection is one horizontal rail.
type HomeSection struct {
	Key      string              `json:"key"`
	Title    string              `json:"title"`
	Products []*postgres.Product `json:"products"`
}

// HomePage is what the app's first screen renders.
type HomePage struct {
	Banners  []*postgres.Banner `json:"banners"`
	Sections []HomeSection      `json:"sections"`
}

// homeSectionSize is how many products a rail carries. Enough to fill a
// phone's horizontal scroll twice over and no more: the home page resolves
// every section's images in one media batch, and media-service caps a batch
// at fifty ids.
const homeSectionSize = 12

// homeBannerLimit bounds the rail. A merchandiser with forty active banners
// is a merchandising problem, not a reason to send forty cards to a phone.
const homeBannerLimit = 10

// bestSellerWindow is how far back "best seller" looks.
const bestSellerWindow = 30 * 24 * time.Hour

// Home builds the landing page.
//
// ─── WHY SECTIONS ARE OMITTED, NEVER EMPTY ──────────────────────────────
//
// A section with no products is not "a section that happens to be empty" —
// it is a heading with a blank strip under it, which on a phone reads as a
// failed load. Dropping the section entirely lets the client render whatever
// it is given, top to bottom, with no emptiness rule of its own to get wrong.
//
// ─── AND WHY BEST SELLERS FALLS BACK ────────────────────────────────────
//
// A marketplace that has not sold anything yet — a fresh install, a dev
// stack, a new region — has no units to rank. Ranking by views instead keeps
// the shape of the page intact while the real signal accumulates; the
// alternative is a home screen that grows a section on the day of its first
// order, which is the least testable possible behaviour.
func (s *Service) Home(ctx context.Context, viewerID uuid.UUID) (*HomePage, error) {
	page := &HomePage{Banners: []*postgres.Banner{}, Sections: []HomeSection{}}

	banners, err := s.store.LiveBanners(ctx, homeBannerLimit)
	if err != nil {
		return nil, err
	}
	if banners != nil {
		page.Banners = banners
	}

	deals, err := s.store.DealProducts(ctx, homeSectionSize)
	if err != nil {
		return nil, err
	}
	best, err := s.store.BestSellerProducts(ctx, bestSellerWindow, homeSectionSize)
	if err != nil {
		return nil, err
	}
	if len(best) == 0 {
		if best, err = s.store.MostViewedProducts(ctx, homeSectionSize); err != nil {
			return nil, err
		}
	}
	fresh, err := s.store.NewArrivalProducts(ctx, homeSectionSize)
	if err != nil {
		return nil, err
	}

	page.Sections = buildHomeSections([]HomeSection{
		{Key: "deals", Title: "Deals of the day", Products: deals},
		{Key: "best_sellers", Title: "Best sellers", Products: best},
		{Key: "new_arrivals", Title: "New arrivals", Products: fresh},
	})

	s.hydrateHome(ctx, viewerID, page)
	return page, nil
}

// buildHomeSections drops the empties. Pure, so the omission rule is testable
// without a database.
func buildHomeSections(in []HomeSection) []HomeSection {
	out := make([]HomeSection, 0, len(in))
	for _, sec := range in {
		if len(sec.Products) == 0 {
			continue
		}
		out = append(out, sec)
	}
	return out
}

// hydrateHome resolves EVERY media id on the page — every section's products
// and every banner — in one batch, then marks favourites in one query.
//
// This is the reason the home page's hydration is not just three calls to
// hydrateProductImages: three sections resolving independently is three
// sequential round trips to media-service on the app's opening screen, and
// products repeat across sections (a discounted best seller is in two of
// them), so a per-section batch also asks for the same id twice.
func (s *Service) hydrateHome(ctx context.Context, viewerID uuid.UUID, page *HomePage) {
	all := make([]*postgres.Product, 0, len(page.Sections)*homeSectionSize)
	for _, sec := range page.Sections {
		all = append(all, sec.Products...)
	}

	ids := make([]uuid.UUID, 0, len(all)+len(page.Banners))
	for _, p := range all {
		if id := productMediaID(p); id != nil {
			ids = append(ids, *id)
		}
	}
	for _, b := range page.Banners {
		if b.ImageMediaID != nil {
			ids = append(ids, *b.ImageMediaID)
		}
	}

	if s.media != nil && len(ids) > 0 {
		resolved := s.media.ResolveURLs(ctx, ids)
		applyResolvedImages(all, resolved)
		for _, b := range page.Banners {
			if b.ImageMediaID == nil {
				continue
			}
			if v, ok := resolved[*b.ImageMediaID]; ok {
				b.ImageURL = v.URL()
			}
		}
	}

	// One favourites query for the whole page, after de-duplication is no
	// longer needed — FavouriteSet takes the ids as an array and the same
	// product appearing in two sections costs nothing.
	s.MarkFavourites(ctx, viewerID, all)
}

// ─── Commerce as a content authority for media-service ──────────────────

// MediaAccess answers media-service's delivery gate for one asset.
//
// See the store's VisibleProductMediaIDs for the rule and for why commerce
// has to answer this at all. In short: media-service asks the service that
// owns the content referencing an asset, that list never included commerce,
// so every product photograph came back denied for every shopper.
func (s *Service) MediaAccess(ctx context.Context, viewerID, mediaID uuid.UUID) (bool, error) {
	set, err := s.store.VisibleProductMediaIDs(ctx, viewerID, []uuid.UUID{mediaID})
	if err != nil {
		return false, err
	}
	return set[mediaID], nil
}

// MediaAccessBatch answers for a whole page in one query — the shape
// media-service's AuthorizeBatch uses so a catalogue page costs one call
// rather than one per tile.
func (s *Service) MediaAccessBatch(ctx context.Context, viewerID uuid.UUID, mediaIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	set, err := s.store.VisibleProductMediaIDs(ctx, viewerID, mediaIDs)
	if err != nil {
		return nil, err
	}
	// Every id asked about must appear in the answer. media-service treats an
	// absent key as "this authority did not resolve it", which would make a
	// definite no look like an outage and keep the asset retryable forever.
	out := make(map[uuid.UUID]bool, len(mediaIDs))
	for _, id := range mediaIDs {
		out[id] = set[id]
	}
	return out, nil
}

// ─── Banner administration ──────────────────────────────────────────────

// BannerInput is the admin write body.
type BannerInput struct {
	ID           *uuid.UUID `json:"id,omitempty"`
	Title        string     `json:"title"`
	Subtitle     *string    `json:"subtitle,omitempty"`
	ImageMediaID *uuid.UUID `json:"image_media_id,omitempty"`
	TargetType   string     `json:"target_type"`
	TargetID     string     `json:"target_id"`
	Position     int        `json:"position"`
	Active       *bool      `json:"active,omitempty"`
	StartsAt     *time.Time `json:"starts_at,omitempty"`
	EndsAt       *time.Time `json:"ends_at,omitempty"`
}

// validate mirrors migration 024's CHECK in Go so the caller gets a sentence
// naming what is wrong rather than a constraint-violation 500.
func (in *BannerInput) validate() error {
	if strings.TrimSpace(in.Title) == "" {
		return fmt.Errorf("%w: title is required", ErrInvalidBanner)
	}
	switch in.TargetType {
	case "category", "product":
		if _, err := uuid.Parse(in.TargetID); err != nil {
			return fmt.Errorf("%w: a %s banner's target_id must be a UUID", ErrInvalidBanner, in.TargetType)
		}
	case "search":
		if strings.TrimSpace(in.TargetID) == "" {
			return fmt.Errorf("%w: a search banner's target_id must be the query text", ErrInvalidBanner)
		}
	default:
		return fmt.Errorf("%w: target_type must be category, product or search", ErrInvalidBanner)
	}
	if in.StartsAt != nil && in.EndsAt != nil && !in.EndsAt.After(*in.StartsAt) {
		return fmt.Errorf("%w: ends_at must be after starts_at", ErrInvalidBanner)
	}
	return nil
}

// SaveBanner creates or updates one banner. Internal-key route only — this is
// merchandising, not a seller capability.
func (s *Service) SaveBanner(ctx context.Context, in BannerInput) (*postgres.Banner, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	active := true
	if in.Active != nil {
		active = *in.Active
	}
	b := &postgres.Banner{
		Title: strings.TrimSpace(in.Title), Subtitle: in.Subtitle,
		ImageMediaID: in.ImageMediaID,
		TargetType:   in.TargetType, TargetID: strings.TrimSpace(in.TargetID),
		Position: in.Position, Active: active,
		StartsAt: in.StartsAt, EndsAt: in.EndsAt,
	}
	if in.ID != nil {
		b.ID = *in.ID
	}
	if err := s.store.UpsertBanner(ctx, b); err != nil {
		return nil, err
	}
	s.hydrateBanner(ctx, b)
	return b, nil
}

// ListBanners is the admin view: every banner, live or not.
func (s *Service) ListBanners(ctx context.Context) ([]*postgres.Banner, error) {
	out, err := s.store.ListAllBanners(ctx)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []*postgres.Banner{}
	}
	ids := make([]uuid.UUID, 0, len(out))
	for _, b := range out {
		if b.ImageMediaID != nil {
			ids = append(ids, *b.ImageMediaID)
		}
	}
	if s.media != nil && len(ids) > 0 {
		resolved := s.media.ResolveURLs(ctx, ids)
		for _, b := range out {
			if b.ImageMediaID == nil {
				continue
			}
			if v, ok := resolved[*b.ImageMediaID]; ok {
				b.ImageURL = v.URL()
			}
		}
	}
	return out, nil
}

// DeleteBanner removes one.
func (s *Service) DeleteBanner(ctx context.Context, id uuid.UUID) error {
	removed, err := s.store.DeleteBanner(ctx, id)
	if err != nil {
		return err
	}
	if !removed {
		return ErrBannerNotFound
	}
	return nil
}

func (s *Service) hydrateBanner(ctx context.Context, b *postgres.Banner) {
	if s.media == nil || b == nil || b.ImageMediaID == nil {
		return
	}
	if v, ok := s.media.ResolveURLs(ctx, []uuid.UUID{*b.ImageMediaID})[*b.ImageMediaID]; ok {
		b.ImageURL = v.URL()
	}
}
