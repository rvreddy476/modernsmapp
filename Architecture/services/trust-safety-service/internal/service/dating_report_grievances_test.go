package service

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBuildDatingReportGrievanceDueRunsFromReportTime(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	reported := now.Add(-3 * 24 * time.Hour) // trust-safety was down for three days
	in := DatingReportGrievanceInput{
		ReportID: uuid.New(), ReporterID: uuid.New(), TargetID: uuid.New(),
		Reason: "underage", Details: "says 16 in bio", ReportedAt: reported,
	}
	g, err := buildDatingReportGrievance(in, now)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if want := reported.AddDate(0, 0, 15); !g.DueAt.Equal(want) {
		t.Fatalf("due_at = %s, want %s (15 days from the report, not the link)", g.DueAt, want)
	}
	if g.ComplainantID != in.ReporterID || g.Status != "open" || g.Subject != "account" {
		t.Fatalf("grievance = %+v, want reporter as complainant, open, subject account", g)
	}

	in.Reason, in.ReportedAt = "harassment", time.Time{}
	g, err = buildDatingReportGrievance(in, now)
	if err != nil || g.Subject != "content_complaint" || !g.DueAt.Equal(now.AddDate(0, 0, 15)) {
		t.Fatalf("harassment without reported_at: g=%+v err=%v", g, err)
	}
}

func TestBuildDatingReportGrievanceRefusesMissingIDs(t *testing.T) {
	_, err := buildDatingReportGrievance(DatingReportGrievanceInput{ReportID: uuid.New(), Reason: "spam"}, time.Now())
	if !errors.Is(err, ErrInvalidDatingReport) {
		t.Fatalf("err = %v, want ErrInvalidDatingReport", err)
	}
}
