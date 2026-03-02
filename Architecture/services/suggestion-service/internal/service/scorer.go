package service

import (
	"fmt"
	"math"
	"strings"
)

// ScoringInput holds all signals for scoring a friend candidate.
type ScoringInput struct {
	// Core graph signals
	MutualFriendCount int
	MutualStrongCount int // mutuals with recent interaction
	CommonGroupCount  int // shared group memberships

	// Community signals
	SameLocation   bool
	SameProfession bool
	SameSchool     bool
	SameCompany    bool

	// Graph structure signals
	MutualFollow        bool
	TriadicClosureScore float32 // 0.0-1.0

	// Behavioral signals
	ProfileView7d         int     // future scaffold (0)
	ReverseView7d         int     // candidate viewed viewer (future scaffold)
	InterestSimilarity    float32 // 0.0-1.0 (future scaffold)
	ContentCoengagement7d int     // content co-engagement count (future scaffold)

	// Contact signals
	ContactMatch bool // future scaffold (false)

	// Location/profession/school/company values for explain text
	LocationValue   string
	ProfessionValue string
	SchoolName      string
	CompanyName     string
	GroupName       string // most relevant shared group name

	// Modifiers
	IsNewUser       bool
	NewUserDays     int
	IsActive        bool
	ImpressionCount int
	ClusterBonus    bool    // cluster completion signal
	DismissPenalty  float32 // from dismiss patterns (0.4-1.0), 0 = no penalty

	// Safety signals
	BlockPropagation   int     // how many of viewer's friends blocked candidate
	CandidateBlockRate float32 // candidate's overall block rate (0.0-1.0)
}

// ScoringOutput holds the computed score and explanation.
type ScoringOutput struct {
	Score       float32
	ReasonCodes []string
	ExplainText string
}

// ScoreFriendCandidate computes the full friend suggestion score.
func ScoreFriendCandidate(input ScoringInput) ScoringOutput {
	var score float64
	var reasons []string
	var parts []string

	// --- Core graph signals ---
	if input.MutualFriendCount > 0 {
		score += 12.0 * math.Log1p(float64(input.MutualFriendCount))
		reasons = append(reasons, "MUTUAL_FRIENDS")
		if input.MutualFriendCount == 1 {
			parts = append(parts, "1 mutual friend")
		} else {
			parts = append(parts, fmt.Sprintf("%d mutual friends", input.MutualFriendCount))
		}
	}

	if input.MutualStrongCount > 0 {
		score += 8.0 * math.Log1p(float64(input.MutualStrongCount))
		// Don't duplicate explain_text — subsumed by MUTUAL_FRIENDS
	}

	if input.CommonGroupCount > 0 {
		score += 6.0 * math.Log1p(float64(input.CommonGroupCount))
		reasons = append(reasons, "COMMON_GROUPS")
		if input.GroupName != "" {
			if input.CommonGroupCount == 1 {
				parts = append(parts, fmt.Sprintf("Both in %s", input.GroupName))
			} else {
				parts = append(parts, fmt.Sprintf("%d groups in common", input.CommonGroupCount))
			}
		}
	}

	// --- Community signals ---
	if input.SameLocation {
		score += 8.0
		reasons = append(reasons, "SAME_CITY")
		if input.LocationValue != "" {
			parts = append(parts, fmt.Sprintf("Lives in %s", input.LocationValue))
		}
	}

	if input.SameSchool {
		score += 10.0
		reasons = append(reasons, "SAME_SCHOOL")
		if input.SchoolName != "" {
			parts = append(parts, fmt.Sprintf("Studied at %s", input.SchoolName))
		}
	}

	if input.SameCompany {
		score += 10.0
		reasons = append(reasons, "SAME_COMPANY")
		if input.CompanyName != "" {
			parts = append(parts, fmt.Sprintf("Works at %s", input.CompanyName))
		}
	}

	if input.SameProfession {
		score += 10.0
		reasons = append(reasons, "SAME_PROFESSION")
		if input.ProfessionValue != "" {
			parts = append(parts, fmt.Sprintf("Also in %s", input.ProfessionValue))
		}
	}

	// --- Graph structure signals ---
	if input.MutualFollow {
		score += 15.0
		reasons = append(reasons, "MUTUAL_FOLLOW")
		parts = append(parts, "You follow each other")
	}

	if input.TriadicClosureScore > 0 {
		score += 7.0 * float64(input.TriadicClosureScore)
		reasons = append(reasons, "TRIADIC_CLOSURE")
		// Subsumed by mutual friends text
	}

	// --- Behavioral signals ---
	if input.ProfileView7d > 0 {
		score += 4.0 * math.Log1p(float64(input.ProfileView7d))
		// Do NOT add to explain_text (creepy)
	}

	if input.ReverseView7d > 0 {
		score += 3.0
		// Do NOT add to explain_text
	}

	if input.InterestSimilarity > 0 {
		score += 3.0 * float64(input.InterestSimilarity)
	}

	if input.ContentCoengagement7d > 0 {
		score += 2.0 * math.Log1p(float64(input.ContentCoengagement7d))
	}

	// --- Contact signals ---
	if input.ContactMatch {
		score += 20.0
		reasons = append(reasons, "CONTACT_MATCH")
		parts = append(parts, "In your contacts")
	}

	// --- Modifiers ---

	// New user boost: 1.0–1.2
	if input.IsNewUser && input.NewUserDays < 14 {
		boost := 1.0 + 0.2*(1.0-float64(input.NewUserDays)/14.0)
		score *= boost
	}

	// Active user boost: 1.0–1.1
	if input.IsActive {
		score *= 1.1
	}

	// Cluster completion bonus: 1.0–1.15
	if input.ClusterBonus {
		score *= 1.15
		reasons = append(reasons, "CLUSTER_COMPLETION")
	}

	// Dismiss penalty: 0.4–1.0
	if input.DismissPenalty > 0 && input.DismissPenalty < 1.0 {
		score *= float64(input.DismissPenalty)
	}

	// Ignore decay: shown 3+ times without action → 0.6^(n-2)
	if input.ImpressionCount > 2 {
		decay := math.Pow(0.6, float64(input.ImpressionCount-2))
		score *= decay
	}

	// --- Safety penalties ---

	// Block propagation: if 3+ of viewer's friends blocked candidate → -30%
	if input.BlockPropagation >= 3 {
		score *= 0.7
	}

	// High block rate: if candidate's overall block rate > 2% → -50%
	if input.CandidateBlockRate > 0.02 {
		score *= 0.5
	}

	// Build explain_text: top 2 parts joined by bullet
	explainText := buildExplainText(parts)

	return ScoringOutput{
		Score:       float32(score),
		ReasonCodes: reasons,
		ExplainText: explainText,
	}
}

// ─── Follow Candidate Scoring ────────────────────────────────

// FollowScoringInput holds signals for scoring a follow suggestion.
type FollowScoringInput struct {
	SocialProofCount int     // how many of viewer's friends follow this creator
	TrendingScore    float32 // normalized trending score (0.0-1.0)
	CreatorAffinity  float32 // viewer's affinity to creator's content (future scaffold)
	FreshnessDays    int     // days since creator's last post

	// Explain text values
	CreatorName    string
	FriendNames    []string // names of friends who follow this creator
	FollowerCount  int
	IsNewCreator   bool // joined within last 30 days
	TrendingRegion string

	// Modifiers
	ImpressionCount int
}

// FollowScoringOutput holds the computed follow suggestion score.
type FollowScoringOutput struct {
	Score       float32
	ReasonCodes []string
	ExplainText string
}

// ScoreFollowCandidate computes the follow suggestion score.
func ScoreFollowCandidate(input FollowScoringInput) FollowScoringOutput {
	var score float64
	var reasons []string
	var parts []string

	// Social proof: friends of viewer also follow this creator
	if input.SocialProofCount > 0 {
		score += 10.0 * math.Log1p(float64(input.SocialProofCount))
		reasons = append(reasons, "FRIENDS_FOLLOW")
		if len(input.FriendNames) == 1 {
			parts = append(parts, fmt.Sprintf("%s follows them", input.FriendNames[0]))
		} else if len(input.FriendNames) > 1 {
			parts = append(parts, fmt.Sprintf("%s and %d others follow them", input.FriendNames[0], input.SocialProofCount-1))
		} else if input.SocialProofCount == 1 {
			parts = append(parts, "1 friend follows them")
		} else {
			parts = append(parts, fmt.Sprintf("%d friends follow them", input.SocialProofCount))
		}
	}

	// Trending score
	if input.TrendingScore > 0 {
		score += 8.0 * float64(input.TrendingScore)
		reasons = append(reasons, "TRENDING_REGION")
		if input.TrendingRegion != "" {
			parts = append(parts, fmt.Sprintf("Trending in %s", input.TrendingRegion))
		} else {
			parts = append(parts, "Trending creator")
		}
	}

	// Creator affinity (future scaffold)
	if input.CreatorAffinity > 0 {
		score += 15.0 * float64(input.CreatorAffinity)
	}

	// Freshness: recent activity boosts
	if input.FreshnessDays < 7 {
		score += 5.0 * (1.0 - float64(input.FreshnessDays)/7.0)
	}

	// New creator bonus
	if input.IsNewCreator {
		score *= 1.2
		reasons = append(reasons, "NEW_CREATOR")
		parts = append(parts, "New to atpost")
	}

	// Ignore decay
	if input.ImpressionCount > 2 {
		decay := math.Pow(0.6, float64(input.ImpressionCount-2))
		score *= decay
	}

	return FollowScoringOutput{
		Score:       float32(score),
		ReasonCodes: reasons,
		ExplainText: buildExplainText(parts),
	}
}

// ─── Helpers ─────────────────────────────────────────────────

func buildExplainText(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	// Top 2 parts, joined by bullet
	return strings.Join(parts[:2], " \u2022 ")
}
