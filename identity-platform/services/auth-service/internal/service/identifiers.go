package service

import (
	"context"
	"errors"
	"net/mail"
	"strings"

	"github.com/atpost/identity-auth-service/internal/store"
)

// Sign-in identifiers: one normal form, decided once, at the boundary.
//
// Before this, an email was stored and looked up exactly as typed, so
// "Alice@Gmail.com" and "alice@gmail.com" were two accounts, and a phone was
// stored unvalidated in the UNIQUE phone column. Login, forgot-password and
// reset-password then tried the PHONE column first for every identifier, so
// registering someone else's email address as your "phone" made their email
// resolve to your account: their login failed and their reset code went to
// you. Every path that turns a typed identifier into an account now goes
// through lookupUserByIdentifier, which picks the column from the shape of the
// value and never matches an email-shaped value against the phone column.

var (
	// ErrInvalidEmail means the address is not a single bare addr-spec.
	ErrInvalidEmail = errors.New("email address is not valid")
	// ErrInvalidPhone means the number is not E.164 (+ then 8 to 15 digits).
	ErrInvalidPhone = errors.New("phone number must be in international format, e.g. +919876543210")
)

// maxEmailLen is the RFC 5321 path limit minus the angle brackets.
const maxEmailLen = 254

// NormalizeEmail trims, lowercases and format-checks an email address.
//
// The whole address is lowercased, not just the domain. RFC 5321 lets a host
// treat the local part as case-sensitive, but no mail provider our users are on
// does, and a case-sensitive local part is exactly how one person ends up with
// two accounts. Uniqueness is enforced on lower(email) in the database to
// match (database/setup.sql, uq_users_email_lower).
//
// net/mail is used for the grammar, and the parsed address must equal the
// input: ParseAddress also accepts "Name <a@b.c>", which is not an address a
// user typed into an email field.
func NormalizeEmail(raw string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(raw))
	if e == "" || len(e) > maxEmailLen {
		return "", ErrInvalidEmail
	}
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Address != e {
		return "", ErrInvalidEmail
	}
	at := strings.LastIndexByte(e, '@')
	if at <= 0 || strings.Count(e, "@") != 1 {
		return "", ErrInvalidEmail
	}
	domain := e[at+1:]
	// A bare host ("a@localhost") is legal grammar but never a deliverable
	// consumer address; it is also how junk like "a@b" gets in.
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", ErrInvalidEmail
	}
	return e, nil
}

// NormalizePhoneE164 returns the E.164 form of a phone number.
//
// Accepted: a leading '+', then 8 to 15 digits, with spaces, hyphens, dots
// and parentheses ignored as visual separators. A number with no country code
// is refused rather than guessed: "9876543210" and "+919876543210" being two
// different identities is one of the defects this exists to end.
func NormalizePhoneE164(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if !strings.HasPrefix(p, "+") {
		return "", ErrInvalidPhone
	}
	var b strings.Builder
	b.WriteByte('+')
	for _, r := range p[1:] {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '.' || r == '(' || r == ')':
			continue
		default:
			return "", ErrInvalidPhone
		}
	}
	out := b.String()
	digits := len(out) - 1
	if digits < 8 || digits > 15 || out[1] == '0' {
		return "", ErrInvalidPhone
	}
	return out, nil
}

// isEmailShaped is the shape test that picks the lookup column.
func isEmailShaped(identifier string) bool {
	return strings.Contains(identifier, "@")
}

// lookupUserByIdentifier resolves a typed sign-in identifier to an account.
//
// An identifier containing '@' is an EMAIL and is only ever matched against the
// email column, after normalisation; an invalid address matches nothing.
// Anything else is a PHONE: its E.164 form when it has one, otherwise the
// trimmed value as typed, so a legacy row stored before normalisation still
// resolves. It is never tried against the email column.
//
// A nil user with a nil error means "no such account".
func (s *Service) lookupUserByIdentifier(ctx context.Context, identifier string) (*store.User, error) {
	id := strings.TrimSpace(identifier)
	if id == "" {
		return nil, nil
	}
	if isEmailShaped(id) {
		email, err := NormalizeEmail(id)
		if err != nil {
			return nil, nil
		}
		return s.store.GetUserByEmail(ctx, email)
	}
	if phone, err := NormalizePhoneE164(id); err == nil {
		id = phone
	}
	return s.store.GetUserByPhone(ctx, id)
}
