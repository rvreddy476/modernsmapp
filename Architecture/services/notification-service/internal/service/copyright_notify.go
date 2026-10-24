package service

// Copyright title review (founder quick wins, 2026-10-24).
//
// post-service holds a long video that names a film or TV title on its
// staff catalogue until a reviewer clears or blocks it, and writes
// post.copyright_review.changed to its outbox in the transaction of each
// transition. The consumer (events/copyright_consumer.go) turns each into a
// CopyrightReviewNotification for the AUTHOR; this file renders and
// delivers it through the ordinary resolution path (suppression, the
// `system` preference category and inbox tab, master toggle, quiet hours),
// under an identity of (review, type) so a redelivered event cannot notify
// twice:
//
//	pending -> copyright_review_hold     "Your video is in copyright review"  → /posttube/hub
//	cleared -> copyright_review_cleared  "Your video cleared copyright review" → the watch page
//	blocked -> copyright_review_blocked  "Your video was blocked …"           → /posttube/hub
//
// The notice names the matched catalogue title, never the reviewer.
//
// A blocked review may be the SYSTEM's (founder 24 Oct, post-service
// migration 075): the rights owner registered the title and asked for
// copies blocked, so no reviewer was involved (reason owner_registered).
// The hold and the block are also emailed (copyright_email.go).

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Copyright review notification types, registered in Templates and in the
// `system` category (push_system / inapp_system) and inbox tab.
const (
	CopyrightTypeHold    = "copyright_review_hold"
	CopyrightTypeCleared = "copyright_review_cleared"
	CopyrightTypeBlocked = "copyright_review_blocked"
)

// CopyrightNotificationTypes lists every copyright type, for the registry tests.
var CopyrightNotificationTypes = []string{CopyrightTypeHold, CopyrightTypeCleared, CopyrightTypeBlocked}

// CopyrightEntityType is the entity a copyright notice points at: the video.
const CopyrightEntityType = "post"

// CopyrightHubDeepLink is where a hold or a block lands: the creator's
// uploads, where the video carries its copyright_review state and the
// "I own the rights" request.
const CopyrightHubDeepLink = "/posttube/hub"

// CopyrightReviewNotification is the author's notice about one review.
type CopyrightReviewNotification struct {
	RecipientID  uuid.UUID
	ReviewID     uuid.UUID
	PostID       uuid.UUID
	Type         string
	MatchedTitle string
	PostTitle    string
	// RightsOwner names the registered owner, when the entry names one.
	RightsOwner string
	// Reason: owner_registered (the system blocked it for the owner),
	// mute_requested (the owner asked for a mute; in review) or "".
	Reason    string
	DeepLink  string
	CreatedAt time.Time
}

// Identity keys the idempotent inbox write: one notice per review and type.
func (n CopyrightReviewNotification) Identity() string {
	return n.ReviewID.String() + ":" + n.Type
}

// RenderCopyrightNotification renders the push copy from the registered
// template. The body names the matched title; with the video's own title it
// reads "{post_title} · matched {matched_title}".
func RenderCopyrightNotification(n CopyrightReviewNotification) RenderOverride {
	t := GetTemplate(n.Type)
	matched, own := strings.TrimSpace(n.MatchedTitle), strings.TrimSpace(n.PostTitle)
	body := ""
	switch {
	case own != "" && matched != "":
		body = own + " · matched " + matched
	case matched != "":
		body = "Matched " + matched
	default:
		body = own
	}
	return RenderOverride{
		Title: t.TitleTemplate,
		Body:  body,
		// Per review: the clear or block replaces the hold on the device.
		CollapseKey: "copyright:" + n.ReviewID.String(),
	}
}

// CreateCopyrightReviewNotification delivers one copyright notice.
func (s *Service) CreateCopyrightReviewNotification(ctx context.Context, n CopyrightReviewNotification) error {
	if n.RecipientID == uuid.Nil || n.ReviewID == uuid.Nil {
		return nil
	}
	if s.recipientSuppressed(ctx, n.RecipientID, n.Type) {
		return nil
	}
	var inApp error
	if s.copyrightInAppSeam != nil {
		inApp = s.copyrightInAppSeam(ctx, n)
	} else {
		decision := s.resolveGeneralDelivery(ctx, n.RecipientID, n.Type)
		// The author is the actor of their own system notice (as
		// post_reclassified is): no other person is named.
		inApp = s.deliverWithDecision(ctx, decision, n.RecipientID, n.RecipientID, n.Type,
			CopyrightEntityType, n.PostID, n.DeepLink, n.CreatedAt, n.Identity(), RenderCopyrightNotification(n))
	}
	// A hold or a block is also EMAILED (copyright_email.go), whatever the
	// in-app/push preferences decided — a legal-type notice — and even when
	// the in-app write failed.
	return errors.Join(inApp, s.sendCopyrightEmail(ctx, n))
}
