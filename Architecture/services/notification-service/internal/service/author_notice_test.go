package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

// reels-29 / REG-6: the three author notice types are registered in the
// system category and tab, and render the reason and the appeal line.

func TestAuthorNoticeTypesAreRegisteredInTheSystemCategory(t *testing.T) {
	if len(AuthorNoticeTypes) != 3 {
		t.Fatalf("author notice types = %v", AuthorNoticeTypes)
	}
	for _, ty := range AuthorNoticeTypes {
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
}

func TestRenderAuthorNotice(t *testing.T) {
	post := uuid.New()
	n := AuthorNotice{RecipientID: uuid.New(), NoticeID: uuid.New(), PostID: post, Type: AuthorNoticeTypeTakedown,
		ContentType: "flick", Reason: "spam reel removed", Appealable: true, CreatedAt: time.Now()}
	r := RenderAuthorNotice(n)
	if r.Title != "Your reel was removed" || r.Body != "spam reel removed You can appeal this decision." ||
		r.CollapseKey != "author_notice:"+post.String() {
		t.Fatalf("takedown render = %+v", r)
	}
	// Not appealable: no appeal line.
	n.Appealable = false
	if r := RenderAuthorNotice(n); strings.Contains(r.Body, "appeal") {
		t.Fatalf("non-appealable body = %q", r.Body)
	}
	restored := n
	restored.Type, restored.ContentType, restored.Title = AuthorNoticeTypeRestored, "long_video", "My film"
	if r := RenderAuthorNotice(restored); r.Title != "Your video is back" || r.Body != "My film is visible again." {
		t.Fatalf("restored render = %+v", r)
	}
	failed := n
	failed.Type, failed.ContentType, failed.Reason = AuthorNoticeTypeScheduledFailed, "video", "The video could not be processed."
	if r := RenderAuthorNotice(failed); r.Title != "Your scheduled video was not published" || r.Body != "The video could not be processed." {
		t.Fatalf("scheduled render = %+v", r)
	}
	if failed.Identity() == n.Identity() {
		t.Fatal("two notice types of one notice id share an identity")
	}
}

func TestCreateAuthorNotice_DropsIncompleteAndSuppressed(t *testing.T) {
	delivered := 0
	s := &Service{authorNoticeInAppSeam: func(context.Context, AuthorNotice) error { delivered++; return nil }}
	full := AuthorNotice{RecipientID: uuid.New(), NoticeID: uuid.New(), PostID: uuid.New(), Type: AuthorNoticeTypeTakedown}
	for _, n := range []AuthorNotice{
		{NoticeID: full.NoticeID, PostID: full.PostID, Type: full.Type},
		{RecipientID: full.RecipientID, PostID: full.PostID, Type: full.Type},
		{RecipientID: full.RecipientID, NoticeID: full.NoticeID, Type: full.Type},
	} {
		if err := s.CreateAuthorNotice(context.Background(), n); err != nil {
			t.Fatal(err)
		}
	}
	if delivered != 0 {
		t.Fatalf("incomplete notices delivered: %d", delivered)
	}
	// A deactivated author gets nothing; a failed lookup is retried.
	s.suppressionSeam = func(context.Context, uuid.UUID) (bool, error) { return true, nil }
	if err := s.CreateAuthorNotice(context.Background(), full); err != nil || delivered != 0 {
		t.Fatalf("suppressed: err=%v delivered=%d", err, delivered)
	}
	s.suppressionSeam = func(context.Context, uuid.UUID) (bool, error) { return false, errors.New("pg down") }
	if err := s.CreateAuthorNotice(context.Background(), full); err == nil || delivered != 0 {
		t.Fatalf("lookup failure: err=%v delivered=%d, want an error to retry", err, delivered)
	}
	s.suppressionSeam = func(context.Context, uuid.UUID) (bool, error) { return false, nil }
	if err := s.CreateAuthorNotice(context.Background(), full); err != nil || delivered != 1 {
		t.Fatalf("deliverable: err=%v delivered=%d", err, delivered)
	}
}
