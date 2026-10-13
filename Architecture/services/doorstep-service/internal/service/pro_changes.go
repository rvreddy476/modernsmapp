package service

import (
	"context"
	"errors"
	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/mediaclient"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
	"net/http"
)

type partnerChangesStore interface {
	PartnerChanges(context.Context, *uuid.UUID) ([]store.PartnerChange, error)
	DecidePartnerChange(context.Context, store.Actor, uuid.UUID, bool, *string) error
}

func partnerChangeError(ctx context.Context, op string, err error) error {
	if errors.Is(err, store.ErrConflict) {
		return apperr.New(http.StatusConflict, apperr.CodeConflict, "A change to these details is already awaiting admin review. Please wait for that decision.")
	}
	if errors.Is(err, store.ErrInvalid) || errors.Is(err, store.ErrBadReference) {
		return apperr.Invalid("change", "These details no longer meet the service eligibility or area rules. Review the partner before approving.")
	}
	return proAdminErr(ctx, op, err)
}

func (s *Service) ProChanges(ctx context.Context, uid uuid.UUID) ([]store.PartnerChange, error) {
	p, err := s.mine(ctx, uid)
	if err != nil {
		return nil, err
	}
	st, ok := s.pro.Store.(partnerChangesStore)
	if !ok {
		return []store.PartnerChange{}, nil
	}
	v, err := st.PartnerChanges(ctx, &p.ID)
	return v, proAdminErr(ctx, "partner changes", err)
}

func (s *Service) AdminPartnerChanges(ctx context.Context) ([]store.PartnerChange, error) {
	st, ok := s.pro.Store.(partnerChangesStore)
	if !ok {
		return []store.PartnerChange{}, nil
	}
	v, err := st.PartnerChanges(ctx, nil)
	return v, proAdminErr(ctx, "partner changes", err)
}

type ChangeDecision struct {
	Decision string  `json:"decision"`
	Reason   *string `json:"reason"`
}

func (s *Service) AdminDecidePartnerChange(ctx context.Context, a store.Actor, id uuid.UUID, in ChangeDecision) error {
	if in.Decision != "approve" && in.Decision != "reject" {
		return apperr.Invalid("decision", "Choose approve or reject.")
	}
	reason, ae := optionalReason(in.Reason)
	if ae != nil {
		return ae
	}
	if in.Decision == "reject" {
		reason, ae = requiredReason(in.Reason)
		if ae != nil {
			return ae
		}
	}
	st, ok := s.pro.Store.(partnerChangesStore)
	if !ok {
		return apperr.New(503, apperr.CodeConflict, "Change review is unavailable.")
	}
	return partnerChangeError(ctx, "review partner change", st.DecidePartnerChange(ctx, a, id, in.Decision == "approve", reason))
}

func (s *Service) AdminChangePhoto(ctx context.Context, a store.Actor, id uuid.UUID) (*DocumentImage, error) {
	st, ok := s.pro.Store.(interface {
		ChangePhoto(context.Context, store.Actor, uuid.UUID) (uuid.UUID, uuid.UUID, error)
	})
	if !ok || s.pro.Images == nil {
		return nil, apperr.New(503, apperr.CodeMediaUnavailable, "Photo preview is unavailable.")
	}
	owner, media, err := st.ChangePhoto(ctx, a, id)
	if err != nil {
		return nil, proAdminErr(ctx, "view change photo", err)
	}
	if err := s.verifyMedia(ctx, "photo_media_id", media, owner, mediaclient.KindImage); err != nil {
		return nil, err
	}
	body, ct, err := s.pro.Images.FetchImage(ctx, media)
	if err != nil {
		return nil, apperr.New(503, apperr.CodeMediaUnavailable, "Photo preview is unavailable.")
	}
	return &DocumentImage{Bytes: body, ContentType: ct}, nil
}
