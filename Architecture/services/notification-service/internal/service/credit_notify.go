package service

// Video credits (cast & crew on a long video, 2026-10-23).
//
// post-service tags VChat accounts in a long video's credits; a tag waits for
// the tagged person's answer before the video shows on their profile. It
// writes two events to its outbox, in the transaction that creates the tag or
// records the answer:
//
//	post.credit.requested -> credit_request  to the tagged person (actor: uploader)
//	post.credit.accepted  -> credit_accepted to the uploader      (actor: tagged person)
//
// The consumer (events/credit_consumer.go) turns each into a
// CreditNotification; this file renders and delivers it through the ordinary
// resolution path (suppression, the `credits` preference category, master
// toggle, quiet hours), under an identity so a redelivered event — or an
// accept, decline, accept again — cannot notify twice.

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Credit notification types. Both are registered in Templates and map to the
// `credits` preference category (push_credits / inapp_credits, migration 017).
const (
	CreditTypeRequest  = "credit_request"
	CreditTypeAccepted = "credit_accepted"
)

// CreditNotificationTypes lists every credit type, for the registry tests.
var CreditNotificationTypes = []string{CreditTypeRequest, CreditTypeAccepted}

// CreditEntityType is the entity a credit notice points at: the video.
const CreditEntityType = "post"

// CreditRequestsDeepLink is where a credit request lands: the tagged person's
// list of pending requests, where they accept or decline.
const CreditRequestsDeepLink = "/credits/requests"

// creditFallbackUploader names an uploader the event could not name.
const creditFallbackUploader = "Someone"

// CreditNotification is one recipient's notice about one credit.
type CreditNotification struct {
	RecipientID uuid.UUID
	ActorID     uuid.UUID
	Type        string // CreditTypeRequest | CreditTypeAccepted
	CreditID    uuid.UUID
	PostID      uuid.UUID
	// UploaderName fills {uploader} on a request (empty: "Someone"); Name
	// fills {name} on an acceptance — the credit as the uploader typed it.
	UploaderName string
	Name         string
	Role         string
	PostTitle    string
	DeepLink     string
	CreatedAt    time.Time
}

// Identity keys the idempotent inbox write: one notice per credit, recipient
// and type, however often Kafka redelivers the event.
func (n CreditNotification) Identity() string {
	return n.CreditID.String() + ":" + n.RecipientID.String() + ":" + n.Type
}

// RenderCreditNotification renders the push copy from the registered
// template. The body is "{role} · {title}", or the role alone when the video
// has no title; a request with no uploader name reads "Someone credited you".
func RenderCreditNotification(n CreditNotification) RenderOverride {
	t := GetTemplate(n.Type)
	uploader := strings.TrimSpace(n.UploaderName)
	if uploader == "" {
		uploader = creditFallbackUploader
	}
	title := RenderTitle(t.TitleTemplate, map[string]string{"uploader": uploader, "name": strings.TrimSpace(n.Name)})
	role, videoTitle := strings.TrimSpace(n.Role), strings.TrimSpace(n.PostTitle)
	body := role
	if videoTitle != "" {
		body = RenderTitle(t.BodyTemplate, map[string]string{"role": role, "title": videoTitle})
		if role == "" {
			body = videoTitle
		}
	}
	return RenderOverride{
		Title: title,
		Body:  body,
		// Per credit and recipient: a redelivery replaces, never stacks.
		CollapseKey: "credit:" + n.CreditID.String() + ":" + n.RecipientID.String(),
	}
}

// CreateCreditNotification delivers one credit notice. A notice to oneself
// (a self-credit is accepted at creation and never asks) is dropped.
func (s *Service) CreateCreditNotification(ctx context.Context, n CreditNotification) error {
	if n.RecipientID == uuid.Nil || n.RecipientID == n.ActorID {
		return nil
	}
	if s.recipientSuppressed(ctx, n.RecipientID, n.Type) {
		return nil
	}
	decision := s.resolveGeneralDelivery(ctx, n.RecipientID, n.Type)
	return s.deliverWithDecision(ctx, decision, n.RecipientID, n.ActorID, n.Type,
		CreditEntityType, n.PostID, n.DeepLink, n.CreatedAt, n.Identity(), RenderCreditNotification(n))
}
