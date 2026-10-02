// Profile options (mechanic M6): the fixed lists the new profile fields and
// filters draw from, and their validation. GET /v1/dating/profile/options
// serves the lists with our own labels, so every client shows the same
// choices and the server accepts only these codes.
package service

import (
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/dating-service/internal/store"
)

// Option is one choice: a stable code and its label.
type Option struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

// Profile field limits.
const (
	MaxInterests = 10
	MaxLanguages = 8
	MinHeightCm  = 120
	MaxHeightCm  = 230
)

// InterestOptions is the fixed interest list, alphabetical by label.
var InterestOptions = []Option{
	{"art", "Art"}, {"baking", "Baking"}, {"board_games", "Board games"}, {"books", "Books"},
	{"chai", "Chai"}, {"coding", "Coding"}, {"comedy", "Comedy"}, {"cooking", "Cooking"},
	{"cricket", "Cricket"}, {"cycling", "Cycling"}, {"dancing", "Dancing"}, {"fashion", "Fashion"},
	{"film", "Film"}, {"fitness", "Fitness"}, {"football", "Football"}, {"gaming", "Gaming"},
	{"gardening", "Gardening"}, {"hiking", "Hiking"}, {"history", "History"}, {"languages", "Languages"},
	{"meditation", "Meditation"}, {"music", "Music"}, {"nature", "Nature"}, {"painting", "Painting"},
	{"pets", "Pets"}, {"photography", "Photography"}, {"podcasts", "Podcasts"}, {"poetry", "Poetry"},
	{"running", "Running"}, {"science", "Science"}, {"singing", "Singing"}, {"startups", "Startups"},
	{"swimming", "Swimming"}, {"technology", "Technology"}, {"theatre", "Theatre"}, {"travel", "Travel"},
	{"trekking", "Trekking"}, {"volunteering", "Volunteering"}, {"writing", "Writing"}, {"yoga", "Yoga"},
}

// LanguageOptions is the fixed language list (ISO 639 codes).
var LanguageOptions = []Option{
	{"ar", "Arabic"}, {"as", "Assamese"}, {"bn", "Bengali"}, {"zh", "Chinese"}, {"en", "English"},
	{"fr", "French"}, {"de", "German"}, {"gu", "Gujarati"}, {"hi", "Hindi"}, {"it", "Italian"},
	{"ja", "Japanese"}, {"kn", "Kannada"}, {"kok", "Konkani"}, {"ko", "Korean"}, {"ml", "Malayalam"},
	{"mr", "Marathi"}, {"ne", "Nepali"}, {"or", "Odia"}, {"pt", "Portuguese"}, {"pa", "Punjabi"},
	{"ru", "Russian"}, {"sa", "Sanskrit"}, {"es", "Spanish"}, {"ta", "Tamil"}, {"te", "Telugu"},
	{"ur", "Urdu"},
}

// Lifestyle basics.
var (
	DrinkingOptions = []Option{{"never", "Never"}, {"rarely", "Rarely"}, {"socially", "Socially"}, {"regularly", "Regularly"}}
	SmokingOptions  = []Option{{"never", "Never"}, {"socially", "Socially"}, {"regularly", "Regularly"}, {"trying_to_quit", "Trying to quit"}}
	ExerciseOptions = []Option{{"never", "Never"}, {"sometimes", "Sometimes"}, {"often", "Often"}, {"daily", "Daily"}}
	DietOptions     = []Option{{"eggetarian", "Eggetarian"}, {"jain", "Jain"}, {"non_vegetarian", "Non-vegetarian"}, {"other", "Other"}, {"vegan", "Vegan"}, {"vegetarian", "Vegetarian"}}
)

// DistanceBucketOptions are the distance filter choices: each is "within"
// its upper edge, and gt_25_km is any distance.
var DistanceBucketOptions = []Option{
	{"lt_5_km", "Under 5 km"}, {"km_5_10", "Within 10 km"}, {"km_10_25", "Within 25 km"}, {"gt_25_km", "Any distance"},
}

// Range is an inclusive numeric range.
type Range struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// ProfileOptions is GET /v1/dating/profile/options.
type ProfileOptions struct {
	Interests       []Option `json:"interests"`
	MaxInterests    int      `json:"max_interests"`
	Languages       []Option `json:"languages"`
	MaxLanguages    int      `json:"max_languages"`
	HeightCm        Range    `json:"height_cm"`
	Drinking        []Option `json:"drinking"`
	Smoking         []Option `json:"smoking"`
	Exercise        []Option `json:"exercise"`
	Diet            []Option `json:"diet"`
	DistanceBuckets []Option `json:"distance_buckets"`
}

// GetProfileOptions returns the lists.
func GetProfileOptions() ProfileOptions {
	return ProfileOptions{
		Interests: InterestOptions, MaxInterests: MaxInterests,
		Languages: LanguageOptions, MaxLanguages: MaxLanguages,
		HeightCm: Range{MinHeightCm, MaxHeightCm},
		Drinking: DrinkingOptions, Smoking: SmokingOptions, Exercise: ExerciseOptions, Diet: DietOptions,
		DistanceBuckets: DistanceBucketOptions,
	}
}

func optionCodes(opts []Option) []string {
	out := make([]string, len(opts))
	for i, o := range opts {
		out[i] = o.Code
	}
	return out
}

func inOptions(opts []Option, code string) bool {
	for _, o := range opts {
		if o.Code == code {
			return true
		}
	}
	return false
}

// FieldError is a refused field with its allowed values or limits, so the
// client can mark the right picker. Code is the stable error code.
type FieldError struct {
	Code    string
	Field   string
	Message string
	Details map[string]any
}

func (e *FieldError) Error() string { return "invalid: " + e.Message }

func refuseField(code, field, msg string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	details["field"] = field
	return &FieldError{Code: code, Field: field, Message: msg, Details: details}
}

// ErrFieldInvalid matches every *FieldError.
var ErrFieldInvalid = errors.New("invalid field")

func (e *FieldError) Is(target error) bool { return target == ErrFieldInvalid }

// checkCodes refuses an unknown or repeated code, or more than max.
func checkCodes(field, code string, opts []Option, values []string, max int) error {
	if max > 0 && len(values) > max {
		return refuseField("TOO_MANY_"+code, field, fmt.Sprintf("at most %d %s", max, strings.ReplaceAll(field, "_", " ")), map[string]any{"max": max})
	}
	seen := map[string]bool{}
	for _, v := range values {
		if !inOptions(opts, v) || seen[v] {
			return refuseField("INVALID_"+code, field, field+" must be codes from the profile options", map[string]any{"allowed": optionCodes(opts)})
		}
		seen[v] = true
	}
	return nil
}

func checkHeight(field string, h *int) error {
	if h != nil && (*h < MinHeightCm || *h > MaxHeightCm) {
		return refuseField("INVALID_HEIGHT", field, fmt.Sprintf("height must be %d to %d cm", MinHeightCm, MaxHeightCm),
			map[string]any{"min": MinHeightCm, "max": MaxHeightCm})
	}
	return nil
}

func checkOne(field string, opts []Option, v *string) error {
	if v != nil && strings.TrimSpace(*v) != "" && !inOptions(opts, *v) {
		return refuseField("INVALID_LIFESTYLE", field, field+" must be a code from the profile options", map[string]any{"allowed": optionCodes(opts)})
	}
	return nil
}

// validateProfileFields checks the fields mechanic M6 added or tightened:
// interests, languages, height and the lifestyle basics. Each is checked
// only when sent; an empty string clears a basic.
func validateProfileFields(p store.UpsertProfileParams) error {
	if p.Interests != nil {
		if err := checkCodes("interests", "INTEREST", InterestOptions, p.Interests, MaxInterests); err != nil {
			return err
		}
	}
	if p.LanguagePrefs != nil {
		if err := checkCodes("language_prefs", "LANGUAGE", LanguageOptions, p.LanguagePrefs, MaxLanguages); err != nil {
			return err
		}
	}
	if err := checkHeight("height_cm", p.HeightCm); err != nil {
		return err
	}
	for _, c := range []struct {
		field string
		opts  []Option
		v     *string
	}{{"drinking", DrinkingOptions, p.Drinking}, {"smoking", SmokingOptions, p.Smoking}, {"exercise", ExerciseOptions, p.Exercise}, {"diet", DietOptions, p.Diet}} {
		if err := checkOne(c.field, c.opts, c.v); err != nil {
			return err
		}
	}
	return nil
}
