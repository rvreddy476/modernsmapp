// Boot-time cleanup of duplicate open matches (founder decision, 2026-09-15:
// count first, never close silently).
//
// uq_dating_matches_open_pair allows one open match per pair. Databases that
// predate it may hold pairs with several open matches. At boot, before the
// index exists, ReconcileOpenMatchDuplicates:
//
//  1. counts duplicate groups and extra rows and logs both;
//  2. when there are extras, closes them only if the policy allows (always in
//     local/dev; elsewhere only with DATING_DEDUPE_OPEN_MATCHES=true),
//     otherwise refuses boot with the counts;
//  3. closes each extra through store.CloseMatchWithReason (closed_by the
//     system actor, close_reason 'duplicate'), publishing dating.match.closed
//     inside the close transaction so chat closes the conversation and a
//     failed publish leaves the row open for the next boot;
//  4. once the count is 0, ensures the unique index.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/atpost/dating-service/internal/store"
)

// OpenMatchDedupePolicy says whether boot may close duplicate open matches.
type OpenMatchDedupePolicy struct {
	// Env is the ENV value, for messages.
	Env string
	// LocalEnv is true for local/dev (datinghttp.IsLocalEnv).
	LocalEnv bool
	// FlagEnabled is DATING_DEDUPE_OPEN_MATCHES=true.
	FlagEnabled bool
}

// CloseAllowed is true in local/dev, or elsewhere when the flag is set.
func (p OpenMatchDedupePolicy) CloseAllowed() bool {
	return p.LocalEnv || p.FlagEnabled
}

// OpenMatchDedupeReport is what the boot step found and did.
type OpenMatchDedupeReport struct {
	Groups       int
	ExtraRows    int
	Closed       int
	IndexCreated bool
}

// ErrDuplicateOpenMatches is returned (wrapped, with the counts) when
// duplicates exist and the policy does not allow closing them.
var ErrDuplicateOpenMatches = errors.New("duplicate open dating matches exist")

// dedupeBatchSize bounds one pass of the close loop.
const dedupeBatchSize = 500

// ReconcileOpenMatchDuplicates runs the boot cleanup described in the file
// comment. It serialises across replicas with an advisory lock.
func (s *Service) ReconcileOpenMatchDuplicates(ctx context.Context, policy OpenMatchDedupePolicy) (OpenMatchDedupeReport, error) {
	var report OpenMatchDedupeReport
	unlock, err := s.store.LockOpenMatchDedupe(ctx)
	if err != nil {
		return report, err
	}
	defer unlock()

	counts, err := s.store.CountDuplicateOpenMatches(ctx)
	if err != nil {
		return report, err
	}
	report.Groups, report.ExtraRows = counts.Groups, counts.ExtraRows
	slog.Info("dating-service: duplicate open matches counted",
		"duplicate_groups", counts.Groups, "extra_rows", counts.ExtraRows, "env", policy.Env)

	if counts.ExtraRows > 0 {
		if !policy.CloseAllowed() {
			return report, fmt.Errorf("%w: %d pairs hold %d extra open matches (ENV=%q). "+
				"Review them with scripts/count-duplicate-matches.sql, then set DATING_DEDUPE_OPEN_MATCHES=true "+
				"for one deploy to close the extras (each emits dating.match.closed) and create %s; "+
				"set it back to false afterwards",
				ErrDuplicateOpenMatches, counts.Groups, counts.ExtraRows, policy.Env, store.OpenPairIndexName)
		}
		if s.producer == nil {
			return report, fmt.Errorf("%w: %d extra open matches, but no event producer is configured to announce the closures",
				ErrDuplicateOpenMatches, counts.ExtraRows)
		}
		closed, err := s.closeDuplicateOpenMatches(ctx)
		report.Closed = closed
		if err != nil {
			return report, err
		}
		after, err := s.store.CountDuplicateOpenMatches(ctx)
		if err != nil {
			return report, err
		}
		if after.ExtraRows > 0 {
			return report, fmt.Errorf("%w: %d extra open matches remain after closing %d",
				ErrDuplicateOpenMatches, after.ExtraRows, closed)
		}
		slog.Warn("dating-service: closed duplicate open matches",
			"closed", closed, "duplicate_groups", counts.Groups, "env", policy.Env)
	}

	created, err := s.store.EnsureOpenPairIndex(ctx)
	if err != nil {
		return report, err
	}
	report.IndexCreated = created
	if created {
		slog.Info("dating-service: created " + store.OpenPairIndexName)
	}
	return report, nil
}

// closeDuplicateOpenMatches closes every extra open match with the system
// actor and reason 'duplicate', one dating.match.closed per closed row.
func (s *Service) closeDuplicateOpenMatches(ctx context.Context) (int, error) {
	closed := 0
	for {
		extras, err := s.store.ListDuplicateOpenMatchExtras(ctx, dedupeBatchSize)
		if err != nil {
			return closed, err
		}
		if len(extras) == 0 {
			return closed, nil
		}
		progress := 0
		for _, m := range extras {
			_, err := s.store.CloseMatchWithReason(ctx, m.ID, store.SystemActorID, store.CloseReasonDuplicate,
				func(c *store.Match) error {
					if perr := s.producer.PublishMatchClosed(ctx, c.ID, store.SystemActorID, c.UserA, c.UserB); perr != nil {
						return fmt.Errorf("publish dating.match.closed for duplicate match %s: %w", c.ID, perr)
					}
					return nil
				})
			if errors.Is(err, store.ErrMatchNotFound) {
				continue // closed meanwhile, or no longer a duplicate
			}
			if err != nil {
				return closed, err
			}
			closed++
			progress++
		}
		if progress == 0 {
			return closed, nil
		}
	}
}
