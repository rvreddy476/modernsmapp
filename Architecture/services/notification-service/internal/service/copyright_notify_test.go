package service

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Copyright title review (2026-10-24): the three types are registered, sit
// in the system category and tab, and render the matched title.

func TestCopyrightTypesAreRegisteredInTheSystemCategory(t *testing.T) {
	if len(CopyrightNotificationTypes) != 3 {
		t.Fatalf("copyright types = %v", CopyrightNotificationTypes)
	}
	for _, ty := range CopyrightNotificationTypes {
		tpl, ok := Templates[ty]
		if !ok {
			t.Errorf("%s not in Templates", ty)
			continue
		}
		if !tpl.PushEligible || tpl.TitleTemplate == "" || tpl.CanAggregate {
			t.Errorf("%s template incomplete: %+v", ty, tpl)
		}
		if categoryForEvent(ty) != catSystem {
			t.Errorf("%s is not in the system category", ty)
		}
		if title, icon := InboxCopy(ty, ""); title == inboxGenericTitle || icon != InboxIconAccount {
			t.Errorf("%s inbox copy = %q / %q", ty, title, icon)
		}
		if inboxTabForType(ty) != InboxTabSystem {
			t.Errorf("%s not in the system tab", ty)
		}
		if title, _ := notifTitleBody(ty); title == "New Notification" {
			t.Errorf("%s has no fallback copy", ty)
		}
		if d := resolveDecision(ty, postgres.DefaultNotificationPreferences("u"), false); !d.CreateInbox || !d.SendPush {
			t.Errorf("%s with defaults: %+v", ty, d)
		}
	}
	if got, _ := InboxCopy(CopyrightTypeHold, ""); got != "Your video is in copyright review" {
		t.Errorf("hold inbox copy = %q", got)
	}
}

func TestRenderCopyrightNotification(t *testing.T) {
	review := uuid.New()
	n := CopyrightReviewNotification{RecipientID: uuid.New(), ReviewID: review, PostID: uuid.New(), Type: CopyrightTypeBlocked,
		MatchedTitle: "Jailer 2", PostTitle: "Jailer 2 full movie", CreatedAt: time.Now()}
	r := RenderCopyrightNotification(n)
	if r.Title != "Your video was blocked for copyright" || r.Body != "Jailer 2 full movie · matched Jailer 2" || r.CollapseKey != "copyright:"+review.String() {
		t.Fatalf("render = %+v", r)
	}
	n.PostTitle = ""
	if r := RenderCopyrightNotification(n); r.Body != "Matched Jailer 2" {
		t.Fatalf("body without title = %q", r.Body)
	}
	hold := n
	hold.Type = CopyrightTypeHold
	if hold.Identity() == n.Identity() {
		t.Fatal("the hold and the block of one review share an identity")
	}
}

// No recipient or review: dropped before any store is touched (a zero
// Service has none, so reaching delivery would panic).
func TestCreateCopyrightReviewNotificationDropsIncomplete(t *testing.T) {
	s := &Service{}
	for _, n := range []CopyrightReviewNotification{
		{ReviewID: uuid.New(), PostID: uuid.New(), Type: CopyrightTypeHold},
		{RecipientID: uuid.New(), PostID: uuid.New(), Type: CopyrightTypeHold},
	} {
		if err := s.CreateCopyrightReviewNotification(context.Background(), n); err != nil {
			t.Fatal(err)
		}
	}
}
