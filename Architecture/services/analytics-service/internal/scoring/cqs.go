package scoring

// AggregateMetrics holds the aggregated metrics for CQS computation.
type AggregateMetrics struct {
	AvgPercentViewed   float64
	Impressions        int64
	Shares             int64
	Saves              int64
	FollowsFromContent int64
	Reports            int64
	NotInterested      int64
}

// ComputeCQS computes the Content Quality Score for a content item.
// PRD Section 10.3. Returns value clamped to [0, 1].
func ComputeCQS(m *AggregateMetrics) float64 {
	if m == nil || m.Impressions == 0 {
		return 0
	}

	// Normalize avg_percent_viewed to 0-1 (it's 0-100)
	avgPctNorm := m.AvgPercentViewed / 100.0
	if avgPctNorm > 1.0 {
		avgPctNorm = 1.0
	}

	// Rates per 1000 impressions, normalized to 0-1 via soft capping
	imp1k := float64(m.Impressions) / 1000.0
	if imp1k < 0.001 {
		imp1k = 0.001
	}

	shareRate := normalizeRate(float64(m.Shares) / imp1k)
	saveRate := normalizeRate(float64(m.Saves) / imp1k)
	followRate := normalizeRate(float64(m.FollowsFromContent) / imp1k)
	negativeRate := normalizeRate(float64(m.Reports+m.NotInterested) / imp1k)

	cqs := 0.55*avgPctNorm +
		0.20*shareRate +
		0.10*saveRate +
		0.10*followRate -
		0.05*negativeRate

	// Clamp to [0, 1]
	if cqs < 0 {
		return 0
	}
	if cqs > 1 {
		return 1
	}
	return cqs
}

// normalizeRate normalizes a rate value to [0, 1] using a soft cap.
// Rates above 50 per 1k impressions are treated as 1.0.
func normalizeRate(rate float64) float64 {
	if rate <= 0 {
		return 0
	}
	if rate >= 50 {
		return 1.0
	}
	return rate / 50.0
}
