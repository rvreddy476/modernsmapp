package scylla

import (
	"fmt"
	"testing"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

// Audit H4: the reels/Tube reader must page a month until a short read
// before stepping to the previous month, or matches deep in a busy month
// are skipped forever.

// fakeBuckets is an in-memory home timeline: rows per bucket, newest first.
type fakeBuckets struct {
	rows  map[int][]FeedItem
	reads int
}

func (f *fakeBuckets) add(at time.Time, ct string) FeedItem {
	ts := gocql.UUIDFromTime(at)
	it := FeedItem{PostID: uuid.New(), CreatedAt: at, ContentType: ct, CursorToken: ts.String()}
	b := bucket(at)
	f.rows[b] = append(f.rows[b], it) // callers add newest first
	return it
}

func (f *fakeBuckets) read(bucketID int, before *gocql.UUID, n int) ([]FeedItem, error) {
	f.reads++
	var out []FeedItem
	for _, it := range f.rows[bucketID] {
		if before != nil && bucket(before.Time().UTC()) == bucketID && !it.CreatedAt.Before(before.Time()) {
			continue
		}
		out = append(out, it)
		if len(out) == n {
			break
		}
	}
	return out, nil
}

func TestCollectByContentTypes_PagesWithinAMonthBeforeMovingOn(t *testing.T) {
	f := &fakeBuckets{rows: map[int][]FeedItem{}}
	oct := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	reelAt := map[int]bool{10: true, 50: true, 350: true, 380: true}
	var want []string
	for i := 0; i < 400; i++ { // 400 October rows, newest first
		ct := "post"
		if reelAt[i] {
			ct = "reel"
		}
		it := f.add(oct.Add(-time.Duration(i)*time.Minute), ct)
		if ct == "reel" {
			want = append(want, it.PostID.String())
		}
	}
	sep := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		it := f.add(sep.Add(-time.Duration(i)*time.Hour), "reel")
		want = append(want, it.PostID.String())
	}

	types := map[string]struct{}{"reel": {}, "flick": {}}
	var got []string
	var before *gocql.UUID
	start := oct.Add(time.Hour)
	for page := 0; page < 20; page++ {
		items, err := collectByContentTypes(f.read, start, before, types, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			got = append(got, it.PostID.String())
		}
		if len(items) < 2 {
			break
		}
		last, _ := gocql.ParseUUID(items[len(items)-1].CursorToken)
		before = &last
		start = last.Time().UTC()
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("walked %d reels %v,\nwant %d %v: reels deep in October were skipped", len(got), got, len(want), want)
	}
}

func TestCollectByContentTypes_ReadsAreBounded(t *testing.T) {
	f := &fakeBuckets{rows: map[int][]FeedItem{}}
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 20000; i++ { // a month of nothing but posts
		f.add(now.Add(-time.Duration(i)*time.Second), "post")
	}
	items, err := collectByContentTypes(f.read, now.Add(time.Minute), nil, map[string]struct{}{"reel": {}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("found %d reels in a month of posts", len(items))
	}
	if f.reads > maxContentTypeReads {
		t.Fatalf("made %d bucket reads, bound is %d", f.reads, maxContentTypeReads)
	}
}
