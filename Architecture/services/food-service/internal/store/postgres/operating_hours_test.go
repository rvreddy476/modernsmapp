package postgres

import (
	"errors"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/atpost/food-service/database"
	"github.com/atpost/food-service/internal/onboarding"
	"github.com/atpost/shared/gst"
)

func dayPtr(d int) *int { return &d }

func hoursCode(err error) string {
	var fe *onboarding.FieldError
	if errors.As(err, &fe) {
		return fe.Code
	}
	if err != nil {
		return "OTHER:" + err.Error()
	}
	return ""
}

func TestNormalizeOperatingHours(t *testing.T) {
	cases := []struct {
		name string
		in   []OperatingHoursInput
		code string
	}{
		{"single window", []OperatingHoursInput{{DayOfWeek: dayPtr(1), OpensAt: "10:00", ClosesAt: "22:00"}}, ""},
		{"overnight window allowed", []OperatingHoursInput{{DayOfWeek: dayPtr(5), OpensAt: "18:00", ClosesAt: "02:00"}}, ""},
		{"split shift", []OperatingHoursInput{{DayOfWeek: dayPtr(2), OpensAt: "11:00", ClosesAt: "15:00"}, {DayOfWeek: dayPtr(2), OpensAt: "18:00", ClosesAt: "23:00"}}, ""},
		{"closed day", []OperatingHoursInput{{DayOfWeek: dayPtr(0), IsClosed: true}}, ""},
		{"empty set", nil, onboarding.CodeOperatingHoursRequired},
		{"day seven", []OperatingHoursInput{{DayOfWeek: dayPtr(7), OpensAt: "10:00", ClosesAt: "22:00"}}, onboarding.CodeOperatingHoursDayInvalid},
		{"negative day", []OperatingHoursInput{{DayOfWeek: dayPtr(-1), OpensAt: "10:00", ClosesAt: "22:00"}}, onboarding.CodeOperatingHoursDayInvalid},
		{"missing day", []OperatingHoursInput{{OpensAt: "10:00", ClosesAt: "22:00"}}, onboarding.CodeOperatingHoursDayInvalid},
		{"hour 24", []OperatingHoursInput{{DayOfWeek: dayPtr(1), OpensAt: "24:00", ClosesAt: "22:00"}}, onboarding.CodeOperatingHoursTime},
		{"single-digit hour", []OperatingHoursInput{{DayOfWeek: dayPtr(1), OpensAt: "9:00", ClosesAt: "22:00"}}, onboarding.CodeOperatingHoursTime},
		{"minute 60", []OperatingHoursInput{{DayOfWeek: dayPtr(1), OpensAt: "10:60", ClosesAt: "22:00"}}, onboarding.CodeOperatingHoursTime},
		{"missing close", []OperatingHoursInput{{DayOfWeek: dayPtr(1), OpensAt: "10:00"}}, onboarding.CodeOperatingHoursTime},
		{"overlap", []OperatingHoursInput{{DayOfWeek: dayPtr(3), OpensAt: "10:00", ClosesAt: "15:00"}, {DayOfWeek: dayPtr(3), OpensAt: "14:00", ClosesAt: "20:00"}}, onboarding.CodeOperatingHoursOverlap},
		{"overlap with overnight", []OperatingHoursInput{{DayOfWeek: dayPtr(3), OpensAt: "18:00", ClosesAt: "02:00"}, {DayOfWeek: dayPtr(3), OpensAt: "20:00", ClosesAt: "23:00"}}, onboarding.CodeOperatingHoursOverlap},
		{"closed and open same day", []OperatingHoursInput{{DayOfWeek: dayPtr(4), IsClosed: true}, {DayOfWeek: dayPtr(4), OpensAt: "10:00", ClosesAt: "22:00"}}, onboarding.CodeOperatingHoursConflict},
		{"too many windows", func() []OperatingHoursInput {
			var in []OperatingHoursInput
			for h := 0; h < 8; h++ {
				in = append(in, OperatingHoursInput{DayOfWeek: dayPtr(6), OpensAt: []string{"00:00", "03:00", "06:00", "09:00", "12:00", "15:00", "18:00", "21:00"}[h], ClosesAt: []string{"01:00", "04:00", "07:00", "10:00", "13:00", "16:00", "19:00", "22:00"}[h]})
			}
			return in
		}(), onboarding.CodeOperatingHoursTooMany},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeOperatingHours(tc.in)
			if got := hoursCode(err); got != tc.code {
				t.Fatalf("code = %q, want %q", got, tc.code)
			}
		})
	}
}

// TestNormalizedHoursFeedOpenAt proves the normaliser produces exactly what
// the Wave 0 serviceability check consumes: an overnight Friday window is
// open at 01:30 on Saturday.
func TestNormalizedHoursFeedOpenAt(t *testing.T) {
	windows, err := NormalizeOperatingHours([]OperatingHoursInput{
		{DayOfWeek: dayPtr(5), OpensAt: "18:00", ClosesAt: "02:00"},
		{DayOfWeek: dayPtr(0), IsClosed: true},
	})
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	sat := time.Date(2026, 9, 19, 1, 30, 0, 0, time.UTC) // Saturday
	if !openAt(sat, windows) {
		t.Fatalf("overnight window must be open after midnight")
	}
	if openAt(time.Date(2026, 9, 18, 17, 0, 0, 0, time.UTC), windows) {
		t.Fatalf("not open before the window")
	}
	view := hoursView(windows)
	if len(view) != 2 || view[0].DayOfWeek != 0 || !view[0].IsClosed || !view[1].Overnight || view[1].OpensAt != "18:00" || view[1].ClosesAt != "02:00" {
		t.Fatalf("view = %+v", view)
	}
}

// TestSetupSQLTaxCategoryCheckMatchesRateTable keeps the database CHECK and
// shared/gst from drifting: the CHECK lists exactly the restaurant categories.
func TestSetupSQLTaxCategoryCheckMatchesRateTable(t *testing.T) {
	re := regexp.MustCompile(`(?s)ck_food_restaurant_tax_category\s+CHECK\s*\(\s*tax_category IS NULL OR tax_category IN \(([^)]*)\)`)
	m := re.FindStringSubmatch(database.SetupSQL)
	if m == nil {
		t.Fatalf("setup.sql has no ck_food_restaurant_tax_category CHECK")
	}
	var got []string
	for _, part := range strings.Split(m[1], ",") {
		got = append(got, strings.Trim(strings.TrimSpace(part), "'"))
	}
	sort.Strings(got)
	var want []string
	for _, c := range onboarding.RestaurantTaxCategories(gst.DefaultRateTable()) {
		want = append(want, string(c))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("CHECK lists %v, rate table has %v", got, want)
	}
}
