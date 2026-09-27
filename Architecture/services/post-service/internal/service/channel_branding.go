package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

/*
	Channel branding (MTube, 2026-09-27; migration 051 part C).

	PATCH /v1/channels/me additionally accepts
	  banner_media_id   an IMAGE the caller uploaded (null clears)
	  links             [{title, url}] — at most 10, https only, title 1-40
	  contact_email     a mailbox address (empty clears)
	  featured_post_id  one of the caller's own live posts (null clears)

	GET /v1/channels/:ref returns them, plus video_count / short_count /
	live_count / collection_count: the public tally, one query, cached in
	process for 60 s (an owner's own edit drops the entry).
*/

const (
	MaxChannelLinks         = 10
	MaxChannelLinkTitle     = 40
	MaxChannelLinkURL       = 2048
	MaxChannelContactEmail  = 254
	channelCountsTTL        = 60 * time.Second
	channelCountsCacheLimit = 10000
)

var (
	ErrInvalidChannelLinks   = errors.New("links must be at most 10 entries of {title (1-40 chars), url (https)}")
	ErrInvalidContactEmail   = errors.New("contact_email must be a valid email address")
	ErrFeaturedPostNotOwned  = errors.New("featured_post_id must be one of your own posts")
	ErrFeaturedPostNotFound  = errors.New("featured post not found")
	ErrBannerMediaNotOwned   = ErrMediaNotOwned
	ErrBannerMediaNotFound   = ErrMediaNotFound
	ErrBannerMediaNotAnImage = ErrMediaTypeMismatch
)

// NormalizeChannelLinks validates and canonicalises the links list: each
// title trimmed (1-40 runes), each url trimmed, absolute, https, at most
// 2048 bytes, no more than MaxChannelLinks entries. nil in -> empty out.
func NormalizeChannelLinks(raw []postgres.ChannelLink) ([]postgres.ChannelLink, error) {
	if len(raw) > MaxChannelLinks {
		return nil, ErrInvalidChannelLinks
	}
	out := make([]postgres.ChannelLink, 0, len(raw))
	for _, l := range raw {
		title := strings.TrimSpace(l.Title)
		if n := utf8.RuneCountInString(title); n < 1 || n > MaxChannelLinkTitle {
			return nil, ErrInvalidChannelLinks
		}
		rawURL := strings.TrimSpace(l.URL)
		if rawURL == "" || len(rawURL) > MaxChannelLinkURL {
			return nil, ErrInvalidChannelLinks
		}
		u, err := url.Parse(rawURL)
		if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
			return nil, ErrInvalidChannelLinks
		}
		out = append(out, postgres.ChannelLink{Title: title, URL: rawURL})
	}
	return out, nil
}

// NormalizeContactEmail trims and validates a mailbox address; empty is
// allowed (it clears the field).
func NormalizeContactEmail(raw string) (string, error) {
	e := strings.TrimSpace(raw)
	if e == "" {
		return "", nil
	}
	if len(e) > MaxChannelContactEmail || strings.ContainsAny(e, " <>\t\r\n") {
		return "", ErrInvalidContactEmail
	}
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Address != e || !strings.Contains(e, "@") {
		return "", ErrInvalidContactEmail
	}
	return e, nil
}

// applyBrandingPatch validates the branding half of UpdateChannelInput and
// fills the store patch. The banner is held to the create path's media
// authority (checkMediaAuthority with an image post's kind rule): it must
// exist, be the caller's own upload, be confirmed and not refused, and be
// an image. The featured post must be one of the caller's own live posts.
func (s *Service) applyBrandingPatch(ctx context.Context, userID uuid.UUID, in UpdateChannelInput, patch *postgres.ChannelPatch) error {
	patch.ClearBanner = in.ClearBanner
	patch.ClearFeatured = in.ClearFeatured
	if in.BannerMediaID != nil && !in.ClearBanner {
		if s.postEdits == nil {
			return ErrAuthoringStoreUnavailable
		}
		ownership, err := s.postEdits.BatchGetMediaOwnership(ctx, []uuid.UUID{*in.BannerMediaID})
		if err != nil {
			return fmt.Errorf("verify banner media: %w", err)
		}
		m, ok := ownership[*in.BannerMediaID]
		if err := checkMediaAuthority(*in.BannerMediaID, userID, m, ok, "post", postTypeImage); err != nil {
			return err
		}
		id := *in.BannerMediaID
		patch.BannerMediaID = &id
	}
	if in.Links != nil {
		links, err := NormalizeChannelLinks(*in.Links)
		if err != nil {
			return err
		}
		patch.Links = &links
	}
	if in.ContactEmail != nil {
		email, err := NormalizeContactEmail(*in.ContactEmail)
		if err != nil {
			return err
		}
		patch.ContactEmail = &email
	}
	if in.FeaturedPostID != nil && !in.ClearFeatured {
		if s.authoringOwners == nil {
			return ErrAuthoringStoreUnavailable
		}
		authorID, err := s.authoringOwners.GetPostAuthorID(ctx, *in.FeaturedPostID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrFeaturedPostNotFound
			}
			return fmt.Errorf("lookup featured post: %w", err)
		}
		if authorID != userID {
			return ErrFeaturedPostNotOwned
		}
		id := *in.FeaturedPostID
		patch.FeaturedPostID = &id
	}
	return nil
}

// attachChannelBranding copies the stored branding onto the view and
// fills the public tally (video_count keeps its historic meaning: public
// long videos).
func (s *Service) attachChannelBranding(ctx context.Context, ch *postgres.Channel, view *ChannelView) {
	view.BannerMediaID = ch.BannerMediaID
	view.Links = ch.Links
	if view.Links == nil {
		view.Links = []postgres.ChannelLink{}
	}
	view.ContactEmail = ch.ContactEmail
	view.FeaturedPostID = ch.FeaturedPostID
	counts, err := s.channelContentCounts(ctx, ch.UserID)
	if err != nil {
		slog.WarnContext(ctx, "channel content counts skipped", "user_id", ch.UserID, "err", err)
		return
	}
	view.VideoCount = counts.Videos
	view.ShortCount = counts.Shorts
	view.LiveCount = counts.Live
	view.CollectionCount = counts.Collections
}

type channelCountsEntry struct {
	counts    postgres.ChannelContentCounts
	fetchedAt time.Time
}

// channelContentCounts is the 60 s cache in front of CountChannelContent.
func (s *Service) channelContentCounts(ctx context.Context, userID uuid.UUID) (postgres.ChannelContentCounts, error) {
	now := time.Now()
	s.channelCountsMu.Lock()
	if e, ok := s.channelCounts[userID]; ok && now.Sub(e.fetchedAt) < channelCountsTTL {
		s.channelCountsMu.Unlock()
		return e.counts, nil
	}
	s.channelCountsMu.Unlock()

	counts, err := s.channels.CountChannelContent(ctx, userID)
	if err != nil {
		return counts, err
	}
	s.channelCountsMu.Lock()
	if s.channelCounts == nil {
		s.channelCounts = make(map[uuid.UUID]channelCountsEntry)
	}
	if len(s.channelCounts) >= channelCountsCacheLimit {
		// Bounded: drop everything rather than grow without limit.
		s.channelCounts = make(map[uuid.UUID]channelCountsEntry)
	}
	s.channelCounts[userID] = channelCountsEntry{counts: counts, fetchedAt: now}
	s.channelCountsMu.Unlock()
	return counts, nil
}

// forgetChannelCounts drops the owner's cached tally (after their own edit).
func (s *Service) forgetChannelCounts(userID uuid.UUID) {
	s.channelCountsMu.Lock()
	delete(s.channelCounts, userID)
	s.channelCountsMu.Unlock()
}
