package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestCronRun_HappyPath_StartFinish ensures one row goes from running to
// succeeded with the right rows_processed.
func TestCronRun_HappyPath_StartFinish(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	id, err := st.StartCronRun(ctx, "test-job-happy")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := st.FinishCronRun(ctx, id, 42, nil); err != nil {
		t.Fatalf("finish: %v", err)
	}
	row, err := st.GetCronRun(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row.Status != "succeeded" {
		t.Errorf("status = %q, want succeeded", row.Status)
	}
	if row.RowsProcessed != 42 {
		t.Errorf("rows = %d, want 42", row.RowsProcessed)
	}
	if row.FinishedAt == nil {
		t.Errorf("finished_at not set")
	}
}

// TestCronRun_FailureRecordsErrSummary verifies error-path writes summary.
func TestCronRun_FailureRecordsErrSummary(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	id, err := st.StartCronRun(ctx, "test-job-fail")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := st.FinishCronRun(ctx, id, 0, errors.New("boom DSN unreachable")); err != nil {
		t.Fatalf("finish: %v", err)
	}
	row, _ := st.GetCronRun(ctx, id)
	if row.Status != "failed" {
		t.Errorf("status = %q, want failed", row.Status)
	}
	if row.ErrorSummary == nil || *row.ErrorSummary == "" {
		t.Errorf("error_summary missing on failure row")
	}
}

// TestCronRun_HasRunningCronRun_Idempotency is the load-bearing test for
// the cron framework's advisory-lock semantics.
func TestCronRun_HasRunningCronRun_Idempotency(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	id1, err := st.StartCronRun(ctx, "test-job-busy")
	if err != nil {
		t.Fatalf("start1: %v", err)
	}
	busy, err := st.HasRunningCronRun(ctx, "test-job-busy", time.Hour)
	if err != nil || !busy {
		t.Fatalf("expected busy=true, got %v err=%v", busy, err)
	}
	// Finish; busy must become false.
	if err := st.FinishCronRun(ctx, id1, 0, nil); err != nil {
		t.Fatalf("finish: %v", err)
	}
	busy, err = st.HasRunningCronRun(ctx, "test-job-busy", time.Hour)
	if err != nil || busy {
		t.Fatalf("expected busy=false after finish, got %v err=%v", busy, err)
	}
}

// TestCronRun_HasRunningCronRun_LookbackHonored ensures an old row no
// longer counts as 'running' once we step beyond the lookback.
func TestCronRun_HasRunningCronRun_LookbackHonored(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := st.StartCronRun(ctx, "test-job-lookback"); err != nil {
		t.Fatalf("start: %v", err)
	}
	// 1 ns lookback → the row is "older than the lookback" so we report
	// not-busy. (This is the semantics that lets a stuck job retry after
	// the configured MaxRunningAge passes.)
	busy, err := st.HasRunningCronRun(ctx, "test-job-lookback", 1*time.Nanosecond)
	if err != nil {
		t.Fatalf("has running: %v", err)
	}
	if busy {
		t.Errorf("with 1ns lookback we should NOT consider an existing run busy")
	}
}

// TestCronRun_ListCronRuns_ByJob validates the filter.
func TestCronRun_ListCronRuns_ByJob(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		id, _ := st.StartCronRun(ctx, "list-test-a")
		_ = st.FinishCronRun(ctx, id, i, nil)
	}
	id, _ := st.StartCronRun(ctx, "list-test-b")
	_ = st.FinishCronRun(ctx, id, 99, nil)

	got, err := st.ListCronRuns(ctx, CronRunFilter{Job: "list-test-a", Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) < 3 {
		t.Errorf("expected >=3 rows for list-test-a, got %d", len(got))
	}
	for _, r := range got {
		if r.Job != "list-test-a" {
			t.Errorf("filter leaked: got job %q", r.Job)
		}
	}
}

// TestCronRun_StartCronRun_RequiresJob covers the input guard.
func TestCronRun_StartCronRun_RequiresJob(t *testing.T) {
	st, cleanup := riderTestStore(t)
	defer cleanup()
	if _, err := st.StartCronRun(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty job name")
	}
}
