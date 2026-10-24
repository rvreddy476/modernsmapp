package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Migration 076 in the service: the author's view of the appeal, and the
// admin's track removal. The transactions are store tests
// (store/postgres/copyright_appeal_test.go).

// A blocked video offers its one appeal; once used it does not, whatever
// the reviewer answered; a pending or cleared one never does.
func TestCopyrightReviewView_Appeal(t *testing.T) {
	at := time.Now()
	for name, c := range map[string]struct {
		r         postgres.TitleReview
		canAppeal bool
	}{
		"blocked by the system":        {postgres.TitleReview{State: postgres.TitleReviewBlocked, AuthorNotified: true, Reason: strPtr(postgres.TitleReviewReasonOwnerRegistered)}, true},
		"blocked by a reviewer":        {postgres.TitleReview{State: postgres.TitleReviewBlocked, AuthorNotified: true}, true},
		"appeal waiting":               {postgres.TitleReview{State: postgres.TitleReviewPending, AuthorNotified: true, AppealedAt: &at}, false},
		"appeal answered with a block": {postgres.TitleReview{State: postgres.TitleReviewBlocked, AuthorNotified: true, AppealedAt: &at}, false},
		"pending":                      {postgres.TitleReview{State: postgres.TitleReviewPending, AuthorNotified: true}, false},
		"cleared":                      {postgres.TitleReview{State: postgres.TitleReviewCleared, AuthorNotified: true}, false},
	} {
		r := c.r
		v := copyrightReviewView(&r)
		if v == nil || v.CanAppeal != c.canAppeal || (r.AppealedAt != nil) != (v.AppealedAt != nil) {
			t.Errorf("%s: view = %+v, want can_appeal=%v", name, v, c.canAppeal)
		}
	}
}

func TestRemoveCopyrightOwnerTrack_Service(t *testing.T) {
	r := newTitleRig(t)
	post, title, actor := uuid.New(), uuid.New(), uuid.New()
	if _, err := r.svc.RemoveCopyrightOwnerTrack(context.Background(), post, title, actor, strings.Repeat("x", 2001)); !errors.Is(err, postgres.ErrTitleReviewInvalid) {
		t.Fatalf("long note: err=%v", err)
	}
	if _, err := r.svc.RemoveCopyrightOwnerTrack(context.Background(), uuid.Nil, title, actor, ""); !errors.Is(err, postgres.ErrTitleReviewInvalid) {
		t.Fatalf("no post: err=%v", err)
	}
	if len(r.store.trackRemovals) != 0 {
		t.Fatalf("an invalid removal reached the store: %+v", r.store.trackRemovals)
	}
	out, err := r.svc.RemoveCopyrightOwnerTrack(context.Background(), post, title, actor, "  licence shown ")
	if err != nil || out.PostID != post || len(r.store.trackRemovals) != 1 ||
		r.store.trackRemovals[0] != (trackRemoval{post, title, actor, "licence shown"}) {
		t.Fatalf("out=%+v removals=%+v err=%v", out, r.store.trackRemovals, err)
	}
}
