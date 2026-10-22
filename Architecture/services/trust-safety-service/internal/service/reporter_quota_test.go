package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Security sweep #23: a reporter may file ReporterReportLimit reports per
// ReporterReportWindow through the user-facing route. The store is nil in
// these tests: a filing that got past the quota would panic on CreateReport,
// so a clean refusal proves nothing was written.

type fakeVolume struct {
	n     int
	err   error
	asked []uuid.UUID
	since time.Time
}

func (f *fakeVolume) CountReportsByReporterSince(_ context.Context, reporterID uuid.UUID, since time.Time) (int, error) {
	f.asked = append(f.asked, reporterID)
	f.since = since
	return f.n, f.err
}

func quotaInput(reporter uuid.UUID) FileReportInput {
	return FileReportInput{
		ReporterID: reporter, EntityType: "post", EntityRef: uuid.NewString(), Reason: "spam",
		EnforceReporterQuota: true,
	}
}

func TestReporterOverTheCapIsRefusedBeforeTheStore(t *testing.T) {
	reporter := uuid.New()
	vol := &fakeVolume{n: ReporterReportLimit}
	s := New(nil, nil).WithReporterVolume(vol)
	if _, err := s.FileReportWith(context.Background(), quotaInput(reporter)); !errors.Is(err, ErrReportRateLimited) {
		t.Fatalf("at the cap: %v, want ErrReportRateLimited", err)
	}
	if len(vol.asked) != 1 || vol.asked[0] != reporter {
		t.Fatalf("quota asked about %v, want the reporter once", vol.asked)
	}
	if age := time.Since(vol.since); age < ReporterReportWindow-time.Minute || age > ReporterReportWindow+time.Minute {
		t.Fatalf("counted from %v ago, want the %v window", age, ReporterReportWindow)
	}
}

func TestReporterQuotaFailsClosed(t *testing.T) {
	s := New(nil, nil).WithReporterVolume(&fakeVolume{err: errors.New("db down")})
	_, err := s.FileReportWith(context.Background(), quotaInput(uuid.New()))
	if err == nil || errors.Is(err, ErrReportRateLimited) {
		t.Fatalf("count failed: %v, want a plain error (no filing)", err)
	}
}

// Under the cap the filing proceeds to the store (nil here: the panic is the
// proof the quota let it through).
func TestReporterUnderTheCapReachesTheStore(t *testing.T) {
	s := New(nil, nil).WithReporterVolume(&fakeVolume{n: ReporterReportLimit - 1})
	defer func() {
		if recover() == nil {
			t.Fatal("a reporter under the cap did not reach the store")
		}
	}()
	_, _ = s.FileReportWith(context.Background(), quotaInput(uuid.New()))
}

// Service-side filers (chat consumer, internal routes) do not set the flag
// and are never counted, so a replayed event is not dropped by the cap.
// Invalid input is refused before the quota is asked.
func TestReporterQuotaOnlyForUserFacingValidFilings(t *testing.T) {
	vol := &fakeVolume{n: ReporterReportLimit}
	s := New(nil, nil).WithReporterVolume(vol)
	bad := quotaInput(uuid.New())
	bad.Reason = "bored"
	if _, err := s.FileReportWith(context.Background(), bad); !errors.Is(err, ErrInvalidReport) {
		t.Fatalf("invalid reason: %v", err)
	}
	internal := quotaInput(uuid.New())
	internal.EnforceReporterQuota = false
	func() {
		defer func() { _ = recover() }() // reaches the nil store
		_, _ = s.FileReportWith(context.Background(), internal)
	}()
	if len(vol.asked) != 0 {
		t.Fatalf("the quota was asked %d time(s) for an invalid or service-side filing", len(vol.asked))
	}
	if ReporterReportLimit != 20 || ReporterReportWindow != time.Hour {
		t.Fatalf("cap is %d per %v, want 20 per hour", ReporterReportLimit, ReporterReportWindow)
	}
}
