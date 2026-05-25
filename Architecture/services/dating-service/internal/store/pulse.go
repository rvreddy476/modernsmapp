package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CandidateProfile is a wide projection used by the Pulse pipeline. It carries
// everything the matcher and the response builder need without forcing a
// second round-trip per candidate.
type CandidateProfile struct {
	UserID            uuid.UUID
	FirstName         *string
	Intent            string
	Bio               string
	Gender            *string
	BirthDate         *time.Time
	City              *string
	Country           *string
	Latitude          *float64
	Longitude         *float64
	LocationGeohash   *string
	Community         *string
	BlurMode          bool
	TrustTier         string
	LastActiveAt      time.Time
	LanguagePrefs     []string
	LifestyleRhythm   *int
	ConversationStyle *string
	FaithWeight       *int
	FamilyWeight      *int
	RegionWeight      *int
	FamilyPlansAxis   *int
	EducationAxis     *int
	PrimaryPhotoMedia *uuid.UUID
}

// Age returns the candidate's age in whole years, or 0 if BirthDate is nil.
func (c *CandidateProfile) Age() int {
	if c.BirthDate == nil {
		return 0
	}
	now := time.Now()
	age := now.Year() - c.BirthDate.Year()
	if now.YearDay() < c.BirthDate.YearDay() {
		age--
	}
	if age < 0 {
		return 0
	}
	return age
}

// EchoCache mirrors dating_echo_cache.
type EchoCache struct {
	UserID      uuid.UUID
	Reels       []byte // JSON
	QAAnswers   []byte // JSON
	Communities []byte // JSON
	Posts       []byte // JSON
	RefreshedAt time.Time
}

// EchoTopics returns the set of "topic" strings extracted from the qa_answers
// payload. Best-effort — unmarshals an array of objects, picks `.topic` /
// `.tag`. Non-fatal on shape mismatch.
func (e *EchoCache) EchoTopics() []string {
	if e == nil || len(e.QAAnswers) == 0 {
		return nil
	}
	var arr []map[string]any
	if err := json.Unmarshal(e.QAAnswers, &arr); err != nil {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, m := range arr {
		for _, key := range []string{"topic", "tag", "topic_id", "topic_slug"} {
			if v, ok := m[key].(string); ok && v != "" {
				out = append(out, v)
				break
			}
		}
	}
	return out
}

// CommunitySlugs returns slugs/ids from the communities payload.
func (e *EchoCache) CommunitySlugs() []string {
	if e == nil || len(e.Communities) == 0 {
		return nil
	}
	var arr []map[string]any
	if err := json.Unmarshal(e.Communities, &arr); err != nil {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, m := range arr {
		for _, key := range []string{"slug", "id", "community_id", "name"} {
			if v, ok := m[key].(string); ok && v != "" {
				out = append(out, v)
				break
			}
		}
	}
	return out
}

const candidateSelectCols = `
    p.user_id, p.first_name, p.intent, p.bio, p.gender, p.birth_date,
    p.city, p.country, p.latitude, p.longitude, p.location_geohash,
    p.community, p.blur_mode, p.trust_tier, p.last_active_at, p.language_prefs,
    t.lifestyle_rhythm, t.conversation_style, t.faith_weight, t.family_weight,
    t.region_weight, t.family_plans_axis, t.education_axis,
    (SELECT media_id FROM dating_photos
        WHERE user_id = p.user_id
          AND is_primary = true
          -- P0-6: discovery must only surface approved photos. Without
          -- this filter a pending/rejected primary photo could leak to
          -- other users.
          AND moderation_status = 'approved'
          AND visibility = 'public'
        LIMIT 1) AS primary_photo_media`

// CandidateQuery encodes the hard-filter knobs from spec §9.1.
type CandidateQuery struct {
	ViewerID       uuid.UUID
	MinAge         int
	MaxAge         int
	GenderFilter   string // "" = no filter
	IntentFilter   []string
	DistanceKmMax  int
	ViewerLat      *float64
	ViewerLon      *float64
	ExcludePassed  bool
	Limit          int
}

// FetchCandidates returns up to Limit profiles that pass the hard-filter
// constraints (intent + age + gender + paused/deleted + not-blocked).
// Distance is filtered post-query (in Go) since haversine isn't expressible
// with a plain pgx query without PostGIS.
func (s *Store) FetchCandidates(ctx context.Context, q CandidateQuery) ([]CandidateProfile, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	args := []any{q.ViewerID}
	where := []string{
		`p.user_id <> $1`,
		// §P1-1 profile-status gate: only fully-activated profiles
		// surface in discovery. The legacy boolean filters below stay
		// as belt-and-braces during the rollout.
		`p.profile_status = 'active'`,
		`p.deleted_at IS NULL`,
		`p.paused = false`,
		`p.visible_to_public = true`,
		// P0-5: absolute age floor — no candidate without a verifiable
		// birth_date AND a computed age of 18+ enters discovery,
		// regardless of the viewer's preference window. Closes the
		// "candidate age = 0" leak from the gap analysis.
		`p.birth_date IS NOT NULL`,
		`EXTRACT(YEAR FROM AGE(p.birth_date)) >= 18`,
		// P0-6: candidate must have at least one approved photo. The
		// SELECT in candidateSelectCols already filters the primary
		// photo to approved-only; this EXISTS clause guarantees the
		// candidate has one rather than relying on the LEFT-JOIN-style
		// scalar subquery returning NULL.
		`EXISTS (SELECT 1 FROM dating_photos ph
		    WHERE ph.user_id = p.user_id
		      AND ph.is_primary = true
		      AND ph.moderation_status = 'approved'
		      AND ph.visibility = 'public')`,
		// Mutual block filter — neither side has blocked the other.
		`NOT EXISTS (SELECT 1 FROM dating_blocks b
		    WHERE (b.user_id = $1 AND b.blocked_id = p.user_id)
		       OR (b.user_id = p.user_id AND b.blocked_id = $1))`,
	}
	if q.MinAge > 0 {
		args = append(args, q.MinAge)
		where = append(where, fmt.Sprintf(`EXTRACT(YEAR FROM AGE(p.birth_date)) >= $%d`, len(args)))
	}
	if q.MaxAge > 0 {
		args = append(args, q.MaxAge)
		where = append(where, fmt.Sprintf(`EXTRACT(YEAR FROM AGE(p.birth_date)) <= $%d`, len(args)))
	}
	if q.GenderFilter != "" {
		args = append(args, q.GenderFilter)
		where = append(where, fmt.Sprintf(`p.gender = $%d`, len(args)))
	}
	if len(q.IntentFilter) > 0 {
		args = append(args, q.IntentFilter)
		where = append(where, fmt.Sprintf(`p.intent = ANY($%d)`, len(args)))
	}
	if q.ExcludePassed {
		where = append(where, `NOT EXISTS (SELECT 1 FROM dating_passes dp
		    WHERE dp.user_id = $1 AND dp.candidate_id = p.user_id)`)
	}

	// P0-10 Phase A: geohash prefix prefilter. When the viewer has a
	// location + a distance cap, compute the viewer's geohash at the
	// precision that bounds the radius and restrict candidates to that
	// cell + its 8 neighbours. Indexed via
	// idx_dating_profiles_geohash_prefix; turns the matcher's hot path
	// from a full table scan into a bounded lookup.
	//
	// Without a viewer location or with an unbounded radius we skip the
	// prefilter — the Go-side haversine then still does the final
	// distance check on the over-fetched batch.
	if q.ViewerLat != nil && q.ViewerLon != nil && q.DistanceKmMax > 0 {
		precision := GeohashPrefixForRadiusKm(q.DistanceKmMax)
		if precision > 0 {
			viewerGH := EncodeGeohash(*q.ViewerLat, *q.ViewerLon, precision)
			if viewerGH != "" {
				cells := GeohashNeighbours(viewerGH)
				// Build a (LIKE 'cell1%' OR LIKE 'cell2%' ...) clause.
				// Each cell adds one parameter to keep the planner
				// happy with prepared-statement caching.
				ors := make([]string, 0, len(cells))
				for _, c := range cells {
					args = append(args, c+"%")
					ors = append(ors, fmt.Sprintf("p.location_geohash LIKE $%d", len(args)))
				}
				where = append(where, "("+strings.Join(ors, " OR ")+")")
			}
		}
	}

	whereClause := ""
	for i, w := range where {
		if i == 0 {
			whereClause = "WHERE " + w
		} else {
			whereClause += "\n  AND " + w
		}
	}

	// Larger inner scan, randomised so subsequent calls vary, then top-N by random.
	args = append(args, limit*4) // over-fetch so distance pruning + diversity have headroom
	sql := `
        SELECT ` + candidateSelectCols + `
        FROM dating_profiles p
        LEFT JOIN dating_tunes t ON t.user_id = p.user_id
        ` + whereClause + `
        ORDER BY random()
        LIMIT $` + fmt.Sprintf("%d", len(args))

	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query candidates: %w", err)
	}
	defer rows.Close()

	out := make([]CandidateProfile, 0, limit)
	for rows.Next() {
		c, err := scanCandidate(rows)
		if err != nil {
			return nil, err
		}
		// Distance hard-filter (post-query — haversine without PostGIS).
		if q.DistanceKmMax > 0 && q.ViewerLat != nil && q.ViewerLon != nil &&
			c.Latitude != nil && c.Longitude != nil {
			d := DistanceKm(*q.ViewerLat, *q.ViewerLon, *c.Latitude, *c.Longitude)
			if d > float64(q.DistanceKmMax) {
				continue
			}
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func scanCandidate(row pgx.Row) (*CandidateProfile, error) {
	c := &CandidateProfile{}
	err := row.Scan(
		&c.UserID, &c.FirstName, &c.Intent, &c.Bio, &c.Gender, &c.BirthDate,
		&c.City, &c.Country, &c.Latitude, &c.Longitude, &c.LocationGeohash,
		&c.Community, &c.BlurMode, &c.TrustTier, &c.LastActiveAt, &c.LanguagePrefs,
		&c.LifestyleRhythm, &c.ConversationStyle, &c.FaithWeight, &c.FamilyWeight,
		&c.RegionWeight, &c.FamilyPlansAxis, &c.EducationAxis,
		&c.PrimaryPhotoMedia,
	)
	if err != nil {
		return nil, fmt.Errorf("scan candidate: %w", err)
	}
	return c, nil
}

// GetCandidateByUserID returns a single CandidateProfile (used by Nebula).
func (s *Store) GetCandidateByUserID(ctx context.Context, userID uuid.UUID) (*CandidateProfile, error) {
	row := s.db.QueryRow(ctx, `
        SELECT `+candidateSelectCols+`
        FROM dating_profiles p
        LEFT JOIN dating_tunes t ON t.user_id = p.user_id
        WHERE p.user_id = $1 AND p.deleted_at IS NULL`, userID)
	return scanCandidate(row)
}

// --- Echo cache ------------------------------------------------------------

// ErrEchoCacheNotFound is returned when no row exists.
var ErrEchoCacheNotFound = errors.New("not_found: echo cache not found")

// GetEchoCache returns the user's echo snapshot or ErrEchoCacheNotFound.
func (s *Store) GetEchoCache(ctx context.Context, userID uuid.UUID) (*EchoCache, error) {
	e := &EchoCache{}
	row := s.db.QueryRow(ctx, `
        SELECT user_id, reels, qa_answers, communities, posts, refreshed_at
        FROM dating_echo_cache WHERE user_id = $1`, userID)
	if err := row.Scan(&e.UserID, &e.Reels, &e.QAAnswers, &e.Communities, &e.Posts, &e.RefreshedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEchoCacheNotFound
		}
		return nil, fmt.Errorf("scan echo cache: %w", err)
	}
	return e, nil
}

// UpsertEchoCache writes the echo snapshot and stamps refreshed_at.
func (s *Store) UpsertEchoCache(ctx context.Context, userID uuid.UUID, reels, qaAnswers, communities, posts []byte) error {
	if reels == nil {
		reels = []byte("[]")
	}
	if qaAnswers == nil {
		qaAnswers = []byte("[]")
	}
	if communities == nil {
		communities = []byte("[]")
	}
	if posts == nil {
		posts = []byte("[]")
	}
	if _, err := s.db.Exec(ctx, `
        INSERT INTO dating_echo_cache (user_id, reels, qa_answers, communities, posts, refreshed_at)
        VALUES ($1, $2::jsonb, $3::jsonb, $4::jsonb, $5::jsonb, now())
        ON CONFLICT (user_id) DO UPDATE
            SET reels = EXCLUDED.reels,
                qa_answers = EXCLUDED.qa_answers,
                communities = EXCLUDED.communities,
                posts = EXCLUDED.posts,
                refreshed_at = now()`,
		userID, reels, qaAnswers, communities, posts); err != nil {
		return fmt.Errorf("upsert echo cache: %w", err)
	}
	if _, err := s.db.Exec(ctx, `
        UPDATE dating_profiles SET echo_refreshed_at = now() WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("stamp echo_refreshed_at: %w", err)
	}
	return nil
}

// EchoRefreshTarget is a row produced by ListEchoRefreshTargets — just the
// fields the refresher needs to make outbound requests.
type EchoRefreshTarget struct {
	UserID uuid.UUID
}

// ListEchoRefreshTargets returns up to `batch` users whose echo cache is
// stale or missing AND who have not opted out (echoes_consent = true).
func (s *Store) ListEchoRefreshTargets(ctx context.Context, batch int) ([]EchoRefreshTarget, error) {
	if batch <= 0 {
		batch = 100
	}
	rows, err := s.db.Query(ctx, `
        SELECT user_id FROM dating_profiles
        WHERE deleted_at IS NULL
          AND echoes_consent = true
          AND (echo_refreshed_at IS NULL OR echo_refreshed_at < now() - INTERVAL '24 hours')
        ORDER BY echo_refreshed_at NULLS FIRST
        LIMIT $1`, batch)
	if err != nil {
		return nil, fmt.Errorf("list refresh targets: %w", err)
	}
	defer rows.Close()
	var out []EchoRefreshTarget
	for rows.Next() {
		var t EchoRefreshTarget
		if err := rows.Scan(&t.UserID); err != nil {
			return nil, fmt.Errorf("scan refresh target: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// --- Passes ----------------------------------------------------------------

// PassedCandidate is one row returned by ListPassedCandidates.
type PassedCandidate struct {
	CandidateID uuid.UUID
	PassedAt    time.Time
	Reason      *string
}

// ListPassedCandidates returns the user's most-recent passes (paged, max
// `limit`). Used by GET /v1/dating/pulse/nebula?filter=passed.
func (s *Store) ListPassedCandidates(ctx context.Context, userID uuid.UUID, limit, offset int) ([]PassedCandidate, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(ctx, `
        SELECT candidate_id, passed_at, reason
        FROM dating_passes
        WHERE user_id = $1
        ORDER BY passed_at DESC
        LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list passes: %w", err)
	}
	defer rows.Close()
	var out []PassedCandidate
	for rows.Next() {
		var p PassedCandidate
		if err := rows.Scan(&p.CandidateID, &p.PassedAt, &p.Reason); err != nil {
			return nil, fmt.Errorf("scan pass: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
