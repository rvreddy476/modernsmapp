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
	UserID          uuid.UUID
	FirstName       *string
	Intent          string
	Bio             string
	Gender          *string
	BirthDate       *time.Time
	City            *string
	Country         *string
	Latitude        *float64
	Longitude       *float64
	LocationGeohash *string
	Community       *string
	// communitySealed is the sealed community as scanned (lane D9), opened
	// into Community for the deck's same-community cap only.
	communitySealed   []byte
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
	// Lane D6: the approved primary photo (id + visibility) and whether the
	// candidate has sparked the viewer (sparked_only photos). Never the
	// media id.
	PrimaryPhotoID         *uuid.UUID
	PrimaryPhotoVisibility string
	SparkedViewer          bool
	// §P1-3 privacy flags. Carried on the candidate row so the
	// response builder can decide whether to omit the last-active
	// bucket or swap in the blurred-photo URL without a second
	// per-card round-trip. approximate_location is no longer read:
	// lane D7 buckets every distance.
	HideLastActive       bool
	BlurPhotosUntilMatch bool
	Incognito            bool
}

// Age returns the candidate's age in whole years, or 0 if BirthDate is nil.
func (c *CandidateProfile) Age() int {
	if c.BirthDate == nil {
		return 0
	}
	return AgeOn(*c.BirthDate, time.Now())
}

// AgeOn is the whole-years age on the calendar date of `at`: a year counts
// only once at's month/day has reached the birthday. Comparing day-of-year
// instead is off by one around 29 February (a 2 March birthday is day 61
// in a common year but 1 March is day 61 in a leap year). Matches
// PostgreSQL EXTRACT(YEAR FROM AGE(...)). Zero or future birth → 0.
func AgeOn(birth, at time.Time) int {
	if birth.IsZero() {
		return 0
	}
	by, bm, bd := birth.Date()
	ay, am, ad := at.Date()
	age := ay - by
	if am < bm || (am == bm && ad < bd) {
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
    p.community, p.community_sealed, p.blur_mode, p.trust_tier, p.last_active_at, p.language_prefs,
    t.lifestyle_rhythm, t.conversation_style, t.faith_weight, t.family_weight,
    t.region_weight, t.family_plans_axis, t.education_axis,
    -- Lane D6: the approved primary photo's id and visibility, never its
    -- media id. The response builder turns them into the viewer's photo
    -- image route (full or blurred). P0-6: approved only.
    (SELECT ph.id FROM dating_photos ph
        WHERE ph.user_id = p.user_id
          AND ph.is_primary = true
          AND ph.moderation_status = 'approved'
        LIMIT 1) AS primary_photo_id,
    COALESCE((SELECT ph.visibility FROM dating_photos ph
        WHERE ph.user_id = p.user_id
          AND ph.is_primary = true
          AND ph.moderation_status = 'approved'
        LIMIT 1), 'public') AS primary_photo_visibility,
    EXISTS (SELECT 1 FROM dating_sparks sv
        WHERE sv.from_user_id = p.user_id AND sv.to_user_id = $1::uuid) AS sparked_viewer,
    p.hide_last_active, p.blur_photos_until_match, p.incognito`

// CandidateQuery encodes the hard-filter knobs from spec §9.1.
type CandidateQuery struct {
	ViewerID     uuid.UUID
	MinAge       int
	MaxAge       int
	GenderFilter string // "" = no filter ("everyone" maps to this)
	// ViewerGender is the viewer's own gender, for the reciprocal half of
	// the gender rule. "" skips that filter.
	ViewerGender  string
	IntentFilter  []string
	DistanceKmMax int
	ViewerLat     *float64
	ViewerLon     *float64
	ExcludePassed bool
	Limit         int
	// VerifiedOnly mirrors the viewer's §P1-3 verified_only_filter
	// toggle. When true the WHERE clause restricts candidate
	// trust_tier to selfie/aadhaar — phone-only trust accounts drop
	// from the deck. Defaults to false so the existing deck shape
	// is preserved.
	VerifiedOnly bool
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
		// Lane D6: a match_only / sparked_only primary no longer hides the
		// profile — the viewer gets its server-blurred image instead.
		`EXISTS (SELECT 1 FROM dating_photos ph
		    WHERE ph.user_id = p.user_id
		      AND ph.is_primary = true
		      AND ph.moderation_status = 'approved')`,
		// Mutual block filter — neither side has blocked the other.
		`NOT ` + blockedPairPredicate("$1", "p.user_id"),
		// §P0-7 Phase A: drop candidates whose enforcement level
		// removes them from discovery. Rows with no risk record (the
		// vast majority before the sweeper has run) implicitly pass
		// because LEFT JOIN'd rar.risk_level is NULL and `IS NOT
		// DISTINCT FROM` would be over-engineering — we just check
		// for the four restrictive levels.
		`NOT EXISTS (SELECT 1 FROM dating_account_risk rar
		    WHERE rar.user_id = p.user_id
		      AND rar.risk_level IN
		          ('hide_from_discovery','chat_hold','admin_review','suspend'))`,
		// §P1-3 (schema doc): an incognito profile appears only to
		// people IT has sparked. The viewer sparking the incognito
		// profile reveals nothing.
		incognitoVisiblePredicate("p", "$1"),
	}
	// Decline cooldown: a candidate who declined one of the viewer's sparks
	// stays out of the viewer's deck for the cooldown. One-directional.
	args = append(args, s.declineCutoff())
	where = append(where, `NOT `+recentDeclinePredicate("$1", "p.user_id", fmt.Sprintf("$%d::timestamptz", len(args))))
	if q.VerifiedOnly {
		// §P1-3 verified-only filter: viewer-side toggle. Phone-only
		// trust is treated as "not verified" — must be selfie or
		// aadhaar to surface. Schema doesn't declare a 'none' tier
		// today but the IN-clause guards future drift.
		where = append(where, `p.trust_tier IN ('selfie','aadhaar')`)
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
	// Lane D10: the gender rule runs BOTH ways. The viewer's preference
	// filters the candidate's gender (above); this filters the candidate's
	// own preference by the viewer's gender. A candidate with no
	// preferences row, an empty preference or "everyone" admits anyone; any
	// other value must equal the viewer's gender exactly. Skipped when the
	// viewer has no gender on their profile — there is nothing to compare,
	// and an empty deck would be worse than an unfiltered one.
	if q.ViewerGender != "" {
		args = append(args, q.ViewerGender)
		where = append(where, fmt.Sprintf(`(NOT EXISTS (SELECT 1 FROM dating_preferences cp WHERE cp.user_id = p.user_id)
		    OR EXISTS (SELECT 1 FROM dating_preferences cp
		        WHERE cp.user_id = p.user_id
		          AND COALESCE(NULLIF(btrim(cp.interested_in_gender), ''), 'everyone') IN ('everyone', $%d)))`, len(args)))
	}
	if len(q.IntentFilter) > 0 {
		args = append(args, q.IntentFilter)
		where = append(where, fmt.Sprintf(`p.intent = ANY($%d)`, len(args)))
	}
	if q.ExcludePassed {
		// Database clock on both sides: passed_at is stamped by now(), so the
		// cutoff is too (see RecordPass).
		args = append(args, PassCooldown.Seconds())
		where = append(where, fmt.Sprintf(`NOT EXISTS (SELECT 1 FROM dating_passes dp
		    WHERE dp.user_id = $1 AND dp.candidate_id = p.user_id
		      AND dp.passed_at > now() - make_interval(secs => $%d))`, len(args)))
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
	//
	// Lane D7: the radius applied is the preference rounded up to a step
	// (EffectiveDiscoveryRadiusKm), so deck membership never measures a
	// candidate more finely than a step.
	radiusKm := EffectiveDiscoveryRadiusKm(q.DistanceKmMax)
	if q.ViewerLat != nil && q.ViewerLon != nil && radiusKm > 0 {
		precision := GeohashPrefixForRadiusKm(radiusKm)
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
		c, err := s.scanCandidate(ctx, rows)
		if err != nil {
			return nil, err
		}
		// Distance hard-filter (post-query — haversine without PostGIS), on
		// the snapped points. The radius is exclusive, so a step that is also
		// a bucket edge (5, 10, 25 km) admits exactly the buckets below it.
		if radiusKm > 0 && q.ViewerLat != nil && q.ViewerLon != nil &&
			c.Latitude != nil && c.Longitude != nil {
			d := SnappedDistanceKm(*q.ViewerLat, *q.ViewerLon, *c.Latitude, *c.Longitude)
			if d >= float64(radiusKm) {
				continue
			}
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// scanCandidate scans a candidate and opens its sealed community (lane D9).
func (s *Store) scanCandidate(ctx context.Context, row pgx.Row) (*CandidateProfile, error) {
	c, err := scanCandidateRow(row)
	if err != nil {
		return nil, err
	}
	c.Community = s.openSensitive(ctx, c.communitySealed, c.Community, "community")
	c.communitySealed = nil
	return c, nil
}

func scanCandidateRow(row pgx.Row) (*CandidateProfile, error) {
	c := &CandidateProfile{}
	err := row.Scan(
		&c.UserID, &c.FirstName, &c.Intent, &c.Bio, &c.Gender, &c.BirthDate,
		&c.City, &c.Country, &c.Latitude, &c.Longitude, &c.LocationGeohash,
		&c.Community, &c.communitySealed, &c.BlurMode, &c.TrustTier, &c.LastActiveAt, &c.LanguagePrefs,
		&c.LifestyleRhythm, &c.ConversationStyle, &c.FaithWeight, &c.FamilyWeight,
		&c.RegionWeight, &c.FamilyPlansAxis, &c.EducationAxis,
		&c.PrimaryPhotoID, &c.PrimaryPhotoVisibility, &c.SparkedViewer,
		&c.HideLastActive, &c.BlurPhotosUntilMatch, &c.Incognito,
	)
	if err != nil {
		return nil, fmt.Errorf("scan candidate: %w", err)
	}
	return c, nil
}

// incognitoVisiblePredicate is true when the profile aliased `alias` is not
// incognito, or has itself sparked the viewer expression.
func incognitoVisiblePredicate(alias, viewer string) string {
	return `(` + alias + `.incognito = false OR EXISTS (
	    SELECT 1 FROM dating_sparks inc
	    WHERE inc.from_user_id = ` + alias + `.user_id AND inc.to_user_id = ` + viewer + `))`
}

// GetCandidateForViewer returns one CandidateProfile as `viewerID` may see
// it (used by Nebula). No row when the profile is deleted or suspended, the
// pair is blocked either way, or the profile is incognito and has not
// sparked the viewer.
func (s *Store) GetCandidateForViewer(ctx context.Context, viewerID, userID uuid.UUID) (*CandidateProfile, error) {
	row := s.db.QueryRow(ctx, `
        SELECT `+candidateSelectCols+`
        FROM dating_profiles p
        LEFT JOIN dating_tunes t ON t.user_id = p.user_id
        WHERE p.user_id = $2
          AND p.deleted_at IS NULL
          AND p.profile_status NOT IN ('suspended','deleted')
          AND NOT `+blockedPairPredicate("$1::uuid", "p.user_id")+`
          AND `+incognitoVisiblePredicate("p", "$1::uuid"), viewerID, userID)
	return s.scanCandidate(ctx, row)
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

// PassCooldown is how long a pass keeps the candidate out of the passer's
// deck. dating_passes has no expiry column, so it runs from passed_at.
const PassCooldown = 30 * 24 * time.Hour

// RecordPass writes the viewer's pass on a candidate. Idempotent: a repeat
// inside the cooldown changes nothing; a repeat after it re-arms the
// cooldown. Returns the passed_at that now stands.
//
// The cooldown cutoff is computed with the database clock (now()), the same
// clock that stamps passed_at. It used to be the app host's time.Now(), so a
// host/DB clock skew moved the cooldown edge (and made the store test depend
// on the Docker VM clock agreeing with the Windows host).
func (s *Store) RecordPass(ctx context.Context, userID, candidateID uuid.UUID, reason string) (time.Time, error) {
	if userID == uuid.Nil || candidateID == uuid.Nil {
		return time.Time{}, fmt.Errorf("invalid: user_id and candidate_id required")
	}
	if userID == candidateID {
		return time.Time{}, fmt.Errorf("invalid: cannot pass yourself")
	}
	var reasonPtr *string
	if reason != "" {
		reasonPtr = &reason
	}
	var passedAt time.Time
	err := s.db.QueryRow(ctx, `
        INSERT INTO dating_passes (user_id, candidate_id, reason)
        VALUES ($1, $2, $3)
        ON CONFLICT (user_id, candidate_id) DO UPDATE
            SET passed_at = CASE WHEN dating_passes.passed_at <= now() - make_interval(secs => $4) THEN now() ELSE dating_passes.passed_at END,
                reason    = CASE WHEN dating_passes.passed_at <= now() - make_interval(secs => $4) THEN EXCLUDED.reason ELSE dating_passes.reason END
        RETURNING passed_at`, userID, candidateID, reasonPtr, PassCooldown.Seconds()).Scan(&passedAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("record pass: %w", err)
	}
	return passedAt, nil
}

// ListPassedCandidates returns the user's most-recent passes (paged, max
// `limit`), skipping candidates blocked either way or whose profile is
// deleted, suspended or gone. Used by GET /v1/dating/pulse/nebula?filter=passed.
func (s *Store) ListPassedCandidates(ctx context.Context, userID uuid.UUID, limit, offset int) ([]PassedCandidate, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(ctx, `
        SELECT dp.candidate_id, dp.passed_at, dp.reason
        FROM dating_passes dp
        WHERE dp.user_id = $1
          AND NOT `+blockedPairPredicate("dp.user_id", "dp.candidate_id")+`
          AND `+visibleProfilePredicate("dp.candidate_id")+`
        ORDER BY dp.passed_at DESC
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
