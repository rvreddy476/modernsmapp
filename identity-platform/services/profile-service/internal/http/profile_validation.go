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

// optionalText is the wire form of every optional text field on
// PUT /v1/profiles/me:
//
//	key absent     -> nil       -> the stored value is left alone
//	null or ""     -> ""        -> the stored value is cleared
//	"value"        -> "value"   -> set
//
// The fields were string or *string, which cannot tell an absent key from ""
// or from null, and the store wrote them unconditionally: a request that sent
// only bio erased last_name, preferred_name, pronouns, gender, category,
// profession, website and location. UnmarshalJSON runs only when the key is
// present, including for a literal null.
type optionalText struct {
	present bool
	value   string
}

func (f *optionalText) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*f = optionalText{present: true}
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*f = optionalText{present: true, value: s}
	return nil
}

// ptr is the store form: nil when absent, otherwise the value, "" for a clear.
func (f optionalText) ptr() *string {
	if !f.present {
		return nil
	}
	v := f.value
	return &v
}

// optionalTime is optionalText for a timestamp: absent leaves it alone, null
// or "" clears it, an RFC 3339 string sets it.
type optionalTime struct {
	present bool
	clear   bool
	value   time.Time
}

func (f *optionalTime) UnmarshalJSON(b []byte) error {
	if string(b) == "null" || string(b) == `""` {
		*f = optionalTime{present: true, clear: true}
		return nil
	}
	var t time.Time
	if err := json.Unmarshal(b, &t); err != nil {
		return err
	}
	*f = optionalTime{present: true, value: t}
	return nil
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
	// An absent field has value "", which passes every rule below.
	limit("display_name", req.DisplayName.value, maxDisplayName)
	limit("bio", req.Bio.value, maxBio)
	limit("category", req.Category.value, maxShortProfileText)
	limit("profession", req.Profession.value, maxShortProfileText)
	limit("location", req.Location.value, maxShortProfileText)
	limit("status_text", req.StatusText.value, maxStatusText)
	limit("cta_label", req.CTALabel.value, maxCTALabel)
	if value := strings.TrimSpace(req.Website.value); value != "" && !SafePublicURL(normalizeProfileURL(value)) {
		problems["website"] = "must be an http or https URL"
	}
	if value := strings.TrimSpace(req.CTAURL.value); value != "" && !SafePublicURL(normalizeProfileURL(value)) {
		problems["cta_url"] = "must be an http or https URL"
	}
	if req.ProfileThemeColor.value != "" && !validHexColor(req.ProfileThemeColor.value) {
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
