// Package prokyc holds the pure rules for Doorstep professional onboarding:
// which steps a professional still owes before an admin may approve them,
// whether what is missing is only an admin review, the gender and salon
// rules at approval, and the weekly-hours shape. Nothing here touches the
// database, a key or the network (the food-service riderkyc pattern).
//
// Step names are the contract's OnboardingStep enum, in the plan's order:
// profile -> aadhaar_digilocker (sets gender) -> selfie_face_match -> skills
// -> service_area -> weekly_hours -> bank -> police_certificate (the
// background check) -> agreement; pan is recommended, never required.
package prokyc

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Step is one onboarding step (contract OnboardingStep).
type Step string

const (
	StepProfile           Step = "profile"
	StepAadhaar           Step = "aadhaar_digilocker"
	StepSelfie            Step = "selfie_face_match"
	StepSkills            Step = "skills"
	StepServiceArea       Step = "service_area"
	StepWeeklyHours       Step = "weekly_hours"
	StepBank              Step = "bank"
	StepPoliceCertificate Step = "police_certificate"
	StepAgreement         Step = "agreement"
	StepPAN               Step = "pan"
)

// RequiredSteps is the required checklist in presentation order.
var RequiredSteps = []Step{
	StepProfile, StepAadhaar, StepSelfie, StepSkills, StepServiceArea,
	StepWeeklyHours, StepBank, StepPoliceCertificate, StepAgreement,
}

// AgreementVersion is the professional agreement the app must show and the
// professional must accept. Bump it (and ship the new text to the pro app)
// to require everyone to accept again.
const AgreementVersion = "2026-10-04"

// Facts are what the store knows about one professional.
type Facts struct {
	DisplayName string
	HasPhoto    bool
	// AadhaarVerified: a passed, unexpired DigiLocker Aadhaar check exists
	// AND the gender was recorded from it (gender_source = digilocker).
	AadhaarVerified bool
	// SelfieMatched: a passed selfie face match exists.
	SelfieMatched bool
	// VerifiedSkills counts pro_skills rows in status verified.
	VerifiedSkills int
	// SkillsAwaitingReview counts declared skills waiting for an admin: a
	// certificate-free skill (every declaration is pending until an admin
	// verifies it), or one whose trade certificate is under review or
	// approved.
	SkillsAwaitingReview int
	// SelfieAwaitingReview: a selfie is pending an admin's decision (the face
	// match is advisory).
	SelfieAwaitingReview bool
	// HasServiceArea: at least one zone and a home point.
	HasServiceArea bool
	HasWeeklyHours bool
	// HasPayoutAccount: an active payout account not marked failed.
	HasPayoutAccount bool
	// BackgroundClear: a clear background check valid today.
	BackgroundClear bool
	// PoliceCertificatePending: a police certificate is under admin review.
	PoliceCertificatePending bool
	// AgreementVersion is the version the professional accepted ("" none).
	AgreementVersion string
	HasPAN           bool
}

func (f Facts) done(s Step) bool {
	switch s {
	case StepProfile:
		return strings.TrimSpace(f.DisplayName) != "" && f.HasPhoto
	case StepAadhaar:
		return f.AadhaarVerified
	case StepSelfie:
		return f.SelfieMatched
	case StepSkills:
		return f.VerifiedSkills > 0
	case StepServiceArea:
		return f.HasServiceArea
	case StepWeeklyHours:
		return f.HasWeeklyHours
	case StepBank:
		return f.HasPayoutAccount
	case StepPoliceCertificate:
		return f.BackgroundClear
	case StepAgreement:
		return f.AgreementVersion == AgreementVersion
	case StepPAN:
		return f.HasPAN
	}
	return false
}

// MissingSteps returns the unmet required steps in order; an empty, non-nil
// slice means the professional may be approved.
func MissingSteps(f Facts) []Step {
	out := []Step{}
	for _, s := range RequiredSteps {
		if !f.done(s) {
			out = append(out, s)
		}
	}
	return out
}

// CompletedSteps returns every step (required and recommended) already done.
func CompletedSteps(f Facts) []Step {
	out := []Step{}
	for _, s := range append(append([]Step{}, RequiredSteps...), StepPAN) {
		if f.done(s) {
			out = append(out, s)
		}
	}
	return out
}

// RecommendedSteps are optional steps not yet done (PAN).
func RecommendedSteps(f Facts) []Step {
	if f.HasPAN {
		return []Step{}
	}
	return []Step{StepPAN}
}

// AwaitingReviewOnly reports whether the professional has done everything
// they can and only admin reviews remain: every missing step is skills
// awaiting an admin, the selfie awaiting an admin, or the police
// certificate under review.
// A draft professional in this state moves to pending_verification. Nothing
// missing at all also counts.
func AwaitingReviewOnly(f Facts) bool {
	for _, s := range MissingSteps(f) {
		switch {
		case s == StepSkills && f.SkillsAwaitingReview > 0:
		case s == StepSelfie && f.SelfieAwaitingReview:
		case s == StepPoliceCertificate && f.PoliceCertificatePending:
		default:
			return false
		}
	}
	return true
}

// Strings converts steps for the wire.
func Strings(steps []Step) []string {
	out := make([]string, 0, len(steps))
	for _, s := range steps {
		out = append(out, string(s))
	}
	return out
}

// ---- gender and salon rules ----

// Category gender rules (doorstep.categories.gender_rule).
const (
	GenderAny        = "any"
	GenderFemaleOnly = "female_pros_only"
	GenderMaleOnly   = "male_pros_only"
)

// FamilySalon is the category family the salon rules apply to.
const FamilySalon = "BEAUTY_SALON"

// GenderAllowed reports whether a professional of gender (DigiLocker only;
// "" unknown) may serve a category with rule. An unknown gender satisfies
// only "any": the rule fails closed.
func GenderAllowed(rule, gender string) bool {
	switch rule {
	case GenderAny:
		return true
	case GenderFemaleOnly:
		return gender == "female"
	case GenderMaleOnly:
		return gender == "male"
	}
	return false
}

// SkillCategory is one active category that uses a skill (through a service
// whose required_skill it is).
type SkillCategory struct {
	Skill      string
	Family     string
	GenderRule string
}

// Block is one reason a professional may not be approved with a skill.
type Block struct {
	Skill  string
	Reason string // BlockGender or BlockBackgroundCheck
}

const (
	BlockGender          = "gender_rule"
	BlockBackgroundCheck = "background_check_required"
)

// ApprovalBlocks applies the category rules to the professional's verified
// skills: every category using a skill must accept the professional's
// DigiLocker gender (women's salon: women only; men's salon: men only), and
// a salon skill needs a clear background check. Results are sorted.
func ApprovalBlocks(cats []SkillCategory, gender string, backgroundClear bool) []Block {
	seen := map[Block]bool{}
	for _, c := range cats {
		if !GenderAllowed(c.GenderRule, gender) {
			seen[Block{Skill: c.Skill, Reason: BlockGender}] = true
		}
		if c.Family == FamilySalon && !backgroundClear {
			seen[Block{Skill: c.Skill, Reason: BlockBackgroundCheck}] = true
		}
	}
	out := make([]Block, 0, len(seen))
	for b := range seen {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Skill != out[j].Skill {
			return out[i].Skill < out[j].Skill
		}
		return out[i].Reason < out[j].Reason
	})
	return out
}

// GenderBlocked reports whether any category using a skill refuses a KNOWN
// gender. Used when a skill is declared: an unknown gender is not refused
// there (DigiLocker may come later) and ApprovalBlocks catches it at approval.
func GenderBlocked(cats []SkillCategory, gender string) bool {
	if gender == "" {
		return false
	}
	for _, c := range cats {
		if !GenderAllowed(c.GenderRule, gender) {
			return true
		}
	}
	return false
}

// ---- weekly hours ----

// Window is one working window on a weekday (0 = Sunday), times HH:MM in
// Asia/Kolkata.
type Window struct {
	Weekday int
	Start   string
	End     string
}

// MaxWindowsPerDay bounds a day's windows (split shifts, not a timetable).
const MaxWindowsPerDay = 6

var hhmm = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// Minutes converts "HH:MM" to minutes after midnight (ok=false if malformed).
func Minutes(s string) (int, bool) {
	if !hhmm.MatchString(s) {
		return 0, false
	}
	return int(s[0]-'0')*600 + int(s[1]-'0')*60 + int(s[3]-'0')*10 + int(s[4]-'0'), true
}

// HoursError names the window an hours payload broke.
type HoursError struct {
	Index   int
	Message string
}

func (e *HoursError) Error() string { return fmt.Sprintf("items[%d]: %s", e.Index, e.Message) }

// ValidateHours checks a weekly-hours replacement: weekday 0-6, HH:MM times,
// end after start (no window crosses midnight), at most MaxWindowsPerDay a
// day, and no two windows of one day overlapping (touching is fine). It
// returns the windows sorted by weekday then start. An empty list is valid
// (it clears the hours) and leaves the weekly_hours step missing.
func ValidateHours(in []Window) ([]Window, error) {
	type indexed struct {
		Window
		i, from, to int
	}
	all := make([]indexed, 0, len(in))
	perDay := map[int]int{}
	for i, w := range in {
		if w.Weekday < 0 || w.Weekday > 6 {
			return nil, &HoursError{i, "weekday must be 0 (Sunday) to 6"}
		}
		from, ok1 := Minutes(w.Start)
		to, ok2 := Minutes(w.End)
		if !ok1 || !ok2 {
			return nil, &HoursError{i, "start and end must be HH:MM"}
		}
		if to <= from {
			return nil, &HoursError{i, "end must be after start on the same day"}
		}
		if to-from < 30 {
			return nil, &HoursError{i, "a window must be at least 30 minutes"}
		}
		perDay[w.Weekday]++
		if perDay[w.Weekday] > MaxWindowsPerDay {
			return nil, &HoursError{i, fmt.Sprintf("at most %d windows a day", MaxWindowsPerDay)}
		}
		all = append(all, indexed{w, i, from, to})
	}
	sort.SliceStable(all, func(a, b int) bool {
		if all[a].Weekday != all[b].Weekday {
			return all[a].Weekday < all[b].Weekday
		}
		return all[a].from < all[b].from
	})
	out := make([]Window, 0, len(all))
	for k, w := range all {
		if k > 0 && all[k-1].Weekday == w.Weekday && w.from < all[k-1].to {
			return nil, &HoursError{w.i, "windows on one day may not overlap"}
		}
		out = append(out, w.Window)
	}
	return out, nil
}
