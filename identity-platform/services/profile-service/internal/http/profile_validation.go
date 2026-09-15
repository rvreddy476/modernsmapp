package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/identity-profile-service/internal/service"
	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
)

// profileDOBField is the wire form of `dob` on PUT /v1/profiles/me.
//
// It was *time.Time, which could not tell an absent field from an explicit
// null (both decoded to nil, and the store then wrote NULL), and which only
// parsed RFC3339, so the obvious "YYYY-MM-DD" was a 400. UnmarshalJSON runs
// only when the key is present, including for a literal null.
type profileDOBField struct {
	present bool
	raw     json.RawMessage
}

func (f *profileDOBField) UnmarshalJSON(b []byte) error {
	f.present = true
	f.raw = append(json.RawMessage(nil), b...)
	return nil
}

// resolve returns nil for an absent field, the parsed date for a valid one,
// and a *service.FieldError otherwise.
func (f profileDOBField) resolve() (*time.Time, error) {
	if !f.present {
		return nil, nil
	}
	required := &service.FieldError{Field: "dob", Code: service.CodeDOBRequired,
		Message: "date of birth cannot be removed; omit the field to leave it unchanged"}
	if string(f.raw) == "null" {
		return nil, required
	}
	var s string
	if err := json.Unmarshal(f.raw, &s); err != nil {
		return nil, &service.FieldError{Field: "dob", Code: service.CodeDOBInvalid,
			Message: "date of birth must be a string in YYYY-MM-DD format"}
	}
	if strings.TrimSpace(s) == "" {
		return nil, required
	}
	born, err := service.ParseProfileDOB(s)
	if err != nil {
		return nil, err
	}
	return &born, nil
}

// writeFieldError renders a validation failure as 422 in the standard
// envelope — error.code is the stable field code, error.details.field names
// the field — and reports whether it wrote. Any other error is left to the
// caller.
func writeFieldError(c *gin.Context, err error) bool {
	fe, ok := service.IsFieldError(err)
	if !ok {
		return false
	}
	api.Error(c.Writer, http.StatusUnprocessableEntity, fe.Code, fe.Message,
		map[string]any{"field": fe.Field}, nil)
	return true
}

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
