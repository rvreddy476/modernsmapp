package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Audit TS-L1: a complainant may lodge GrievanceLimit grievances per
// GrievanceWindow and hold one open grievance per item. The store is nil in
// these tests: a filing that got past the checks would panic on
// CreateGrievance, so a clean refusal proves nothing was written.

type fakeGrievanceVolume struct {
	n                int
	countErr, dupErr error
	active           bool
	asked, dupAsked  []uuid.UUID
	aboutAsked       []uuid.UUID
	since            time.Time
}

func (f *fakeGrievanceVolume) CountGrievancesByComplainantSince(_ context.Context, id uuid.UUID, since time.Time) (int, error) {
	f.asked = append(f.asked, id)
	f.since = since
	return f.n, f.countErr
}

func (f *fakeGrievanceVolume) HasActiveGrievanceAbout(_ context.Context, id, about uuid.UUID) (bool, error) {
	f.dupAsked = append(f.dupAsked, id)
	f.aboutAsked = append(f.aboutAsked, about)
	return f.active, f.dupErr
}

func fileUser(s *Service, who uuid.UUID, about string) error {
	_, err := s.FileUserGrievance(context.Background(), who, "privacy", "post", about, "please take this down")
	return err
}

func TestGrievanceOverTheCapIsRefusedBeforeTheStore(t *testing.T) {
	who := uuid.New()
	vol := &fakeGrievanceVolume{n: GrievanceLimit}
	s := New(nil, nil).WithGrievanceVolume(vol)
	if err := fileUser(s, who, ""); !errors.Is(err, ErrGrievanceRateLimited) {
		t.Fatalf("at the cap: %v, want ErrGrievanceRateLimited", err)
	}
	if len(vol.asked) != 1 || vol.asked[0] != who {
		t.Fatalf("quota asked about %v, want the complainant once", vol.asked)
	}
	if age := time.Since(vol.since); age < GrievanceWindow-time.Minute || age > GrievanceWindow+time.Minute {
		t.Fatalf("counted from %v ago, want the %v window", age, GrievanceWindow)
	}
}

func TestGrievanceDuplicateOpenAboutTheSameItemIsRefused(t *testing.T) {
	who, about := uuid.New(), uuid.New()
	vol := &fakeGrievanceVolume{n: GrievanceLimit - 1, active: true}
	s := New(nil, nil).WithGrievanceVolume(vol)
	if err := fileUser(s, who, about.String()); !errors.Is(err, ErrActiveGrievanceExists) {
		t.Fatalf("open grievance about the item: %v, want ErrActiveGrievanceExists", err)
	}
	if len(vol.dupAsked) != 1 || vol.dupAsked[0] != who || vol.aboutAsked[0] != about {
		t.Fatalf("duplicate check asked (%v, %v), want (%s, %s)", vol.dupAsked, vol.aboutAsked, who, about)
	}
}

func TestGrievanceChecksFailClosed(t *testing.T) {
	for name, vol := range map[string]*fakeGrievanceVolume{
		"count": {countErr: errors.New("db down")},
		"dup":   {dupErr: errors.New("db down")},
	} {
		s := New(nil, nil).WithGrievanceVolume(vol)
		err := fileUser(s, uuid.New(), uuid.NewString())
		if err == nil || errors.Is(err, ErrGrievanceRateLimited) || errors.Is(err, ErrActiveGrievanceExists) {
			t.Fatalf("%s read failed: %v, want a plain error (no filing)", name, err)
		}
	}
}

func TestGrievanceInvalidSubjectIsRefusedBeforeAnyRead(t *testing.T) {
	vol := &fakeGrievanceVolume{}
	s := New(nil, nil).WithGrievanceVolume(vol)
	if _, err := s.FileUserGrievance(context.Background(), uuid.New(), "nonsense", "", "", "please take this down"); err == nil {
		t.Fatal("invalid subject accepted")
	}
	if len(vol.asked) != 0 {
		t.Fatalf("quota read for an invalid filing: %v", vol.asked)
	}
}

// Under the cap with nothing open, the filing proceeds to the store (nil
// here: the panic is the proof it got that far). With no item named, the
// duplicate check is not asked.
func TestGrievanceUnderTheCapReachesTheStore(t *testing.T) {
	vol := &fakeGrievanceVolume{n: GrievanceLimit - 1}
	s := New(nil, nil).WithGrievanceVolume(vol)
	defer func() {
		if recover() == nil {
			t.Fatal("under the cap, the filing never reached the store")
		}
		if len(vol.dupAsked) != 0 {
			t.Fatalf("duplicate check asked with no item: %v", vol.dupAsked)
		}
	}()
	_ = fileUser(s, uuid.New(), "")
}
