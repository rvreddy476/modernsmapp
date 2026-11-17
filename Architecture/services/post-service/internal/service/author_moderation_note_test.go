package service

import (
	"context"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

type fakeModerationNotes struct {
	note  *postgres.AuthorModerationNote
	calls int
}

func (f *fakeModerationNotes) LatestModerationDecision(context.Context, uuid.UUID) (*postgres.AuthorModerationNote, error) {
	f.calls++
	return f.note, nil
}

func rejectedDetail(author uuid.UUID, status string) *PostDetail {
	return &PostDetail{Post: &postgres.Post{ID: uuid.New(), AuthorID: author, ReviewStatus: status}}
}

// The author of a rejected post sees why and whether it can be appealed;
// nobody else ever gets the fields, and they go once the post is restored.
func TestAttachModerationNoteIsTheAuthorsAlone(t *testing.T) {
	author, stranger := uuid.New(), uuid.New()
	notes := &fakeModerationNotes{note: &postgres.AuthorModerationNote{
		Reason: "Spam: repeated promotional links", Source: "report", ResultingStatus: "rejected"}}
	s := &Service{moderationNotesSeam: notes}
	ctx := context.Background()

	d := rejectedDetail(author, "rejected")
	s.attachModerationNote(ctx, d, &author)
	if d.ModerationReason == nil || *d.ModerationReason != "Spam: repeated promotional links" ||
		d.ModerationAppealable == nil || !*d.ModerationAppealable {
		t.Fatalf("author view: reason=%v appealable=%v", d.ModerationReason, d.ModerationAppealable)
	}

	for name, viewer := range map[string]*uuid.UUID{"stranger": &stranger, "signed out": nil} {
		d := rejectedDetail(author, "rejected")
		s.attachModerationNote(ctx, d, viewer)
		if d.ModerationReason != nil || d.ModerationAppealable != nil {
			t.Fatalf("%s got the author's moderation note", name)
		}
	}

	// Restored by an appeal: the post is approved again, no fields.
	d = rejectedDetail(author, "approved")
	s.attachModerationNote(ctx, d, &author)
	if d.ModerationReason != nil || d.ModerationAppealable != nil {
		t.Fatal("a restored post still carries the takedown reason")
	}

	// Rejected, but the latest decision is not the rejecting one (the
	// rejection came from the video gate): no moderator reason to show.
	notes.note = &postgres.AuthorModerationNote{Reason: "ok", Source: "appeal", ResultingStatus: "approved"}
	d = rejectedDetail(author, "rejected")
	s.attachModerationNote(ctx, d, &author)
	if d.ModerationReason != nil {
		t.Fatal("a reason from a decision that did not reject was shown")
	}

	// A denied appeal: the reason shows, and it cannot be appealed again.
	notes.note = &postgres.AuthorModerationNote{Reason: "Upheld on appeal", Source: "appeal", ResultingStatus: "rejected"}
	d = rejectedDetail(author, "rejected")
	s.attachModerationNote(ctx, d, &author)
	if d.ModerationReason == nil || d.ModerationAppealable == nil || *d.ModerationAppealable {
		t.Fatalf("denied appeal: reason=%v appealable=%v", d.ModerationReason, d.ModerationAppealable)
	}
}
