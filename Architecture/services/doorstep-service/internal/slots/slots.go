// Package slots derives bookable time from professionals' calendars. It has
// no I/O: the store loads each candidate professional (hard-filter facts,
// weekly hours, active calendar blocks, jobs per day) and this package
// decides, for a slot, who could do the job.
//
// A slot is offered only when at least one professional passes every hard
// filter (Qualifies + FreeAt):
//
//   - approved, not suspended after an incident;
//   - the service's required skill verified;
//   - the category gender rule and the customer's woman-professional
//     preference, against the DigiLocker gender (never self-declared);
//   - the booking's zone among the professional's zones and the address
//     within their home radius;
//   - a clear background check valid on the slot's date;
//   - the job [start, start+duration] inside one of their weekly-hours
//     windows that day (IST, weekday 0 = Sunday);
//   - [start, end + travel buffer) overlapping no active calendar block
//     (day off, hold, booking, break) — the same interval the hold inserts,
//     so the exclusion constraint and this check agree;
//   - fewer jobs that day than their daily cap.
//
// The answer is advisory (it may be cached for 30 s): the hold insert and
// the btree_gist exclusion constraint are the source of truth.
package slots

import (
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
)

// IST is the calendar's zone (Asia/Kolkata has no DST).
var IST = time.FixedZone("IST", 5*3600+30*60)

// Config is a city's (or category's) slot configuration.
type Config struct {
	OpenMinute  int // minutes after midnight IST
	CloseMinute int
	StepMinutes int
	LeadMinutes int
	HorizonDays int
	HoldMinutes int
}

// Window is one weekly-hours window: Weekday 0 = Sunday, minutes after
// midnight IST, [Start, End].
type Window struct {
	Weekday    int
	Start, End int
}

// Interval is [Start, End).
type Interval struct {
	Start, End time.Time
}

// Overlaps reports whether two half-open intervals share an instant.
func (a Interval) Overlaps(b Interval) bool {
	return a.Start.Before(b.End) && b.Start.Before(a.End)
}

// DateRange is a background check's validity: valid on day d when
// From <= d < Until (dates at IST midnight), as the onboarding SQL reads it.
type DateRange struct {
	From, Until time.Time
}

// Pro is one candidate professional as the store loaded it.
type Pro struct {
	ID     uuid.UUID
	UserID uuid.UUID

	Status            string
	IncidentSuspended bool
	Gender            string // from DigiLocker; "" when not verified
	SkillVerified     bool   // the service's required skill
	InZone            bool   // the booking's zone is one of theirs
	WithinRadius      bool   // the address is within their home radius
	BackgroundClear   []DateRange
	MaxJobsPerDay     int

	Hours  []Window
	Blocks []Interval // active calendar blocks
	// JobsByDay counts active hold/booking blocks per IST date
	// ("2006-01-02").
	JobsByDay map[string]int

	// Scoring inputs (internal/matcher).
	DistanceM      float64
	RatingSum      int64
	RatingCount    int
	OffersReceived int
	OffersAccepted int
	Cancellations  int
	JobsCompleted  int
}

// Request is what a slot must fit.
type Request struct {
	DurationMinutes int
	BufferMinutes   int // the zone's travel buffer, after the job
	GenderRule      string
	RequireFemale   bool
}

// Gender rules (doorstep.categories.gender_rule).
const (
	GenderAny         = "any"
	GenderFemaleOnly  = "female_pros_only"
	GenderMaleOnly    = "male_pros_only"
	GenderFemale      = "female"
	GenderMale        = "male"
	StatusApproved    = "approved"
	ReasonNotApproved = "not_approved"
)

// Hard-filter reasons (observability; never shown to customers).
const (
	ReasonIncident   = "incident_suspended"
	ReasonSkill      = "skill_not_verified"
	ReasonGender     = "gender_rule"
	ReasonZone       = "zone"
	ReasonRadius     = "radius"
	ReasonBackground = "background_check"
	ReasonHours      = "outside_hours"
	ReasonBusy       = "calendar_busy"
	ReasonDailyCap   = "daily_cap"
)

// GenderAllowed applies the category rule and the customer's preference to
// a DigiLocker gender. A professional with no verified gender is admitted
// only where nothing is required.
func GenderAllowed(rule string, requireFemale bool, gender string) bool {
	switch rule {
	case GenderFemaleOnly:
		if gender != GenderFemale {
			return false
		}
	case GenderMaleOnly:
		if gender != GenderMale {
			return false
		}
	case GenderAny:
	default:
		return false // unknown rule: fail closed
	}
	if requireFemale && gender != GenderFemale {
		return false
	}
	return true
}

// Qualifies applies the date-independent hard filters.
func Qualifies(p Pro, r Request) (string, bool) {
	switch {
	case p.Status != StatusApproved:
		return ReasonNotApproved, false
	case p.IncidentSuspended:
		return ReasonIncident, false
	case !p.SkillVerified:
		return ReasonSkill, false
	case !GenderAllowed(r.GenderRule, r.RequireFemale, p.Gender):
		return ReasonGender, false
	case !p.InZone:
		return ReasonZone, false
	case !p.WithinRadius:
		return ReasonRadius, false
	}
	return "", true
}

// DayKey is the IST date of t.
func DayKey(t time.Time) string { return t.In(IST).Format("2006-01-02") }

// midnight is the IST midnight starting t's day.
func midnight(t time.Time) time.Time {
	l := t.In(IST)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, IST)
}

// BlockFor is the calendar interval a job starting at start occupies: the
// job plus the travel buffer after it. The hold and booking blocks are
// inserted with exactly this interval.
func BlockFor(start time.Time, r Request) Interval {
	end := start.Add(time.Duration(r.DurationMinutes) * time.Minute)
	return Interval{Start: start, End: end.Add(time.Duration(r.BufferMinutes) * time.Minute)}
}

// FreeAt reports whether a qualified professional can take a job starting
// at start: background check valid that day, inside a weekly-hours window,
// no overlapping active block, under the daily cap. (Reschedule: the store
// leaves the booking's own block out of Blocks and JobsByDay.)
func FreeAt(p Pro, start time.Time, r Request) (string, bool) {
	day := midnight(start)
	clear := false
	for _, bg := range p.BackgroundClear {
		if !bg.From.After(day) && bg.Until.After(day) {
			clear = true
			break
		}
	}
	if !clear {
		return ReasonBackground, false
	}
	local := start.In(IST)
	startMin := local.Hour()*60 + local.Minute()
	endMin := startMin + r.DurationMinutes
	inHours := false
	for _, w := range p.Hours {
		if w.Weekday == int(local.Weekday()) && w.Start <= startMin && endMin <= w.End {
			inHours = true
			break
		}
	}
	if !inHours {
		return ReasonHours, false
	}
	block := BlockFor(start, r)
	jobs := p.JobsByDay[DayKey(start)]
	for _, b := range p.Blocks {
		if b.Overlaps(block) {
			return ReasonBusy, false
		}
	}
	if p.MaxJobsPerDay > 0 && jobs >= p.MaxJobsPerDay {
		return ReasonDailyCap, false
	}
	return "", true
}

// Available returns the professionals who can take a job starting at start,
// in input order.
func Available(pros []Pro, start time.Time, r Request) []Pro {
	var out []Pro
	for _, p := range pros {
		if _, ok := Qualifies(p, r); !ok {
			continue
		}
		if _, ok := FreeAt(p, start, r); !ok {
			continue
		}
		out = append(out, p)
	}
	return out
}

// Day is one date of the grid.
type Day struct {
	Date  string
	Slots []Slot
}

// Slot is one start on the grid.
type Slot struct {
	Start, End time.Time
	Available  bool
}

// Starts lists the grid of one IST day: from open, every step, while the
// job still ends by close.
func Starts(day time.Time, cfg Config, durationMinutes int) []time.Time {
	m := midnight(day)
	var out []time.Time
	if cfg.StepMinutes <= 0 {
		return nil
	}
	for min := cfg.OpenMinute; min+durationMinutes <= cfg.CloseMinute; min += cfg.StepMinutes {
		out = append(out, m.Add(time.Duration(min)*time.Minute))
	}
	return out
}

// Days builds the horizon: HorizonDays dates from today (IST), each with its
// grid; a start is available when it is at least the lead time away and some
// professional can take it.
func Days(now time.Time, cfg Config, r Request, pros []Pro) []Day {
	earliest := now.Add(time.Duration(cfg.LeadMinutes) * time.Minute)
	var qualified []Pro
	for _, p := range pros {
		if _, ok := Qualifies(p, r); ok {
			qualified = append(qualified, p)
		}
	}
	today := midnight(now)
	days := make([]Day, 0, cfg.HorizonDays)
	for d := 0; d < cfg.HorizonDays; d++ {
		day := today.AddDate(0, 0, d)
		out := Day{Date: day.Format("2006-01-02"), Slots: []Slot{}}
		for _, s := range Starts(day, cfg, r.DurationMinutes) {
			slot := Slot{Start: s.UTC(), End: s.Add(time.Duration(r.DurationMinutes) * time.Minute).UTC()}
			if !s.Before(earliest) {
				for _, p := range qualified {
					if _, ok := FreeAt(p, s, r); ok {
						slot.Available = true
						break
					}
				}
			}
			out.Slots = append(out.Slots, slot)
		}
		days = append(days, out)
	}
	return days
}

// Range is the window of time the horizon spans (for loading blocks).
func Range(now time.Time, cfg Config) (from, to time.Time) {
	from = midnight(now)
	return from, from.AddDate(0, 0, cfg.HorizonDays+1)
}

// Slot refusals.
var (
	ErrOffGrid  = errors.New("slots: start is not on the slot grid or outside open hours")
	ErrTooSoon  = errors.New("slots: start is inside the lead time")
	ErrTooFar   = errors.New("slots: start is beyond the booking horizon")
	ErrNotFound = errors.New("slots: no slot configuration")
)

// Validate checks a requested start against the grid, lead time and horizon.
func Validate(now, start time.Time, cfg Config, durationMinutes int) error {
	if start.Before(now.Add(time.Duration(cfg.LeadMinutes) * time.Minute)) {
		return ErrTooSoon
	}
	last := midnight(now).AddDate(0, 0, cfg.HorizonDays)
	if !start.Before(last) {
		return ErrTooFar
	}
	for _, s := range Starts(start, cfg, durationMinutes) {
		if s.Equal(start) {
			return nil
		}
	}
	return ErrOffGrid
}

// WeekLoad is how many jobs a professional has in the Sunday-to-Saturday
// week of t (fairness input for the matcher).
func WeekLoad(p Pro, t time.Time) int {
	m := midnight(t)
	sunday := m.AddDate(0, 0, -int(m.Weekday()))
	n := 0
	for d := 0; d < 7; d++ {
		n += p.JobsByDay[sunday.AddDate(0, 0, d).Format("2006-01-02")]
	}
	return n
}

// SortByID orders professionals deterministically (store output order).
func SortByID(pros []Pro) {
	sort.Slice(pros, func(i, j int) bool { return pros[i].ID.String() < pros[j].ID.String() })
}
