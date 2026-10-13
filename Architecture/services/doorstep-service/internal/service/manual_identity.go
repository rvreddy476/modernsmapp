package service

import (
	"context"
	"errors"
	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/prokyc"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
)

type manualIdentityReviewer interface {
	ReviewManualIdentity(context.Context, store.Actor, uuid.UUID, string, string) (*model.Professional, error)
}

func (s *Service) AdminReviewManualIdentity(ctx context.Context, a store.Actor, id uuid.UUID, in model.ManualIdentityReviewInput) (*model.Professional, error) {
	if in.Gender == nil {
		return nil, apperr.Invalid("gender", "record reviewed eligibility, or unknown when it cannot be established")
	}
	switch *in.Gender {
	case "female", "male", "transgender", "unknown":
	default:
		return nil, apperr.Invalid("gender", "unsupported gender value")
	}
	reason, err := requiredReason(in.Reason)
	if err != nil {
		return nil, err
	}
	reviewer, ok := s.pro.Store.(manualIdentityReviewer)
	if !ok {
		return nil, internal(ctx, "manual identity review", errors.New("review store unavailable"))
	}
	facts, e := s.pro.Store.ProFacts(ctx, id, s.today())
	if e != nil {
		return nil, proAdminErr(ctx, "manual identity facts", e)
	}
	if facts.SimpleApplication && !facts.IdentityDocumentApproved {
		return nil, incomplete(prokyc.StepIdentity)
	}
	if !facts.SimpleApplication && (!facts.HasPAN || !facts.SelfieMatched) {
		return nil, incomplete(prokyc.StepPAN, prokyc.StepSelfie)
	}
	out, e := reviewer.ReviewManualIdentity(ctx, a, id, *in.Gender, *reason)
	return out, proAdminErr(ctx, "manual identity review", e)
}
