// Package handle derives a public username for a brand-new account.
//
// Until 22 September 2026 registration wrote no username at all:
// profile.profiles.username stayed NULL, app.users.username stayed NULL,
// and the only auto-generated thing anywhere was app.users.handle — a
// different column, in a different database, derived from the display name
// and never copied across. The visible effect was that a new account had no
// /u/<name> address until the person went and set one by hand, and most
// never did.
//
// So every account now leaves registration with a handle, derived from the
// email address the way LinkedIn and YouTube do it.
//
// One consequence worth stating plainly: the local part of an email address
// becomes public. `john.doe@example.com` becomes @johndoe, which is visible
// to everyone. That is the behaviour of the products this was modelled on,
// and it is what was asked for, but it does mean the handle is a hint about
// the address. Anyone who minds can change it — profile-service owns the
// rename, with its 30-day cooldown.
package handle

import (
	"crypto/rand"
	"math/big"
	"strings"
)

const (
	// MinLength/MaxLength match Architecture/services/user-service's handle
	// package, so a handle minted here is valid there too.
	MinLength = 3
	MaxLength = 24

	// maxBase leaves room for a disambiguating suffix inside MaxLength.
	maxBase = 16

	alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
)

// reserved are handles that must never belong to a person, because a route,
// a support identity or a brand already answers to them.
var reserved = map[string]bool{
	"admin": true, "administrator": true, "api": true, "app": true,
	"auth": true, "billing": true, "blog": true, "channel": true,
	"channels": true, "contact": true, "dashboard": true, "dev": true,
	"explore": true, "feed": true, "help": true, "home": true,
	"login": true, "logout": true, "me": true, "messages": true,
	"messenger": true, "new": true, "news": true, "notifications": true,
	"official": true, "privacy": true, "profile": true, "register": true,
	"root": true, "search": true, "security": true, "settings": true,
	"shop": true, "signup": true, "staff": true, "static": true,
	"status": true, "support": true, "system": true, "terms": true,
	"test": true, "trending": true, "u": true, "user": true,
	"users": true, "vchat": true, "www": true,
}

// banned are substrings that must not appear in an auto-assigned handle.
// A derived handle is not a name someone chose, so it must not accidentally
// hand out something that reads as staff, or as abuse.
var banned = []string{
	"admin", "moderator", "official", "support", "vchat", "atpost",
	"fuck", "shit", "cunt", "nigger", "rape", "porn", "nazi",
}

// Slugify reduces text to the handle charset: lowercase [a-z0-9_].
//
// Dots are DROPPED rather than mapped to underscores, so `john.doe` becomes
// `johndoe`. Gmail ignores dots in the local part, which means john.doe@ and
// johndoe@ are one mailbox; mapping them to two different handles would be
// inventing a distinction that does not exist.
func Slugify(s string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastUnderscore = false
		case r == '.':
			// dropped, see above
		case r == '_' || r == '-' || r == ' ':
			// Collapse runs of separators, and never lead with one.
			if b.Len() > 0 && !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		default:
			// Anything else (including non-Latin scripts) is dropped. The
			// caller falls back to a name, then to a random handle, so a
			// person whose address is entirely non-Latin still gets one.
		}
	}
	return strings.Trim(b.String(), "_")
}

// emailLocalPart returns the part before '@', with any +tag removed.
func emailLocalPart(email string) string {
	at := strings.IndexByte(email, '@')
	if at <= 0 {
		return ""
	}
	local := email[:at]
	if plus := strings.IndexByte(local, '+'); plus >= 0 {
		local = local[:plus]
	}
	return local
}

// IsAcceptable reports whether a base is usable as an auto-assigned handle.
func IsAcceptable(base string) bool {
	if len(base) < MinLength || len(base) > MaxLength {
		return false
	}
	if reserved[base] {
		return false
	}
	for _, w := range banned {
		if strings.Contains(base, w) {
			return false
		}
	}
	return true
}

// Base derives the preferred handle for an account, in order: the email
// local part, then the person's name, then "member". The result is always
// acceptable — callers only have to make it unique.
func Base(email, firstName, lastName string) string {
	for _, candidate := range []string{
		Slugify(emailLocalPart(email)),
		Slugify(firstName + " " + lastName),
	} {
		if len(candidate) > maxBase {
			candidate = strings.Trim(candidate[:maxBase], "_")
		}
		if IsAcceptable(candidate) {
			return candidate
		}
	}
	return "member"
}

// Candidates yields the handles to try, in order, for one base: the base
// itself, then the base with a small number, then the base with a random
// suffix. The final entries are random enough that a caller which exhausts
// the list has hit something other than contention.
//
// The numbered attempts come first because `johndoe2` is a handle a person
// will recognise as theirs; `johndoe_7fk2m1` is one they will want to change.
func Candidates(base string) []string {
	out := make([]string, 0, 12)
	out = append(out, base)
	for n := 2; n <= 9; n++ {
		out = append(out, trimTo(base, MaxLength-1)+string(rune('0'+n)))
	}
	for i := 0; i < 3; i++ {
		out = append(out, trimTo(base, MaxLength-7)+"_"+randomSuffix(6))
	}
	return out
}

// Fallback is the last resort: a handle that does not depend on the base at
// all, for the case where every candidate collided. 36^8 keeps this
// effectively unique without a further round trip.
func Fallback() string {
	return "member_" + randomSuffix(8)
}

func trimTo(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.Trim(s[:n], "_")
}

// randomSuffix returns n crypto-random characters from the handle alphabet.
// crypto/rand, not math/rand: a predictable suffix would let someone work
// out the handle of an account created just after their own.
func randomSuffix(n int) string {
	b := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range b {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			// crypto/rand failing is not survivable for a uniqueness
			// suffix; fall back to a fixed character and let the
			// uniqueness loop in the caller find another candidate.
			b[i] = 'x'
			continue
		}
		b[i] = alphabet[v.Int64()]
	}
	return string(b)
}
