package service

import (
	"errors"
	"testing"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Gate line 3 (long video): the caption writes — create, replace (the same
// upsert), publish/unpublish, correct, request — all decide through
// AssertMediaOwner. Its verdict: the uploader only.
func TestCaptionOwnerVerdictRefusesAStranger(t *testing.T) {
	owner, stranger := uuid.New(), uuid.New()
	media := &postgres.MediaAsset{ID: uuid.New(), UploaderID: owner}
	if err := mediaOwnerVerdict(media, owner); err != nil {
		t.Fatalf("owner refused: %v", err)
	}
	for name, actor := range map[string]uuid.UUID{"stranger": stranger, "anonymous": uuid.Nil} {
		if err := mediaOwnerVerdict(media, actor); !errors.Is(err, ErrNotMediaOwner) {
			t.Fatalf("%s: %v, want ErrNotMediaOwner", name, err)
		}
	}
	if err := mediaOwnerVerdict(nil, owner); !errors.Is(err, ErrNotMediaOwner) {
		t.Fatalf("no asset: %v", err)
	}
	// A zero uploader must not match the anonymous actor.
	if err := mediaOwnerVerdict(&postgres.MediaAsset{}, uuid.Nil); !errors.Is(err, ErrNotMediaOwner) {
		t.Fatalf("nil uploader, nil actor: %v", err)
	}
}

// Every caption write the stranger could reach goes through the shared check
// before it touches the store: a zero Service with no store would panic
// past it, so the refusal is the only possible answer for uuid.Nil.
func TestCaptionWritesRefuseTheAnonymousActorBeforeTheStore(t *testing.T) {
	s := &Service{}
	media := uuid.New()
	if _, err := s.CreateSubtitle(t.Context(), uuid.Nil, &postgres.MediaSubtitle{MediaAssetID: media, Language: "en"}); !errors.Is(err, ErrNotMediaOwner) {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.CorrectCaption(t.Context(), media, uuid.Nil, "en", "x"); !errors.Is(err, ErrNotMediaOwner) {
		t.Fatalf("correct: %v", err)
	}
	if _, err := s.RequestCaptions(t.Context(), media, uuid.Nil, "en"); !errors.Is(err, ErrNotMediaOwner) {
		t.Fatalf("request: %v", err)
	}
	if _, err := s.SetCaptionPublished(t.Context(), uuid.Nil, media, "en", false); !errors.Is(err, ErrNotMediaOwner) {
		t.Fatalf("unpublish: %v", err)
	}
}
