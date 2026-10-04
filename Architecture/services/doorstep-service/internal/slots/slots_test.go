package slots

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func ist(y int, m time.Month, d, hh, mm int) time.Time { return time.Date(y, m, d, hh, mm, 0, 0, IST) }

var cfg = Config{OpenMinute: 8 * 60, CloseMinute: 20 * 60, StepMinutes: 30, LeadMinutes: 120, HorizonDays: 7, HoldMinutes: 10}

// pro is approved and qualified, works every day 08:00-20:00, background
// clear for 2026.
func pro() Pro {
	p := Pro{ID: uuid.MustParse("00000000-0000-0000-0000-000000000001"), Status: StatusApproved, Gender: GenderFemale,
		SkillVerified: true, InZone: true, WithinRadius: true, MaxJobsPerDay: 3, JobsByDay: map[string]int{},
		BackgroundClear: []DateRange{{From: ist(2026, 1, 1, 0, 0), Until: ist(2027, 1, 1, 0, 0)}}}
	for d := 0; d < 7; d++ {
		p.Hours = append(p.Hours, Window{Weekday: d, Start: 8 * 60, End: 20 * 60})
	}
	return p
}

var req = Request{DurationMinutes: 60, BufferMinutes: 30, GenderRule: GenderAny}

func TestGenderAllowed(t *testing.T) {
	cases := []struct {
		rule    string
		female  bool
		gender  string
		allowed bool
	}{
		{GenderAny, false, GenderMale, true},
		{GenderAny, false, "", true},
		{GenderAny, true, GenderMale, false},
		{GenderAny, true, "", false},
		{GenderAny, true, GenderFemale, true},
		{GenderFemaleOnly, false, GenderFemale, true},
		{GenderFemaleOnly, false, GenderMale, false},
		{GenderFemaleOnly, false, "", false},
		{GenderFemaleOnly, false, "other", false},
		{GenderMaleOnly, false, GenderMale, true},
		{GenderMaleOnly, false, GenderFemale, false},
		{GenderMaleOnly, true, GenderMale, false}, // men's salon + woman-pro preference: nobody
		{"unknown_rule", false, GenderFemale, false},
	}
	for _, c := range cases {
		if got := GenderAllowed(c.rule, c.female, c.gender); got != c.allowed {
			t.Errorf("GenderAllowed(%s, %t, %q) = %t", c.rule, c.female, c.gender, got)
		}
	}
}

func TestQualifiesHardFilters(t *testing.T) {
	if _, ok := Qualifies(pro(), req); !ok {
		t.Fatal("a qualified professional was refused")
	}
	for _, c := range []struct {
		reason string
		change func(*Pro, *Request)
	}{
		{ReasonNotApproved, func(p *Pro, _ *Request) { p.Status = "suspended" }},
		{ReasonIncident, func(p *Pro, _ *Request) { p.IncidentSuspended = true }},
		{ReasonSkill, func(p *Pro, _ *Request) { p.SkillVerified = false }},
		{ReasonGender, func(p *Pro, r *Request) { r.GenderRule = GenderMaleOnly }},
		{ReasonGender, func(p *Pro, r *Request) { p.Gender = GenderMale; r.RequireFemale = true }},
		{ReasonZone, func(p *Pro, _ *Request) { p.InZone = false }},
		{ReasonRadius, func(p *Pro, _ *Request) { p.WithinRadius = false }},
	} {
		p, r := pro(), req
		c.change(&p, &r)
		if reason, ok := Qualifies(p, r); ok || reason != c.reason {
			t.Errorf("want %s, got %q ok=%t", c.reason, reason, ok)
		}
	}
}

func TestFreeAtCalendar(t *testing.T) {
	start := ist(2026, 10, 6, 10, 0) // a Tuesday
	if _, ok := FreeAt(pro(), start, req); !ok {
		t.Fatal("free professional refused")
	}
	busy := func(blocks ...Interval) Pro {
		p := pro()
		p.Blocks = blocks
		return p
	}
	// A job ending at 09:30 with its buffer to 10:00 does not overlap 10:00.
	if _, ok := FreeAt(busy(Interval{ist(2026, 10, 6, 8, 30), ist(2026, 10, 6, 10, 0)}), start, req); !ok {
		t.Fatal("a block ending at the start overlapped")
	}
	// A block one minute into the job is a clash.
	if r, ok := FreeAt(busy(Interval{ist(2026, 10, 6, 8, 30), ist(2026, 10, 6, 10, 1)}), start, req); ok || r != ReasonBusy {
		t.Fatal("overlapping block ignored")
	}
	// The next job may start once this one's travel buffer has passed (11:30).
	if _, ok := FreeAt(busy(Interval{ist(2026, 10, 6, 11, 30), ist(2026, 10, 6, 13, 0)}), start, req); !ok {
		t.Fatal("block after the buffer clashed")
	}
	if r, ok := FreeAt(busy(Interval{ist(2026, 10, 6, 11, 29), ist(2026, 10, 6, 13, 0)}), start, req); ok || r != ReasonBusy {
		t.Fatal("travel buffer not kept")
	}
	// A day off covers the day.
	if _, ok := FreeAt(busy(Interval{ist(2026, 10, 6, 0, 0), ist(2026, 10, 7, 0, 0)}), start, req); ok {
		t.Fatal("day off ignored")
	}
}

func TestFreeAtHoursBackgroundAndCap(t *testing.T) {
	start := ist(2026, 10, 6, 10, 0)
	p := pro()
	p.Hours = []Window{{Weekday: int(time.Tuesday), Start: 9 * 60, End: 11 * 60}}
	if _, ok := FreeAt(p, start, req); !ok {
		t.Fatal("job inside the window refused")
	}
	if r, ok := FreeAt(p, ist(2026, 10, 6, 10, 30), req); ok || r != ReasonHours {
		t.Fatal("job running past the window accepted")
	}
	p.Hours = []Window{{Weekday: int(time.Monday), Start: 8 * 60, End: 20 * 60}}
	if r, ok := FreeAt(p, start, req); ok || r != ReasonHours {
		t.Fatal("weekday ignored (0 = Sunday)")
	}

	p = pro()
	p.BackgroundClear = []DateRange{{From: ist(2026, 1, 1, 0, 0), Until: ist(2026, 10, 6, 0, 0)}}
	if r, ok := FreeAt(p, start, req); ok || r != ReasonBackground {
		t.Fatal("a check expiring on the slot date was accepted (valid_until is exclusive)")
	}
	p.BackgroundClear = []DateRange{{From: ist(2026, 10, 7, 0, 0), Until: ist(2027, 10, 7, 0, 0)}}
	if _, ok := FreeAt(p, start, req); ok {
		t.Fatal("a check valid only from tomorrow was accepted")
	}
	p.BackgroundClear = nil
	if _, ok := FreeAt(p, start, req); ok {
		t.Fatal("no background check accepted")
	}

	p = pro()
	p.JobsByDay["2026-10-06"] = 3
	if r, ok := FreeAt(p, start, req); ok || r != ReasonDailyCap {
		t.Fatal("daily cap ignored")
	}
	p.JobsByDay["2026-10-06"] = 2
	if _, ok := FreeAt(p, start, req); !ok {
		t.Fatal("under the cap refused")
	}
}

func TestDaysGridLeadAndHorizon(t *testing.T) {
	now := ist(2026, 10, 4, 12, 0)
	days := Days(now, cfg, req, []Pro{pro()})
	if len(days) != 7 || days[0].Date != "2026-10-04" || days[6].Date != "2026-10-10" {
		t.Fatalf("horizon %d days from %s", len(days), days[0].Date)
	}
	// 08:00..19:00 starts for a 60-minute job (it must end by 20:00).
	if n := len(days[1].Slots); n != 23 {
		t.Fatalf("grid has %d starts, want 23", n)
	}
	last := days[1].Slots[len(days[1].Slots)-1]
	if !last.Start.Equal(ist(2026, 10, 5, 19, 0).UTC()) || !last.End.Equal(ist(2026, 10, 5, 20, 0).UTC()) {
		t.Fatalf("last slot %s-%s", last.Start, last.End)
	}
	for _, s := range days[0].Slots {
		earliest := now.Add(2 * time.Hour)
		if s.Available == s.Start.Before(earliest) {
			t.Fatalf("lead time: %s available=%t", s.Start, s.Available)
		}
	}
	// Nobody qualified: nothing available.
	p := pro()
	p.Gender = GenderMale
	female := req
	female.GenderRule = GenderFemaleOnly
	for _, d := range Days(now, cfg, female, []Pro{p}) {
		for _, s := range d.Slots {
			if s.Available {
				t.Fatal("women-only slot offered with only a male professional")
			}
		}
	}
}

func TestValidate(t *testing.T) {
	now := ist(2026, 10, 4, 12, 0)
	for _, c := range []struct {
		start time.Time
		want  error
	}{
		{ist(2026, 10, 4, 14, 0), nil},
		{ist(2026, 10, 4, 13, 30), ErrTooSoon},
		{ist(2026, 10, 4, 14, 15), ErrOffGrid},
		{ist(2026, 10, 5, 19, 30), ErrOffGrid}, // would end after close
		{ist(2026, 10, 5, 7, 30), ErrOffGrid},
		{ist(2026, 10, 10, 19, 0), nil},
		{ist(2026, 10, 11, 8, 0), ErrTooFar},
	} {
		if got := Validate(now, c.start, cfg, 60); got != c.want {
			t.Errorf("Validate(%s) = %v, want %v", c.start, got, c.want)
		}
	}
}

func TestWeekLoad(t *testing.T) {
	p := pro()
	p.JobsByDay = map[string]int{"2026-10-04": 1, "2026-10-06": 2, "2026-10-10": 1, "2026-10-11": 5, "2026-10-03": 4}
	// Sunday 4 Oct to Saturday 10 Oct.
	if n := WeekLoad(p, ist(2026, 10, 7, 9, 0)); n != 4 {
		t.Fatalf("week load %d, want 4", n)
	}
}

func TestBlockForAndAvailable(t *testing.T) {
	start := ist(2026, 10, 6, 10, 0)
	b := BlockFor(start, req)
	if !b.Start.Equal(start) || !b.End.Equal(start.Add(90*time.Minute)) {
		t.Fatalf("block %v", b)
	}
	a, c := pro(), pro()
	c.ID = uuid.MustParse("00000000-0000-0000-0000-000000000002")
	c.Blocks = []Interval{b}
	got := Available([]Pro{a, c}, start, req)
	if len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("available %v", got)
	}
}

// The matcher's origin is the previous job that day, else home.
func TestOriginPreviousJobElseHome(t *testing.T) {
	day := time.Date(2026, 10, 5, 4, 30, 0, 0, time.UTC) // 10:00 IST
	p := Pro{HasHome: true, HomeLat: 17.40, HomeLng: 78.40, DistanceM: 999, Jobs: []JobAt{
		{Start: day.Add(-2 * time.Hour), Lat: 17.41, Lng: 78.41},  // 08:00 same day
		{Start: day.Add(-1 * time.Hour), Lat: 17.42, Lng: 78.42},  // 09:00 same day: the latest earlier one
		{Start: day.Add(2 * time.Hour), Lat: 17.43, Lng: 78.43},   // later that day: ignored
		{Start: day.Add(-20 * time.Hour), Lat: 17.44, Lng: 78.44}, // previous day: ignored
	}}
	if lat, lng, ok := Origin(p, day); !ok || lat != 17.42 || lng != 78.42 {
		t.Fatalf("origin %v %v %v", lat, lng, ok)
	}
	if lat, _, _ := Origin(p, day.Add(-90*time.Minute)); lat != 17.41 {
		t.Fatalf("origin before the 09:00 job: %v", lat)
	}
	early := day.Add(-3 * time.Hour) // 07:00: no earlier job that day -> home
	if lat, _, ok := Origin(p, early); !ok || lat != 17.40 {
		t.Fatalf("home origin: %v", lat)
	}
	if d := DistanceFrom(Pro{DistanceM: 777}, day, 17.4, 78.4); d != 777 {
		t.Fatalf("no home, no job: %v", d)
	}
	if d := DistanceFrom(p, day, 17.42, 78.42); d > 1 {
		t.Fatalf("distance from the previous job: %v", d)
	}
	if d := HaversineM(17.0, 78.0, 18.0, 78.0); d < 110000 || d > 112500 {
		t.Fatalf("one degree of latitude: %v", d)
	}
}
