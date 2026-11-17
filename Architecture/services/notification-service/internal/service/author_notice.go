package service

// Post author notices (reels-29, REG-6).
//
// post-service writes post.author_notice to its outbox in the transaction
// of each decision that the author must hear about, and the consumer
// (events/author_notice_consumer.go) turns each into an AuthorNotice for the
// post's AUTHOR. This file renders and delivers it through the ordinary
// resolution path (suppression, the `system` preference category and inbox
// tab, master toggle, quiet hours), under an identity of (notice_id, type)
// so a redelivered event cannot notify twice — the copyright notices'
// pattern (copyright_notify.go):
//
//	moderation_takedown    -> post_moderation_takedown     "Your reel was removed"   body: the reason, "You can appeal."
//	moderation_restored    -> post_moderation_restored     "Your reel is back"
//	scheduled_video_failed -> post_scheduled_video_failed  "Your scheduled video was not published"
//
// The inbox row has no free text (its title is InboxCopy of the type), so
// the moderator's reason rides the push body; the row opens the post, where
// the author sees its state.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Author notice notification types, registered in Templates and in the
// `system` category (push_system / inapp_system) and inbox tab.
const (
	AuthorNoticeTypeTakedown        = "post_moderation_takedown"
	AuthorNoticeTypeRestored        = "post_moderation_restored"
	AuthorNoticeTypeScheduledFailed = "post_scheduled_video_failed"
)

// AuthorNoticeTypes lists every author notice type, for the registry tests.
var AuthorNoticeTypes = []string{AuthorNoticeTypeTakedown, AuthorNoticeTypeRestored, AuthorNoticeTypeScheduledFailed}

// AuthorNotice is the author's notice about one decision on their post.
type AuthorNotice struct {
	RecipientID uuid.UUID
	// NoticeID is post-service's dedupe key: the decision id, or the post
	// id for a failed scheduled video.
	NoticeID    uuid.UUID
	PostID      uuid.UUID
	Type        string
	ContentType string
	Title       string
	Reason      string
	Appealable  bool
	DeepLink    string
	CreatedAt   time.Time
}

// Identity keys the idempotent inbox write: one notice per notice id and type.
func (n AuthorNotice) Identity() string {
	return "author_notice:" + n.NoticeID.String() + ":" + n.Type
}

// AuthorNoticeDeepLink opens the post itself: the author still sees a
// removed post, with its state.
func AuthorNoticeDeepLink(postID uuid.UUID) string {
	return "/post/" + postID.String()
}

// authorNoticeNoun names the post as its author knows it.
func authorNoticeNoun(contentType string) string {
	switch contentType {
	case "flick", "reel":
		return "reel"
	case "video", "long_video":
		return "video"
	}
	return "post"
}

// authorNoticeAppealLine closes a takedown the author may appeal.
const authorNoticeAppealLine = "You can appeal this decision."

// RenderAuthorNotice renders the push copy: the title names the kind of
// post, the body carries the moderator's reason (takedown), the post's
// title (restored) or the system reason (scheduled video).
func RenderAuthorNotice(n AuthorNotice) RenderOverride {
	noun := authorNoticeNoun(n.ContentType)
	reason, title := strings.TrimSpace(n.Reason), strings.TrimSpace(n.Title)
	var heading, body string
	switch n.Type {
	case AuthorNoticeTypeTakedown:
		heading = "Your " + noun + " was removed"
		body = reason
		if n.Appealable {
			body = strings.TrimSpace(body + " " + authorNoticeAppealLine)
		}
	case AuthorNoticeTypeRestored:
		heading = "Your " + noun + " is back"
		body = "It is visible again."
		if title != "" {
			body = title + " is visible again."
		}
	case AuthorNoticeTypeScheduledFailed:
		heading = "Your scheduled video was not published"
		body = reason
	default:
		heading = GetTemplate(n.Type).TitleTemplate
	}
	return RenderOverride{
		Title: heading,
		Body:  body,
		// Per post: a restore replaces the takedown on the device.
		CollapseKey: "author_notice:" + n.PostID.String(),
	}
}

// CreateAuthorNotice delivers one author notice. The consumer commits the
// event only after this returns nil, so a failed suppression lookup is an
// error to retry, not a silent drop.
func (s *Service) CreateAuthorNotice(ctx context.Context, n AuthorNotice) error {
	if n.RecipientID == uuid.Nil || n.NoticeID == uuid.Nil || n.PostID == uuid.Nil {
		return nil
	}
	suppressed, err := s.uploadRecipientSuppressed(ctx, n.RecipientID)
	if err != nil {
		return fmt.Errorf("author notice: suppression lookup: %w", err)
	}
	if suppressed {
		return nil
	}
	if s.authorNoticeInAppSeam != nil {
		return s.authorNoticeInAppSeam(ctx, n)
	}
	decision := s.resolveGeneralDelivery(ctx, n.RecipientID, n.Type)
	// The author is the actor of their own system notice (as
	// post_reclassified and the copyright notices are): the moderator is
	// never named.
	return s.deliverWithDecision(ctx, decision, n.RecipientID, n.RecipientID, n.Type,
		"post", n.PostID, n.DeepLink, n.CreatedAt, n.Identity(), RenderAuthorNotice(n))
}
