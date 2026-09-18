package pricing

import (
	"time"
	_ "time/tzdata" // the alpine image ships no zoneinfo; fare windows are city-local
)

// NoChangeBPS is a window multiplier that leaves the fare alone.
const NoChangeBPS = 10000

// Weekday bits for Window.DaysOfWeek: Mon=1 .. Sun=64.
const (
	Monday    = 1 << 0
	Tuesday   = 1 << 1
	Wednesday = 1 << 2
	Thursday  = 1 << 3
	Friday    = 1 << 4
	Saturday  = 1 << 5
	Sunday    = 1 << 6
	Weekdays  = Monday | Tuesday | Wednesday | Thursday | Friday
	EveryDay  = 127
)

// Window is one rider_fare_windows row.
type Window struct {
	ID            string
	Name          string
	VehicleType   string // "" = every vehicle type
	DaysOfWeek    int
	StartMinute   int // local minutes since midnight
	EndMinute     int // exclusive; < StartMinute means the window wraps midnight
	MultiplierBPS int64
	Priority      int
	IsActive      bool
	EffectiveFrom time.Time
	EffectiveTo   *time.Time
}

// DayBit is the DaysOfWeek bit for a weekday.
func DayBit(d time.Weekday) int {
	// Go: Sunday=0 .. Saturday=6; ours: Monday=bit0 .. Sunday=bit6.
	return 1 << ((int(d) + 6) % 7)
}

// Covers reports whether the window applies at the local time t. A window
// that wraps midnight belongs to the day it started on: 23:00-05:00 on a
// Friday-only window covers Saturday 02:00 but not Friday 02:00.
func (w Window) Covers(t time.Time) bool {
	if !w.IsActive || w.MultiplierBPS <= 0 {
		return false
	}
	if t.Before(w.EffectiveFrom) || (w.EffectiveTo != nil && !t.Before(*w.EffectiveTo)) {
		return false
	}
	m := t.Hour()*60 + t.Minute()
	if w.StartMinute == w.EndMinute {
		return false
	}
	if w.StartMinute < w.EndMinute {
		return w.DaysOfWeek&DayBit(t.Weekday()) != 0 && m >= w.StartMinute && m < w.EndMinute
	}
	// Wraps midnight.
	if m >= w.StartMinute {
		return w.DaysOfWeek&DayBit(t.Weekday()) != 0
	}
	if m < w.EndMinute {
		return w.DaysOfWeek&DayBit(t.AddDate(0, 0, -1).Weekday()) != 0
	}
	return false
}

// SelectWindow returns the applicable window for a vehicle type at the local
// time t: the highest-priority active window covering t, ties broken by the
// larger multiplier then the name. Windows never stack. nil when none apply.
func SelectWindow(windows []Window, vehicleType string, t time.Time) *Window {
	var best *Window
	for i := range windows {
		w := &windows[i]
		if w.VehicleType != "" && w.VehicleType != vehicleType {
			continue
		}
		if !w.Covers(t) {
			continue
		}
		if best == nil || w.Priority > best.Priority ||
			(w.Priority == best.Priority && (w.MultiplierBPS > best.MultiplierBPS ||
				(w.MultiplierBPS == best.MultiplierBPS && w.Name < best.Name))) {
			best = w
		}
	}
	return best
}

// WindowExtraBPS is the extra over the base a window adds (12500 -> 2500).
func WindowExtraBPS(w *Window) int64 {
	if w == nil || w.MultiplierBPS <= NoChangeBPS {
		return 0
	}
	return w.MultiplierBPS - NoChangeBPS
}

// LocalTime converts t to the city's zone; an unknown zone falls back to
// Asia/Kolkata, the zone every launch city is in.
func LocalTime(t time.Time, zone string) time.Time {
	loc, err := time.LoadLocation(zone)
	if err != nil || zone == "" {
		loc, err = time.LoadLocation("Asia/Kolkata")
		if err != nil {
			loc = time.FixedZone("IST", 5*3600+1800)
		}
	}
	return t.In(loc)
}
