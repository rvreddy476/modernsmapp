package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/atpost/suggestion-service/internal/store"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Service handles suggestion business logic.
type Service struct {
	store       *store.Store
	scyllaStore *store.ScyllaStore
	rdb         *redis.Client
}

// New creates a new Service.
func New(s *store.Store, rdb *redis.Client) *Service {
	return &Service{store: s, rdb: rdb}
}

// SetScyllaStore sets the optional ScyllaDB store for pair signals.
func (s *Service) SetScyllaStore(ss *store.ScyllaStore) {
	s.scyllaStore = ss
}

// SuggestionItem is the API-facing suggestion object.
type SuggestionItem struct {
	CandidateUserID   string   `json:"candidate_user_id"`
	Username          *string  `json:"username,omitempty"`
	DisplayName       string   `json:"display_name"`
	AvatarMediaID     *string  `json:"avatar_media_id,omitempty"`
	Score             float32  `json:"score"`
	ReasonCodes       []string `json:"reason_codes"`
	ExplainText       string   `json:"explain_text"`
	SourceBucket      string   `json:"source_bucket"`
	MutualFriendCount int      `json:"mutual_friend_count"`
	MutualFriendIDs   []string `json:"mutual_friend_ids,omitempty"`
	IsFresh           bool     `json:"is_fresh"`
	GeneratedAt       string   `json:"generated_at,omitempty"`
}

// SuggestionsResponse is the full response payload.
type SuggestionsResponse struct {
	Type         string           `json:"type"`
	Items        []SuggestionItem `json:"items"`
	NextCursor   string           `json:"next_cursor,omitempty"`
	Surface      string           `json:"surface,omitempty"`
	ExperimentID string           `json:"experiment_id,omitempty"`
	VariantID    string           `json:"variant_id,omitempty"`
	GeneratedAt  string           `json:"generated_at,omitempty"`
}

// ImpressionRequest is the request body for logging impressions.
type ImpressionRequest struct {
	Type         string `json:"type"`
	Surface      string `json:"surface"`
	SessionID    string `json:"session_id,omitempty"`
	ExperimentID string `json:"experiment_id,omitempty"`
	VariantID    string `json:"variant_id,omitempty"`
	Items        []struct {
		CandidateUserID string  `json:"candidate_user_id"`
		Rank            int16   `json:"rank"`
		Score           float32 `json:"score"`
	} `json:"items"`
}

// ActionRequest is the request body for recording actions.
type ActionRequest struct {
	Type            string `json:"type"`
	Surface         string `json:"surface"`
	CandidateUserID string `json:"candidate_user_id"`
	Action          string `json:"action"`
	SignalType      string `json:"signal_type,omitempty"` // for dismiss_category
	ExperimentID    string `json:"experiment_id,omitempty"`
	VariantID       string `json:"variant_id,omitempty"`
}

// GetSuggestions returns ranked suggestions for a viewer.
func (s *Service) GetSuggestions(ctx context.Context, viewerID uuid.UUID, suggType string, limit int, cursor string, surface string) (*SuggestionsResponse, error) {
	cacheKey := fmt.Sprintf("suggestions:%s:%s:%s", viewerID, suggType, surface)
	if surface == "" {
		cacheKey = fmt.Sprintf("suggestions:%s:%s", viewerID, suggType)
	}
	offset := decodeCursor(cursor)

	// 1. Try Redis cache
	val, err := s.rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		var items []SuggestionItem
		if json.Unmarshal([]byte(val), &items) == nil && len(items) > 0 {
			resp := s.paginateItems(items, suggType, offset, limit)
			resp.Surface = surface
			resp.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
			return resp, nil
		}
	}

	// 2. Cache miss → read from DB
	candidates, err := s.store.GetCandidates(ctx, viewerID, suggType, 50, 0)
	if err != nil {
		log.Printf("[suggestions] DB read error: %v", err)
	}

	// 3. No candidates → on-demand batch (5s timeout)
	if len(candidates) == 0 {
		batchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := s.RunBatchForUser(batchCtx, viewerID); err != nil {
			log.Printf("[suggestions] on-demand batch error: %v", err)
		}
		candidates, _ = s.store.GetCandidates(ctx, viewerID, suggType, 50, 0)
	}

	// 4. Still empty → popular users fallback
	if len(candidates) == 0 {
		return s.getPopularFallback(ctx, viewerID, suggType, limit, surface)
	}

	// 5. Enrich with profile data + mutual friend IDs
	items := s.candidatesToItems(ctx, viewerID, candidates)

	// 6. Cache
	s.cacheItems(ctx, viewerID, suggType, items)

	// 7. Paginate and return
	resp := s.paginateItems(items, suggType, offset, limit)
	resp.Surface = surface
	resp.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	return resp, nil
}

// GetInterstitialSuggestions returns contextual suggestions after an action.
func (s *Service) GetInterstitialSuggestions(ctx context.Context, viewerID uuid.UUID, triggerType string, triggerUserID uuid.UUID, limit int) (*SuggestionsResponse, error) {
	var candidateIDs []uuid.UUID

	switch triggerType {
	case "friend_accept":
		// Return candidates who are friends with both viewer AND the new friend
		mutualFriends, _ := s.store.GetMutualFriendIDs(ctx, viewerID, triggerUserID, limit*3)
		// Filter to those NOT already friends with viewer
		viewerFriends, _ := s.store.GetFriendIDs(ctx, viewerID)
		viewerFriendSet := make(map[uuid.UUID]bool)
		viewerFriendSet[viewerID] = true
		viewerFriendSet[triggerUserID] = true
		for _, id := range viewerFriends {
			viewerFriendSet[id] = true
		}
		// Get friends of the trigger user as candidates
		triggerFriends, _ := s.store.GetFriendIDs(ctx, triggerUserID)
		for _, fid := range triggerFriends {
			if !viewerFriendSet[fid] && len(candidateIDs) < limit {
				candidateIDs = append(candidateIDs, fid)
			}
		}
		// Also add mutual friends not yet friends with viewer
		for _, mfid := range mutualFriends {
			if !viewerFriendSet[mfid] && len(candidateIDs) < limit {
				candidateIDs = append(candidateIDs, mfid)
			}
		}

	case "follow":
		// Return candidates followed by people who also follow the trigger user
		socialProof, _ := s.store.GetSocialProofCandidates(ctx, viewerID, limit*3)
		for cid := range socialProof {
			if cid != viewerID && cid != triggerUserID && len(candidateIDs) < limit {
				candidateIDs = append(candidateIDs, cid)
			}
		}
	}

	if len(candidateIDs) == 0 {
		return &SuggestionsResponse{Type: "friend", Items: []SuggestionItem{}, Surface: "interstitial"}, nil
	}

	if len(candidateIDs) > limit {
		candidateIDs = candidateIDs[:limit]
	}

	// Enrich with profiles
	profiles, _ := s.store.GetProfileInfoBatch(ctx, candidateIDs)
	items := make([]SuggestionItem, 0, len(candidateIDs))
	for _, cid := range candidateIDs {
		profile := profiles[cid]
		item := SuggestionItem{
			CandidateUserID: cid.String(),
			ReasonCodes:     []string{"INTERSTITIAL"},
			ExplainText:     "You might also know them",
			SourceBucket:    "interstitial",
			IsFresh:         true,
			GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
		}
		if profile != nil {
			item.DisplayName = profile.DisplayName
			item.Username = profile.Username
			if profile.AvatarMediaID != nil {
				aid := profile.AvatarMediaID.String()
				item.AvatarMediaID = &aid
			}
		} else {
			item.DisplayName = "Unknown User"
		}
		items = append(items, item)
	}

	return &SuggestionsResponse{
		Type:        "friend",
		Items:       items,
		Surface:     "interstitial",
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// LogImpressions records suggestion impressions.
func (s *Service) LogImpressions(ctx context.Context, viewerID uuid.UUID, req *ImpressionRequest) error {
	var sessionID *uuid.UUID
	if req.SessionID != "" {
		if id, err := uuid.Parse(req.SessionID); err == nil {
			sessionID = &id
		}
	}

	for _, item := range req.Items {
		candidateID, err := uuid.Parse(item.CandidateUserID)
		if err != nil {
			continue
		}
		imp := &store.Impression{
			ViewerID:       viewerID,
			CandidateID:    candidateID,
			Surface:        req.Surface,
			SuggestionType: req.Type,
			RankPosition:   item.Rank,
			Score:          item.Score,
			SessionID:      sessionID,
			ExperimentID:   req.ExperimentID,
			VariantID:      req.VariantID,
		}
		if err := s.store.LogImpression(ctx, imp); err != nil {
			log.Printf("[impressions] log error: %v", err)
		}
	}
	return nil
}

// RecordAction records a user action on a suggestion.
func (s *Service) RecordAction(ctx context.Context, viewerID uuid.UUID, req *ActionRequest) error {
	candidateID, err := uuid.Parse(req.CandidateUserID)
	if err != nil {
		return fmt.Errorf("invalid candidate_user_id: %w", err)
	}

	// Log the action
	action := &store.Action{
		ViewerID:       viewerID,
		CandidateID:    candidateID,
		ActionType:     req.Action,
		Surface:        req.Surface,
		SuggestionType: req.Type,
		ExperimentID:   req.ExperimentID,
		VariantID:      req.VariantID,
	}
	if err := s.store.LogAction(ctx, action); err != nil {
		log.Printf("[action] log error: %v", err)
	}

	// Apply side effects
	switch req.Action {
	case "hide", "dismiss":
		cooldownUntil := time.Now().Add(30 * 24 * time.Hour) // 30 days
		cd := store.CooldownEntry{
			ViewerID:      viewerID,
			CandidateID:   candidateID,
			CooldownType:  "hide",
			CooldownUntil: &cooldownUntil,
		}
		s.store.CreateCooldown(ctx, cd)
		s.store.RemoveCandidateForViewer(ctx, viewerID, candidateID, req.Type)
		s.invalidateCache(ctx, viewerID, req.Type)

	case "decline":
		cooldownUntil := time.Now().Add(90 * 24 * time.Hour) // 90 days
		cd := store.CooldownEntry{
			ViewerID:      viewerID,
			CandidateID:   candidateID,
			CooldownType:  "decline",
			CooldownUntil: &cooldownUntil,
		}
		s.store.CreateCooldown(ctx, cd)
		s.store.RemoveCandidateForViewer(ctx, viewerID, candidateID, req.Type)
		s.invalidateCache(ctx, viewerID, req.Type)

	case "block":
		cd := store.CooldownEntry{
			ViewerID:     viewerID,
			CandidateID:  candidateID,
			CooldownType: "block",
			// CooldownUntil nil = permanent
		}
		s.store.CreateCooldown(ctx, cd)
		s.store.RemoveCandidateForViewer(ctx, viewerID, candidateID, req.Type)
		s.store.RemoveCandidateFromAllViewers(ctx, candidateID)
		s.invalidateCache(ctx, viewerID, req.Type)

	case "dismiss_category":
		// Update dismiss pattern for the signal type
		if req.SignalType != "" {
			s.store.UpsertDismissPattern(ctx, viewerID, req.SignalType)
		}
		s.invalidateCache(ctx, viewerID, req.Type)

	case "friend_request", "follow":
		// Just log the action; remove from candidates
		s.store.RemoveCandidateForViewer(ctx, viewerID, candidateID, req.Type)
		s.invalidateCache(ctx, viewerID, req.Type)
	}

	return nil
}

// CreateCooldownFromEvent creates a cooldown triggered by an event (not direct API).
func (s *Service) CreateCooldownFromEvent(ctx context.Context, viewerID, candidateID uuid.UUID, cooldownType string) {
	var cooldownUntil *time.Time
	switch cooldownType {
	case "decline":
		t := time.Now().Add(90 * 24 * time.Hour)
		cooldownUntil = &t
	case "removed_friend":
		t := time.Now().Add(180 * 24 * time.Hour)
		cooldownUntil = &t
	case "block":
		// nil = permanent
	default:
		t := time.Now().Add(30 * 24 * time.Hour)
		cooldownUntil = &t
	}

	cd := store.CooldownEntry{
		ViewerID:      viewerID,
		CandidateID:   candidateID,
		CooldownType:  cooldownType,
		CooldownUntil: cooldownUntil,
	}
	s.store.CreateCooldown(ctx, cd)
}

// InvalidateCache removes cached suggestions for a user.
func (s *Service) InvalidateCache(ctx context.Context, userID string, suggType string) {
	s.invalidateCache(ctx, uuid.MustParse(userID), suggType)
}

// ─── Internal helpers ────────────────────────────────────────

func (s *Service) candidatesToItems(ctx context.Context, viewerID uuid.UUID, candidates []store.SuggestionCandidate) []SuggestionItem {
	// Collect candidate IDs for batch profile fetch
	ids := make([]uuid.UUID, len(candidates))
	for i, c := range candidates {
		ids[i] = c.CandidateID
	}

	profiles, err := s.store.GetProfileInfoBatch(ctx, ids)
	if err != nil {
		log.Printf("[suggestions] profile batch fetch error: %v", err)
		profiles = map[uuid.UUID]*store.ProfileInfo{}
	}

	items := make([]SuggestionItem, 0, len(candidates))
	for _, c := range candidates {
		profile := profiles[c.CandidateID]
		item := SuggestionItem{
			CandidateUserID:   c.CandidateID.String(),
			Score:             c.BaseScore,
			ReasonCodes:       c.ReasonCodes,
			ExplainText:       c.ExplainText,
			SourceBucket:      c.SourceBucket,
			MutualFriendCount: int(c.MutualFriendCount),
			IsFresh:           c.ImpressionCount < 2,
			GeneratedAt:       c.GeneratedAt.UTC().Format(time.RFC3339),
		}

		// Fetch mutual friend IDs (up to 3 for display)
		if c.MutualFriendCount > 0 {
			mfIDs, _ := s.store.GetMutualFriendIDs(ctx, viewerID, c.CandidateID, 3)
			for _, mfid := range mfIDs {
				item.MutualFriendIDs = append(item.MutualFriendIDs, mfid.String())
			}
		}

		if profile != nil {
			item.DisplayName = profile.DisplayName
			item.Username = profile.Username
			if profile.AvatarMediaID != nil {
				aid := profile.AvatarMediaID.String()
				item.AvatarMediaID = &aid
			}
		} else {
			item.DisplayName = "Unknown User"
		}
		items = append(items, item)
	}
	return items
}

func (s *Service) cacheItems(ctx context.Context, viewerID uuid.UUID, suggType string, items []SuggestionItem) {
	cacheKey := fmt.Sprintf("suggestions:%s:%s", viewerID, suggType)
	data, err := json.Marshal(items)
	if err != nil {
		return
	}
	ttl := time.Hour
	if suggType == "follow" {
		ttl = 30 * time.Minute
	}
	s.rdb.Set(ctx, cacheKey, data, ttl)
}

func (s *Service) invalidateCache(ctx context.Context, viewerID uuid.UUID, suggType string) {
	key := fmt.Sprintf("suggestions:%s:%s", viewerID, suggType)
	s.rdb.Del(ctx, key)
}

func (s *Service) paginateItems(items []SuggestionItem, suggType string, offset, limit int) *SuggestionsResponse {
	if offset >= len(items) {
		return &SuggestionsResponse{Type: suggType, Items: []SuggestionItem{}}
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	page := items[offset:end]
	nextCursor := ""
	if end < len(items) {
		nextCursor = encodeCursor(end)
	}
	return &SuggestionsResponse{
		Type:       suggType,
		Items:      page,
		NextCursor: nextCursor,
	}
}

func (s *Service) getPopularFallback(ctx context.Context, viewerID uuid.UUID, suggType string, limit int, surface string) (*SuggestionsResponse, error) {
	excludeIDs := []uuid.UUID{viewerID}
	friendIDs, _ := s.store.GetFriendIDs(ctx, viewerID)
	excludeIDs = append(excludeIDs, friendIDs...)

	popular, err := s.store.GetPopularUsers(ctx, limit+len(excludeIDs))
	if err != nil {
		return &SuggestionsResponse{Type: suggType, Items: []SuggestionItem{}}, nil
	}

	excludeSet := make(map[uuid.UUID]bool)
	for _, id := range excludeIDs {
		excludeSet[id] = true
	}

	var filtered []uuid.UUID
	for _, id := range popular {
		if !excludeSet[id] && len(filtered) < limit {
			filtered = append(filtered, id)
		}
	}

	profiles, _ := s.store.GetProfileInfoBatch(ctx, filtered)
	items := make([]SuggestionItem, 0, len(filtered))
	for _, id := range filtered {
		profile := profiles[id]
		item := SuggestionItem{
			CandidateUserID: id.String(),
			ReasonCodes:     []string{"POPULAR"},
			ExplainText:     "Popular on atpost",
			SourceBucket:    "trending",
			IsFresh:         true,
			GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
		}
		if profile != nil {
			item.DisplayName = profile.DisplayName
			item.Username = profile.Username
			if profile.AvatarMediaID != nil {
				aid := profile.AvatarMediaID.String()
				item.AvatarMediaID = &aid
			}
		}
		items = append(items, item)
	}

	return &SuggestionsResponse{
		Type:        suggType,
		Items:       items,
		Surface:     surface,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// ─── Cursor helpers ──────────────────────────────────────────

func encodeCursor(offset int) string {
	data := fmt.Sprintf(`{"offset":%d}`, offset)
	return base64.StdEncoding.EncodeToString([]byte(data))
}

func decodeCursor(cursor string) int {
	if cursor == "" {
		return 0
	}
	data, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return 0
	}
	var c struct {
		Offset int `json:"offset"`
	}
	json.Unmarshal(data, &c)
	return c.Offset
}
