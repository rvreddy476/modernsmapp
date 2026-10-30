package scylla

import (
	"errors"
	"testing"

	"github.com/gocql/gocql"
)

// fakeRows feeds the chart accumulators rows without a cluster and
// records how many were pulled, so the scan bound is observable.
type fakeRows struct {
	rows   [][]any
	pulled int
	err    error
}

func (f *fakeRows) Scan(dest ...interface{}) bool {
	if f.pulled >= len(f.rows) {
		return false
	}
	row := f.rows[f.pulled]
	f.pulled++
	for i, d := range dest {
		switch p := d.(type) {
		case *int64:
			*p = row[i].(int64)
		case *string:
			*p = row[i].(string)
		case *gocql.UUID:
			*p = row[i].(gocql.UUID)
		}
	}
	return true
}

func (f *fakeRows) Close() error { return f.err }

func watchedRows(ms ...int64) *fakeRows {
	f := &fakeRows{}
	for _, v := range ms {
		f.rows = append(f.rows, []any{v})
	}
	return f
}

// Below the scan limit the curve is exactly what it always was.
func TestRetentionCurveIsExactBelowTheLimit(t *testing.T) {
	rows := watchedRows(0, 1_500, 2_500, 2_900, 10_000)
	curve, sampled, err := retentionFromRows(rows, 1, 5, 100)
	if err != nil || sampled {
		t.Fatalf("err=%v sampled=%v", err, sampled)
	}
	want := []int64{5, 4, 3, 1, 1, 1}
	if len(curve) != len(want) {
		t.Fatalf("curve=%+v", curve)
	}
	for i, p := range curve {
		if p.SessionCount != want[i] || p.SecondOffset != int64(i) || p.RetainedRatio != float64(want[i])/5 {
			t.Fatalf("point %d = %+v want count %d", i, p, want[i])
		}
	}
}

// Audit M9: a huge partition is read only up to the limit (plus the one
// row that proves there was more), and the curve says it is sampled.
func TestRetentionCurveStopsAtTheLimit(t *testing.T) {
	ms := make([]int64, 1_000)
	for i := range ms {
		ms[i] = int64(i%10) * 1_000
	}
	rows := watchedRows(ms...)
	curve, sampled, err := retentionFromRows(rows, 1, 20, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !sampled {
		t.Fatal("a partition larger than the limit was not reported as sampled")
	}
	if rows.pulled != 101 {
		t.Fatalf("pulled %d rows, want 101 (the limit plus the one that proves more)", rows.pulled)
	}
	if curve[0].SessionCount != 100 {
		t.Fatalf("first point counts %d sessions, want the 100 sampled", curve[0].SessionCount)
	}
	// The ratios are the population's, because the sample is uniform here.
	if got := curve[5].RetainedRatio; got != 0.5 {
		t.Fatalf("retained at 5s = %v want 0.5", got)
	}

	// Exactly at the limit is not sampled.
	_, sampled, _ = retentionFromRows(watchedRows(ms[:100]...), 1, 20, 100)
	if sampled {
		t.Fatal("a partition of exactly the limit was reported as sampled")
	}
}

func TestRetentionCurveSurfacesAScanError(t *testing.T) {
	rows := watchedRows(1_000)
	rows.err = errors.New("timeout")
	if _, _, err := retentionFromRows(rows, 1, 5, 100); err == nil {
		t.Fatal("a failed scan was reported as a curve")
	}
}

func demographicRows(n int) *fakeRows {
	f := &fakeRows{}
	countries := []string{"IN", "IN", "US", ""}
	for i := 0; i < n; i++ {
		viewer := gocql.UUID{byte(i % 7)}
		f.rows = append(f.rows, []any{viewer, countries[i%len(countries)], "posttube"})
	}
	return f
}

func TestDemographicsAreExactBelowTheLimit(t *testing.T) {
	dem, err := demographicsFromRows(demographicRows(8), 10, 100)
	if err != nil {
		t.Fatal(err)
	}
	if dem.Sampled || dem.TotalSessions != 8 || dem.UniqueViewers != 7 {
		t.Fatalf("dem=%+v", dem)
	}
	if dem.TopCountries[0].Key != "IN" || dem.TopCountries[0].Count != 4 || dem.TopCountries[0].Share != 0.5 {
		t.Fatalf("top country %+v", dem.TopCountries[0])
	}
}

// Audit M9: demographics stop at the limit too, and say so.
func TestDemographicsStopAtTheLimit(t *testing.T) {
	rows := demographicRows(1_000)
	dem, err := demographicsFromRows(rows, 10, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !dem.Sampled || dem.TotalSessions != 100 || rows.pulled != 101 {
		t.Fatalf("sampled=%v total=%d pulled=%d, want true / 100 / 101", dem.Sampled, dem.TotalSessions, rows.pulled)
	}
	if dem.TopCountries[0].Share != 0.5 {
		t.Fatalf("sampled share %v want 0.5", dem.TopCountries[0].Share)
	}
	if _, err := demographicsFromRows(demographicRows(100), 10, 100); err != nil {
		t.Fatal(err)
	}
	exact, _ := demographicsFromRows(demographicRows(100), 10, 100)
	if exact.Sampled {
		t.Fatal("a partition of exactly the limit was reported as sampled")
	}
}
