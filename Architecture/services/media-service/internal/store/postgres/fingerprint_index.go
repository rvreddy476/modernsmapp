package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/media-service/internal/fingerprint"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The index lookup (plan 12.3): for every duration bucket in range and
// every band, the postings whose band value is in the probe set, with a
// per-key cap, a hot-key stop-list, a per-upload row cap and a statement
// timeout. Stale-generation postings are dropped before anything votes on
// them (T11-3), by checking every candidate (asset, generation) against
// the live media row.

// LookupParams is one lookup.
type LookupParams struct {
	Algo    int
	Buckets []int
	Probes  fingerprint.ProbeSet
	// ExcludeMedia is the querying asset: its own postings are already in
	// the index (insert-then-query) and must not match themselves.
	ExcludeMedia uuid.UUID
	// PerKeyCap: a key returning this many rows is hot and dropped whole.
	PerKeyCap int
	// MaxRows: the per-upload cap; past it the lookup is truncated.
	MaxRows int
	// StatementTimeout bounds each query.
	StatementTimeout time.Duration
}

// DefaultLookupParams are the plan's numbers.
func DefaultLookupParams() LookupParams {
	return LookupParams{
		Algo:             FingerprintAlgoVersion,
		PerKeyCap:        257,
		MaxRows:          2_000_000,
		StatementTimeout: 5 * time.Second,
	}
}

// LookupHit is one posting returned by the lookup.
type LookupHit struct {
	MediaID    uuid.UUID
	Generation int64
	AnchorMs   int32
	Hash       uint64
}

// LookupResult is what the lookup produced, with every truncation recorded.
type LookupResult struct {
	Hits           []LookupHit
	RowsScanned    int
	HotKeysDropped int
	StaleDropped   int
	Truncated      bool
	TruncationKind string // lookup_truncated | hot_key
}

// LookupAnchorCandidates runs the probes.
func (s *MediaAssetStore) LookupAnchorCandidates(ctx context.Context, p LookupParams) (*LookupResult, error) {
	if p.PerKeyCap <= 0 || p.MaxRows <= 0 {
		return nil, fmt.Errorf("lookup: caps are required")
	}
	res := &LookupResult{}
	type keyHits struct {
		hits []LookupHit
	}
	var raw []LookupHit
	for _, bucket := range p.Buckets {
		for band := 0; band < fingerprint.Bands; band++ {
			values := p.Probes[band]
			if len(values) == 0 {
				continue
			}
			vals := make([]int32, len(values))
			for i, v := range values {
				vals[i] = int32(v)
			}
			rows, err := s.lookupBand(ctx, p, bucket, band, vals)
			if err != nil {
				return nil, err
			}
			// Group by key; a key at the cap is dropped entirely.
			perKey := map[int32]*keyHits{}
			for _, r := range rows {
				k := perKey[r.value]
				if k == nil {
					k = &keyHits{}
					perKey[r.value] = k
				}
				k.hits = append(k.hits, r.hit)
			}
			for _, k := range perKey {
				res.RowsScanned += len(k.hits)
				if len(k.hits) >= p.PerKeyCap {
					res.HotKeysDropped++
					continue
				}
				raw = append(raw, k.hits...)
			}
			if res.RowsScanned > p.MaxRows {
				res.Truncated = true
				res.TruncationKind = "lookup_truncated"
				return s.finishLookup(ctx, res, raw)
			}
		}
	}
	if res.HotKeysDropped > 0 && res.TruncationKind == "" {
		res.TruncationKind = "hot_key"
	}
	return s.finishLookup(ctx, res, raw)
}

type bandRow struct {
	value int32
	hit   LookupHit
}

func (s *MediaAssetStore) lookupBand(ctx context.Context, p LookupParams, bucket, band int, values []int32) ([]bandRow, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin lookup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if p.StatementTimeout > 0 {
		if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", p.StatementTimeout.Milliseconds())); err != nil {
			return nil, fmt.Errorf("lookup statement timeout: %w", err)
		}
	}
	// LATERAL with LIMIT cap: a key that fills the cap is reported as hot
	// by the caller and dropped. The hot stop-list is applied first.
	rows, err := tx.Query(ctx, `
		SELECT v.band_value, h.media_asset_id, h.media_generation, h.anchor_ms, h.hash
		  FROM unnest($4::int[]) AS v(band_value)
		 CROSS JOIN LATERAL (
		     SELECT p.media_asset_id, p.media_generation, p.anchor_ms, p.hash
		       FROM copyright_anchor_postings p
		      WHERE p.algo_version = $1 AND p.dur_bucket = $2 AND p.band_no = $3
		        AND p.band_value = v.band_value
		        AND p.media_asset_id <> $6
		      LIMIT $5) AS h
		 WHERE NOT EXISTS (SELECT 1 FROM copyright_band_hot hot
		                    WHERE hot.algo_version = $1 AND hot.dur_bucket = $2
		                      AND hot.band_no = $3 AND hot.band_value = v.band_value)
	`, p.Algo, bucket, band, values, p.PerKeyCap, p.ExcludeMedia)
	if err != nil {
		return nil, fmt.Errorf("lookup bucket %d band %d: %w", bucket, band, err)
	}
	defer rows.Close()
	var out []bandRow
	for rows.Next() {
		var r bandRow
		var hash int64
		if err := rows.Scan(&r.value, &r.hit.MediaID, &r.hit.Generation, &r.hit.AnchorMs, &hash); err != nil {
			return nil, fmt.Errorf("scan lookup row: %w", err)
		}
		r.hit.Hash = uint64(hash)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("lookup bucket %d band %d: %w", bucket, band, err)
	}
	return out, tx.Commit(ctx)
}

// finishLookup drops stale-generation postings: only candidates whose
// (asset, generation) is the live ready generation survive.
func (s *MediaAssetStore) finishLookup(ctx context.Context, res *LookupResult, raw []LookupHit) (*LookupResult, error) {
	if len(raw) == 0 {
		return res, nil
	}
	type key struct {
		id  uuid.UUID
		gen int64
	}
	ids := make([]uuid.UUID, 0, 64)
	seen := map[uuid.UUID]bool{}
	for _, h := range raw {
		if !seen[h.MediaID] {
			seen[h.MediaID] = true
			ids = append(ids, h.MediaID)
		}
	}
	live := map[key]bool{}
	rows, err := s.db.Query(ctx, `
		SELECT id, media_generation FROM media_assets
		 WHERE id = ANY($1) AND processing_status = 'ready' AND ready_generation = media_generation
	`, ids)
	if err != nil {
		return nil, fmt.Errorf("check candidate generations: %w", err)
	}
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.id, &k.gen); err != nil {
			rows.Close()
			return nil, err
		}
		live[k] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	res.Hits = raw[:0]
	for _, h := range raw {
		if live[key{h.MediaID, h.Generation}] {
			res.Hits = append(res.Hits, h)
		} else {
			res.StaleDropped++
		}
	}
	return res, nil
}

// CandidateState is the other side of a candidate pair as the verifier
// needs it: its fingerprint and its upload-time identity.
type CandidateState struct {
	MediaID          uuid.UUID
	Generation       int64
	UploadConfirmed  *time.Time
	UploadTimeSource string
	Fingerprint      *StoredFingerprint
}

// LoadCandidate reads a candidate's fingerprint and identity. nil when the
// fingerprint is gone or superseded (the pair cannot be verified).
func (s *MediaAssetStore) LoadCandidate(ctx context.Context, mediaID uuid.UUID, generation int64, algo int) (*CandidateState, error) {
	fp, err := s.LoadFingerprint(ctx, mediaID, generation, algo)
	if err != nil {
		return nil, err
	}
	if fp == nil || fp.SupersededAt != nil {
		return nil, nil
	}
	c := &CandidateState{MediaID: mediaID, Generation: generation, Fingerprint: fp}
	err = s.db.QueryRow(ctx, `
		SELECT upload_confirmed_at, COALESCE(upload_time_source, '') FROM media_assets WHERE id = $1
	`, mediaID).Scan(&c.UploadConfirmed, &c.UploadTimeSource)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load candidate %s: %w", mediaID, err)
	}
	return c, nil
}
