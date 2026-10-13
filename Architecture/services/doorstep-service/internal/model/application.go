package model

import "time"

type PartnerApplicationInput struct {
	ReviewConsent       bool      `json:"review_consent"`
	FirstName           string    `json:"first_name"`
	LastName            string    `json:"last_name"`
	Gender              string    `json:"gender"`
	StateCode           string    `json:"state_code"`
	Town                string    `json:"town"`
	AlternatePhone      string    `json:"alternate_phone"`
	SkillCodes          []string  `json:"skill_codes"`
	IdentityKind        string    `json:"identity_kind"`
	IdentityMediaID     string    `json:"identity_media_id"`
	SelfieMediaID       string    `json:"selfie_media_id"`
	IntroductionMediaID string    `json:"introduction_media_id"`
	ExperienceMediaID   string    `json:"experience_media_id"`
	PAN                 string    `json:"pan"`
	Bank                BankInput `json:"bank"`
}
type PartnerApplication struct {
	FirstName                string     `json:"first_name"`
	LastName                 string     `json:"last_name"`
	DeclaredGender           string     `json:"declared_gender"`
	StateCode                string     `json:"state_code"`
	StateName                string     `json:"state_name"`
	Town                     string     `json:"town"`
	PrimaryPhoneLast4        string     `json:"primary_phone_last4"`
	AlternatePhoneLast4      *string    `json:"alternate_phone_last4"`
	AlternatePhoneVerifiedAt *time.Time `json:"alternate_phone_verified_at"`
	SubmittedAt              time.Time  `json:"submitted_at"`
}
