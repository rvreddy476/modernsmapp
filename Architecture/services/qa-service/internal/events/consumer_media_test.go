package events

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/atpost/qa-service/internal/store"
	"github.com/google/uuid"
)

type fakeEraser struct {
	erased store.Erased
	err    error
}

func (f fakeEraser) AnonymizeUser(context.Context, uuid.UUID) (store.Erased, error) {
	return f.erased, f.err
}

type recordingReleaser struct {
	calls     int
	questions []uuid.UUID
	answers   []uuid.UUID
}

func (r *recordingReleaser) ReleaseRemovedMedia(_ context.Context, q, a []uuid.UUID) {
	r.calls++
	r.questions, r.answers = q, a
}

// An account erasure releases the media claims of every question and answer
// it deleted, after the delete.
func TestEraseUserReleasesErasedContentsMedia(t *testing.T) {
	erased := store.Erased{QuestionIDs: []uuid.UUID{uuid.New(), uuid.New()}, AnswerIDs: []uuid.UUID{uuid.New()}}
	rel := &recordingReleaser{}
	c := (&Consumer{}).WithEraser(fakeEraser{erased: erased}).WithMediaRelease(rel)
	if err := c.eraseUser(context.Background(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	if rel.calls != 1 || !slices.Equal(rel.questions, erased.QuestionIDs) || !slices.Equal(rel.answers, erased.AnswerIDs) {
		t.Fatalf("released %d times, questions %v answers %v; want %v", rel.calls, rel.questions, rel.answers, erased)
	}
}

// A failed erasure releases nothing (the content is still shown) and reports
// the failure; no releaser configured never fails the erasure.
func TestEraseUserFailedOrUnwired(t *testing.T) {
	rel := &recordingReleaser{}
	c := (&Consumer{}).WithEraser(fakeEraser{err: errors.New("db down")}).WithMediaRelease(rel)
	if err := c.eraseUser(context.Background(), uuid.New()); err == nil {
		t.Fatal("a failed erasure was reported done")
	}
	if rel.calls != 0 {
		t.Fatal("a failed erasure released media")
	}
	c = (&Consumer{}).WithEraser(fakeEraser{erased: store.Erased{QuestionIDs: []uuid.UUID{uuid.New()}}})
	if err := c.eraseUser(context.Background(), uuid.New()); err != nil {
		t.Fatalf("an erasure without a releaser failed: %v", err)
	}
}
