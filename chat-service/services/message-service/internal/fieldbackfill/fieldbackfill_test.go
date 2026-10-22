package fieldbackfill

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/atpost/chat-message-service/internal/contentcrypt"
	"github.com/atpost/chat-shared/fieldcrypt"
	"github.com/google/uuid"
)

// memTable is a fake store: rows keyed by an ordered integer, walked the way
// the real targets walk a table (key order, a batch at a time, compare-and-
// set writes through contentcrypt.Reseal).
type memTable struct {
	conv  uuid.UUID
	ids   map[int]uuid.UUID
	rows  map[int]*string
	steps int
	// mutateDuring runs between a batch's read and its writes (a live
	// request changing a row).
	mutateDuring func(m *memTable)
}

func newMemTable(n int) *memTable {
	m := &memTable{conv: uuid.New(), ids: map[int]uuid.UUID{}, rows: map[int]*string{}}
	for i := 1; i <= n; i++ {
		v := fmt.Sprintf("legacy message %d", i)
		if i%5 == 0 {
			v = ""
		}
		m.ids[i] = uuid.New()
		m.rows[i] = &v
	}
	return m
}

func (m *memTable) Name() string { return "mem.messages" }

func (m *memTable) binding(k int) []byte { return contentcrypt.MessageRow(m.conv, m.ids[k]) }

func (m *memTable) Step(ctx context.Context, c *fieldcrypt.Cipher, resumeAfter string, limit int) (StepResult, error) {
	m.steps++
	after := 0
	if resumeAfter != "" {
		after, _ = strconv.Atoi(resumeAfter)
	}
	var keys []int
	for k := range m.rows {
		if k > after {
			keys = append(keys, k)
		}
	}
	sort.Ints(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	read := map[int]*string{}
	for _, k := range keys {
		if v := m.rows[k]; v != nil {
			cp := *v
			read[k] = &cp
		}
	}
	if m.mutateDuring != nil {
		m.mutateDuring(m)
		m.mutateDuring = nil
	}
	res := StepResult{Next: resumeAfter, Scanned: len(keys), Done: len(keys) < limit}
	for _, k := range keys {
		out, changed, err := contentcrypt.Reseal(ctx, c, []contentcrypt.Field{{Scope: contentcrypt.ScopeMessageText, Row: m.binding(k), Value: read[k]}})
		if err != nil {
			return res, err
		}
		cur := m.rows[k]
		// Compare-and-set, like the real UPDATE ... IS NOT DISTINCT FROM.
		if changed && cur != nil && read[k] != nil && *cur == *read[k] {
			m.rows[k] = out[0]
			res.Sealed++
		}
		res.Next = strconv.Itoa(k)
	}
	return res, nil
}

type memProgress struct {
	resume map[string]string
	done   map[string]bool
	saves  int
}

func newMemProgress() *memProgress {
	return &memProgress{resume: map[string]string{}, done: map[string]bool{}}
}

func (p *memProgress) LoadProgress(_ context.Context, t string) (string, bool, error) {
	return p.resume[t], p.done[t], nil
}

func (p *memProgress) SaveProgress(_ context.Context, t, r string, d bool, _, _ int) error {
	p.saves++
	p.resume[t], p.done[t] = r, d
	return nil
}

func testCipher(t *testing.T) *fieldcrypt.Cipher {
	t.Helper()
	ring, err := fieldcrypt.ParseKeyList("v1:" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := fieldcrypt.New(ring)
	return c
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestBackfillRefusesToRunWithoutAKeyRing(t *testing.T) {
	m := newMemTable(3)
	for _, c := range []*fieldcrypt.Cipher{nil, fieldcrypt.Plaintext()} {
		if _, err := Run(context.Background(), c, []Target{m}, newMemProgress(), Options{}, quiet); !errors.Is(err, ErrNoKeyRing) {
			t.Fatalf("ran without a key ring: %v", err)
		}
	}
	if m.steps != 0 {
		t.Fatal("a target was walked without a key ring")
	}
}

func TestBackfillSealsEverythingAndASecondRunChangesNothing(t *testing.T) {
	ctx := context.Background()
	c := testCipher(t)
	m := newMemTable(23)
	rep, err := Run(ctx, c, []Target{m}, newMemProgress(), Options{Batch: 5}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if !rep[0].Done || rep[0].Scanned != 23 || rep[0].Sealed != 19 { // every 5th row is empty
		t.Fatalf("first run = %+v", rep[0])
	}
	snapshot := map[int]string{}
	for k, v := range m.rows {
		snapshot[k] = *v
		if *v == "" {
			continue
		}
		if !fieldcrypt.IsSealed(*v) || strings.Contains(*v, "legacy") {
			t.Fatalf("row %d still plaintext", k)
		}
		got, err := c.OpenString(ctx, contentcrypt.ScopeMessageText, m.binding(k), *v)
		if err != nil || got != fmt.Sprintf("legacy message %d", k) {
			t.Fatalf("row %d opens as %q, %v", k, got, err)
		}
	}
	// Idempotent: a full second pass from the start rewrites nothing.
	rep, err = Run(ctx, c, []Target{m}, newMemProgress(), Options{Batch: 5, Restart: true}, quiet)
	if err != nil || rep[0].Sealed != 0 || rep[0].Scanned != 23 {
		t.Fatalf("second run = %+v, %v", rep[0], err)
	}
	for k, v := range m.rows {
		if *v != snapshot[k] {
			t.Fatalf("row %d rewritten on the second run", k)
		}
	}
}

func TestBackfillResumesWhereItStopped(t *testing.T) {
	ctx := context.Background()
	c := testCipher(t)
	m := newMemTable(12)
	p := newMemProgress()
	rep, err := Run(ctx, c, []Target{m}, p, Options{Batch: 4, MaxBatches: 1}, quiet)
	if err != nil || rep[0].Done || rep[0].Scanned != 4 || p.resume[m.Name()] != "4" {
		t.Fatalf("first slice = %+v, resume=%q, %v", rep, p.resume[m.Name()], err)
	}
	rep, err = Run(ctx, c, []Target{m}, p, Options{Batch: 4}, quiet)
	if err != nil || !rep[0].Done || rep[0].Scanned != 8 {
		t.Fatalf("resumed run = %+v, %v (it must start after row 4)", rep, err)
	}
	steps := m.steps
	if rep, _ := Run(ctx, c, []Target{m}, p, Options{Batch: 4}, quiet); !rep[0].Done || m.steps != steps {
		t.Fatal("a finished target was walked again without -restart")
	}
}

// A row a live request changed between the batch's read and its write keeps
// the live value: the backfill never writes stale content back.
func TestBackfillNeverOverwritesAConcurrentChange(t *testing.T) {
	ctx := context.Background()
	c := testCipher(t)
	m := newMemTable(3)
	m.mutateDuring = func(m *memTable) {
		redacted := ""
		m.rows[2] = &redacted // an unsend landed mid-batch
	}
	if _, err := Run(ctx, c, []Target{m}, newMemProgress(), Options{Batch: 10}, quiet); err != nil {
		t.Fatal(err)
	}
	if *m.rows[2] != "" {
		t.Fatal("the backfill wrote the unsent text back")
	}
}
