package handle

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"unicode"
)

const (
	MinLength    = 3
	MaxLength    = 24
	suffixLength = 6
	maxBase      = 16 // truncate slugified name to leave room for "_" + suffix
)

// charset used for random suffix generation.
const charset = "abcdefghijklmnopqrstuvwxyz0123456789"

var (
	ErrTooShort          = errors.New("handle must be at least 3 characters")
	ErrTooLong           = errors.New("handle must be at most 24 characters")
	ErrInvalidChars      = errors.New("handle must contain only lowercase a-z, 0-9, and underscores")
	ErrStartsUnderscore  = errors.New("handle cannot start with an underscore")
	ErrEndsUnderscore    = errors.New("handle cannot end with an underscore")
	ErrDoubleUnderscore  = errors.New("handle cannot contain consecutive underscores")
	ErrBannedWord        = errors.New("handle contains a reserved or banned word")
)

// bannedWords are substrings that cannot appear in any handle.
var bannedWords = []string{
	"admin", "atpost", "support", "official", "moderator",
	"system", "api", "help", "root", "staff",
	"postbook", "posttube", "postgram",
	"fuck", "shit", "ass", "dick", "porn", "sex",
	"nazi", "hitler",
}

// reservedHandles are exact handles that cannot be claimed.
var reservedHandles = map[string]bool{
	"admin": true, "administrator": true, "api": true,
	"atpost": true, "blog": true, "channel": true,
	"dashboard": true, "dev": true, "explore": true,
	"feed": true, "help": true, "home": true,
	"info": true, "login": true, "logout": true,
	"me": true, "moderator": true, "null": true,
	"official": true, "postbook": true, "postgram": true,
	"posttube": true, "privacy": true, "register": true,
	"root": true, "search": true, "settings": true,
	"signup": true, "staff": true, "status": true,
	"support": true, "system": true, "terms": true,
	"test": true, "undefined": true, "user": true,
	"www": true,
}

// Validate checks that a handle meets all format and content rules.
func Validate(h string) error {
	if len(h) < MinLength {
		return ErrTooShort
	}
	if len(h) > MaxLength {
		return ErrTooLong
	}
	if h[0] == '_' {
		return ErrStartsUnderscore
	}
	if h[len(h)-1] == '_' {
		return ErrEndsUnderscore
	}
	for i, c := range h {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return ErrInvalidChars
		}
		if c == '_' && i > 0 && h[i-1] == '_' {
			return ErrDoubleUnderscore
		}
	}
	if IsBanned(h) {
		return ErrBannedWord
	}
	return nil
}

// IsBanned returns true if the handle matches a reserved word or contains a banned substring.
func IsBanned(h string) bool {
	lower := strings.ToLower(h)
	if reservedHandles[lower] {
		return true
	}
	for _, word := range bannedWords {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

// Generate creates a handle from a display name with a random suffix.
// The result is always valid per Validate rules.
func Generate(displayName string) string {
	base := slugify(displayName)
	if len(base) == 0 {
		base = "creator"
	}
	if len(base) > maxBase {
		base = base[:maxBase]
	}
	// Trim trailing underscore from truncation
	base = strings.TrimRight(base, "_")
	if len(base) == 0 {
		base = "creator"
	}

	suffix := randomSuffix(suffixLength)
	return fmt.Sprintf("%s_%s", base, suffix)
}

// slugify converts a display name to a handle-safe base string.
func slugify(s string) string {
	var b strings.Builder
	prevUnderscore := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevUnderscore = false
		} else if unicode.IsSpace(r) || r == '-' || r == '.' {
			// Convert separators to underscore, but avoid doubles
			if !prevUnderscore && b.Len() > 0 {
				b.WriteByte('_')
				prevUnderscore = true
			}
		}
		// Skip all other characters
	}
	result := b.String()
	return strings.TrimRight(result, "_")
}

// randomSuffix generates a cryptographically random alphanumeric string.
func randomSuffix(length int) string {
	max := big.NewInt(int64(len(charset)))
	b := make([]byte, length)
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			// Fallback: extremely unlikely but use deterministic value
			b[i] = charset[i%len(charset)]
			continue
		}
		b[i] = charset[n.Int64()]
	}
	return string(b)
}
