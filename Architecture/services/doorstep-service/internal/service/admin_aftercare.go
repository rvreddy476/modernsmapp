package service

import (
	"context"
	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
	"strings"
	"time"
	"unicode/utf8"
)

type IncidentResolution struct {
	Resolution     *string `json:"resolution"`
	LiftSuspension bool    `json:"lift_suspension"`
}
type TicketStatusInput struct {
	Status *string `json:"status"`
	Note   *string `json:"note"`
}
type AdminAftercareStore interface {
	TaxRegistration(context.Context, uuid.UUID) (*model.TaxRegistration, error)
	SetTaxRegistration(context.Context, store.Actor, uuid.UUID, *string, string, time.Time) (*model.TaxRegistration, error)
	AdminCareList(context.Context, string, string, string) ([]any, error)
	AdminIncidentDecision(context.Context, store.Actor, uuid.UUID, string, *string, bool, time.Time) (*model.Incident, error)
	AdminTicketStatus(context.Context, store.Actor, uuid.UUID, string, *string, time.Time) (*model.Ticket, error)
	AdminHideVisitRating(context.Context, store.Actor, uuid.UUID, string, time.Time) (*model.Rating, error)
	ComputeVisitSettlements(context.Context, time.Time) (int, error)
}

func (s *Service) WithAdminAftercare(st AdminAftercareStore) *Service { s.careAdmin = st; return s }
func (s *Service) AdminCareList(ctx context.Context, kind, status, period string) ([]any, error) {
	statuses := map[string][]string{
		"incidents":   {"open", "acknowledged", "resolved"},
		"tickets":     {"open", "in_progress", "resolved", "closed"},
		"settlements": {"computed", "approved", "paid"},
		"ratings":     {},
	}
	if status != "" && !store.Contains(statuses[kind], status) {
		return nil, apperr.Invalid("status", "unknown status for this queue")
	}
	if period != "" {
		if _, e := time.Parse("2006-01-02", period); e != nil {
			return nil, apperr.Invalid("period_start", "use YYYY-MM-DD")
		}
	}
	return s.careAdmin.AdminCareList(ctx, kind, status, period)
}
func (s *Service) AdminAcknowledgeIncident(ctx context.Context, a store.Actor, id uuid.UUID) (*model.Incident, error) {
	return s.careAdmin.AdminIncidentDecision(ctx, a, id, "acknowledged", nil, false, s.nowUTC())
}
func (s *Service) AdminResolveVisitIncident(ctx context.Context, a store.Actor, id uuid.UUID, in IncidentResolution) (*model.Incident, error) {
	if in.Resolution == nil || len(strings.TrimSpace(*in.Resolution)) < 10 || utf8.RuneCountInString(*in.Resolution) > 1000 {
		return nil, apperr.Invalid("resolution", "explain the resolution in 10 to 1000 characters")
	}
	return s.careAdmin.AdminIncidentDecision(ctx, a, id, "resolved", in.Resolution, in.LiftSuspension, s.nowUTC())
}
func (s *Service) AdminTicketStatus(ctx context.Context, a store.Actor, id uuid.UUID, in TicketStatusInput) (*model.Ticket, error) {
	if in.Status == nil || !store.Contains([]string{"open", "in_progress", "resolved", "closed"}, *in.Status) {
		return nil, apperr.Invalid("status", "choose a ticket status")
	}
	if in.Note != nil && utf8.RuneCountInString(*in.Note) > 1000 {
		return nil, apperr.Invalid("note", "use at most 1000 characters")
	}
	return s.careAdmin.AdminTicketStatus(ctx, a, id, *in.Status, in.Note, s.nowUTC())
}
func (s *Service) AdminHideVisitRating(ctx context.Context, a store.Actor, id uuid.UUID, in model.ReasonInput) (*model.Rating, error) {
	if in.Reason == nil || len(strings.TrimSpace(*in.Reason)) < 10 || utf8.RuneCountInString(*in.Reason) > 1000 {
		return nil, apperr.Invalid("reason", "explain the moderation reason in 10 to 1000 characters")
	}
	return s.careAdmin.AdminHideVisitRating(ctx, a, id, *in.Reason, s.nowUTC())
}
