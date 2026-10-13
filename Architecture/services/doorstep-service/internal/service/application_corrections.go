package service

import (
	"context"
	"errors"

	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
)

// IdentityCorrectionInput replaces only the rejected identity proof of a
// pending application. It cannot change identity approval, name or gender.
type IdentityCorrectionInput struct {
	Kind    string `json:"kind"`
	MediaID string `json:"media_id"`
}

type identityCorrectionStore interface {
	ReplaceRejectedApplicationIdentity(context.Context, uuid.UUID, string, uuid.UUID) (*model.ProDocument, error)
}

func (s *Service) ProCorrectIdentity(ctx context.Context, uid uuid.UUID, in IdentityCorrectionInput) (*model.ProDocument, error) {
	p, err := s.editable(ctx, uid)
	if err != nil {
		return nil, err
	}
	if p.Status != "pending_verification" || p.Application == nil {
		return nil, apperr.New(409, "IDENTITY_CORRECTION_UNAVAILABLE", "Identity corrections are available for an application awaiting verification.")
	}
	if in.Kind != "passport" && in.Kind != "voter_id" && in.Kind != "driving_license" {
		return nil, apperr.Invalid("kind", "Choose Passport, Voter ID or Driving Licence.")
	}
	mediaID, err := uuid.Parse(in.MediaID)
	if err != nil || mediaID == uuid.Nil {
		return nil, apperr.Invalid("media_id", "Upload your replacement identity proof.")
	}
	st, ok := s.pro.Store.(identityCorrectionStore)
	if !ok || s.pro.Documents == nil {
		return nil, apperr.New(503, "APPLICATION_UNAVAILABLE", "Document corrections are temporarily unavailable. Please try again.")
	}
	if err := s.pro.Documents.VerifyOwnedImage(ctx, uid, mediaID); err != nil {
		return nil, apperr.New(422, "DOCUMENT_NOT_READY", "Use a private, processed image you uploaded. Wait a moment and retry without uploading again.")
	}
	doc, err := st.ReplaceRejectedApplicationIdentity(ctx, p.ID, in.Kind, mediaID)
	if errors.Is(err, store.ErrConflict) {
		return nil, apperr.New(409, "IDENTITY_CORRECTION_UNAVAILABLE", "Your identity document is already awaiting review or has been approved.")
	}
	if err != nil {
		return nil, proAdminErr(ctx, "correct identity document", err)
	}
	return doc, nil
}
