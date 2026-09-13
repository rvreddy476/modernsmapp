package postgres

import (
	"context"
	"fmt"
	"sort"

	"github.com/atpost/food-service/internal/onboarding"
	"github.com/google/uuid"
)

// OperatingHoursInput is one requested window. DayOfWeek is a pointer so a
// missing day is refused rather than read as Sunday.
type OperatingHoursInput struct {
	DayOfWeek *int
	OpensAt   string
	ClosesAt  string
	IsClosed  bool
}

type OperatingHoursWindow struct {
	DayOfWeek int    `json:"day_of_week"`
	OpensAt   string `json:"opens_at"`
	ClosesAt  string `json:"closes_at"`
	IsClosed  bool   `json:"is_closed"`
	Overnight bool   `json:"overnight"`
}

type OperatingHours struct {
	RestaurantID uuid.UUID              `json:"restaurant_id"`
	Timezone     string                 `json:"timezone"`
	IsOpenNow    bool                   `json:"is_open_now"`
	Windows      []OperatingHoursWindow `json:"windows"`
}

const maxWindowsPerDay = 7

func hoursErr(code, field, msg string) error {
	return &onboarding.FieldError{Code: code, Field: field, Message: msg}
}

// parseClock reads strict "HH:MM" into seconds since midnight.
func parseClock(s string) (int, bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, false
	}
	for _, i := range []int{0, 1, 3, 4} {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	h := int(s[0]-'0')*10 + int(s[1]-'0')
	m := int(s[3]-'0')*10 + int(s[4]-'0')
	if h > 23 || m > 59 {
		return 0, false
	}
	return h*3600 + m*60, true
}

func formatClock(sec int) string {
	return fmt.Sprintf("%02d:%02d", sec/3600, (sec%3600)/60)
}

// windowEnd is where a window stops within its own day. closes <= opens is
// the overnight shape openAt understands, so it runs to midnight here.
func windowEnd(w hoursWindow) int {
	if w.Closes > w.Opens {
		return w.Closes
	}
	return 24 * 3600
}

// NormalizeOperatingHours validates a full replacement set and returns it in
// exactly the hoursWindow shape openAt (serviceability.go) consumes:
// overnight windows (closes <= opens) are allowed, several windows per day
// are allowed, a closed day may not also carry windows, and windows on one day
// may not overlap.
func NormalizeOperatingHours(in []OperatingHoursInput) ([]hoursWindow, error) {
	if len(in) == 0 {
		return nil, hoursErr(onboarding.CodeOperatingHoursRequired, "windows", "at least one window or closed day is required")
	}
	byDay := map[int][]hoursWindow{}
	closed := map[int]bool{}
	for i, w := range in {
		field := fmt.Sprintf("windows[%d]", i)
		if w.DayOfWeek == nil || *w.DayOfWeek < 0 || *w.DayOfWeek > 6 {
			return nil, hoursErr(onboarding.CodeOperatingHoursDayInvalid, field+".day_of_week", "day_of_week must be 0 (Sunday) to 6 (Saturday)")
		}
		day := *w.DayOfWeek
		if w.IsClosed {
			closed[day] = true
			continue
		}
		opens, ok1 := parseClock(w.OpensAt)
		closes, ok2 := parseClock(w.ClosesAt)
		if !ok1 || !ok2 {
			return nil, hoursErr(onboarding.CodeOperatingHoursTime, field, "opens_at and closes_at must be HH:MM (00:00-23:59)")
		}
		byDay[day] = append(byDay[day], hoursWindow{Day: day, Opens: opens, Closes: closes})
	}
	var out []hoursWindow
	for day := 0; day < 7; day++ {
		ws := byDay[day]
		if closed[day] && len(ws) > 0 {
			return nil, hoursErr(onboarding.CodeOperatingHoursConflict, "windows", fmt.Sprintf("day %d is marked closed and also has windows", day))
		}
		if len(ws) > maxWindowsPerDay {
			return nil, hoursErr(onboarding.CodeOperatingHoursTooMany, "windows", fmt.Sprintf("day %d has more than %d windows", day, maxWindowsPerDay))
		}
		sort.Slice(ws, func(i, j int) bool { return ws[i].Opens < ws[j].Opens })
		for j := 1; j < len(ws); j++ {
			if ws[j].Opens < windowEnd(ws[j-1]) {
				return nil, hoursErr(onboarding.CodeOperatingHoursOverlap, "windows", fmt.Sprintf("day %d has overlapping windows", day))
			}
		}
		if closed[day] {
			out = append(out, hoursWindow{Day: day, Closed: true})
		}
		out = append(out, ws...)
	}
	return out, nil
}

func hoursView(windows []hoursWindow) []OperatingHoursWindow {
	out := make([]OperatingHoursWindow, 0, len(windows))
	for _, w := range windows {
		if w.Closed {
			out = append(out, OperatingHoursWindow{DayOfWeek: w.Day, IsClosed: true})
			continue
		}
		out = append(out, OperatingHoursWindow{
			DayOfWeek: w.Day, OpensAt: formatClock(w.Opens), ClosesAt: formatClock(w.Closes),
			Overnight: w.Closes <= w.Opens,
		})
	}
	return out
}

// ReplaceOperatingHours swaps the restaurant's whole schedule in one
// transaction and reports whether the new schedule is open now, using the
// same openAt the order path uses.
func (s *Store) ReplaceOperatingHours(ctx context.Context, ownerID, restaurantID uuid.UUID, in []OperatingHoursInput) (*OperatingHours, error) {
	windows, err := NormalizeOperatingHours(in)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := lockOwnedRestaurantTx(ctx, tx, ownerID, restaurantID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM food.restaurant_operating_hours WHERE restaurant_id = $1`, restaurantID); err != nil {
		return nil, err
	}
	for _, w := range windows {
		opens, closes := formatClock(w.Opens), formatClock(w.Closes)
		if _, err := tx.Exec(ctx, `
			INSERT INTO food.restaurant_operating_hours (restaurant_id, day_of_week, opens_at, closes_at, is_closed)
			VALUES ($1, $2, $3::time, $4::time, $5)
		`, restaurantID, w.Day, opens, closes, w.Closed); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	loc := s.ordering.Location
	return &OperatingHours{
		RestaurantID: restaurantID,
		Timezone:     loc.String(),
		IsOpenNow:    openAt(s.ordering.Now().In(loc), windows),
		Windows:      hoursView(windows),
	}, nil
}
