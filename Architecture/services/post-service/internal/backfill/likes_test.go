package backfill

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	start = time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	post1 = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	post2 = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	user1 = uuid.MustParse("33333333-3333-4333-8333-333333333333")
)

func TestNormalize(t *testing.T) {
	old := time.Date(2026, 9, 1, 12, 0, 0, 0, time.FixedZone("IST", 19800))
	cases := []struct {
		name, post, user, reaction string
		at                         time.Time
		skip, wantType             string
		wantAt                     time.Time
	}{
		{"like keeps its time", post1.String(), user1.String(), "like", old, "", "like", old.UTC()},
		{"unknown name is a like", post1.String(), user1.String(), "heart", old, "", "like", old.UTC()},
		{"known emoji kept", post1.String(), user1.String(), "Spark", old, "", "spark", old.UTC()},
		{"missing time is the run start", post1.String(), user1.String(), "like", time.Time{}, "", "like", start},
		{"dislike never copied", post1.String(), user1.String(), "tune", old, SkipDislike, "", time.Time{}},
		{"dislike any case", post1.String(), user1.String(), " TUNE ", old, SkipDislike, "", time.Time{}},
		{"bad post id", "not-a-uuid", user1.String(), "like", old, SkipBadPostID, "", time.Time{}},
		{"nil post id", uuid.Nil.String(), user1.String(), "like", old, SkipBadPostID, "", time.Time{}},
		{"bad user id", post1.String(), "", "like", old, SkipBadUserID, "", time.Time{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			row, skip := Normalize(c.post, c.user, c.reaction, c.at, start)
			if skip != c.skip {
				t.Fatalf("skip = %q, want %q", skip, c.skip)
			}
			if skip != "" {
				return
			}
			if row.Reaction != c.wantType || !row.CreatedAt.Equal(c.wantAt) || row.CreatedAt.Location() != time.UTC {
				t.Fatalf("row = %+v, want type %q at %v UTC", row, c.wantType, c.wantAt)
			}
		})
	}
}

type fakeRow struct {
	post, user, reaction string
	at                   time.Time
}

type fakeSource struct {
	name     string
	rows     []fakeRow
	i        int
	closeErr error
	closed   bool
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) Next() (string, string, string, time.Time, bool) {
	if f.i >= len(f.rows) {
		return "", "", "", time.Time{}, false
	}
	r := f.rows[f.i]
	f.i++
	return r.post, r.user, r.reaction, r.at, true
}
func (f *fakeSource) Close() error { f.closed = true; return f.closeErr }

// fakeWriter knows post1 only; a (post,user) pair it has seen is present.
type fakeWriter struct {
	present map[[2]uuid.UUID]bool
	batches []int
	applied []bool
	err     error
}

func (w *fakeWriter) WriteBatch(_ context.Context, rows []Row, apply bool) (int64, int64, error) {
	if w.err != nil {
		return 0, 0, w.err
	}
	w.batches = append(w.batches, len(rows))
	w.applied = append(w.applied, apply)
	var known, inserted int64
	for _, r := range rows {
		if r.PostID != post1 {
			continue
		}
		known++
		k := [2]uuid.UUID{r.PostID, r.UserID}
		if !w.present[k] {
			inserted++
			if apply {
				w.present[k] = true
			}
		}
	}
	return known, inserted, nil
}

func TestRunCountsAndBatches(t *testing.T) {
	user2 := uuid.New()
	src := &fakeSource{name: "reactions_by_post", rows: []fakeRow{
		{post1.String(), user1.String(), "like", start},          // already present
		{post1.String(), user2.String(), "like", start},          // inserted
		{post2.String(), user1.String(), "like", start},          // post gone
		{post1.String(), uuid.NewString(), "tune", start},        // dislike
		{"garbage", user1.String(), "like", start},               // bad id
		{post1.String(), uuid.NewString(), "heart", time.Time{}}, // inserted
	}}
	w := &fakeWriter{present: map[[2]uuid.UUID]bool{{post1, user1}: true}}

	rep, err := Run(context.Background(), []Source{src}, w, true, 2, start)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Read != 6 || rep.Inserted != 2 || rep.Existing != 1 || rep.MissingPost != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if rep.Skipped[SkipDislike] != 1 || rep.Skipped[SkipBadPostID] != 1 {
		t.Fatalf("skipped = %v", rep.Skipped)
	}
	if got := rep.PerSource["reactions_by_post"]["inserted"]; got != 2 {
		t.Fatalf("per-source inserted = %d", got)
	}
	// 4 copyable rows in batches of 2.
	if len(w.batches) != 2 || w.batches[0] != 2 || w.batches[1] != 2 {
		t.Fatalf("batches = %v", w.batches)
	}
	if !src.closed {
		t.Fatal("source not closed")
	}
}

func TestRunDryRunPassesApplyFalse(t *testing.T) {
	src := &fakeSource{name: "s", rows: []fakeRow{{post1.String(), user1.String(), "like", start}}}
	w := &fakeWriter{present: map[[2]uuid.UUID]bool{}}
	rep, err := Run(context.Background(), []Source{src}, w, false, 10, start)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Applied || len(w.applied) != 1 || w.applied[0] {
		t.Fatalf("dry run must write with apply=false; report applied=%v writes=%v", rep.Applied, w.applied)
	}
	if rep.Inserted != 1 {
		t.Fatalf("dry run should still report what it would insert, got %d", rep.Inserted)
	}
}

func TestRunStopsOnErrors(t *testing.T) {
	src := &fakeSource{name: "s", rows: []fakeRow{{post1.String(), user1.String(), "like", start}}}
	if _, err := Run(context.Background(), []Source{src}, &fakeWriter{err: errors.New("db down")}, true, 10, start); err == nil {
		t.Fatal("write error must fail the run")
	}
	if !src.closed {
		t.Fatal("source must be closed on a write error")
	}
	bad := &fakeSource{name: "s", closeErr: errors.New("scylla page failed")}
	if _, err := Run(context.Background(), []Source{bad}, &fakeWriter{present: map[[2]uuid.UUID]bool{}}, true, 10, start); err == nil {
		t.Fatal("a read error reported at Close must fail the run")
	}
}
