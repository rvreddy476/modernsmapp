package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SuggestionCandidate is a precomputed suggestion record.
type SuggestionCandidate struct {
	ViewerID          uuid.UUID `json:"viewer_id"`
	CandidateID       uuid.UUID `json:"candidate_id"`
	SuggestionType    string    `json:"suggestion_type"`
	BaseScore         float32   `json:"base_score"`
	ReasonCodes       []string  `json:"reason_codes"`
	ExplainText       string    `json:"explain_text"`
	SourceBucket      string    `json:"source_bucket"`
	MutualFriendCount int16     `json:"mutual_friend_count"`
	ImpressionCount   int16     `json:"impression_count"`
	GeneratedAt       time.Time `json:"generated_at"`
}

// ProfileInfo holds minimal profile fields for scoring.
type ProfileInfo struct {
	UserID        uuid.UUID
	Username      *string
	DisplayName   string
	AvatarMediaID *uuid.UUID
	Location      string
	Profession    string
	CreatedAt     time.Time
}

// Impression records a suggestion view.
type Impression struct {
	ViewerID       uuid.UUID
	CandidateID    uuid.UUID
	Surface        string
	SuggestionType string
	RankPosition   int16
	Score          float32
	SessionID      *uuid.UUID
	ExperimentID   string
	VariantID      string
}

// Action records a user action on a suggestion.
type Action struct {
	ViewerID       uuid.UUID
	CandidateID    uuid.UUID
	ActionType     string
	Surface        string
	SuggestionType string
	ExperimentID   string
	VariantID      string
}

// CooldownEntry represents a hide/block cooldown.
type CooldownEntry struct {
	ViewerID      uuid.UUID
	CandidateID   uuid.UUID
	CooldownType  string
	CooldownUntil *time.Time
}

// Store manages all suggestion-related database operations.
type Store struct {
	appDB      *pgxpool.Pool
	identityDB *pgxpool.Pool
}

// New creates a new Store with connections to both databases.
func New(appDB, identityDB *pgxpool.Pool) *Store {
	return &Store{appDB: appDB, identityDB: identityDB}
}

// EnsureSchema creates suggestion tables if they don't exist.
func (s *Store) EnsureSchema(ctx context.Context) error {
	ddl := `
	CREATE TABLE IF NOT EXISTS suggestion_candidates (
		viewer_id UUID NOT NULL, candidate_id UUID NOT NULL,
		suggestion_type VARCHAR(10) NOT NULL DEFAULT 'friend',
		base_score REAL NOT NULL DEFAULT 0,
		reason_codes TEXT[] NOT NULL DEFAULT '{}',
		explain_text VARCHAR(200) NOT NULL DEFAULT '',
		source_bucket VARCHAR(20) NOT NULL DEFAULT 'fof',
		mutual_friend_count SMALLINT DEFAULT 0,
		impression_count SMALLINT DEFAULT 0,
		generated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		expires_at TIMESTAMPTZ,
		PRIMARY KEY (viewer_id, candidate_id, suggestion_type)
	);
	CREATE INDEX IF NOT EXISTS idx_sc_score ON suggestion_candidates (viewer_id, suggestion_type, base_score DESC);
	CREATE TABLE IF NOT EXISTS suggestion_impressions (
		id UUID DEFAULT gen_random_uuid() PRIMARY KEY,
		viewer_id UUID NOT NULL, candidate_id UUID NOT NULL,
		surface VARCHAR(20) NOT NULL DEFAULT 'mycircle',
		suggestion_type VARCHAR(10) NOT NULL DEFAULT 'friend',
		rank_position SMALLINT, score REAL,
		shown_at TIMESTAMPTZ NOT NULL DEFAULT now(), session_id UUID
	);
	CREATE INDEX IF NOT EXISTS idx_si_viewer ON suggestion_impressions (viewer_id, shown_at DESC);
	CREATE TABLE IF NOT EXISTS suggestion_actions (
		id UUID DEFAULT gen_random_uuid() PRIMARY KEY,
		viewer_id UUID NOT NULL, candidate_id UUID NOT NULL,
		action VARCHAR(20) NOT NULL,
		surface VARCHAR(20) NOT NULL DEFAULT 'mycircle',
		suggestion_type VARCHAR(10) NOT NULL DEFAULT 'friend',
		created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	);
	CREATE INDEX IF NOT EXISTS idx_sa_viewer ON suggestion_actions (viewer_id, created_at DESC);
	CREATE TABLE IF NOT EXISTS suggestion_cooldowns (
		viewer_id UUID NOT NULL, candidate_id UUID NOT NULL,
		cooldown_type VARCHAR(20) NOT NULL,
		cooldown_until TIMESTAMPTZ,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		PRIMARY KEY (viewer_id, candidate_id)
	);
	CREATE INDEX IF NOT EXISTS idx_cooldowns_expiry ON suggestion_cooldowns (cooldown_until) WHERE cooldown_until IS NOT NULL;
	`
	_, err := s.appDB.Exec(ctx, ddl)
	return err
}

// ─── Graph reads (app DB) ────────────────────────────────────

// GetFriendIDs returns all friend user IDs for a given user, read from the
// canonical graph-service connections table in the app database.
func (s *Store) GetFriendIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.appDB.Query(ctx, `
		SELECT CASE WHEN user_a = $1 THEN user_b ELSE user_a END AS connection_id
		FROM connections
		WHERE user_a = $1 OR user_b = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetBlockedIDs returns all user IDs blocked by or blocking the given user.
func (s *Store) GetBlockedIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.appDB.Query(ctx, `
		SELECT blocked_id FROM blocks WHERE blocker_id = $1
		UNION
		SELECT blocker_id FROM blocks WHERE blocked_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetPendingRequestIDs returns user IDs with pending friend requests (sent or received).
func (s *Store) GetPendingRequestIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.appDB.Query(ctx, `
		SELECT receiver_id FROM connection_requests WHERE sender_id = $1 AND status = 'pending'
		UNION
		SELECT sender_id FROM connection_requests WHERE receiver_id = $1 AND status = 'pending'
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetFollowingIDs returns user IDs that the given user follows.
func (s *Store) GetFollowingIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.appDB.Query(ctx, `
		SELECT followee_id FROM follows WHERE follower_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetFriendsOfFriends returns a map of candidateID → mutual friend count.
func (s *Store) GetFriendsOfFriends(ctx context.Context, userID uuid.UUID, excludeIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	q := `
	WITH my_friends AS (
		SELECT CASE WHEN user_a = $1 THEN user_b ELSE user_a END AS friend_id
		FROM connections WHERE user_a = $1 OR user_b = $1
	),
	fof AS (
		SELECT
			CASE WHEN f.user_a = mf.friend_id THEN f.user_b ELSE f.user_a END AS candidate_id,
			mf.friend_id AS via_friend
		FROM connections f
		JOIN my_friends mf ON (f.user_a = mf.friend_id OR f.user_b = mf.friend_id)
		WHERE CASE WHEN f.user_a = mf.friend_id THEN f.user_b ELSE f.user_a END != $1
	)
	SELECT candidate_id, COUNT(DISTINCT via_friend)::int AS mutual_count
	FROM fof
	WHERE candidate_id != ALL($2::uuid[])
	GROUP BY candidate_id
	ORDER BY mutual_count DESC
	LIMIT 1000
	`
	rows, err := s.appDB.Query(ctx, q, userID, excludeIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[uuid.UUID]int)
	for rows.Next() {
		var cid uuid.UUID
		var count int
		if err := rows.Scan(&cid, &count); err != nil {
			return nil, err
		}
		result[cid] = count
	}
	return result, rows.Err()
}

// CheckMutualFollow checks if both users follow each other.
func (s *Store) CheckMutualFollow(ctx context.Context, userA, userB uuid.UUID) (bool, error) {
	var count int
	err := s.appDB.QueryRow(ctx, `
		SELECT COUNT(*) FROM follows
		WHERE (follower_id = $1 AND followee_id = $2) OR (follower_id = $2 AND followee_id = $1)
	`, userA, userB).Scan(&count)
	return count == 2, err
}

// GetPopularUsers returns user IDs sorted by follower count (fallback).
func (s *Store) GetPopularUsers(ctx context.Context, limit int) ([]uuid.UUID, error) {
	rows, err := s.appDB.Query(ctx, `
		SELECT user_id FROM counts ORDER BY follower_count DESC LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetAllUsersWithFriends returns user IDs that have at least one friend.
func (s *Store) GetAllUsersWithFriends(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.appDB.Query(ctx, `
		SELECT DISTINCT user_id FROM (
			SELECT user_a AS user_id FROM connections
			UNION
			SELECT user_b AS user_id FROM connections
		) t
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ─── Profile reads (identity_db) ─────────────────────────────

// GetProfileInfo fetches a single profile.
func (s *Store) GetProfileInfo(ctx context.Context, userID uuid.UUID) (*ProfileInfo, error) {
	p := &ProfileInfo{}
	err := s.identityDB.QueryRow(ctx, `
		SELECT user_id, username, display_name, avatar_media_id, COALESCE(location,''), COALESCE(profession,''), created_at
		FROM profile.profiles WHERE user_id = $1
	`, userID).Scan(&p.UserID, &p.Username, &p.DisplayName, &p.AvatarMediaID, &p.Location, &p.Profession, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// GetProfileInfoBatch fetches profiles for multiple user IDs.
func (s *Store) GetProfileInfoBatch(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]*ProfileInfo, error) {
	if len(userIDs) == 0 {
		return map[uuid.UUID]*ProfileInfo{}, nil
	}
	rows, err := s.identityDB.Query(ctx, `
		SELECT user_id, username, display_name, avatar_media_id, COALESCE(location,''), COALESCE(profession,''), created_at
		FROM profile.profiles WHERE user_id = ANY($1)
	`, userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[uuid.UUID]*ProfileInfo, len(userIDs))
	for rows.Next() {
		p := &ProfileInfo{}
		if err := rows.Scan(&p.UserID, &p.Username, &p.DisplayName, &p.AvatarMediaID, &p.Location, &p.Profession, &p.CreatedAt); err != nil {
			return nil, err
		}
		result[p.UserID] = p
	}
	return result, rows.Err()
}

// GetProfilesByLocation returns user IDs matching a location.
func (s *Store) GetProfilesByLocation(ctx context.Context, location string, limit int) ([]uuid.UUID, error) {
	if location == "" {
		return nil, nil
	}
	rows, err := s.identityDB.Query(ctx, `
		SELECT user_id FROM profile.profiles
		WHERE LOWER(location) = LOWER($1) LIMIT $2
	`, location, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetProfilesByProfession returns user IDs matching a profession.
func (s *Store) GetProfilesByProfession(ctx context.Context, profession string, limit int) ([]uuid.UUID, error) {
	if profession == "" {
		return nil, nil
	}
	rows, err := s.identityDB.Query(ctx, `
		SELECT user_id FROM profile.profiles
		WHERE LOWER(profession) = LOWER($1) LIMIT $2
	`, profession, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ─── Suggestion candidates CRUD ──────────────────────────────

// UpsertCandidates inserts or updates suggestion candidates.
func (s *Store) UpsertCandidates(ctx context.Context, candidates []SuggestionCandidate) error {
	if len(candidates) == 0 {
		return nil
	}
	// Build batch insert
	var vals []string
	var args []interface{}
	idx := 1
	for _, c := range candidates {
		vals = append(vals, fmt.Sprintf(
			"($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			idx, idx+1, idx+2, idx+3, idx+4, idx+5, idx+6, idx+7, idx+8, idx+9,
		))
		args = append(args,
			c.ViewerID, c.CandidateID, c.SuggestionType, c.BaseScore,
			c.ReasonCodes, c.ExplainText, c.SourceBucket, c.MutualFriendCount,
			c.ImpressionCount, c.GeneratedAt,
		)
		idx += 10
	}
	q := `INSERT INTO suggestion_candidates
		(viewer_id, candidate_id, suggestion_type, base_score, reason_codes, explain_text, source_bucket, mutual_friend_count, impression_count, generated_at)
		VALUES ` + strings.Join(vals, ",") + `
		ON CONFLICT (viewer_id, candidate_id, suggestion_type) DO UPDATE SET
			base_score = EXCLUDED.base_score,
			reason_codes = EXCLUDED.reason_codes,
			explain_text = EXCLUDED.explain_text,
			source_bucket = EXCLUDED.source_bucket,
			mutual_friend_count = EXCLUDED.mutual_friend_count,
			generated_at = EXCLUDED.generated_at`
	_, err := s.appDB.Exec(ctx, q, args...)
	return err
}

// GetCandidates reads scored candidates ordered by score.
func (s *Store) GetCandidates(ctx context.Context, viewerID uuid.UUID, suggType string, limit, offset int) ([]SuggestionCandidate, error) {
	rows, err := s.appDB.Query(ctx, `
		SELECT viewer_id, candidate_id, suggestion_type, base_score, reason_codes, explain_text,
			   source_bucket, mutual_friend_count, impression_count, generated_at
		FROM suggestion_candidates
		WHERE viewer_id = $1 AND suggestion_type = $2
		ORDER BY base_score DESC
		LIMIT $3 OFFSET $4
	`, viewerID, suggType, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []SuggestionCandidate
	for rows.Next() {
		var c SuggestionCandidate
		if err := rows.Scan(&c.ViewerID, &c.CandidateID, &c.SuggestionType, &c.BaseScore,
			&c.ReasonCodes, &c.ExplainText, &c.SourceBucket, &c.MutualFriendCount,
			&c.ImpressionCount, &c.GeneratedAt); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

// DeleteCandidatesForViewer removes all candidates for a viewer+type.
func (s *Store) DeleteCandidatesForViewer(ctx context.Context, viewerID uuid.UUID, suggType string) error {
	_, err := s.appDB.Exec(ctx, `
		DELETE FROM suggestion_candidates WHERE viewer_id = $1 AND suggestion_type = $2
	`, viewerID, suggType)
	return err
}

// RemoveCandidateForViewer removes a single candidate.
func (s *Store) RemoveCandidateForViewer(ctx context.Context, viewerID, candidateID uuid.UUID, suggType string) error {
	_, err := s.appDB.Exec(ctx, `
		DELETE FROM suggestion_candidates WHERE viewer_id = $1 AND candidate_id = $2 AND suggestion_type = $3
	`, viewerID, candidateID, suggType)
	return err
}

// ─── Impressions & Actions ───────────────────────────────────

// LogImpression records a suggestion impression.
func (s *Store) LogImpression(ctx context.Context, imp *Impression) error {
	_, err := s.appDB.Exec(ctx, `
		INSERT INTO suggestion_impressions (viewer_id, candidate_id, surface, suggestion_type, rank_position, score, session_id, experiment_id, variant_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, imp.ViewerID, imp.CandidateID, imp.Surface, imp.SuggestionType, imp.RankPosition, imp.Score, imp.SessionID, nilIfEmpty(imp.ExperimentID), nilIfEmpty(imp.VariantID))
	return err
}

// LogAction records a user action on a suggestion.
func (s *Store) LogAction(ctx context.Context, a *Action) error {
	_, err := s.appDB.Exec(ctx, `
		INSERT INTO suggestion_actions (viewer_id, candidate_id, action, surface, suggestion_type, experiment_id, variant_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, a.ViewerID, a.CandidateID, a.ActionType, a.Surface, a.SuggestionType, nilIfEmpty(a.ExperimentID), nilIfEmpty(a.VariantID))
	return err
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ─── Cooldowns ───────────────────────────────────────────────

// CreateCooldown inserts or updates a cooldown entry.
func (s *Store) CreateCooldown(ctx context.Context, cd CooldownEntry) error {
	_, err := s.appDB.Exec(ctx, `
		INSERT INTO suggestion_cooldowns (viewer_id, candidate_id, cooldown_type, cooldown_until)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (viewer_id, candidate_id) DO UPDATE SET
			cooldown_type = EXCLUDED.cooldown_type,
			cooldown_until = EXCLUDED.cooldown_until,
			created_at = now()
	`, cd.ViewerID, cd.CandidateID, cd.CooldownType, cd.CooldownUntil)
	return err
}

// GetActiveCooldowns returns all active cooldowns for a viewer.
func (s *Store) GetActiveCooldowns(ctx context.Context, viewerID uuid.UUID) (map[uuid.UUID]CooldownEntry, error) {
	rows, err := s.appDB.Query(ctx, `
		SELECT viewer_id, candidate_id, cooldown_type, cooldown_until
		FROM suggestion_cooldowns
		WHERE viewer_id = $1 AND (cooldown_until IS NULL OR cooldown_until > now())
	`, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[uuid.UUID]CooldownEntry)
	for rows.Next() {
		var cd CooldownEntry
		if err := rows.Scan(&cd.ViewerID, &cd.CandidateID, &cd.CooldownType, &cd.CooldownUntil); err != nil {
			return nil, err
		}
		result[cd.CandidateID] = cd
	}
	return result, rows.Err()
}

// CleanExpiredCooldowns removes expired cooldown entries.
func (s *Store) CleanExpiredCooldowns(ctx context.Context) (int64, error) {
	tag, err := s.appDB.Exec(ctx, `
		DELETE FROM suggestion_cooldowns WHERE cooldown_until IS NOT NULL AND cooldown_until < now()
	`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ─── Helpers ─────────────────────────────────────────────────

// BatchCheckMutualFollows checks mutual follow status for a viewer against multiple candidates.
func (s *Store) BatchCheckMutualFollows(ctx context.Context, viewerID uuid.UUID, candidateIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	if len(candidateIDs) == 0 {
		return map[uuid.UUID]bool{}, nil
	}
	// Get all follow relationships involving the viewer and the candidates
	rows, err := s.appDB.Query(ctx, `
		SELECT followee_id FROM follows WHERE follower_id = $1 AND followee_id = ANY($2)
	`, viewerID, candidateIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	viewerFollows := make(map[uuid.UUID]bool)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		viewerFollows[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Now check reverse direction
	rows2, err := s.appDB.Query(ctx, `
		SELECT follower_id FROM follows WHERE followee_id = $1 AND follower_id = ANY($2)
	`, viewerID, candidateIDs)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()
	result := make(map[uuid.UUID]bool)
	for rows2.Next() {
		var id uuid.UUID
		if err := rows2.Scan(&id); err != nil {
			return nil, err
		}
		if viewerFollows[id] {
			result[id] = true
		}
	}
	return result, rows2.Err()
}

// CountCandidates returns count of candidates for a viewer.
func (s *Store) CountCandidates(ctx context.Context, viewerID uuid.UUID, suggType string) (int, error) {
	var count int
	err := s.appDB.QueryRow(ctx, `
		SELECT COUNT(*) FROM suggestion_candidates WHERE viewer_id = $1 AND suggestion_type = $2
	`, viewerID, suggType).Scan(&count)
	return count, err
}

// ─── Group queries (app DB) ──────────────────────────────────

// GetUserGroupIDs returns the group IDs a user belongs to.
func (s *Store) GetUserGroupIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.appDB.Query(ctx, `
		SELECT group_id FROM group_members WHERE user_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetCommonGroupCountBatch returns a map of candidateID → common group count with viewer.
func (s *Store) GetCommonGroupCountBatch(ctx context.Context, viewerID uuid.UUID, candidateIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	if len(candidateIDs) == 0 {
		return map[uuid.UUID]int{}, nil
	}
	rows, err := s.appDB.Query(ctx, `
		SELECT gm2.user_id, COUNT(DISTINCT gm1.group_id)::int
		FROM group_members gm1
		JOIN group_members gm2 ON gm1.group_id = gm2.group_id
		WHERE gm1.user_id = $1 AND gm2.user_id = ANY($2) AND gm2.user_id != $1
		GROUP BY gm2.user_id
	`, viewerID, candidateIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[uuid.UUID]int)
	for rows.Next() {
		var id uuid.UUID
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, err
		}
		result[id] = count
	}
	return result, rows.Err()
}

// GetGroupMemberCandidates returns user IDs from viewer's groups as community candidates.
func (s *Store) GetGroupMemberCandidates(ctx context.Context, viewerID uuid.UUID, excludeIDs []uuid.UUID, limit int) ([]uuid.UUID, error) {
	rows, err := s.appDB.Query(ctx, `
		SELECT DISTINCT gm2.user_id
		FROM group_members gm1
		JOIN group_members gm2 ON gm1.group_id = gm2.group_id
		WHERE gm1.user_id = $1 AND gm2.user_id != $1
		  AND gm2.user_id != ALL($2::uuid[])
		LIMIT $3
	`, viewerID, excludeIDs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ─── School/Company queries (identity_db user_about) ─────────

// LifeEntry represents a school or company from user_about life_entry section.
type LifeEntry struct {
	Type string // "school" or "company"
	Name string
}

// GetUserLifeEntries returns schools and companies from user_about life_entry.
func (s *Store) GetUserLifeEntries(ctx context.Context, userID uuid.UUID) ([]LifeEntry, error) {
	rows, err := s.identityDB.Query(ctx, `
		SELECT data FROM profile.user_about
		WHERE user_id = $1 AND section = 'life_entry'
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []LifeEntry
	for rows.Next() {
		var data map[string]interface{}
		if err := rows.Scan(&data); err != nil {
			continue
		}
		entryType, _ := data["type"].(string)
		name, _ := data["name"].(string)
		if name != "" && (entryType == "school" || entryType == "company" || entryType == "work" || entryType == "education") {
			t := "school"
			if entryType == "company" || entryType == "work" {
				t = "company"
			}
			entries = append(entries, LifeEntry{Type: t, Name: name})
		}
	}
	return entries, rows.Err()
}

// GetUsersByLifeEntry returns user IDs with a matching life_entry name.
func (s *Store) GetUsersByLifeEntry(ctx context.Context, entryName string, limit int) ([]uuid.UUID, error) {
	if entryName == "" {
		return nil, nil
	}
	rows, err := s.identityDB.Query(ctx, `
		SELECT DISTINCT user_id FROM profile.user_about
		WHERE section = 'life_entry' AND data->>'name' ILIKE $1
		LIMIT $2
	`, entryName, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ─── Triadic closure (app DB) ────────────────────────────────

// GetTriadicClosureCandidates finds users D where V↔B, V↔C, B↔C, B↔D, C↔D but NOT V↔D.
// Returns map[candidateID]score where score is count of unique triads.
func (s *Store) GetTriadicClosureCandidates(ctx context.Context, viewerID uuid.UUID, excludeIDs []uuid.UUID, limit int) (map[uuid.UUID]int, error) {
	q := `
	WITH my_friends AS (
		SELECT CASE WHEN user_a = $1 THEN user_b ELSE user_a END AS fid
		FROM connections WHERE user_a = $1 OR user_b = $1
	),
	triads AS (
		SELECT mf1.fid AS b, mf2.fid AS c
		FROM my_friends mf1
		JOIN my_friends mf2 ON mf1.fid < mf2.fid
		WHERE EXISTS (
			SELECT 1 FROM connections
			WHERE (user_a = LEAST(mf1.fid, mf2.fid) AND user_b = GREATEST(mf1.fid, mf2.fid))
		)
	),
	candidates AS (
		SELECT CASE WHEN f.user_a = t.b THEN f.user_b ELSE f.user_a END AS d_id, t.b, t.c
		FROM triads t
		JOIN connections f ON (f.user_a = t.b OR f.user_b = t.b)
		WHERE CASE WHEN f.user_a = t.b THEN f.user_b ELSE f.user_a END != $1
		  AND CASE WHEN f.user_a = t.b THEN f.user_b ELSE f.user_a END != t.c
		  AND EXISTS (
			SELECT 1 FROM connections
			WHERE (user_a = LEAST(CASE WHEN f.user_a = t.b THEN f.user_b ELSE f.user_a END, t.c)
			   AND user_b = GREATEST(CASE WHEN f.user_a = t.b THEN f.user_b ELSE f.user_a END, t.c))
		  )
	)
	SELECT d_id, COUNT(*)::int AS triad_count
	FROM candidates
	WHERE d_id != ALL($2::uuid[])
	GROUP BY d_id
	ORDER BY triad_count DESC
	LIMIT $3
	`
	rows, err := s.appDB.Query(ctx, q, viewerID, excludeIDs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[uuid.UUID]int)
	for rows.Next() {
		var id uuid.UUID
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, err
		}
		result[id] = count
	}
	return result, rows.Err()
}

// ─── Mutual follow non-friends (app DB) ──────────────────────

// GetMutualFollowNonFriends returns users where both follow each other but are not friends.
func (s *Store) GetMutualFollowNonFriends(ctx context.Context, viewerID uuid.UUID, excludeIDs []uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.appDB.Query(ctx, `
		SELECT f1.followee_id
		FROM follows f1
		JOIN follows f2 ON f1.followee_id = f2.follower_id AND f1.follower_id = f2.followee_id
		WHERE f1.follower_id = $1
		  AND f1.followee_id != ALL($2::uuid[])
		  AND NOT EXISTS (
			SELECT 1 FROM connections
			WHERE (user_a = LEAST($1, f1.followee_id) AND user_b = GREATEST($1, f1.followee_id))
		  )
	`, viewerID, excludeIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ─── Follow suggestion queries (app DB) ──────────────────────

// GetSocialProofCandidates returns followees of viewer's friends that viewer doesn't follow.
func (s *Store) GetSocialProofCandidates(ctx context.Context, viewerID uuid.UUID, limit int) (map[uuid.UUID]int, error) {
	q := `
	WITH my_friends AS (
		SELECT CASE WHEN user_a = $1 THEN user_b ELSE user_a END AS fid
		FROM connections WHERE user_a = $1 OR user_b = $1
	)
	SELECT f.followee_id, COUNT(DISTINCT mf.fid)::int AS friend_count
	FROM follows f
	JOIN my_friends mf ON f.follower_id = mf.fid
	WHERE f.followee_id != $1
	  AND NOT EXISTS (SELECT 1 FROM follows WHERE follower_id = $1 AND followee_id = f.followee_id)
	GROUP BY f.followee_id
	ORDER BY friend_count DESC
	LIMIT $2
	`
	rows, err := s.appDB.Query(ctx, q, viewerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[uuid.UUID]int)
	for rows.Next() {
		var id uuid.UUID
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, err
		}
		result[id] = count
	}
	return result, rows.Err()
}

// GetTrendingCreators returns popular users by follower count with recent activity.
func (s *Store) GetTrendingCreators(ctx context.Context, excludeIDs []uuid.UUID, limit int) ([]uuid.UUID, error) {
	rows, err := s.appDB.Query(ctx, `
		SELECT user_id FROM counts
		WHERE user_id != ALL($1::uuid[]) AND follower_count > 0
		ORDER BY follower_count DESC
		LIMIT $2
	`, excludeIDs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ─── Dismiss patterns ────────────────────────────────────────

// GetDismissPatterns returns dismiss penalty weights for a viewer.
func (s *Store) GetDismissPatterns(ctx context.Context, viewerID uuid.UUID) (map[string]float32, error) {
	rows, err := s.appDB.Query(ctx, `
		SELECT signal_type, penalty_weight FROM suggestion_dismiss_patterns
		WHERE viewer_id = $1
	`, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]float32)
	for rows.Next() {
		var sig string
		var weight float32
		if err := rows.Scan(&sig, &weight); err != nil {
			return nil, err
		}
		result[sig] = weight
	}
	return result, rows.Err()
}

// UpsertDismissPattern increments dismiss count and reduces penalty weight.
func (s *Store) UpsertDismissPattern(ctx context.Context, viewerID uuid.UUID, signalType string) error {
	_, err := s.appDB.Exec(ctx, `
		INSERT INTO suggestion_dismiss_patterns (viewer_id, signal_type, dismiss_count, penalty_weight, last_dismissed)
		VALUES ($1, $2, 1, 0.8, now())
		ON CONFLICT (viewer_id, signal_type) DO UPDATE SET
			dismiss_count = suggestion_dismiss_patterns.dismiss_count + 1,
			penalty_weight = GREATEST(0.4, suggestion_dismiss_patterns.penalty_weight * 0.8),
			last_dismissed = now()
	`, viewerID, signalType)
	return err
}

// DecayDismissPatterns moves penalty weights toward 1.0 over time.
func (s *Store) DecayDismissPatterns(ctx context.Context) error {
	_, err := s.appDB.Exec(ctx, `
		UPDATE suggestion_dismiss_patterns
		SET penalty_weight = LEAST(1.0, penalty_weight + 0.05)
		WHERE penalty_weight < 1.0 AND last_dismissed < now() - INTERVAL '7 days'
	`)
	return err
}

// ─── Block propagation & safety ──────────────────────────────

// GetBlockCountByFriends returns how many of viewer's friends have blocked the candidate.
func (s *Store) GetBlockCountByFriends(ctx context.Context, viewerID, candidateID uuid.UUID) (int, error) {
	var count int
	err := s.appDB.QueryRow(ctx, `
		SELECT COUNT(*)::int FROM blocks b
		JOIN (
			SELECT CASE WHEN user_a = $1 THEN user_b ELSE user_a END AS fid
			FROM connections WHERE user_a = $1 OR user_b = $1
		) mf ON b.blocker_id = mf.fid
		WHERE b.blocked_id = $2
	`, viewerID, candidateID).Scan(&count)
	return count, err
}

// GetBlockCountByFriendsBatch returns block counts for multiple candidates.
func (s *Store) GetBlockCountByFriendsBatch(ctx context.Context, viewerID uuid.UUID, candidateIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	if len(candidateIDs) == 0 {
		return map[uuid.UUID]int{}, nil
	}
	rows, err := s.appDB.Query(ctx, `
		SELECT b.blocked_id, COUNT(*)::int
		FROM blocks b
		JOIN (
			SELECT CASE WHEN user_a = $1 THEN user_b ELSE user_a END AS fid
			FROM connections WHERE user_a = $1 OR user_b = $1
		) mf ON b.blocker_id = mf.fid
		WHERE b.blocked_id = ANY($2)
		GROUP BY b.blocked_id
	`, viewerID, candidateIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[uuid.UUID]int)
	for rows.Next() {
		var id uuid.UUID
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, err
		}
		result[id] = count
	}
	return result, rows.Err()
}

// GetCandidateBlockRate returns the overall block rate for a candidate.
func (s *Store) GetCandidateBlockRate(ctx context.Context, candidateID uuid.UUID) (float32, error) {
	var blockCount, followerCount int
	err := s.appDB.QueryRow(ctx, `
		SELECT COALESCE((SELECT COUNT(*)::int FROM blocks WHERE blocked_id = $1), 0),
		       COALESCE((SELECT follower_count FROM counts WHERE user_id = $1), 0)
	`, candidateID).Scan(&blockCount, &followerCount)
	if err != nil || followerCount == 0 {
		return 0, err
	}
	return float32(blockCount) / float32(followerCount+blockCount), nil
}

// ─── Mutual friend IDs (for API response) ────────────────────

// GetMutualFriendIDs returns actual mutual friend IDs between viewer and candidate.
func (s *Store) GetMutualFriendIDs(ctx context.Context, viewerID, candidateID uuid.UUID, limit int) ([]uuid.UUID, error) {
	rows, err := s.appDB.Query(ctx, `
		SELECT vf.fid FROM (
			SELECT CASE WHEN user_a = $1 THEN user_b ELSE user_a END AS fid
			FROM connections WHERE user_a = $1 OR user_b = $1
		) vf
		JOIN (
			SELECT CASE WHEN user_a = $2 THEN user_b ELSE user_a END AS fid
			FROM connections WHERE user_a = $2 OR user_b = $2
		) cf ON vf.fid = cf.fid
		LIMIT $3
	`, viewerID, candidateID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ─── Stale candidate rotation ────────────────────────────────

// RotateStaleCandidates applies decay to candidates shown 5+ times.
func (s *Store) RotateStaleCandidates(ctx context.Context) (int64, error) {
	tag, err := s.appDB.Exec(ctx, `
		UPDATE suggestion_candidates
		SET base_score = base_score * 0.3
		WHERE impression_count >= 5 AND base_score > 0
	`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// RemoveCandidateFromAllViewers removes a candidate from all viewers' pools.
func (s *Store) RemoveCandidateFromAllViewers(ctx context.Context, candidateID uuid.UUID) error {
	_, err := s.appDB.Exec(ctx, `
		DELETE FROM suggestion_candidates WHERE candidate_id = $1
	`, candidateID)
	return err
}

// ─── Active users query ──────────────────────────────────────

// GetActiveUsers returns user IDs with recent activity (based on counts table updated_at).
func (s *Store) GetActiveUsers(ctx context.Context, window time.Duration) ([]uuid.UUID, error) {
	cutoff := time.Now().Add(-window)
	rows, err := s.appDB.Query(ctx, `
		SELECT user_id FROM counts WHERE updated_at >= $1
	`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ─── EnsureSchema update ─────────────────────────────────────

func (s *Store) EnsureSchemaV2(ctx context.Context) error {
	ddl := `
	CREATE TABLE IF NOT EXISTS suggestion_dismiss_patterns (
		viewer_id UUID NOT NULL, signal_type VARCHAR(50) NOT NULL,
		dismiss_count SMALLINT DEFAULT 1, penalty_weight REAL DEFAULT 0.8,
		last_dismissed TIMESTAMPTZ DEFAULT now(),
		PRIMARY KEY (viewer_id, signal_type)
	);
	ALTER TABLE suggestion_impressions ADD COLUMN IF NOT EXISTS experiment_id VARCHAR(50);
	ALTER TABLE suggestion_impressions ADD COLUMN IF NOT EXISTS variant_id VARCHAR(20);
	ALTER TABLE suggestion_actions ADD COLUMN IF NOT EXISTS experiment_id VARCHAR(50);
	ALTER TABLE suggestion_actions ADD COLUMN IF NOT EXISTS variant_id VARCHAR(20);
	CREATE INDEX IF NOT EXISTS idx_sc_expiry ON suggestion_candidates (expires_at) WHERE expires_at IS NOT NULL;
	`
	_, err := s.appDB.Exec(ctx, ddl)
	return err
}
