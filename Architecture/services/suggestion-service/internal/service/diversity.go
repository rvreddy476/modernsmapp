package service

import "github.com/facebook-like/suggestion-service/internal/store"

// DiversityConfig controls the diversity algorithm limits.
type DiversityConfig struct {
	MaxSameSchool  int // max candidates from same school
	MaxSameCompany int // max candidates from same company
	MaxSameCity    int // max candidates where city is the only signal
	MaxSameBucket  int // max candidates from same source_bucket
	MinFreshPct    float32 // minimum % of fresh candidates (impression_count < 2)
}

// DefaultDiversityConfig returns the default diversity limits.
func DefaultDiversityConfig() DiversityConfig {
	return DiversityConfig{
		MaxSameSchool:  3,
		MaxSameCompany: 3,
		MaxSameCity:    2,
		MaxSameBucket:  4,
		MinFreshPct:    0.20, // 20%
	}
}

// DiversifiedCandidate extends a candidate with metadata for diversity checks.
type DiversifiedCandidate struct {
	store.SuggestionCandidate

	// Metadata used for diversity enforcement
	SchoolName  string
	CompanyName string
	CityName    string
	CityOnly    bool // true if city is the only signal (no mutual friends, no school, etc.)
	IsFresh     bool // impression_count < 2
}

// ApplyDiversity uses a greedy slot-fill algorithm to select a diverse set of
// candidates from a score-sorted list. Candidates should be sorted by
// BaseScore DESC before calling.
func ApplyDiversity(candidates []DiversifiedCandidate, pageSize int, cfg DiversityConfig) []DiversifiedCandidate {
	if len(candidates) <= pageSize {
		return candidates
	}

	result := make([]DiversifiedCandidate, 0, pageSize)

	// Track counts per constraint dimension
	schoolCount := make(map[string]int)
	companyCount := make(map[string]int)
	cityOnlyCount := make(map[string]int)
	bucketCount := make(map[string]int)

	used := make([]bool, len(candidates))
	freshCount := 0

	for len(result) < pageSize {
		picked := -1

		// Try to find the highest-scoring candidate that doesn't violate constraints
		for i, c := range candidates {
			if used[i] {
				continue
			}

			if violates(c, schoolCount, companyCount, cityOnlyCount, bucketCount, cfg) {
				continue
			}

			picked = i
			break
		}

		// If all remaining candidates violate constraints, take the best available anyway
		if picked == -1 {
			for i := range candidates {
				if !used[i] {
					picked = i
					break
				}
			}
		}

		if picked == -1 {
			break // no more candidates at all
		}

		c := candidates[picked]
		used[picked] = true
		result = append(result, c)

		// Update counts
		if c.SchoolName != "" {
			schoolCount[c.SchoolName]++
		}
		if c.CompanyName != "" {
			companyCount[c.CompanyName]++
		}
		if c.CityOnly && c.CityName != "" {
			cityOnlyCount[c.CityName]++
		}
		bucketCount[c.SourceBucket]++
		if c.IsFresh {
			freshCount++
		}
	}

	// Ensure minimum fresh candidate percentage
	minFresh := int(float32(len(result)) * cfg.MinFreshPct)
	if freshCount < minFresh {
		result = boostFreshCandidates(result, candidates, used, minFresh-freshCount)
	}

	return result
}

// violates checks if adding this candidate would violate any diversity constraint.
func violates(
	c DiversifiedCandidate,
	schoolCount, companyCount, cityOnlyCount, bucketCount map[string]int,
	cfg DiversityConfig,
) bool {
	if c.SchoolName != "" && schoolCount[c.SchoolName] >= cfg.MaxSameSchool {
		return true
	}
	if c.CompanyName != "" && companyCount[c.CompanyName] >= cfg.MaxSameCompany {
		return true
	}
	if c.CityOnly && c.CityName != "" && cityOnlyCount[c.CityName] >= cfg.MaxSameCity {
		return true
	}
	if bucketCount[c.SourceBucket] >= cfg.MaxSameBucket {
		return true
	}
	return false
}

// boostFreshCandidates swaps in fresh candidates from the pool to meet the minimum.
func boostFreshCandidates(result, pool []DiversifiedCandidate, used []bool, needed int) []DiversifiedCandidate {
	if needed <= 0 || len(result) == 0 {
		return result
	}

	// Find fresh candidates from pool not yet used
	var freshPool []int
	for i, c := range pool {
		if !used[i] && c.IsFresh {
			freshPool = append(freshPool, i)
		}
	}

	if len(freshPool) == 0 {
		return result
	}

	// Replace lowest-scored non-fresh items in result with fresh ones
	swapped := 0
	for ri := len(result) - 1; ri >= 0 && swapped < needed && swapped < len(freshPool); ri-- {
		if result[ri].IsFresh {
			continue
		}
		result[ri] = pool[freshPool[swapped]]
		used[freshPool[swapped]] = true
		swapped++
	}

	return result
}
