package http

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maxDisplayName      = 80
	maxBio              = 500
	maxShortProfileText = 120
	maxStatusText       = 120
	maxCTALabel         = 40
)

// validateProfileUpdate mirrors durable publication constraints at the server.
// Client validation improves feedback; it is not an enforcement boundary.
func validateProfileUpdate(req UpdateProfileRequest) map[string]any {
	problems := map[string]any{}
	limit := func(field, value string, max int) {
		if utf8.RuneCountInString(value) > max {
			problems[field] = map[string]any{"max_characters": max}
		}
	}
	limit("display_name", req.DisplayName, maxDisplayName)
	limit("bio", req.Bio, maxBio)
	limit("category", req.Category, maxShortProfileText)
	limit("profession", req.Profession, maxShortProfileText)
	limit("location", req.Location, maxShortProfileText)
	if req.StatusText != nil {
		limit("status_text", *req.StatusText, maxStatusText)
	}
	if req.CTALabel != nil {
		limit("cta_label", *req.CTALabel, maxCTALabel)
	}
	if value := strings.TrimSpace(req.Website); value != "" && !SafePublicURL(normalizeProfileURL(value)) {
		problems["website"] = "must be an http or https URL"
	}
	if req.CTAURL != nil && strings.TrimSpace(*req.CTAURL) != "" && !SafePublicURL(normalizeProfileURL(*req.CTAURL)) {
		problems["cta_url"] = "must be an http or https URL"
	}
	if req.ProfileThemeColor != "" && !validHexColor(req.ProfileThemeColor) {
		problems["profile_theme_color"] = "must be #RRGGBB"
	}
	return problems
}

func normalizeProfileURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.Contains(raw, "://") {
		return raw
	}
	return "https://" + raw
}

func validHexColor(raw string) bool {
	if len(raw) != 7 || raw[0] != '#' {
		return false
	}
	for _, r := range raw[1:] {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') && !(r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

var allowedAboutSections = map[string]bool{
	"work": true, "education": true, "interests": true, "hobbies": true,
	"skills": true, "languages": true, "places": true, "achievements": true,
}

func validateAboutItem(section string, data map[string]interface{}, visibility string) error {
	if !allowedAboutSections[strings.ToLower(strings.TrimSpace(section))] {
		return fmt.Errorf("unsupported about section")
	}
	if visibility != "" && visibility != "public" && visibility != "private" {
		return fmt.Errorf("visibility must be public or private")
	}
	if len(data) == 0 {
		return fmt.Errorf("data is required")
	}
	raw, err := json.Marshal(data)
	if err != nil || len(raw) > 16*1024 {
		return fmt.Errorf("about item must be valid JSON no larger than 16 KB")
	}
	return nil
}

func validateProfileLink(title, rawURL, visibility string) error {
	if strings.TrimSpace(title) == "" || utf8.RuneCountInString(title) > 80 {
		return fmt.Errorf("title is required and must be 80 characters or fewer")
	}
	if !SafePublicURL(normalizeProfileURL(rawURL)) {
		return fmt.Errorf("URL must use http or https")
	}
	if visibility != "" && visibility != "public" && visibility != "private" {
		return fmt.Errorf("visibility must be public or private")
	}
	return nil
}
