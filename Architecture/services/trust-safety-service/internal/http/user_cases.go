package http

import (
	"context"

	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

// userCasesAPI answers the routes a user reads their own cases on (reports,
// appeals, grievances, verification); svc by default, a fake in tests. The
// handlers answer these through the views in user_views.go unless the
// caller is an admin.
type userCasesAPI interface {
	FileReportWith(ctx context.Context, in service.FileReportInput) (*postgres.Report, error)
	ListOwnReports(ctx context.Context, reporterID uuid.UUID, limit, offset int) ([]postgres.Report, error)
	ListUserAppeals(ctx context.Context, userID uuid.UUID) ([]postgres.ContentAppeal, error)
	FileGrievance(ctx context.Context, complainantID uuid.UUID, subject, aboutType, aboutID, description string) (*postgres.Grievance, error)
	FileUserGrievance(ctx context.Context, complainantID uuid.UUID, subject, aboutType, aboutID, description string) (*postgres.Grievance, error)
	GetGrievance(ctx context.Context, id uuid.UUID) (*postgres.Grievance, error)
	ListMyGrievances(ctx context.Context, userID uuid.UUID, limit, offset int) ([]postgres.Grievance, error)
	SubmitVerificationRequest(ctx context.Context, userID uuid.UUID, vtype string, docs map[string]string) (*postgres.VerificationRequest, error)
	OwnVerificationRequest(ctx context.Context, userID uuid.UUID) (*postgres.VerificationRequest, error)
}

var _ userCasesAPI = (*service.Service)(nil)
