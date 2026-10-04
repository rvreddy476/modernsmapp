package slots

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func asapPro(now time.Time) Pro {
	fix := now.Add(-time.Minute)
	p := Pro{ID: uuid.New(), Status: StatusApproved, Gender: "female", SkillVerified: true, InZone: true, WithinRadius: true,
		MaxJobsPerDay: 4, JobsByDay: map[string]int{}, OnDuty: true, LastFixAt: &fix, HasLive: true, LiveDistanceM: 3200,
		ServiceRadiusM: 5000, SameDay: true,
		BackgroundClear: []DateRange{{From: time.Date(2026, 1, 1, 0, 0, 0, 0, IST), Until: time.Date(2027, 1, 1, 0, 0, 0, 0, IST)}}}
	return p
}

func TestQualifiesASAP(t *testing.T) {
	now := time.Date(2026, 10, 5, 11, 0, 0, 0, IST)
	r := Request{DurationMinutes: 60, BufferMinutes: 30, GenderRule: GenderAny}
	if _, ok := QualifiesASAP(asapPro(now), r, now); !ok {
		t.Fatal("a ready professional refused")
	}
	stale := now.Add(-StaleFix - time.Second)
	for name, mut := range map[string]func(*Pro){
		"off duty":       func(p *Pro) { p.OnDuty = false },
		"no fix":         func(p *Pro) { p.HasLive = false },
		"stale fix":      func(p *Pro) { p.LastFixAt = &stale },
		"no same-day":    func(p *Pro) { p.SameDay = false },
		"out of range":   func(p *Pro) { p.LiveDistanceM = 5001 },
		"not approved":   func(p *Pro) { p.Status = "suspended" },
		"skill":          func(p *Pro) { p.SkillVerified = false },
		"zone":           func(p *Pro) { p.InZone = false },
		"gender (salon)": func(p *Pro) { p.Gender = "male" },
	} {
		p := asapPro(now)
		mut(&p)
		rr := r
		if name == "gender (salon)" {
			rr.GenderRule = GenderFemaleOnly
		}
		if _, ok := QualifiesASAP(p, rr, now); ok {
			t.Errorf("%s: qualified", name)
		}
	}
}

func TestASAPWindowAndFree(t *testing.T) {
	now := time.Date(2026, 10, 5, 11, 0, 20, 0, IST)
	cfg := Config{OpenMinute: 8 * 60, CloseMinute: 20 * 60, StepMinutes: 30, LeadMinutes: 120, HorizonDays: 7, HoldMinutes: 10}
	r := Request{DurationMinutes: 60, BufferMinutes: 30, GenderRule: GenderAny}
	p := asapPro(now)
	if EtaMinutes(3200) != 15 || EtaMinutes(0) != 5 || EtaMinutes(10000) != 35 {
		t.Fatalf("eta %d %d %d", EtaMinutes(3200), EtaMinutes(0), EtaMinutes(10000))
	}
	w := ASAPFor(p, now, r)
	if !w.BlockStart.Equal(now.Truncate(time.Minute)) || !w.Start.Equal(w.BlockStart.Add(15*time.Minute)) ||
		!w.End.Equal(w.Start.Add(time.Hour)) || !w.BlockEnd.Equal(w.End.Add(30*time.Minute)) {
		t.Fatalf("window %+v", w)
	}
	if _, ok := FreeASAP(p, w, cfg); !ok {
		t.Fatal("free professional refused")
	}
	busy := p
	busy.Blocks = []Interval{{Start: now.Add(30 * time.Minute), End: now.Add(2 * time.Hour)}}
	if _, ok := FreeASAP(busy, w, cfg); ok {
		t.Fatal("a busy professional is free for ASAP")
	}
	late := now.Add(8*time.Hour + 30*time.Minute) // 19:30: the job would end after 20:00
	if _, ok := FreeASAP(p, ASAPFor(p, late, r), cfg); ok {
		t.Fatal("an ASAP job past closing time")
	}
	capped := p
	capped.JobsByDay[DayKey(now)] = 4
	if _, ok := FreeASAP(capped, w, cfg); ok {
		t.Fatal("daily cap ignored")
	}
}

func TestNextFreeAndBands(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, IST) // Sunday
	cfg := Config{OpenMinute: 8 * 60, CloseMinute: 20 * 60, StepMinutes: 30, LeadMinutes: 120, HorizonDays: 7}
	r := Request{DurationMinutes: 60, BufferMinutes: 30, GenderRule: GenderAny}
	p := asapPro(now)
	for d := 1; d <= 6; d++ {
		p.Hours = append(p.Hours, Window{Weekday: d, Start: 9 * 60, End: 19 * 60})
	}
	free := NextFree(p, now, cfg, r, time.Time{}, 3)
	if len(free) != 3 || !free[0].Start.Equal(time.Date(2026, 10, 5, 9, 0, 0, 0, IST)) {
		t.Fatalf("next free %+v", free)
	}
	monday := time.Date(2026, 10, 5, 0, 0, 0, 0, IST)
	if got := NextFree(p, now, cfg, r, monday, 100); len(got) != 19 { // 09:00..18:00 every 30 min
		t.Fatalf("Monday starts %d", len(got))
	}
	if got := NextFree(p, now, cfg, r, now, 10); len(got) != 0 {
		t.Fatalf("Sunday (no hours) %+v", got)
	}
	for m, want := range map[float64]string{0: "under_2_km", 1999: "under_2_km", 2000: "2_to_5_km", 9999: "5_to_10_km", 25000: "over_10_km"} {
		if got := DistanceBand(m); got != want {
			t.Errorf("band(%v) = %s", m, got)
		}
	}
	if !InHorizon(now, monday, cfg) || InHorizon(now, monday.AddDate(0, 0, 7), cfg) || InHorizon(now, monday.AddDate(0, 0, -2), cfg) {
		t.Fatal("horizon")
	}
}
