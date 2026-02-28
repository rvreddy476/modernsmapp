package privacy

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
)

var countryCodeRegex = regexp.MustCompile(`^[A-Z]{2}$`)

// HashDeviceID returns a SHA-256 hash of a device identifier.
// If the input is already hashed (64 hex chars), it is returned as-is.
func HashDeviceID(raw string) string {
	if raw == "" {
		return ""
	}
	// Check if already a 64-char hex string (SHA-256 output)
	if len(raw) == 64 {
		return raw
	}
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// ValidateCountry checks that a country code is a valid ISO 3166-1 alpha-2 code.
// Returns the code if valid, empty string otherwise.
func ValidateCountry(code string) string {
	if countryCodeRegex.MatchString(code) {
		return code
	}
	return ""
}

// SanitizeIP strips IP addresses — analytics should never persist raw IPs.
// Returns an empty string always.
func SanitizeIP(_ string) string {
	return ""
}

// DataRetentionDays defines TTLs per data tier:
//   - Raw events (events_raw): 90 days
//   - ScyllaDB watch_sessions: 90 days (set via table TTL)
//   - Hourly aggregates: 730 days (2 years)
//   - Daily summaries: indefinite
//   - Redis view counters: 7 days
//   - Redis trust factor cache: 10 minutes
var DataRetentionDays = map[string]int{
	"events_raw":     90,
	"watch_sessions": 90,
	"hourly_agg":     730,
	"daily_summary":  0, // indefinite
}
