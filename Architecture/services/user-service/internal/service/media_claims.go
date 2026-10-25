package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/user-service/internal/claimsync"
	"github.com/atpost/user-service/internal/mediaclient"
	"github.com/atpost/user-service/internal/store"
	"github.com/google/uuid"
)

// Media authority and media claims for the rows user-service writes media
// ids into: channels (avatar, banner), portfolio_items (media) and
// business_pages (avatar, cover).
//
// # The holes
//
//  1. Nothing asked whether an attached asset existed, whose it was, or
//     whether its bytes had arrived. Any id lifted from a public payload
//     could be shown as someone's channel picture or page cover, and a
//     `pending_upload` id could be swept by media-service's orphan reclaim.
//  2. Nothing stopped the uploader deleting the asset afterwards
//     (DELETE /v1/media/{id}): media-service cannot see these rows, so the
//     channel, item or page was left naming an asset that no longer exists.
//
// # The rule (channel-service's update_media.go, post-service's create rule)
//
// Every id a write ADDS must be the actor's own upload, CONFIRMED
// (`uploaded`, `processing` or `ready`) and not refused by moderation. An id
// the row already holds is not re-checked: it attaches nothing new.
//
// # The protocol (media-service internal/store/postgres/media_claims.go)
//
//  1. CLAIM every id a write adds, before the write; media-service locks the
//     asset while it records the claim, so a racing owner delete either lands
//     first (the claim is refused, nothing is written) or waits and is refused.
//  2. WRITE the row. If the write fails, RELEASE exactly what step 1 claimed.
//  3. After a successful write, RELEASE every id the row no longer holds.
//
// Every step is ordered so that a failure leaves an EXTRA claim (the owner is
// refused a delete until the repair runs), never a MISSING one (the defect).
// A failed release is queued in media_claim_pending; deletes queue their
// release in their own transaction. DrainMediaClaims and cmd/media-claims
// settle the queue.
//
// Claimant "user-service"; kinds in mediaclient (channel_avatar,
// channel_banner, portfolio_item, business_page_avatar, business_page_cover);
// ref is always the holding row's id.
//
// users.avatar_media_id / cover_media_id are NOT claimed here: their only
// writer is profile-service (PUT /v1/users/me answers 410, see
// retired_profile_routes.go); this service mirrors them into its projection.
//
// # Fail closed
//
// No client, an unreachable media-service or an unreadable answer refuses a
// write that ADDS media with ErrMediaUnavailable (503). A write that only
// removes media is never refused for that reason; its release is best effort.

var (
	// ErrMediaNotFound: media-service has no such asset (or will not admit it).
	ErrMediaNotFound = errors.New("media does not exist")
	// ErrMediaNotOwned is worded to say nothing about the foreign asset.
	ErrMediaNotOwned = errors.New("media cannot be attached by this user")
	// ErrMediaNotReady: the upload has not been confirmed, failed, or was
	// refused by moderation.
	ErrMediaNotReady = errors.New("media has not finished uploading")
	// ErrMediaUnavailable: the check or the claim could not be made. Retryable.
	ErrMediaUnavailable = errors.New("media-service is unavailable")
	// ErrMediaInvalid: a business page media field that is not a media id.
	ErrMediaInvalid = errors.New("media id is not valid")
	// ErrPageDisabled: media cannot be added to a disabled (terminal) page.
	ErrPageDisabled = errors.New("page is disabled")
)

// MediaLookup is what the attach gate needs from media-service.
type MediaLookup interface {
	Get(ctx context.Context, mediaID uuid.UUID) (*mediaclient.Asset, error)
}

// MediaClaims is media-service's claim API (mediaclient.Client).
type MediaClaims = claimsync.Claims

// mediaStore is every store call the media-writing paths make, so the
// protocol is tested end to end against an in-memory fake (store.Store in
// production).
type mediaStore interface {
	LockMediaHolder(ctx context.Context, ref uuid.UUID) (func(), error)
	MediaClaimRowFor(ctx context.Context, kind string, ref uuid.UUID) (store.MediaClaimRow, error)
	MarkMediaClaimHolderPending(ctx context.Context, kind string, ref uuid.UUID) error
	PendingMediaClaimHolders(ctx context.Context, limit int) ([]store.PendingMediaClaimHolder, error)
	ClearMediaClaimHolderPending(ctx context.Context, p store.PendingMediaClaimHolder) error

	CreateChannel(ctx context.Context, ch *store.Channel) error
	GetChannelByID(ctx context.Context, id uuid.UUID) (*store.Channel, error)
	UpdateChannel(ctx context.Context, u *store.ChannelUpdate) error
	DeleteChannel(ctx context.Context, id, userID uuid.UUID) error

	CreatePortfolioItem(ctx context.Context, item *store.PortfolioItem) error
	DeletePortfolioItem(ctx context.Context, id, userID uuid.UUID) (hadMedia bool, err error)

	CreateBusinessPage(ctx context.Context, p *store.BusinessPage) error
	GetBusinessPageByID(ctx context.Context, id uuid.UUID, viewerID *uuid.UUID) (*store.BusinessPage, error)
	UpdateBusinessPage(ctx context.Context, p *store.BusinessPage) error
	DeleteBusinessPage(ctx context.Context, pageID, userID uuid.UUID) error
}

// WithMedia installs the media-service lookup. A lookup that also claims
// (mediaclient.Client does) is installed as the claim client too: wiring one
// without the other would refuse every attach.
func (s *Service) WithMedia(m MediaLookup) *Service {
	s.media = m
	if c, ok := m.(MediaClaims); ok {
		s.claims = c
	}
	return s
}

// WithMediaClaims installs the claim client on its own.
func (s *Service) WithMediaClaims(c MediaClaims) *Service {
	s.claims = c
	return s
}

// releaseTimeout bounds one best-effort release. Releases run on a context
// detached from the request: a client hanging up after its write committed
// must not leave the compensation undone.
const releaseTimeout = 5 * time.Second

// maxAttachPerWrite bounds the lookups one request can cause. Every writer
// here adds at most two (a page's avatar and cover).
const maxAttachPerWrite = 4

var confirmedMediaStatuses = map[string]bool{
	"uploaded":   true,
	"processing": true,
	"ready":      true,
}

// checkAttachAsset is the per-asset decision, as a pure function.
func checkAttachAsset(a *mediaclient.Asset, actorID uuid.UUID) error {
	if a.UploaderID != actorID {
		return ErrMediaNotOwned
	}
	if !confirmedMediaStatuses[a.ProcessingStatus] {
		return fmt.Errorf("%w: %s is %q", ErrMediaNotReady, a.ID, a.ProcessingStatus)
	}
	if a.ModerationStatus == "" || a.ModerationStatus == "rejected" {
		return fmt.Errorf("%w: %s moderation is %q", ErrMediaNotReady, a.ID, a.ModerationStatus)
	}
	return nil
}

// verifyAttach checks every id a write adds. It stops at the first refusal:
// reporting which ids of a batch belong to someone else would be an
// ownership oracle.
func (s *Service) verifyAttach(ctx context.Context, actorID uuid.UUID, added []uuid.UUID) error {
	if len(added) > maxAttachPerWrite {
		return fmt.Errorf("%w: too many media ids in one write", ErrMediaInvalid)
	}
	for _, id := range added {
		if id == uuid.Nil {
			return ErrMediaNotFound
		}
		if s.media == nil {
			return fmt.Errorf("%w: no media-service client configured", ErrMediaUnavailable)
		}
		a, err := s.media.Get(ctx, id)
		switch {
		case errors.Is(err, mediaclient.ErrNotFound):
			return ErrMediaNotFound
		case err != nil:
			return fmt.Errorf("%w: %v", ErrMediaUnavailable, err)
		case a == nil:
			return fmt.Errorf("%w: empty answer", ErrMediaUnavailable)
		}
		if err := checkAttachAsset(a, actorID); err != nil {
			return err
		}
	}
	return nil
}

// claimChange is what one write does to one holder's media.
type claimChange struct {
	kind      string
	ref       uuid.UUID
	add, drop []uuid.UUID
}

func (ch claimChange) String() string { return ch.kind + "/" + ch.ref.String() }

// mediaDiff returns the ids in next not in prev (added) and in prev not in
// next (removed). uuid.Nil is not media and is ignored; duplicates collapse.
func mediaDiff(prev, next []uuid.UUID) (added, removed []uuid.UUID) {
	inPrev := map[uuid.UUID]bool{}
	for _, id := range prev {
		if id != uuid.Nil {
			inPrev[id] = true
		}
	}
	inNext := map[uuid.UUID]bool{}
	for _, id := range next {
		if id == uuid.Nil || inNext[id] {
			continue
		}
		inNext[id] = true
		if !inPrev[id] {
			added = append(added, id)
		}
	}
	for _, id := range prev {
		if id != uuid.Nil && !inNext[id] {
			removed = append(removed, id)
			inNext[id] = true // collapse duplicates in prev
		}
	}
	return added, removed
}

// optionalMediaDiff is mediaDiff for a single optional column.
func optionalMediaDiff(prev, next *uuid.UUID) (added, removed []uuid.UUID) {
	var p, n []uuid.UUID
	if prev != nil {
		p = []uuid.UUID{*prev}
	}
	if next != nil {
		n = []uuid.UUID{*next}
	}
	return mediaDiff(p, n)
}

func addedOf(changes []claimChange) []uuid.UUID {
	var out []uuid.UUID
	for _, ch := range changes {
		out = append(out, ch.add...)
	}
	return out
}

// writeWithClaims runs write under the claim protocol for changes.
func (s *Service) writeWithClaims(ctx context.Context, changes []claimChange, write func() error) error {
	// 1. Claim, in order. A refusal part way releases what earlier changes
	// claimed: all or nothing per change, compensated across them.
	var claimed []claimChange
	for _, ch := range changes {
		if len(ch.add) == 0 {
			continue
		}
		if s.claims == nil {
			s.releaseClaimed(ctx, claimed)
			return fmt.Errorf("%w: no media-service claim client configured", ErrMediaUnavailable)
		}
		if err := s.claims.Claim(ctx, ch.kind, ch.ref, ch.add); err != nil {
			s.releaseClaimed(ctx, claimed) // compensate a partial claim
			if errors.Is(err, mediaclient.ErrNotFound) {
				return ErrMediaNotFound
			}
			return fmt.Errorf("%w: claim: %v", ErrMediaUnavailable, err)
		}
		claimed = append(claimed, ch)
	}

	// 2. Write; compensate on failure.
	if err := write(); err != nil {
		s.releaseClaimed(ctx, claimed) // compensate the failed write
		return err
	}

	// 3. Release what the row no longer holds.
	for _, ch := range changes {
		if len(ch.drop) > 0 {
			s.release(ctx, ch.kind, ch.ref, ch.drop)
		}
	}
	return nil
}

func (s *Service) releaseClaimed(ctx context.Context, claimed []claimChange) {
	for _, ch := range claimed {
		s.release(ctx, ch.kind, ch.ref, ch.add)
	}
}

// release is best effort: a failure leaves a stale claim, never a dangling
// reference, and queues the holder in media_claim_pending so the drain
// settles it. ids nil releases everything ref holds of kind.
func (s *Service) release(ctx context.Context, kind string, ref uuid.UUID, ids []uuid.UUID) bool {
	var err error
	if s.claims == nil {
		err = errors.New("no claim client configured")
	} else {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
		err = s.claims.Release(rctx, kind, ref, ids)
		cancel()
	}
	if err == nil {
		return true
	}
	slog.Warn("user-service: media claim release failed; queued for the drain",
		"kind", kind, "ref", ref, "media", len(ids), "error", err)
	if s.ms != nil {
		if qerr := s.ms.MarkMediaClaimHolderPending(context.WithoutCancel(ctx), kind, ref); qerr != nil {
			slog.Error("user-service: media claim release could not be queued; run cmd/media-claims -apply",
				"kind", kind, "ref", ref, "error", qerr)
		}
	}
	return false
}

// lockMediaHolder takes the store's per-row media lock.
func (s *Service) lockMediaHolder(ctx context.Context, ref uuid.UUID) (func(), error) {
	unlock, err := s.ms.LockMediaHolder(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to lock media holder: %w", err)
	}
	return unlock, nil
}

// DrainBatch is how many pending holders one DrainMediaClaims call settles.
const DrainBatch = 200

// DrainMediaClaims settles queued holders (deleted rows, disabled pages,
// purged accounts, failed releases). Without a claim client it does nothing.
func (s *Service) DrainMediaClaims(ctx context.Context) (claimsync.Report, error) {
	var rep claimsync.Report
	if s.claims == nil || s.ms == nil {
		return rep, nil
	}
	_, err := claimsync.Drain(ctx, s.ms, s.claims, DrainBatch, &rep)
	return rep, err
}

// ── Channels ─────────────────────────────────────────────────────────────

// CreateChannel creates a new creator channel. A picture or banner set at
// creation is verified and claimed before the row exists.
func (s *Service) CreateChannel(ctx context.Context, ch *store.Channel) error {
	if (ch.AvatarMediaID != nil && *ch.AvatarMediaID == uuid.Nil) || (ch.BannerMediaID != nil && *ch.BannerMediaID == uuid.Nil) {
		return ErrMediaNotFound
	}
	avatarAdd, _ := optionalMediaDiff(nil, ch.AvatarMediaID)
	bannerAdd, _ := optionalMediaDiff(nil, ch.BannerMediaID)
	if len(avatarAdd)+len(bannerAdd) == 0 {
		return s.ms.CreateChannel(ctx, ch)
	}
	if ch.ID == uuid.Nil {
		ch.ID = uuid.New()
	}
	changes := []claimChange{
		{kind: mediaclient.KindChannelAvatar, ref: ch.ID, add: avatarAdd},
		{kind: mediaclient.KindChannelBanner, ref: ch.ID, add: bannerAdd},
	}
	if err := s.verifyAttach(ctx, ch.UserID, addedOf(changes)); err != nil {
		return err
	}
	return s.writeWithClaims(ctx, changes, func() error { return s.ms.CreateChannel(ctx, ch) })
}

// UpdateChannel updates a channel's editable fields. A change of picture or
// banner runs under the holder lock: the new one is verified and claimed
// before the row names it, the old one released after the row stops naming it.
func (s *Service) UpdateChannel(ctx context.Context, upd *store.ChannelUpdate) error {
	if upd.AvatarMediaID == nil && upd.BannerMediaID == nil {
		return s.ms.UpdateChannel(ctx, upd) // media untouched (COALESCE keeps it)
	}
	if (upd.AvatarMediaID != nil && *upd.AvatarMediaID == uuid.Nil) || (upd.BannerMediaID != nil && *upd.BannerMediaID == uuid.Nil) {
		return ErrMediaNotFound
	}
	unlock, err := s.lockMediaHolder(ctx, upd.ID)
	if err != nil {
		return err
	}
	defer unlock()

	cur, err := s.ms.GetChannelByID(ctx, upd.ID)
	if err != nil || cur == nil || cur.UserID != upd.UserID {
		if err != nil && !isNoRows(err) {
			return err
		}
		return fmt.Errorf("CHANNEL_NOT_FOUND") // same answer as the owner-bound SQL
	}
	nextAvatar, nextBanner := cur.AvatarMediaID, cur.BannerMediaID
	if upd.AvatarMediaID != nil {
		nextAvatar = upd.AvatarMediaID
	}
	if upd.BannerMediaID != nil {
		nextBanner = upd.BannerMediaID
	}
	avatarAdd, avatarDrop := optionalMediaDiff(cur.AvatarMediaID, nextAvatar)
	bannerAdd, bannerDrop := optionalMediaDiff(cur.BannerMediaID, nextBanner)
	changes := []claimChange{
		{kind: mediaclient.KindChannelAvatar, ref: upd.ID, add: avatarAdd, drop: avatarDrop},
		{kind: mediaclient.KindChannelBanner, ref: upd.ID, add: bannerAdd, drop: bannerDrop},
	}
	if err := s.verifyAttach(ctx, upd.UserID, addedOf(changes)); err != nil {
		return err
	}
	return s.writeWithClaims(ctx, changes, func() error { return s.ms.UpdateChannel(ctx, upd) })
}

// DeleteChannel removes a channel and releases its picture and banner. The
// release is also queued in the delete's own transaction (store.DeleteChannel).
func (s *Service) DeleteChannel(ctx context.Context, id, userID uuid.UUID) error {
	unlock, err := s.lockMediaHolder(ctx, id)
	if err != nil {
		return err
	}
	defer unlock()
	cur, _ := s.ms.GetChannelByID(ctx, id) // only to skip needless releases
	if err := s.ms.DeleteChannel(ctx, id, userID); err != nil {
		return err
	}
	if cur == nil || cur.AvatarMediaID != nil {
		s.release(ctx, mediaclient.KindChannelAvatar, id, nil)
	}
	if cur == nil || cur.BannerMediaID != nil {
		s.release(ctx, mediaclient.KindChannelBanner, id, nil)
	}
	return nil
}

// ── Portfolio ────────────────────────────────────────────────────────────

// AddPortfolioItem creates a new portfolio entry for a user. Its media is
// verified and claimed before the row exists.
func (s *Service) AddPortfolioItem(ctx context.Context, userID uuid.UUID, title, desc, itemType, url string, mediaID *uuid.UUID, order int) (*store.PortfolioItem, error) {
	item := &store.PortfolioItem{
		ID:          uuid.New(),
		UserID:      userID,
		Title:       title,
		Description: desc,
		Type:        itemType,
		URL:         url,
		MediaID:     mediaID,
		SortOrder:   order,
	}
	if mediaID != nil && *mediaID == uuid.Nil {
		return nil, ErrMediaNotFound
	}
	add, _ := optionalMediaDiff(nil, mediaID)
	changes := []claimChange{{kind: mediaclient.KindPortfolioItem, ref: item.ID, add: add}}
	if err := s.verifyAttach(ctx, userID, add); err != nil {
		return nil, err
	}
	if err := s.writeWithClaims(ctx, changes, func() error { return s.ms.CreatePortfolioItem(ctx, item) }); err != nil {
		return nil, err
	}
	return item, nil
}

// DeletePortfolioItem removes a portfolio item if owned by userID, and
// releases its media (also queued in the delete's own transaction).
func (s *Service) DeletePortfolioItem(ctx context.Context, id, userID uuid.UUID) error {
	unlock, err := s.lockMediaHolder(ctx, id)
	if err != nil {
		return err
	}
	defer unlock()
	hadMedia, err := s.ms.DeletePortfolioItem(ctx, id, userID)
	if err != nil {
		return err
	}
	if hadMedia {
		s.release(ctx, mediaclient.KindPortfolioItem, id, nil)
	}
	return nil
}

// ── Business pages ───────────────────────────────────────────────────────

// pageMediaFor reads a TEXT media column as an optional id. A value that is
// not a media id holds nothing claimable.
func pageMediaFor(v string) *uuid.UUID {
	if id, ok, _ := store.ParsePageMediaID(v); ok {
		return &id
	}
	return nil
}

// normalisePageMedia validates a NEW value for a page media column: ""
// clears nothing (the handlers treat it as "unchanged"), a UUID is stored in
// canonical form, anything else is refused.
func normalisePageMedia(v *string) error {
	id, ok, present := store.ParsePageMediaID(*v)
	switch {
	case !present:
		*v = ""
	case !ok:
		return ErrMediaInvalid
	default:
		*v = id.String()
	}
	return nil
}

// CreateBusinessPage creates a new business page; its avatar and cover are
// verified and claimed before the row exists.
func (s *Service) CreateBusinessPage(ctx context.Context, p *store.BusinessPage) error {
	if err := normalisePageMedia(&p.AvatarMediaID); err != nil {
		return err
	}
	if err := normalisePageMedia(&p.CoverMediaID); err != nil {
		return err
	}
	avatarAdd, _ := optionalMediaDiff(nil, pageMediaFor(p.AvatarMediaID))
	coverAdd, _ := optionalMediaDiff(nil, pageMediaFor(p.CoverMediaID))
	if len(avatarAdd)+len(coverAdd) == 0 {
		return s.ms.CreateBusinessPage(ctx, p)
	}
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	changes := []claimChange{
		{kind: mediaclient.KindBusinessPageAvatar, ref: p.ID, add: avatarAdd},
		{kind: mediaclient.KindBusinessPageCover, ref: p.ID, add: coverAdd},
	}
	if err := s.verifyAttach(ctx, p.UserID, addedOf(changes)); err != nil {
		return err
	}
	return s.writeWithClaims(ctx, changes, func() error { return s.ms.CreateBusinessPage(ctx, p) })
}

// UpdateBusinessPage writes p (the caller's edited copy; p.UserID is the
// actor and the owner-bound SQL's owner). Its media is diffed against the
// row as it is NOW, re-read under the holder lock: p was read before the
// lock, and diffing against that copy could release a claim a concurrent
// edit relies on.
func (s *Service) UpdateBusinessPage(ctx context.Context, p *store.BusinessPage) error {
	unlock, err := s.lockMediaHolder(ctx, p.ID)
	if err != nil {
		return err
	}
	defer unlock()

	cur, err := s.ms.GetBusinessPageByID(ctx, p.ID, nil)
	if err != nil || cur == nil || cur.UserID != p.UserID {
		if err != nil && !isNoRows(err) {
			return err
		}
		return fmt.Errorf("PAGE_NOT_FOUND")
	}
	// A value carried over unchanged (even a legacy non-id) is left alone;
	// a changed one must be a media id.
	if p.AvatarMediaID != cur.AvatarMediaID {
		if err := normalisePageMedia(&p.AvatarMediaID); err != nil {
			return err
		}
	}
	if p.CoverMediaID != cur.CoverMediaID {
		if err := normalisePageMedia(&p.CoverMediaID); err != nil {
			return err
		}
	}
	avatarAdd, avatarDrop := optionalMediaDiff(pageMediaFor(cur.AvatarMediaID), pageMediaFor(p.AvatarMediaID))
	coverAdd, coverDrop := optionalMediaDiff(pageMediaFor(cur.CoverMediaID), pageMediaFor(p.CoverMediaID))
	changes := []claimChange{
		{kind: mediaclient.KindBusinessPageAvatar, ref: p.ID, add: avatarAdd, drop: avatarDrop},
		{kind: mediaclient.KindBusinessPageCover, ref: p.ID, add: coverAdd, drop: coverDrop},
	}
	added := addedOf(changes)
	if len(added) > 0 && cur.Status == "disabled" {
		// Terminal: a disabled page holds no claims (store pageLiveSQL), so
		// media added to it could not be protected.
		return ErrPageDisabled
	}
	if err := s.verifyAttach(ctx, p.UserID, added); err != nil {
		return err
	}
	return s.writeWithClaims(ctx, changes, func() error { return s.ms.UpdateBusinessPage(ctx, p) })
}

// DeleteBusinessPage soft-deletes (disables, terminal) a page owned by the
// caller and releases its avatar and cover (also queued in the same
// transaction as the disable).
func (s *Service) DeleteBusinessPage(ctx context.Context, pageID, userID uuid.UUID) error {
	unlock, err := s.lockMediaHolder(ctx, pageID)
	if err != nil {
		return err
	}
	defer unlock()
	cur, _ := s.ms.GetBusinessPageByID(ctx, pageID, nil) // only to skip needless releases
	if err := s.ms.DeleteBusinessPage(ctx, pageID, userID); err != nil {
		return err
	}
	if cur == nil || pageMediaFor(cur.AvatarMediaID) != nil {
		s.release(ctx, mediaclient.KindBusinessPageAvatar, pageID, nil)
	}
	if cur == nil || pageMediaFor(cur.CoverMediaID) != nil {
		s.release(ctx, mediaclient.KindBusinessPageCover, pageID, nil)
	}
	return nil
}
