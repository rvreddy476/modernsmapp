// Package fieldbackfill re-seals chat content written before field
// encryption at rest (22 Oct 2026). Every target walks one table in key
// order, a batch at a time, sealing legacy (plaintext) values and leaving
// sealed ones alone, so a run can stop anywhere and a second run over the
// same rows changes nothing. Resume points live in
// chat.field_backfill_progress. Only counts are logged — never content,
// keys or row keys.
package fieldbackfill

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/atpost/chat-shared/fieldcrypt"
)

// StepResult is one batch of one target.
type StepResult struct {
	// Next is the resume point after this batch (opaque to the runner).
	Next string
	// Done means the walk reached the end of the table.
	Done bool
	// Scanned rows read; Sealed rows rewritten.
	Scanned, Sealed int
}

// Target walks one table.
type Target interface {
	Name() string
	// Step processes up to limit rows after resumeAfter ("" = the start).
	Step(ctx context.Context, c *fieldcrypt.Cipher, resumeAfter string, limit int) (StepResult, error)
}

// Progress stores resume points.
type Progress interface {
	LoadProgress(ctx context.Context, target string) (resumeAfter string, done bool, err error)
	SaveProgress(ctx context.Context, target, resumeAfter string, done bool, scanned, sealed int) error
}

// Options bound a run.
type Options struct {
	// Batch is the rows per step (default 500).
	Batch int
	// MaxBatches caps the steps per target in this run (0 = until done).
	MaxBatches int
	// Restart ignores saved progress and walks every target from the start.
	Restart bool
}

// TargetReport is the outcome for one target in this run.
type TargetReport struct {
	Name            string
	Batches         int
	Scanned, Sealed int
	Done            bool
}

// ErrNoKeyRing is returned when the cipher cannot seal: the backfill never
// runs in plaintext mode.
var ErrNoKeyRing = errors.New("fieldbackfill: refusing to run without a key ring")

// Run walks the targets in order.
func Run(ctx context.Context, c *fieldcrypt.Cipher, targets []Target, progress Progress, opts Options, log *slog.Logger) ([]TargetReport, error) {
	if !c.Enabled() {
		return nil, ErrNoKeyRing
	}
	if progress == nil {
		return nil, errors.New("fieldbackfill: a progress store is required")
	}
	if opts.Batch <= 0 {
		opts.Batch = 500
	}
	if log == nil {
		log = slog.Default()
	}
	var reports []TargetReport
	for _, t := range targets {
		rep := TargetReport{Name: t.Name()}
		resume, done := "", false
		if !opts.Restart {
			var err error
			if resume, done, err = progress.LoadProgress(ctx, t.Name()); err != nil {
				return reports, fmt.Errorf("%s: load progress: %w", t.Name(), err)
			}
		}
		if done {
			rep.Done = true
			reports = append(reports, rep)
			log.Info("fieldcrypt backfill: target already done", "target", t.Name())
			continue
		}
		for opts.MaxBatches == 0 || rep.Batches < opts.MaxBatches {
			if err := ctx.Err(); err != nil {
				return append(reports, rep), err
			}
			res, err := t.Step(ctx, c, resume, opts.Batch)
			if err != nil {
				return append(reports, rep), fmt.Errorf("%s: batch %d: %w", t.Name(), rep.Batches+1, err)
			}
			rep.Batches++
			rep.Scanned += res.Scanned
			rep.Sealed += res.Sealed
			if err := progress.SaveProgress(ctx, t.Name(), res.Next, res.Done, res.Scanned, res.Sealed); err != nil {
				return append(reports, rep), fmt.Errorf("%s: save progress: %w", t.Name(), err)
			}
			resume = res.Next
			if res.Done {
				rep.Done = true
				break
			}
		}
		log.Info("fieldcrypt backfill: target pass", "target", t.Name(), "batches", rep.Batches,
			"scanned", rep.Scanned, "sealed", rep.Sealed, "done", rep.Done)
		reports = append(reports, rep)
	}
	return reports, nil
}
