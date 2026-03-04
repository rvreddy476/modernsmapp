package spam

import (
	"context"
	"testing"
)

// TestNewDetector verifies that New(nil) does not panic and returns a non-nil Detector.
func TestNewDetector(t *testing.T) {
	d := New(nil)
	if d == nil {
		t.Fatal("New(nil) returned nil")
	}
}

// TestCheckCleanText verifies that normal, clean text scores 0.0 and is not flagged as spam.
func TestCheckCleanText(t *testing.T) {
	d := New(nil)
	ctx := context.Background()

	result := d.Check(ctx, "user1", "Hello world", 0)

	if result.Score != 0.0 {
		t.Errorf("expected Score=0.0, got %f", result.Score)
	}
	if result.IsSpam {
		t.Errorf("expected IsSpam=false, got true")
	}
	if result.Reason != "" {
		t.Errorf("expected empty Reason, got %q", result.Reason)
	}
}

// TestCheckBlocklistedText verifies that text matching a blocklist pattern scores >= 0.6.
func TestCheckBlocklistedText(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"buy followers", "buy followers now for cheap"},
		{"cheap likes", "get cheap likes fast"},
		{"make money fast", "make money fast with this one trick"},
		{"click here to claim", "click here to claim your prize"},
		{"you have won", "you have won a special reward"},
		{"congratulations you", "congratulations you are selected"},
		{"casino site", "casino site bonus offer"},
		{"poker link", "poker link click here"},
		{"betting bonus", "betting bonus available"},
	}

	d := New(nil)
	ctx := context.Background()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := d.Check(ctx, "user1", tc.text, 0)
			if result.Score < 0.6 {
				t.Errorf("text %q: expected Score >= 0.6, got %f", tc.text, result.Score)
			}
			if result.Reason != "blocklist" && result.Reason != "link_spam" && result.Reason != "rate_burst" {
				// reason is "blocklist" unless overridden by later signals
				// We specifically want blocklist to be the first signal set
			}
			// The blocklist signal sets reason="blocklist" unless a later signal (link_spam) overwrites it.
			// Since these texts don't contain 6+ URLs, reason should be "blocklist".
			if result.Reason != "blocklist" {
				t.Errorf("text %q: expected Reason=%q, got %q", tc.text, "blocklist", result.Reason)
			}
		})
	}
}

// TestCheckURLSpam verifies that text containing 6 or more URLs scores >= 0.5 for link_spam.
func TestCheckURLSpam(t *testing.T) {
	d := New(nil)
	ctx := context.Background()

	// Six URLs — should trigger link_spam signal (score += 0.5, reason = "link_spam")
	sixURLs := "http://a.com http://b.com http://c.com http://d.com http://e.com http://f.com"
	result := d.Check(ctx, "user2", sixURLs, 0)

	if result.Score < 0.5 {
		t.Errorf("expected Score >= 0.5, got %f", result.Score)
	}
	if result.Reason != "link_spam" {
		t.Errorf("expected Reason=%q, got %q", "link_spam", result.Reason)
	}

	// Five URLs — should NOT trigger link_spam (threshold is > 5, i.e., 6+)
	fiveURLs := "http://a.com http://b.com http://c.com http://d.com http://e.com"
	resultFive := d.Check(ctx, "user3", fiveURLs, 0)
	if resultFive.Reason == "link_spam" {
		t.Errorf("5 URLs should not trigger link_spam, but Reason=%q", resultFive.Reason)
	}
}

// TestCheckRateBurst verifies that with a nil Redis client the rate burst check is skipped
// and Check does not panic.
func TestCheckRateBurst(t *testing.T) {
	d := New(nil) // rdb is nil — rate burst logic must be skipped
	ctx := context.Background()

	// This must not panic even when called many times
	for i := 0; i < 35; i++ {
		result := d.Check(ctx, "userRateBurst", "normal text", 0)
		// With nil redis, only content signals matter. Clean text should score 0.
		if result.Reason == "rate_burst" {
			t.Errorf("call %d: got unexpected rate_burst reason with nil redis", i)
		}
	}
}

// TestCheckScoreClamp verifies that the combined score of multiple signals is clamped to 1.0.
func TestCheckScoreClamp(t *testing.T) {
	d := New(nil)
	ctx := context.Background()

	// This text triggers blocklist (0.6) AND has 6+ URLs (0.5) — total would be 1.1 without clamping.
	// With clamping it must be exactly 1.0.
	spamWithURLs := "buy followers http://a.com http://b.com http://c.com http://d.com http://e.com http://f.com"
	result := d.Check(ctx, "user4", spamWithURLs, 0)

	if result.Score > 1.0 {
		t.Errorf("score exceeded 1.0: got %f", result.Score)
	}
	if result.Score != 1.0 {
		t.Errorf("expected clamped score of 1.0, got %f", result.Score)
	}
	if !result.IsSpam {
		t.Errorf("expected IsSpam=true at score=1.0")
	}
}
