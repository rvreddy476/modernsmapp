package cron

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeStore is the in-memory JobStore used by the runner unit tests.
type fakeStore struct {
	mu       sync.Mutex
	rows     map[uuid.UUID]*fakeRunRow
	running  map[string]bool // job -> still running
	startErr error
	endErr   error
}

type fakeRunRow struct {
	ID            uuid.UUID
	Job           string
	Status        string
	RowsProcessed int
	ErrSummary    string
	StartedAt     time.Time
	FinishedAt    time.Time
}

func newFakeStore() *fakeStore {
	return &fakeStore{rows: map[uuid.UUID]*fakeRunRow{}, running: map[string]bool{}}
}

func (f *fakeStore) StartCronRun(_ context.Context, job string) (uuid.UUID, error) {
	if f.startErr != nil {
		return uuid.Nil, f.startErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id := uuid.New()
	f.rows[id] = &fakeRunRow{ID: id, Job: job, Status: "running", StartedAt: time.Now()}
	f.running[job] = true
	return id, nil
}

func (f *fakeStore) FinishCronRun(_ context.Context, id uuid.UUID, rows int, jobErr error) error {
	if f.endErr != nil {
		return f.endErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[id]
	if !ok {
		return errors.New("not found")
	}
	r.FinishedAt = time.Now()
	r.RowsProcessed = rows
	if jobErr != nil {
		r.Status = "failed"
		r.ErrSummary = jobErr.Error()
	} else {
		r.Status = "succeeded"
	}
	delete(f.running, r.Job)
	return nil
}

func (f *fakeStore) HasRunningCronRun(_ context.Context, job string, _ time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running[job], nil
}

func (f *fakeStore) snapshot() []fakeRunRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeRunRow, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, *r)
	}
	return out
}

// TestRunner_RegisterJob_PanicOnEmptyName covers the boot-time guard.
func TestRunner_RegisterJob_PanicOnEmptyName(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on empty name")
		}
	}()
	r := NewRunner(newFakeStore(), nil)
	r.RegisterJob("", JobOptions{Interval: time.Second}, func(_ context.Context) (int, error) { return 0, nil })
}

// TestRunner_RegisterJob_PanicOnZeroInterval covers another guard.
func TestRunner_RegisterJob_PanicOnZeroInterval(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on zero interval")
		}
	}()
	r := NewRunner(newFakeStore(), nil)
	r.RegisterJob("x", JobOptions{Interval: 0}, func(_ context.Context) (int, error) { return 0, nil })
}

// TestRunner_HappyPath verifies the runner ticks once and records a
// 'succeeded' row.
func TestRunner_HappyPath(t *testing.T) {
	st := newFakeStore()
	r := NewRunner(st, nil)
	var calls atomic.Int32
	r.RegisterJob("happy", JobOptions{
		Interval: 30 * time.Millisecond, RunImmediately: true,
	}, func(_ context.Context) (int, error) {
		calls.Add(1)
		return 7, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	r.Run(ctx)
	rows := st.snapshot()
	if len(rows) == 0 {
		t.Fatalf("no run rows recorded")
	}
	if rows[0].Status != "succeeded" {
		t.Errorf("status = %q, want succeeded", rows[0].Status)
	}
	if rows[0].RowsProcessed != 7 {
		t.Errorf("rows_processed = %d, want 7", rows[0].RowsProcessed)
	}
	if calls.Load() == 0 {
		t.Errorf("job func never called")
	}
}

// TestRunner_ErrorPath checks failures land on the row.
func TestRunner_ErrorPath(t *testing.T) {
	st := newFakeStore()
	r := NewRunner(st, nil)
	r.RegisterJob("bad", JobOptions{
		Interval: 30 * time.Millisecond, RunImmediately: true,
	}, func(_ context.Context) (int, error) {
		return 0, errors.New("boom")
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	r.Run(ctx)
	rows := st.snapshot()
	if len(rows) == 0 {
		t.Fatalf("no run rows recorded")
	}
	if rows[0].Status != "failed" {
		t.Errorf("status = %q, want failed", rows[0].Status)
	}
	if rows[0].ErrSummary == "" {
		t.Errorf("error summary should be set on failure")
	}
}

// TestRunner_PanicRecover verifies a panicking job converts to a
// failed-status row rather than killing the runner.
func TestRunner_PanicRecover(t *testing.T) {
	st := newFakeStore()
	r := NewRunner(st, nil)
	r.RegisterJob("panicky", JobOptions{
		Interval: 30 * time.Millisecond, RunImmediately: true,
	}, func(_ context.Context) (int, error) {
		panic("oops")
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	r.Run(ctx)
	rows := st.snapshot()
	if len(rows) == 0 {
		t.Fatalf("no run rows recorded")
	}
	if rows[0].Status != "failed" {
		t.Errorf("panic should land as failed; got %q", rows[0].Status)
	}
}

// TestRunner_SkipIfRunning verifies the in-process re-entry guard. We
// construct a job that holds for longer than its interval and assert the
// extra ticks are dropped.
func TestRunner_SkipIfRunning(t *testing.T) {
	st := newFakeStore()
	r := NewRunner(st, nil)
	var calls atomic.Int32
	r.RegisterJob("slow", JobOptions{
		Interval:       10 * time.Millisecond,
		RunImmediately: true,
		SkipIfRunning:  true,
	}, func(_ context.Context) (int, error) {
		calls.Add(1)
		time.Sleep(120 * time.Millisecond)
		return 1, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(80 * time.Millisecond)
		cancel()
	}()
	r.Run(ctx)
	// Even though the ticker fires multiple times in 80ms, the in-flight
	// guard should let only one (or at most two if the first finished)
	// invocation through.
	if c := calls.Load(); c > 2 {
		t.Errorf("too many concurrent invocations: %d (re-entry guard failed)", c)
	}
}

// TestRunner_TimeoutEnforced verifies a job context cancels at Timeout.
func TestRunner_TimeoutEnforced(t *testing.T) {
	st := newFakeStore()
	r := NewRunner(st, nil)
	var sawDeadline atomic.Bool
	r.RegisterJob("tt", JobOptions{
		Interval:       30 * time.Millisecond,
		Timeout:        20 * time.Millisecond,
		RunImmediately: true,
	}, func(ctx context.Context) (int, error) {
		select {
		case <-ctx.Done():
			sawDeadline.Store(true)
			return 0, ctx.Err()
		case <-time.After(200 * time.Millisecond):
			return 0, nil
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(80 * time.Millisecond)
		cancel()
	}()
	r.Run(ctx)
	if !sawDeadline.Load() {
		t.Errorf("job context never hit deadline")
	}
}

// TestRunner_JobsExposed verifies Jobs() returns registered names.
func TestRunner_JobsExposed(t *testing.T) {
	r := NewRunner(newFakeStore(), nil)
	r.RegisterJob("a", JobOptions{Interval: time.Second}, func(_ context.Context) (int, error) { return 0, nil })
	r.RegisterJob("b", JobOptions{Interval: time.Second}, func(_ context.Context) (int, error) { return 0, nil })
	got := r.Jobs()
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("Jobs() = %v, want [a b]", got)
	}
}
