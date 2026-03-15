package service

import (
	"context"
	"log"
	"sort"
	"time"

	"github.com/atpost/suggestion-service/internal/store"
	"github.com/google/uuid"
)

// ─── RunHotSignalsRefresh — every 1 hour (active users only) ──

// RunHotSignalsRefresh recomputes fast-changing signals for recently active users
// and writes them to ScyllaDB user_pair_signals.
func (s *Service) RunHotSignalsRefresh(ctx context.Context) error {
	log.Println("[batch:hot] Starting hot signals refresh")
	start := time.Now()

	// Get users active in last 24 hours
	activeUserIDs, err := s.store.GetActiveUsers(ctx, 24*time.Hour)
	if err != nil {
		return err
	}

	log.Printf("[batch:hot] Processing %d active users", len(activeUserIDs))
	var errCount int

	for _, viewerID := range activeUserIDs {
		if ctx.Err() != nil {
			break
		}

		// Get viewer's current friend candidate IDs
		candidates, err := s.store.GetCandidates(ctx, viewerID, "friend", 200, 0)
		if err != nil || len(candidates) == 0 {
			continue
		}

		candidateIDs := make([]uuid.UUID, len(candidates))
		for i, c := range candidates {
			candidateIDs[i] = c.CandidateID
		}

		// Batch check mutual follows
		mutualFollows, _ := s.store.BatchCheckMutualFollows(ctx, viewerID, candidateIDs)

		// Write signals to ScyllaDB for each pair
		if s.scyllaStore != nil {
			for _, cid := range candidateIDs {
				ps := &store.PairSignals{
					ViewerID:     viewerID,
					CandidateID:  cid,
					MutualFollow: mutualFollows[cid],
				}
				if err := s.scyllaStore.UpsertPairSignals(ctx, ps); err != nil {
					errCount++
				}
			}
		}
	}

	log.Printf("[batch:hot] Complete: %d users in %s (%d errors)", len(activeUserIDs), time.Since(start), errCount)
	return nil
}

// ─── RunFriendCandidatesFull — every 6 hours ──────────────────

// RunFriendCandidatesFull generates, scores, and stores friend candidates for all users.
func (s *Service) RunFriendCandidatesFull(ctx context.Context) error {
	log.Println("[batch:friend] Starting full friend candidates run")
	start := time.Now()

	userIDs, err := s.store.GetAllUsersWithFriends(ctx)
	if err != nil {
		return err
	}

	log.Printf("[batch:friend] Processing %d users", len(userIDs))
	var errCount int
	for i, uid := range userIDs {
		if ctx.Err() != nil {
			break
		}
		if err := s.runFriendBatchForUser(ctx, uid); err != nil {
			errCount++
			log.Printf("[batch:friend] error for user %s: %v", uid, err)
		}
		if (i+1)%100 == 0 {
			log.Printf("[batch:friend] progress: %d/%d users", i+1, len(userIDs))
		}
	}

	log.Printf("[batch:friend] Complete: %d users in %s (%d errors)", len(userIDs), time.Since(start), errCount)
	return nil
}

// runFriendBatchForUser generates and scores friend candidates for a single user.
func (s *Service) runFriendBatchForUser(ctx context.Context, viewerID uuid.UUID) error {
	// 1. Load exclusion set
	friendIDs, err := s.store.GetFriendIDs(ctx, viewerID)
	if err != nil {
		return err
	}
	blockedIDs, _ := s.store.GetBlockedIDs(ctx, viewerID)
	pendingIDs, _ := s.store.GetPendingRequestIDs(ctx, viewerID)
	cooldowns, _ := s.store.GetActiveCooldowns(ctx, viewerID)

	excludeSet := make(map[uuid.UUID]bool)
	excludeSet[viewerID] = true
	for _, id := range friendIDs {
		excludeSet[id] = true
	}
	for _, id := range blockedIDs {
		excludeSet[id] = true
	}
	for _, id := range pendingIDs {
		excludeSet[id] = true
	}
	for id := range cooldowns {
		excludeSet[id] = true
	}
	excludeSlice := mapKeys(excludeSet)

	// 2. Generate candidates from multiple sources

	// FoF candidates (friends-of-friends)
	fofCandidates, err := s.store.GetFriendsOfFriends(ctx, viewerID, excludeSlice)
	if err != nil {
		return err
	}

	// Triadic closure candidates
	triadicCandidates, _ := s.store.GetTriadicClosureCandidates(ctx, viewerID, excludeSlice, 100)

	// Mutual follow non-friends
	mutualFollowIDs, _ := s.store.GetMutualFollowNonFriends(ctx, viewerID, excludeSlice)

	// 3. Load viewer profile for community matching
	viewerProfile, err := s.store.GetProfileInfo(ctx, viewerID)
	if err != nil {
		log.Printf("[batch:friend] could not load viewer profile %s: %v", viewerID, err)
		viewerProfile = &store.ProfileInfo{UserID: viewerID}
	}

	// 4. Community candidates: location & profession
	var communityCandidateIDs []uuid.UUID
	if viewerProfile.Location != "" {
		locUsers, _ := s.store.GetProfilesByLocation(ctx, viewerProfile.Location, 200)
		communityCandidateIDs = append(communityCandidateIDs, locUsers...)
	}
	if viewerProfile.Profession != "" {
		profUsers, _ := s.store.GetProfilesByProfession(ctx, viewerProfile.Profession, 200)
		communityCandidateIDs = append(communityCandidateIDs, profUsers...)
	}

	// 5. Group-based community candidates
	var groupCandidateIDs []uuid.UUID
	groupCandidateIDs, _ = s.store.GetGroupMemberCandidates(ctx, viewerID, excludeSlice, 200)

	// 6. School/company-based community candidates
	viewerLifeEntries, _ := s.store.GetUserLifeEntries(ctx, viewerID)
	var schoolCandidateIDs, companyCandidateIDs []uuid.UUID
	var viewerSchools, viewerCompanies []string
	for _, entry := range viewerLifeEntries {
		if entry.Name == "" {
			continue
		}
		switch entry.Type {
		case "school", "university":
			viewerSchools = append(viewerSchools, entry.Name)
			users, _ := s.store.GetUsersByLifeEntry(ctx, entry.Name, 100)
			schoolCandidateIDs = append(schoolCandidateIDs, users...)
		case "company", "work":
			viewerCompanies = append(viewerCompanies, entry.Name)
			users, _ := s.store.GetUsersByLifeEntry(ctx, entry.Name, 100)
			companyCandidateIDs = append(companyCandidateIDs, users...)
		}
	}

	// 7. Merge all candidate pools, deduplicate, exclude
	allCandidateIDs := collectUniqueMulti(
		fofCandidates, triadicCandidates,
		mutualFollowIDs, communityCandidateIDs,
		groupCandidateIDs, schoolCandidateIDs, companyCandidateIDs,
		excludeSet,
	)

	if len(allCandidateIDs) == 0 {
		// Fallback to popular users
		popular, _ := s.store.GetPopularUsers(ctx, 200)
		for _, id := range popular {
			if !excludeSet[id] {
				allCandidateIDs = append(allCandidateIDs, id)
			}
		}
	}

	if len(allCandidateIDs) == 0 {
		return nil
	}

	// 8. Batch load profiles and life entries
	profiles, err := s.store.GetProfileInfoBatch(ctx, allCandidateIDs)
	if err != nil {
		return err
	}

	// 9. Batch check mutual follows
	mutualFollows, _ := s.store.BatchCheckMutualFollows(ctx, viewerID, allCandidateIDs)

	// 10. Batch load common group counts
	commonGroupCounts, _ := s.store.GetCommonGroupCountBatch(ctx, viewerID, allCandidateIDs)

	// 11. Batch load block propagation signals
	blockByFriends, _ := s.store.GetBlockCountByFriendsBatch(ctx, viewerID, allCandidateIDs)

	// 12. Load dismiss patterns
	dismissPatterns, _ := s.store.GetDismissPatterns(ctx, viewerID)

	// 13. Load pair signals from ScyllaDB (if available)
	var pairSignals map[uuid.UUID]*store.PairSignals
	if s.scyllaStore != nil {
		pairSignals, _ = s.scyllaStore.GetPairSignalsBatch(ctx, viewerID, allCandidateIDs)
	}
	if pairSignals == nil {
		pairSignals = make(map[uuid.UUID]*store.PairSignals)
	}

	// Prepare viewer state
	viewerIsNew := time.Since(viewerProfile.CreatedAt) < 14*24*time.Hour
	viewerNewDays := int(time.Since(viewerProfile.CreatedAt).Hours() / 24)

	// Build school/company lookup sets for the viewer
	viewerSchoolSet := toSet(viewerSchools)
	viewerCompanySet := toSet(viewerCompanies)

	// 14. Score each candidate
	type scoredEntry struct {
		candidate   store.SuggestionCandidate
		schoolName  string
		companyName string
		cityName    string
		cityOnly    bool
		isFresh     bool
	}
	var scored []scoredEntry

	for _, candidateID := range allCandidateIDs {
		profile := profiles[candidateID]
		if profile == nil {
			continue
		}

		mutualCount := fofCandidates[candidateID]
		triadicRaw := triadicCandidates[candidateID]
		// Normalize triadic closure count to 0.0-1.0 score
		var triadicScore float32
		if triadicRaw > 0 {
			triadicScore = float32(triadicRaw) / float32(triadicRaw+5) // asymptotic normalization
		}
		sameLocation := viewerProfile.Location != "" && viewerProfile.Location == profile.Location
		sameProfession := viewerProfile.Profession != "" && viewerProfile.Profession == profile.Profession
		isMutualFollow := mutualFollows[candidateID]
		commonGroups := commonGroupCounts[candidateID]

		// Check school/company overlap from candidate's life entries
		candidateEntries, _ := s.store.GetUserLifeEntries(ctx, candidateID)
		var sameSchool, sameCompany bool
		var matchedSchool, matchedCompany string
		for _, entry := range candidateEntries {
			switch entry.Type {
			case "school", "university":
				if viewerSchoolSet[entry.Name] {
					sameSchool = true
					matchedSchool = entry.Name
				}
			case "company", "work":
				if viewerCompanySet[entry.Name] {
					sameCompany = true
					matchedCompany = entry.Name
				}
			}
		}

		// Determine dismiss penalty
		var dismissPenalty float32
		reasons := computeReasonTypes(mutualCount, commonGroups, sameLocation, sameSchool, sameCompany, sameProfession, isMutualFollow)
		for _, r := range reasons {
			if pen, ok := dismissPatterns[r]; ok && (dismissPenalty == 0 || pen < dismissPenalty) {
				dismissPenalty = pen
			}
		}

		// Safety signals
		blockCount := blockByFriends[candidateID]
		blockRate, _ := s.store.GetCandidateBlockRate(ctx, candidateID)

		// Pair signals from ScyllaDB (nil-safe)
		ps := pairSignals[candidateID]
		if ps == nil {
			ps = &store.PairSignals{}
		}

		// Find best group name for explain text
		var groupName string
		if commonGroups > 0 {
			groupName = "a group" // Could be enhanced with actual group name lookup
		}

		input := ScoringInput{
			MutualFriendCount:     mutualCount,
			MutualStrongCount:     ps.MutualStrongCount,
			CommonGroupCount:      commonGroups,
			SameLocation:          sameLocation,
			SameProfession:        sameProfession,
			SameSchool:            sameSchool,
			SameCompany:           sameCompany,
			MutualFollow:          isMutualFollow,
			TriadicClosureScore:   triadicScore,
			ProfileView7d:         ps.ProfileView7d,
			ReverseView7d:         ps.ReverseView7d,
			InterestSimilarity:    ps.InterestSimilarity,
			ContentCoengagement7d: ps.ContentCoengagement7d,
			ContactMatch:          false, // no contact import feature
			LocationValue:         profile.Location,
			ProfessionValue:       profile.Profession,
			SchoolName:            matchedSchool,
			CompanyName:           matchedCompany,
			GroupName:             groupName,
			IsNewUser:             viewerIsNew,
			NewUserDays:           viewerNewDays,
			IsActive:              time.Since(profile.CreatedAt) < 30*24*time.Hour,
			ImpressionCount:       0, // will be applied at query time
			DismissPenalty:        dismissPenalty,
			BlockPropagation:      blockCount,
			CandidateBlockRate:    blockRate,
		}

		result := ScoreFriendCandidate(input)
		if result.Score <= 0 {
			continue
		}

		bucket := categorizeBucket(mutualCount, commonGroups, sameSchool, sameCompany, sameLocation, isMutualFollow, triadicScore)

		cityOnly := sameLocation && mutualCount == 0 && !sameSchool && !sameCompany && commonGroups == 0 && !isMutualFollow

		scored = append(scored, scoredEntry{
			candidate: store.SuggestionCandidate{
				ViewerID:          viewerID,
				CandidateID:       candidateID,
				SuggestionType:    "friend",
				BaseScore:         result.Score,
				ReasonCodes:       result.ReasonCodes,
				ExplainText:       result.ExplainText,
				SourceBucket:      bucket,
				MutualFriendCount: int16(mutualCount),
				GeneratedAt:       time.Now(),
			},
			schoolName:  matchedSchool,
			companyName: matchedCompany,
			cityName:    profile.Location,
			cityOnly:    cityOnly,
			isFresh:     true, // newly generated
		})
	}

	// 15. Sort by score DESC
	sort.Slice(scored, func(i, j int) bool { return scored[i].candidate.BaseScore > scored[j].candidate.BaseScore })

	// 16. Apply diversity algorithm
	divCandidates := make([]DiversifiedCandidate, len(scored))
	for i, s := range scored {
		divCandidates[i] = DiversifiedCandidate{
			SuggestionCandidate: s.candidate,
			SchoolName:          s.schoolName,
			CompanyName:         s.companyName,
			CityName:            s.cityName,
			CityOnly:            s.cityOnly,
			IsFresh:             s.isFresh,
		}
	}
	diversified := ApplyDiversity(divCandidates, 200, DefaultDiversityConfig())

	// Extract final candidates
	finalCandidates := make([]store.SuggestionCandidate, len(diversified))
	for i, d := range diversified {
		finalCandidates[i] = d.SuggestionCandidate
	}

	// 17. Write to DB
	if err := s.store.DeleteCandidatesForViewer(ctx, viewerID, "friend"); err != nil {
		log.Printf("[batch:friend] delete old candidates for %s failed: %v", viewerID, err)
	}
	if len(finalCandidates) > 0 {
		if err := s.store.UpsertCandidates(ctx, finalCandidates); err != nil {
			return err
		}
	}

	// 18. Cache top 50 in Redis
	cacheLimit := 50
	if len(finalCandidates) < cacheLimit {
		cacheLimit = len(finalCandidates)
	}
	if cacheLimit > 0 {
		items := s.candidatesToItems(ctx, viewerID, finalCandidates[:cacheLimit])
		s.cacheItems(ctx, viewerID, "friend", items)
	}

	log.Printf("[batch:friend] viewer=%s candidates=%d scored=%d final=%d", viewerID, len(allCandidateIDs), len(scored), len(finalCandidates))
	return nil
}

// ─── RunFollowCandidatesFull — every 6 hours ─────────────────

// RunFollowCandidatesFull generates follow suggestions for all users.
func (s *Service) RunFollowCandidatesFull(ctx context.Context) error {
	log.Println("[batch:follow] Starting full follow candidates run")
	start := time.Now()

	userIDs, err := s.store.GetAllUsersWithFriends(ctx)
	if err != nil {
		return err
	}

	log.Printf("[batch:follow] Processing %d users", len(userIDs))
	var errCount int
	for i, uid := range userIDs {
		if ctx.Err() != nil {
			break
		}
		if err := s.runFollowBatchForUser(ctx, uid); err != nil {
			errCount++
			if errCount <= 10 {
				log.Printf("[batch:follow] error for user %s: %v", uid, err)
			}
		}
		if (i+1)%100 == 0 {
			log.Printf("[batch:follow] progress: %d/%d users", i+1, len(userIDs))
		}
	}

	log.Printf("[batch:follow] Complete: %d users in %s (%d errors)", len(userIDs), time.Since(start), errCount)
	return nil
}

func (s *Service) runFollowBatchForUser(ctx context.Context, viewerID uuid.UUID) error {
	// Load exclusion set — must include existing friends, follows, blocked, and cooldowns
	friendIDs, _ := s.store.GetFriendIDs(ctx, viewerID)
	followingIDs, _ := s.store.GetFollowingIDs(ctx, viewerID)
	blockedIDs, _ := s.store.GetBlockedIDs(ctx, viewerID)
	cooldowns, _ := s.store.GetActiveCooldowns(ctx, viewerID)

	excludeSet := make(map[uuid.UUID]bool)
	excludeSet[viewerID] = true
	for _, id := range friendIDs {
		excludeSet[id] = true
	}
	for _, id := range followingIDs {
		excludeSet[id] = true
	}
	for _, id := range blockedIDs {
		excludeSet[id] = true
	}
	for id := range cooldowns {
		excludeSet[id] = true
	}

	// Social proof candidates: "friends of viewer also follow X"
	socialProof, _ := s.store.GetSocialProofCandidates(ctx, viewerID, 200)

	// Trending creators
	excludeSlice := mapKeys(excludeSet)
	trending, _ := s.store.GetTrendingCreators(ctx, excludeSlice, 100)

	// Merge and score
	candidateScores := make(map[uuid.UUID]float32)
	candidateReasons := make(map[uuid.UUID][]string)
	candidateExplain := make(map[uuid.UUID]string)
	candidateBuckets := make(map[uuid.UUID]string)

	for candidateID, friendCount := range socialProof {
		if excludeSet[candidateID] {
			continue
		}
		result := ScoreFollowCandidate(FollowScoringInput{
			SocialProofCount: friendCount,
		})
		candidateScores[candidateID] = result.Score
		candidateReasons[candidateID] = result.ReasonCodes
		candidateExplain[candidateID] = result.ExplainText
		candidateBuckets[candidateID] = "social_proof"
	}

	for _, creatorID := range trending {
		if excludeSet[creatorID] {
			continue
		}
		// Only add if not already from social proof (or if social proof score is lower)
		result := ScoreFollowCandidate(FollowScoringInput{
			TrendingScore: 0.7,
		})
		if existing, ok := candidateScores[creatorID]; !ok || result.Score > existing {
			candidateScores[creatorID] = result.Score
			candidateReasons[creatorID] = result.ReasonCodes
			candidateExplain[creatorID] = result.ExplainText
			candidateBuckets[creatorID] = "trending"
		}
	}

	// Convert to sorted slice
	type followEntry struct {
		id    uuid.UUID
		score float32
	}
	var entries []followEntry
	for id, score := range candidateScores {
		entries = append(entries, followEntry{id, score})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].score > entries[j].score })
	if len(entries) > 200 {
		entries = entries[:200]
	}

	// Build store candidates
	candidates := make([]store.SuggestionCandidate, len(entries))
	for i, e := range entries {
		candidates[i] = store.SuggestionCandidate{
			ViewerID:       viewerID,
			CandidateID:    e.id,
			SuggestionType: "follow",
			BaseScore:      e.score,
			ReasonCodes:    candidateReasons[e.id],
			ExplainText:    candidateExplain[e.id],
			SourceBucket:   candidateBuckets[e.id],
			GeneratedAt:    time.Now(),
		}
	}

	// Write to DB
	if err := s.store.DeleteCandidatesForViewer(ctx, viewerID, "follow"); err != nil {
		log.Printf("[batch:follow] delete old candidates for %s failed: %v", viewerID, err)
	}
	if len(candidates) > 0 {
		if err := s.store.UpsertCandidates(ctx, candidates); err != nil {
			return err
		}
	}

	// Cache top 50
	cacheLimit := 50
	if len(candidates) < cacheLimit {
		cacheLimit = len(candidates)
	}
	if cacheLimit > 0 {
		items := s.candidatesToItems(ctx, viewerID, candidates[:cacheLimit])
		s.cacheItems(ctx, viewerID, "follow", items)
	}

	return nil
}

// ─── RunColdSignalsRefresh — every 24 hours ───────────────────

// RunColdSignalsRefresh performs daily maintenance: cleanup, rotation, and decay.
func (s *Service) RunColdSignalsRefresh(ctx context.Context) error {
	log.Println("[batch:cold] Starting cold signals refresh")
	start := time.Now()

	// 1. Clean expired cooldowns
	cleaned, err := s.store.CleanExpiredCooldowns(ctx)
	if err != nil {
		log.Printf("[batch:cold] cooldown cleanup error: %v", err)
	} else {
		log.Printf("[batch:cold] cleaned %d expired cooldowns", cleaned)
	}

	// 2. Rotate stale candidates (impression_count >= 5 → apply 0.3 multiplier)
	rotated, err := s.store.RotateStaleCandidates(ctx)
	if err != nil {
		log.Printf("[batch:cold] stale rotation error: %v", err)
	} else {
		log.Printf("[batch:cold] rotated %d stale candidates", rotated)
	}

	// 3. Decay dismiss patterns (reduce penalty_weight toward 1.0 over time)
	if err := s.store.DecayDismissPatterns(ctx); err != nil {
		log.Printf("[batch:cold] dismiss decay error: %v", err)
	}

	log.Printf("[batch:cold] Complete in %s", time.Since(start))
	return nil
}

// ─── Legacy API compatibility ─────────────────────────────────

// RunBatchForUser runs the friend batch for a single user (used for on-demand generation).
func (s *Service) RunBatchForUser(ctx context.Context, viewerID uuid.UUID) error {
	return s.runFriendBatchForUser(ctx, viewerID)
}

// RunFullBatch runs both friend and follow batches for all users.
func (s *Service) RunFullBatch(ctx context.Context) error {
	log.Println("[batch] Starting full batch run")
	start := time.Now()

	if err := s.RunFriendCandidatesFull(ctx); err != nil {
		log.Printf("[batch] friend candidates error: %v", err)
	}
	if err := s.RunFollowCandidatesFull(ctx); err != nil {
		log.Printf("[batch] follow candidates error: %v", err)
	}

	log.Printf("[batch] Full batch complete in %s", time.Since(start))
	return nil
}

// ─── Helpers ─────────────────────────────────────────────────

func mapKeys(m map[uuid.UUID]bool) []uuid.UUID {
	keys := make([]uuid.UUID, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func collectUniqueMulti(
	fofMap map[uuid.UUID]int,
	triadicMap map[uuid.UUID]int,
	mutualFollowIDs []uuid.UUID,
	communityIDs []uuid.UUID,
	groupIDs []uuid.UUID,
	schoolIDs []uuid.UUID,
	companyIDs []uuid.UUID,
	excludeSet map[uuid.UUID]bool,
) []uuid.UUID {
	seen := make(map[uuid.UUID]bool)
	var result []uuid.UUID

	add := func(id uuid.UUID) {
		if !excludeSet[id] && !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}

	for id := range fofMap {
		add(id)
	}
	for id := range triadicMap {
		add(id)
	}
	for _, id := range mutualFollowIDs {
		add(id)
	}
	for _, id := range communityIDs {
		add(id)
	}
	for _, id := range groupIDs {
		add(id)
	}
	for _, id := range schoolIDs {
		add(id)
	}
	for _, id := range companyIDs {
		add(id)
	}

	return result
}

func toSet(items []string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, item := range items {
		s[item] = true
	}
	return s
}

func categorizeBucket(mutualCount, commonGroups int, sameSchool, sameCompany, sameLocation, mutualFollow bool, triadicScore float32) string {
	if mutualCount > 0 {
		return "fof"
	}
	if triadicScore > 0 {
		return "triadic"
	}
	if mutualFollow {
		return "mutual_follow"
	}
	if commonGroups > 0 {
		return "group"
	}
	if sameSchool {
		return "school"
	}
	if sameCompany {
		return "company"
	}
	if sameLocation {
		return "location"
	}
	return "community"
}

func computeReasonTypes(mutualCount, commonGroups int, sameLocation, sameSchool, sameCompany, sameProfession, mutualFollow bool) []string {
	var reasons []string
	if mutualCount > 0 {
		reasons = append(reasons, "MUTUAL_FRIENDS")
	}
	if commonGroups > 0 {
		reasons = append(reasons, "COMMON_GROUPS")
	}
	if sameLocation {
		reasons = append(reasons, "SAME_CITY")
	}
	if sameSchool {
		reasons = append(reasons, "SAME_SCHOOL")
	}
	if sameCompany {
		reasons = append(reasons, "SAME_COMPANY")
	}
	if sameProfession {
		reasons = append(reasons, "SAME_PROFESSION")
	}
	if mutualFollow {
		reasons = append(reasons, "MUTUAL_FOLLOW")
	}
	return reasons
}
