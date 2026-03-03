package spam

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/redis/go-redis/v9"
)

// Result holds the spam detection outcome.
type Result struct {
	IsSpam bool
	Reason string  // "blocklist", "rate_burst", "link_spam"
	Score  float64 // 0.0 (clean) to 1.0 (spam)
}

// Detector identifies spam content using heuristic rules.
type Detector struct {
	patterns []*regexp.Regexp
	urlRegex *regexp.Regexp
	rdb      *redis.Client
}

// New creates a Detector with default blocklist patterns.
func New(rdb *redis.Client) *Detector {
	// Default spam patterns — can be extended via config
	rawPatterns := []string{
		`(?i)(buy\s+followers|cheap\s+likes|make\s+money\s+fast)`,
		`(?i)(click\s+here\s+to\s+claim|you\s+have\s+won|congratulations\s+you)`,
		`(?i)(casino|poker|betting)\s+(site|link|bonus)`,
	}
	patterns := make([]*regexp.Regexp, 0, len(rawPatterns))
	for _, p := range rawPatterns {
		patterns = append(patterns, regexp.MustCompile(p))
	}
	return &Detector{
		patterns: patterns,
		urlRegex: regexp.MustCompile(`https?://\S+`),
		rdb:      rdb,
	}
}

// Check evaluates a post for spam signals and returns a Result.
func (d *Detector) Check(ctx context.Context, userID, text string, mediaCount int) Result {
	score := 0.0
	reason := ""

	// Signal 1: Blocklist keyword match
	for _, p := range d.patterns {
		if p.MatchString(text) {
			score += 0.6
			reason = "blocklist"
			break
		}
	}

	// Signal 2: Excessive URLs (link spam)
	urls := d.urlRegex.FindAllString(text, -1)
	if len(urls) > 5 {
		score += 0.5
		reason = "link_spam"
	}

	// Signal 3: Rate burst — too many posts in a short window
	if d.rdb != nil {
		key := fmt.Sprintf("post_rate:%s", userID)
		count, err := d.rdb.Incr(ctx, key).Result()
		if err == nil {
			// Set expiry only on first increment
			if count == 1 {
				d.rdb.Expire(ctx, key, 60*time.Minute)
			}
			if count > 30 {
				score += 0.5
				reason = "rate_burst"
			}
		}
	}

	// Clamp to [0, 1]
	if score > 1.0 {
		score = 1.0
	}

	isSpam := score >= 0.7
	if isSpam {
		slog.Info("spam: detected", "user_id", userID, "score", score, "reason", reason)
	}

	return Result{IsSpam: isSpam, Reason: reason, Score: score}
}
