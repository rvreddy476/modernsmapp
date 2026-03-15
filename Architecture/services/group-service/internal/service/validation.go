package service

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

var handleRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*[a-z0-9]$`)
var handleSingleChar = regexp.MustCompile(`^[a-z0-9]$`)

var reservedHandles = map[string]bool{
	"admin": true, "administrator": true, "help": true, "support": true,
	"about": true, "contact": true, "settings": true, "search": true,
	"discover": true, "explore": true, "create": true, "new": true,
	"invite": true, "invites": true, "join": true, "leave": true,
	"members": true, "rules": true, "feed": true, "posts": true,
	"my": true, "api": true, "v1": true, "v2": true,
	"null": true, "undefined": true, "mod": true, "moderator": true,
	"official": true, "atpost": true, "system": true, "bot": true,
}

// ValidateHandle checks if a handle is valid per the spec rules.
func ValidateHandle(handle string) error {
	length := utf8.RuneCountInString(handle)
	if length < 3 || length > 50 {
		return fmt.Errorf("handle must be between 3 and 50 characters")
	}

	if handle != strings.ToLower(handle) {
		return fmt.Errorf("handle must be lowercase")
	}

	if !handleRegex.MatchString(handle) && !handleSingleChar.MatchString(handle) {
		return fmt.Errorf("handle must contain only lowercase letters, digits, and hyphens, and cannot start or end with a hyphen")
	}

	if strings.Contains(handle, "--") {
		return fmt.Errorf("handle cannot contain consecutive hyphens")
	}

	if reservedHandles[handle] {
		return fmt.Errorf("handle '%s' is reserved", handle)
	}

	return nil
}

// ValidateGroupName checks if a group name is valid per the spec.
func ValidateGroupName(name string) error {
	length := utf8.RuneCountInString(name)
	if length < 3 || length > 100 {
		return fmt.Errorf("group name must be between 3 and 100 characters")
	}
	return nil
}

// ValidatePrivacyJoinCombo validates that the privacy_level and join_mode combination is allowed.
// Allowed combinations:
//   - public + open
//   - public + request
//   - restricted + request
//   - restricted + invite_only
//   - private + invite_only
func ValidatePrivacyJoinCombo(privacyLevel, joinMode string) error {
	allowed := map[string][]string{
		"public":     {"open", "request"},
		"restricted": {"request", "invite_only"},
		"private":    {"invite_only"},
	}

	modes, ok := allowed[privacyLevel]
	if !ok {
		return fmt.Errorf("invalid privacy_level: %s", privacyLevel)
	}

	for _, m := range modes {
		if m == joinMode {
			return nil
		}
	}

	return fmt.Errorf("join_mode '%s' is not allowed with privacy_level '%s'", joinMode, privacyLevel)
}

// SlugifyName generates a handle from a group name.
func SlugifyName(name string) string {
	s := strings.ToLower(name)
	// Replace spaces and underscores with hyphens
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")

	// Remove non-alphanumeric characters (except hyphens)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	s = b.String()

	// Collapse consecutive hyphens
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}

	// Trim leading/trailing hyphens
	s = strings.Trim(s, "-")

	// Clamp length
	if len(s) > 50 {
		s = s[:50]
		s = strings.TrimRight(s, "-")
	}
	if len(s) < 3 {
		return ""
	}

	return s
}

// ValidateWhoCanPost validates the who_can_post field.
func ValidateWhoCanPost(v string) error {
	switch v {
	case "all_members", "admins_mods", "admins_only":
		return nil
	}
	return fmt.Errorf("invalid who_can_post value: %s", v)
}

// ValidateWhoCanInvite validates the who_can_invite field.
func ValidateWhoCanInvite(v string) error {
	switch v {
	case "all_members", "admins_mods", "admins_only":
		return nil
	}
	return fmt.Errorf("invalid who_can_invite value: %s", v)
}
