// Package moderation implements the spec §15.4 two-layer chat-safety net.
//
// Layer 1: synchronous regex match on the message body. Used by
// message-service (dating-context messages only) over an internal HTTP
// endpoint. This file provides the matcher; the HTTP plumbing lives in
// internal/http/handler_moderation.go.
//
// SHADOW MODE FOR v1 (CRITICAL RULES #5):
//   - Layer 1 result is LOGGED + emitted as dating.moderation.layer1.result.
//   - The action_taken field is ALWAYS "shadow" when shadow mode is on.
//   - The caller (message-service) receives the result but takes NO
//     user-visible action. No banner, no block, no soft-delete.
//   - Strict mode is feature-flag-gated by `pulse_moderation_strict` and
//     is OFF by default; when on, action_taken may be 'warn'|'block'|'held'.
package moderation

import (
	"regexp"
	"strings"
)

// ScanResult is the per-message verdict.
type ScanResult struct {
	Confidence  float64  `json:"confidence"`
	Patterns    []string `json:"patterns"`
	Suggestion  string   `json:"suggestion,omitempty"`
	// ActionTaken is the *recommendation*. The caller decides whether to
	// take user-visible action. In shadow mode the moderation service
	// itself overwrites this to "shadow" before persisting/emitting.
	ActionTaken string `json:"action_taken"`
}

// PatternKind names a regex bucket so the dashboard can break results out.
const (
	PatternPhone        = "phone_number"
	PatternEmail        = "email"
	PatternURL          = "external_url"
	PatternMoneyRequest = "money_request"
	PatternAbuse        = "abusive_vocabulary"
)

// patterns describes the regex set. Each rule has a weight that contributes
// to the confidence score; the highest single rule cannot push confidence
// past 1.0 even on a 5-match message.
type patternRule struct {
	Kind   string
	Re     *regexp.Regexp
	Weight float64
}

// safeURLDomains is a whitelist of domains that should NOT trigger the URL
// rule (e.g. AtPost itself, the official Pulse help center). The list is
// intentionally short — if a partner's domain needs whitelisting it goes
// through trust-safety review.
var safeURLDomains = []string{
	"atpost.com",
	"atpost.in",
	"pulse.atpost.com",
}

// moneyKeywords is a lower-cased English+Hindi list. Trust-safety lead
// owns this list; treat it as a placeholder that ships with v1.
var moneyKeywords = []string{
	"send me money", "transfer money", "wire money", "loan",
	"upi", "phonepe", "paytm", "gpay", "bhim", "razorpay",
	"paisa bhej", "rupay bhej", "paise bhejo",
}

// abusiveKeywords is the placeholder vocabulary list. Trust-safety lead owns.
var abusiveKeywords = []string{
	// Placeholder — vendor gives us the curated list pre-launch.
	"slut", "whore", "rape",
}

// compiledPatterns is the canonical rule set.
var compiledPatterns = []patternRule{
	{
		Kind:   PatternPhone,
		Re:     regexp.MustCompile(`\+?\d[\d\s\-\(\)]{8,}\d`),
		Weight: 0.6,
	},
	{
		Kind:   PatternEmail,
		Re:     regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`),
		Weight: 0.5,
	},
	// URL detection runs as a separate function (because of the safe-domain
	// whitelist exception) — see scanForURLs.
}

// ScanMessage applies the layer-1 ruleset and returns a ScanResult.
//
// Confidence buckets:
//
//	confidence >= 0.8 → block-recommend
//	0.5  ..  < 0.8    → warn
//	          < 0.5   → ok
//
// In shadow mode the caller MUST overwrite ActionTaken to "shadow" before
// persisting. This function returns the *recommendation*; it does not know
// about feature flags.
func ScanMessage(msg string) ScanResult {
	if msg == "" {
		return ScanResult{ActionTaken: "ok"}
	}
	lower := strings.ToLower(msg)
	hits := []string{}
	var conf float64

	for _, rule := range compiledPatterns {
		if rule.Re.FindStringIndex(msg) != nil {
			hits = append(hits, rule.Kind)
			conf += rule.Weight
		}
	}

	if scanForURLs(msg) {
		hits = append(hits, PatternURL)
		conf += 0.4
	}
	if containsAny(lower, moneyKeywords) {
		hits = append(hits, PatternMoneyRequest)
		conf += 0.7
	}
	if containsAny(lower, abusiveKeywords) {
		hits = append(hits, PatternAbuse)
		conf += 0.8
	}

	// Cap confidence at 1.0.
	if conf > 1.0 {
		conf = 1.0
	}
	rec := actionRecommendation(conf)
	suggestion := suggestionFor(rec, hits)
	return ScanResult{
		Confidence:  conf,
		Patterns:    hits,
		ActionTaken: rec,
		Suggestion:  suggestion,
	}
}

// actionRecommendation maps the confidence bucket to a recommendation.
// "block" is only ever a *recommendation*; the runtime decides whether to
// honor it based on shadow vs strict mode.
func actionRecommendation(conf float64) string {
	switch {
	case conf >= 0.8:
		return "block"
	case conf >= 0.5:
		return "warn"
	default:
		return "ok"
	}
}

// suggestionFor returns a human-readable nudge keyed off the recommendation.
// The mobile app may surface this in strict mode; shadow mode logs it only.
func suggestionFor(rec string, hits []string) string {
	if rec == "ok" {
		return ""
	}
	for _, h := range hits {
		switch h {
		case PatternMoneyRequest:
			return "Money requests are a common scam signal. Please reconsider."
		case PatternPhone:
			return "Sharing phone numbers in early chats is risky. Try keeping the conversation in app for now."
		case PatternAbuse:
			return "This message may violate community guidelines."
		}
	}
	return "Take a moment before sending."
}

// containsAny returns true if `haystack` contains any of `needles`. We use
// strings.Contains for simplicity; word-boundary matching can come later.
func containsAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if n == "" {
			continue
		}
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

// urlRe is a forgiving URL matcher (we only care about whether *any* URL
// appears, then we whitelist by domain).
var urlRe = regexp.MustCompile(`https?://[^\s]+|www\.[^\s]+`)

// scanForURLs returns true when the message contains a URL whose domain is
// not on safeURLDomains.
func scanForURLs(msg string) bool {
	matches := urlRe.FindAllString(msg, -1)
	if len(matches) == 0 {
		return false
	}
	for _, raw := range matches {
		// Strip protocol.
		domain := strings.TrimPrefix(raw, "https://")
		domain = strings.TrimPrefix(domain, "http://")
		domain = strings.TrimPrefix(domain, "www.")
		// First slash → path; trim.
		if idx := strings.IndexAny(domain, "/?#"); idx >= 0 {
			domain = domain[:idx]
		}
		domain = strings.ToLower(domain)
		if !isSafeDomain(domain) {
			return true
		}
	}
	return false
}

// isSafeDomain returns true when `domain` matches an entry in
// safeURLDomains exactly or is a subdomain of one.
func isSafeDomain(domain string) bool {
	for _, safe := range safeURLDomains {
		if domain == safe || strings.HasSuffix(domain, "."+safe) {
			return true
		}
	}
	return false
}
