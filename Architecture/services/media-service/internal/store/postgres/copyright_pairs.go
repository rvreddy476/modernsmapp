package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Pairs and the pair outbox (plan 6.1, 6.6, 9.1). A pair is media facts
// only. media_lo < media_hi by byte order. pair_revision bumps only when
// class, score, status or direction changes, and every revision writes
// exactly one outbox row in the same transaction. media_event_outbox is
// never touched by anything in this file.

// Invalidation reasons.
const (
	PairInvalidatedReprocessed = "reprocessed"
	PairInvalidatedDeleted     = "deleted"
	PairInvalidatedAlgoRetired = "algo_retired"
)

// Directions (a fact about upload time, never a verdict).
const (
	DirectionLoEarlier       = "lo_earlier"
	DirectionHiEarlier       = "hi_earlier"
	DirectionContemporaneous = "contemporaneous"
	DirectionAmbiguousLegacy = "ambiguous_legacy"
)

const (
	// ContemporaneousWithin: |Δ upload_confirmed_at| at or under this is
	// "contemporaneous".
	ContemporaneousWithin = 60 * time.Second
	// LegacyAmbiguousWithin: with a legacy_created_at side, |Δ| under this
	// is "ambiguous_legacy".
	LegacyAmbiguousWithin = 24 * time.Hour
)

// PairSide is one asset of a pair with its upload-time identity.
type PairSide struct {
	MediaID          uuid.UUID
	Generation       int64
	UploadConfirmed  *time.Time
	UploadTimeSource string
}

// UploadDirection applies the precedence rules to the lo and hi sides.
// A side with no upload time counts as legacy.
func UploadDirection(lo, hi PairSide) string {
	legacy := func(s PairSide) bool {
		return s.UploadConfirmed == nil || s.UploadTimeSource != UploadTimeConfirmed
	}
	if lo.UploadConfirmed == nil || hi.UploadConfirmed == nil {
		return DirectionAmbiguousLegacy
	}
	delta := hi.UploadConfirmed.Sub(*lo.UploadConfirmed)
	abs := delta
	if abs < 0 {
		abs = -abs
	}
	if (legacy(lo) || legacy(hi)) && abs < LegacyAmbiguousWithin {
		return DirectionAmbiguousLegacy
	}
	if abs <= ContemporaneousWithin {
		return DirectionContemporaneous
	}
	if delta > 0 {
		return DirectionLoEarlier
	}
	return DirectionHiEarlier
}

// PairScores are the verified numbers, as computed with Copy = the job's
// asset and Ref = the candidate.
type PairScores struct {
	Class               string
	RefCoverage         float64
	CopyCoverage        float64
	MatchedS            float64
	MatchedInformativeS float64
	MedianHamming       float64
	P90Hamming          float64
	Diversity           int
}

// PairWrite is one verified pair to upsert.
type PairWrite struct {
	Copy PairSide // the job's asset
	Ref  PairSide // the candidate
	Algo int
	PairScores
}

// canonical orders a write as (lo, hi) and puts ref/copy coverage in the
// stored orientation: ref is the earlier side when there is one, else lo.
// Both jobs of a pair therefore write the same numbers, so the second
// never bumps the revision over a flipped orientation.
func (w PairWrite) canonical() (lo, hi PairSide, direction string, refCov, copyCov float64) {
	lo, hi = w.Copy, w.Ref
	copyIsLo := true
	if bytes.Compare(w.Ref.MediaID[:], w.Copy.MediaID[:]) < 0 {
		lo, hi = w.Ref, w.Copy
		copyIsLo = false
	}
	direction = UploadDirection(lo, hi)
	// Coverage of lo and of hi, from the job's orientation.
	loCov, hiCov := w.CopyCoverage, w.RefCoverage
	if !copyIsLo {
		loCov, hiCov = w.RefCoverage, w.CopyCoverage
	}
	switch direction {
	case DirectionHiEarlier:
		return lo, hi, direction, hiCov, loCov
	default:
		return lo, hi, direction, loCov, hiCov
	}
}

// Observation is one shadow row: a verified candidate that is not
// actionable, or a truncation.
type Observation struct {
	Other           PairSide
	MinBandDistance int
	Truncated       bool
	PairScores
}

// FingerprintCompletion is the job's final write.
type FingerprintCompletion struct {
	Pairs        []PairWrite
	Observations []Observation
	Truncation   json.RawMessage
}

// CompleteFingerprintJob writes pairs (with their outbox rows), shadow
// observations and the job's 'done' status in one fenced transaction.
func (s *MediaAssetStore) CompleteFingerprintJob(ctx context.Context, key FingerprintJobKey, token uuid.UUID, c FingerprintCompletion) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin complete fingerprint: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fingerprintFenceTx(ctx, tx, key, token); err != nil {
		return err
	}
	for _, p := range c.Pairs {
		if _, _, err := upsertPairTx(ctx, tx, p); err != nil {
			return err
		}
	}
	for _, o := range c.Observations {
		if _, err := tx.Exec(ctx, `
			INSERT INTO copyright_match_observations
			       (media_asset_id, media_generation, algo_version, other_media_id, other_generation, class,
			        ref_coverage, copy_coverage, matched_s, matched_informative_s, median_hamming, p90_hamming,
			        diversity, min_band_distance, truncated, computed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, NOW())
			ON CONFLICT (media_asset_id, media_generation, algo_version, other_media_id, other_generation) DO UPDATE
			   SET class = EXCLUDED.class, ref_coverage = EXCLUDED.ref_coverage, copy_coverage = EXCLUDED.copy_coverage,
			       matched_s = EXCLUDED.matched_s, matched_informative_s = EXCLUDED.matched_informative_s,
			       median_hamming = EXCLUDED.median_hamming, p90_hamming = EXCLUDED.p90_hamming,
			       diversity = EXCLUDED.diversity, min_band_distance = EXCLUDED.min_band_distance,
			       truncated = EXCLUDED.truncated, computed_at = NOW()
		`, key.MediaID, key.Generation, key.Algo, o.Other.MediaID, o.Other.Generation, o.Class,
			o.RefCoverage, o.CopyCoverage, o.MatchedS, o.MatchedInformativeS, o.MedianHamming, o.P90Hamming,
			o.Diversity, o.MinBandDistance, o.Truncated); err != nil {
			return fmt.Errorf("insert observation: %w", err)
		}
	}
	var truncation any
	if len(c.Truncation) > 0 {
		truncation = c.Truncation
	}
	tag, err := tx.Exec(ctx, `
		UPDATE media_fingerprint_jobs
		   SET status = 'done', claim_token = NULL, truncation = $5::jsonb, last_error = NULL, updated_at = NOW()
		 WHERE media_asset_id = $1 AND media_generation = $2 AND algo_version = $3
		   AND status = 'claimed' AND claim_token = $4
	`, key.MediaID, key.Generation, key.Algo, token, truncation)
	if err != nil {
		return fmt.Errorf("mark fingerprint job done: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrFingerprintClaimLost
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit complete fingerprint: %w", err)
	}
	return nil
}

// scoresEqual compares the numbers that decide a revision, to three
// decimals, so float noise between two computations of the same pair does
// not bump the revision.
func scoresEqual(a, b [6]float64, da, db int) bool {
	for i := range a {
		if math.Round(a[i]*1000) != math.Round(b[i]*1000) {
			return false
		}
	}
	return da == db
}

// upsertPairTx inserts or revises one pair and writes its outbox row.
// Returns the pair's revision and whether anything changed.
func upsertPairTx(ctx context.Context, tx pgx.Tx, w PairWrite) (int64, bool, error) {
	lo, hi, direction, refCov, copyCov := w.canonical()
	if w.Class == "" {
		return 0, false, fmt.Errorf("upsert pair: class is required")
	}
	var (
		pairID   uuid.UUID
		revision int64
		status   string
		oldClass string
		oldDir   string
		oldNums  [6]float64
		oldDiv   int
	)
	err := tx.QueryRow(ctx, `
		SELECT pair_id, pair_revision, status, class, direction,
		       COALESCE(ref_coverage, 0), COALESCE(copy_coverage, 0), COALESCE(matched_s, 0),
		       COALESCE(matched_informative_s, 0), COALESCE(median_hamming, 0), COALESCE(p90_hamming, 0),
		       COALESCE(diversity, 0)
		  FROM copyright_pairs
		 WHERE media_lo = $1 AND gen_lo = $2 AND media_hi = $3 AND gen_hi = $4 AND algo_version = $5
		 FOR UPDATE
	`, lo.MediaID, lo.Generation, hi.MediaID, hi.Generation, w.Algo).Scan(
		&pairID, &revision, &status, &oldClass, &oldDir,
		&oldNums[0], &oldNums[1], &oldNums[2], &oldNums[3], &oldNums[4], &oldNums[5], &oldDiv)
	newNums := [6]float64{refCov, copyCov, w.MatchedS, w.MatchedInformativeS, w.MedianHamming, w.P90Hamming}
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		pairID = uuid.New()
		revision = 1
		if _, err := tx.Exec(ctx, `
			INSERT INTO copyright_pairs
			       (pair_id, media_lo, gen_lo, media_hi, gen_hi, algo_version, pair_revision, status, direction, class,
			        ref_coverage, copy_coverage, matched_s, matched_informative_s, median_hamming, p90_hamming, diversity, computed_at)
			VALUES ($1, $2, $3, $4, $5, $6, 1, 'active', $7, $8, $9, $10, $11, $12, $13, $14, $15, NOW())
		`, pairID, lo.MediaID, lo.Generation, hi.MediaID, hi.Generation, w.Algo, direction, w.Class,
			refCov, copyCov, w.MatchedS, w.MatchedInformativeS, w.MedianHamming, w.P90Hamming, w.Diversity); err != nil {
			return 0, false, fmt.Errorf("insert pair: %w", err)
		}
	case err != nil:
		return 0, false, fmt.Errorf("lock pair: %w", err)
	default:
		if status == "active" && oldClass == w.Class && oldDir == direction && scoresEqual(oldNums, newNums, oldDiv, w.Diversity) {
			return revision, false, nil
		}
		revision++
		if _, err := tx.Exec(ctx, `
			UPDATE copyright_pairs
			   SET pair_revision = $2, status = 'active', invalidated_reason = NULL, direction = $3, class = $4,
			       ref_coverage = $5, copy_coverage = $6, matched_s = $7, matched_informative_s = $8,
			       median_hamming = $9, p90_hamming = $10, diversity = $11, computed_at = NOW()
			 WHERE pair_id = $1
		`, pairID, revision, direction, w.Class, refCov, copyCov, w.MatchedS, w.MatchedInformativeS,
			w.MedianHamming, w.P90Hamming, w.Diversity); err != nil {
			return 0, false, fmt.Errorf("revise pair: %w", err)
		}
	}
	payload := sharedevents.MediaFingerprintPairPayload{
		PairID: pairID.String(), PairRevision: revision,
		MediaLo: lo.MediaID.String(), GenLo: lo.Generation,
		MediaHi: hi.MediaID.String(), GenHi: hi.Generation,
		AlgoVersion: w.Algo, Status: "active", Class: w.Class, Direction: direction,
		RefCoverage: refCov, CopyCoverage: copyCov, MatchedInformativeS: w.MatchedInformativeS,
		MedianHamming: w.MedianHamming, Diversity: w.Diversity,
	}
	if err := insertPairOutboxTx(ctx, tx, sharedevents.MediaFingerprintPairFound, payload); err != nil {
		return 0, false, err
	}
	return revision, true, nil
}

// insertPairOutboxTx writes one outbox row for one pair revision. The
// event id is minted here and lives in the payload too, so a relay that
// publishes twice publishes the same id.
func insertPairOutboxTx(ctx context.Context, tx pgx.Tx, eventType string, payload sharedevents.MediaFingerprintPairPayload) error {
	eventID := uuid.New()
	payload.EventID = eventID.String()
	payload.OccurredAt = time.Now().UTC()
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal pair event: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO copyright_pair_outbox (event_id, pair_id, pair_revision, event_type, payload)
		VALUES ($1, $2, $3, $4, $5::jsonb)
	`, eventID, payload.PairID, payload.PairRevision, eventType, body); err != nil {
		return fmt.Errorf("insert pair outbox row: %w", err)
	}
	return nil
}

// invalidatePairsForMediaTx marks every active pair on either side of the
// asset invalidated (revision bumped) and writes one outbox row per pair.
// Returns how many pairs it invalidated.
func invalidatePairsForMediaTx(ctx context.Context, tx pgx.Tx, mediaID uuid.UUID, reason string) (int, error) {
	rows, err := tx.Query(ctx, `
		UPDATE copyright_pairs
		   SET status = 'invalidated', invalidated_reason = $2, pair_revision = pair_revision + 1
		 WHERE (media_lo = $1 OR media_hi = $1) AND status = 'active'
		 RETURNING pair_id, pair_revision, media_lo, gen_lo, media_hi, gen_hi, algo_version, class, direction,
		           COALESCE(ref_coverage, 0), COALESCE(copy_coverage, 0), COALESCE(matched_informative_s, 0),
		           COALESCE(median_hamming, 0), COALESCE(diversity, 0)
	`, mediaID, reason)
	if err != nil {
		return 0, fmt.Errorf("invalidate pairs for %s: %w", mediaID, err)
	}
	var payloads []sharedevents.MediaFingerprintPairPayload
	for rows.Next() {
		var p sharedevents.MediaFingerprintPairPayload
		var pairID, lo, hi uuid.UUID
		if err := rows.Scan(&pairID, &p.PairRevision, &lo, &p.GenLo, &hi, &p.GenHi, &p.AlgoVersion, &p.Class, &p.Direction,
			&p.RefCoverage, &p.CopyCoverage, &p.MatchedInformativeS, &p.MedianHamming, &p.Diversity); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan invalidated pair: %w", err)
		}
		p.PairID, p.MediaLo, p.MediaHi = pairID.String(), lo.String(), hi.String()
		p.Status, p.InvalidatedReason = "invalidated", reason
		payloads = append(payloads, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("invalidate pairs for %s: %w", mediaID, err)
	}
	for _, p := range payloads {
		if err := insertPairOutboxTx(ctx, tx, sharedevents.MediaFingerprintPairInvalidated, p); err != nil {
			return 0, err
		}
	}
	return len(payloads), nil
}

// purgeCopyrightDataForUploaderTx runs purgeCopyrightDataTx for every asset
// the user uploaded (the account purge).
func purgeCopyrightDataForUploaderTx(ctx context.Context, tx pgx.Tx, uploaderID uuid.UUID) error {
	ok, err := tableExists(ctx, tx, "copyright_pairs")
	if err != nil || !ok {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM media_assets WHERE uploader_id = $1`, uploaderID)
	if err != nil {
		return fmt.Errorf("list assets for copyright purge: %w", err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := purgeCopyrightDataTx(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}

// InvalidatePairsForMedia is invalidatePairsForMediaTx in its own
// transaction (operator tooling, tests).
func (s *MediaAssetStore) InvalidatePairsForMedia(ctx context.Context, mediaID uuid.UUID, reason string) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin invalidate pairs: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	n, err := invalidatePairsForMediaTx(ctx, tx, mediaID, reason)
	if err != nil {
		return 0, err
	}
	return n, tx.Commit(ctx)
}

// purgeCopyrightDataTx is the asset-purge hook (plan 6.1, "Purge"): write
// the invalidations first, then delete the derived rows. The pair outbox is
// left alone: it carries no PII and an unpublished invalidation must
// survive the deletion. Tables are probed so a database that has not run
// migration 023 yet purges cleanly.
func purgeCopyrightDataTx(ctx context.Context, tx pgx.Tx, mediaID uuid.UUID) error {
	ok, err := tableExists(ctx, tx, "copyright_pairs")
	if err != nil || !ok {
		return err
	}
	if _, err := invalidatePairsForMediaTx(ctx, tx, mediaID, PairInvalidatedDeleted); err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM copyright_pairs WHERE media_lo = $1 OR media_hi = $1`,
		`DELETE FROM copyright_match_observations WHERE media_asset_id = $1 OR other_media_id = $1`,
		`DELETE FROM copyright_anchor_postings WHERE media_asset_id = $1`,
		`DELETE FROM copyright_fingerprints WHERE media_asset_id = $1`,
		`DELETE FROM media_fingerprint_jobs WHERE media_asset_id = $1`,
	} {
		if _, err := tx.Exec(ctx, q, mediaID); err != nil {
			return fmt.Errorf("purge copyright data for %s: %w", mediaID, err)
		}
	}
	return nil
}
