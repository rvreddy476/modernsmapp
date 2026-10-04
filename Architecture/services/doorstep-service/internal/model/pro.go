package model

import (
	"time"

	"github.com/google/uuid"
)

// ---- professional (contract: Professional, ProReadiness, ...) ----

// Professional is the contract's Professional. Gender comes from DigiLocker
// only; no request body carries it.
type Professional struct {
	ID            uuid.UUID `json:"id"`
	UserID        uuid.UUID `json:"user_id"`
	Status        string    `json:"status"`
	DisplayName   string    `json:"display_name"`
	CityCode      string    `json:"city_code"`
	Gender        *string   `json:"gender"`
	PhotoMediaID  *string   `json:"photo_media_id"`
	RatingAvg     *float64  `json:"rating_avg"`
	RatingCount   int       `json:"rating_count"`
	JobsCompleted int       `json:"jobs_completed"`
	MaxJobsPerDay int       `json:"max_jobs_per_day"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ProApplyInput is POST /pro/apply. Strictly decoded: a gender field is
// refused as an unknown field.
type ProApplyInput struct {
	DisplayName *string     `json:"display_name"`
	CityCode    *string     `json:"city_code"`
	CategoryIDs []uuid.UUID `json:"category_ids"`
}

// ProPatchInput is PATCH /pro/me.
type ProPatchInput struct {
	DisplayName  *string `json:"display_name"`
	PhotoMediaID *string `json:"photo_media_id"`
}

// ProReadiness is the contract's ProReadiness.
type ProReadiness struct {
	Status           string   `json:"status"`
	MissingSteps     []string `json:"missing_steps"`
	CompletedSteps   []string `json:"completed_steps"`
	RecommendedSteps []string `json:"recommended_steps"`
	CanGoOnDuty      bool     `json:"can_go_on_duty"`
}

// DigiLockerStart is POST /pro/digilocker/start.
type DigiLockerStart struct {
	AuthorizationURL string `json:"authorization_url"`
	State            string `json:"state"`
}

// DigiLockerCallbackInput is POST /pro/digilocker/callback.
type DigiLockerCallbackInput struct {
	Code  *string `json:"code"`
	State *string `json:"state"`
}

// MediaInput carries one media id (selfie).
type MediaInput struct {
	MediaID *string `json:"media_id"`
}

// KycCheck is the contract's KycCheck.
type KycCheck struct {
	Kind       string     `json:"kind"`
	Status     string     `json:"status"`
	Score      *float64   `json:"score"`
	VerifiedAt *time.Time `json:"verified_at"`
}

// ProSkillsInput is PUT /pro/me/skills.
type ProSkillsInput struct {
	SkillCodes []string `json:"skill_codes"`
}

// ProSkill is the contract's ProSkill.
type ProSkill struct {
	SkillCode  string     `json:"skill_code"`
	Status     string     `json:"status"`
	VerifiedAt *time.Time `json:"verified_at"`
}

// CertificateInput is a police or trade certificate upload.
type CertificateInput struct {
	MediaID           *string `json:"media_id"`
	IssuedOn          *string `json:"issued_on"`
	CertificateNumber *string `json:"certificate_number"`
}

// ProAreaInput is PUT /pro/me/area.
type ProAreaInput struct {
	ZoneIDs []uuid.UUID `json:"zone_ids"`
	HomeLat *float64    `json:"home_lat"`
	HomeLng *float64    `json:"home_lng"`
	RadiusM *int        `json:"radius_m"`
}

// ProArea is the saved service area (home null until one is saved).
type ProArea struct {
	ZoneIDs []uuid.UUID `json:"zone_ids"`
	HomeLat *float64    `json:"home_lat"`
	HomeLng *float64    `json:"home_lng"`
	RadiusM int         `json:"radius_m"`
}

// HoursWindow is one weekly-hours window (Asia/Kolkata).
type HoursWindow struct {
	Weekday int    `json:"weekday"`
	Start   string `json:"start"`
	End     string `json:"end"`
}

// WeeklyHours is the contract's WeeklyHours (request and response).
type WeeklyHours struct {
	Items []HoursWindow `json:"items"`
}

// DayOff is the contract's DayOff.
type DayOff struct {
	Date   string  `json:"date"`
	Reason *string `json:"reason"`
}

// BankInput is PUT /pro/me/bank.
type BankInput struct {
	AccountHolder *string `json:"account_holder"`
	AccountNumber *string `json:"account_number"`
	IFSC          *string `json:"ifsc"`
}

// PayoutAccount is the masked payout account: never the number.
type PayoutAccount struct {
	AccountHolder string `json:"account_holder"`
	AccountLast4  string `json:"account_last4"`
	IFSC          string `json:"ifsc"`
	Status        string `json:"status"`
}

// AgreementInput is POST /pro/me/agreement.
type AgreementInput struct {
	Version *string `json:"version"`
}

// PANInput is PUT /pro/me/pan.
type PANInput struct {
	PAN *string `json:"pan"`
}

// ProDocument is the contract's ProDocument plus skill_code (the skill a
// trade certificate is for; null for a police certificate).
type ProDocument struct {
	ID        uuid.UUID `json:"id"`
	ProID     uuid.UUID `json:"pro_id"`
	Kind      string    `json:"kind"`
	SkillCode *string   `json:"skill_code"`
	MediaID   string    `json:"media_id"`
	Status    string    `json:"status"`
	IssuedOn  *string   `json:"issued_on"`
	ExpiresOn *string   `json:"expires_on"`
	Reason    *string   `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

// ---- admin ----

// BackgroundCheckView is one background check in the admin detail.
type BackgroundCheckView struct {
	ID         uuid.UUID `json:"id"`
	Source     string    `json:"source"`
	Status     string    `json:"status"`
	ValidUntil *string   `json:"valid_until"`
}

// AdminProfessionalDetail is the contract's AdminProfessionalDetail. KYC is
// masked: check statuses and scores, document media ids, the payout
// account's last four digits; never a number.
type AdminProfessionalDetail struct {
	Professional     Professional          `json:"professional"`
	Readiness        ProReadiness          `json:"readiness"`
	Skills           []ProSkill            `json:"skills"`
	ZoneIDs          []uuid.UUID           `json:"zone_ids"`
	Documents        []ProDocument         `json:"documents"`
	BackgroundChecks []BackgroundCheckView `json:"background_checks"`
	KycChecks        []KycCheck            `json:"kyc_checks"`
	PayoutAccount    *PayoutAccount        `json:"payout_account"`
}

// ProfessionalPage is the admin list.
type ProfessionalPage struct {
	Items      []Professional `json:"items"`
	NextCursor *string        `json:"next_cursor"`
}

// ReasonInput is the contract's ReasonInput.
type ReasonInput struct {
	Reason *string `json:"reason"`
}

// SkillVerifyInput is POST .../skills/{code}/verify.
type SkillVerifyInput struct {
	Verified *bool   `json:"verified"`
	Reason   *string `json:"reason"`
}

// DocumentDecisionInput is POST /documents/{id}/decide.
type DocumentDecisionInput struct {
	Decision *string `json:"decision"`
	Reason   *string `json:"reason"`
}
