package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

/*
	Creator Hub read side (2026-09-28): what GET /v1/posts/:postId adds.

	For every viewer: age_restricted, hide_like_count, default_comment_sort,
	related_post_id (on the post), like_count and related_post (on the
	detail). For the owner: notify_subscribers (the other owner settings —
	paid_promotion, altered_content, license, allow_embedding,
	recording_date, recording_location, remix_setting, comment_moderation,
	comment_access — were already on the post for everyone and stay there).

	hide_like_count: the owner gets the number; everyone else gets
	like_count null and counts.likes 0, on the direct read and on every
	list this service builds (applyLikeCountPrivacy).
*/

// LikeCount marshals as the number, or null when hidden from this viewer.
type LikeCount struct {
	Value  int64
	Hidden bool
}

// MarshalJSON implements json.Marshaler.
func (c LikeCount) MarshalJSON() ([]byte, error) {
	if c.Hidden {
		return []byte("null"), nil
	}
	return strconv.AppendInt(nil, c.Value, 10), nil
}

// RelatedPostCard is the related post as the watch page links it.
type RelatedPostCard struct {
	ID              uuid.UUID `json:"id"`
	Title           string    `json:"title"`
	ThumbnailURL    string    `json:"thumbnail_url"`
	DurationSeconds int       `json:"duration_seconds"`
	ChannelName     string    `json:"channel_name"`
}

// RelatedPostField marshals as the card, or null: the direct read always
// carries related_post, list surfaces never do.
type RelatedPostField struct {
	Card *RelatedPostCard
}

// MarshalJSON implements json.Marshaler.
func (f RelatedPostField) MarshalJSON() ([]byte, error) {
	if f.Card == nil {
		return []byte("null"), nil
	}
	return json.Marshal(f.Card)
}

// likeCountHidden reports whether p's like number is hidden from viewerID.
func likeCountHidden(p *postgres.Post, viewerID *uuid.UUID) bool {
	if p == nil || !p.HideLikeCount {
		return false
	}
	return viewerID == nil || *viewerID != p.AuthorID
}

// applyLikeCountPrivacy enforces hide_like_count on one detail. direct is
// the single-post read, which always carries like_count.
func applyLikeCountPrivacy(d *PostDetail, viewerID *uuid.UUID, direct bool) {
	if d == nil || d.Post == nil {
		return
	}
	if likeCountHidden(d.Post, viewerID) {
		if d.Counts != nil {
			c := *d.Counts
			c.Likes = 0
			d.Counts = &c
		}
		d.LikeCount = &LikeCount{Hidden: true}
		return
	}
	if direct {
		var n int64
		if d.Counts != nil {
			n = d.Counts.Likes
		}
		d.LikeCount = &LikeCount{Value: n}
	}
}

// applyHubDetail fills the Creator Hub fields on the direct read.
func (s *Service) applyHubDetail(ctx context.Context, d *PostDetail, viewerID *uuid.UUID) {
	if d == nil || d.Post == nil {
		return
	}
	applyLikeCountPrivacy(d, viewerID, true)
	isOwner := viewerID != nil && *viewerID == d.Post.AuthorID
	card := s.relatedPostCard(ctx, d.Post, viewerID)
	d.RelatedPost = &RelatedPostField{Card: card}
	if card == nil && !isOwner {
		// A related post this viewer cannot open is not named to them.
		d.Post.RelatedPostID = nil
	}
	if isOwner {
		ns := resolvedNotifySubscribers(d.Post)
		d.NotifySubscribers = &ns
	}
}

// resolvedNotifySubscribers is the stored policy's notify_subscribers after
// the legacy defaults (distribution.go). A policy that no longer parses
// reads as the default rather than failing the owner's read.
func resolvedNotifySubscribers(p *postgres.Post) bool {
	policy, err := ParseDistributionPolicy(p.Distribution)
	if err != nil {
		policy = nil
	}
	return ResolveDistribution(policy).NotifySubscribers
}

// relatedPostCard loads p's related post through the same gates the direct
// read applies (review, processing / scheduled, visibility incl. private
// shares, age) and returns nil when any of them says no.
func (s *Service) relatedPostCard(ctx context.Context, p *postgres.Post, viewerID *uuid.UUID) *RelatedPostCard {
	if p == nil || p.RelatedPostID == nil || s.pgStore == nil {
		return nil
	}
	rp, err := s.getCachedPostBody(ctx, *p.RelatedPostID)
	if err != nil || rp == nil {
		return nil
	}
	isAuthor := viewerID != nil && *viewerID == rp.AuthorID
	if rp.ReviewStatus != "" && rp.ReviewStatus != "approved" && !isAuthor {
		return nil
	}
	if err := s.attachMediaState(ctx, []*postgres.Post{rp}); err != nil {
		return nil
	}
	if hiddenFromViewer(rp, viewerID) || !s.viewerMayViewPost(ctx, rp, viewerID) {
		return nil
	}
	if s.checkAgeGate(ctx, rp, viewerID) != nil {
		return nil
	}
	card := &RelatedPostCard{ID: rp.ID, Title: strings.TrimSpace(rp.Title)}
	if card.Title == "" {
		card.Title = firstTextLine(rp.Text)
	}
	var durationMs int
	var imageID *uuid.UUID
	for i := range rp.Media {
		if rp.Media[i].DurationMs > durationMs {
			durationMs = rp.Media[i].DurationMs
		}
		if imageID == nil && rp.Media[i].Kind == "image" {
			id := rp.Media[i].MediaID
			imageID = &id
		}
	}
	card.DurationSeconds = int(math.Round(float64(durationMs) / 1000))
	if vm, err := s.pgStore.GetVideoMetadata(ctx, rp.ID); err == nil && vm != nil {
		if vm.ThumbnailURL != nil && *vm.ThumbnailURL != "" {
			card.ThumbnailURL = *vm.ThumbnailURL
		}
		if card.DurationSeconds == 0 && vm.DurationSeconds > 0 {
			card.DurationSeconds = int(math.Round(vm.DurationSeconds))
		}
	}
	if card.ThumbnailURL == "" {
		switch {
		case rp.CoverMediaID != nil:
			card.ThumbnailURL = fmt.Sprintf("/v1/media/%s/serve", rp.CoverMediaID)
		case imageID != nil:
			card.ThumbnailURL = fmt.Sprintf("/v1/media/%s/serve", imageID)
		}
	}
	tmp := &PostDetail{Post: rp}
	attachViewer := uuid.Nil
	if viewerID != nil {
		attachViewer = *viewerID
	}
	s.attachChannelRefs(ctx, attachViewer, []*PostDetail{tmp})
	if tmp.Channel != nil {
		card.ChannelName = tmp.Channel.Name
	} else if a := s.fetchPostAuthor(ctx, viewerID, rp.AuthorID); a != nil {
		card.ChannelName = a.DisplayName
	}
	return card
}

// firstTextLine is the first non-blank line of a description.
func firstTextLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if l := strings.TrimSpace(line); l != "" {
			return l
		}
	}
	return ""
}
