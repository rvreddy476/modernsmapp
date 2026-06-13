// Package store provides PostgreSQL access for the dating-service.
//
// Domain types live alongside the Store struct so the http and service
// packages can share JSON shapes without circular imports.
package store

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store wraps a pgxpool and exposes per-aggregate methods (profiles, tunes, …).
type Store struct {
	db *pgxpool.Pool
}

// New returns a Store backed by the given pool.
func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// --- Domain models ---------------------------------------------------------

// Profile is a user's dating profile (spec §10 dating_profiles).
type Profile struct {
	UserID           uuid.UUID  `json:"user_id"`
	Intent           string     `json:"intent"`
	Bio              string     `json:"bio"`
	Gender           *string    `json:"gender,omitempty"`
	BirthDate        *time.Time `json:"birth_date,omitempty"`
	City             *string    `json:"city,omitempty"`
	State            *string    `json:"state,omitempty"`
	Country          *string    `json:"country,omitempty"`
	Latitude         *float64   `json:"latitude,omitempty"`
	Longitude        *float64   `json:"longitude,omitempty"`
	LocationGeohash  *string    `json:"location_geohash,omitempty"`
	HeightCm         *int       `json:"height_cm,omitempty"`
	Religion         *string    `json:"religion,omitempty"`
	Community        *string    `json:"community,omitempty"`
	Occupation       *string    `json:"occupation,omitempty"`
	Education        *string    `json:"education,omitempty"`
	Drinking         *string    `json:"drinking,omitempty"`
	Smoking          *string    `json:"smoking,omitempty"`
	Exercise         *string    `json:"exercise,omitempty"`
	Diet             *string    `json:"diet,omitempty"`
	WantsChildren    *string    `json:"wants_children,omitempty"`
	FamilyPlans      *string    `json:"family_plans,omitempty"`
	BlurMode         bool       `json:"blur_mode"`
	VisibleToPublic  bool       `json:"visible_to_public"`
	Paused           bool       `json:"paused"`
	LanguagePrefs    []string   `json:"language_prefs"`
	TrustTier        string     `json:"trust_tier"`
	// ProfileStatus is the §P1-1 lifecycle column. Values: draft,
	// pending_photo, pending_selfie, pending_review, active, paused,
	// restricted, suspended, deleted. Discovery filters on 'active'.
	ProfileStatus    string     `json:"profile_status"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	DeletedAt        *time.Time `json:"deleted_at,omitempty"`
}

// Profile status constants (§P1-1). Centralised to avoid string drift
// between transition logic and discovery query filters.
const (
	ProfileStatusDraft         = "draft"
	ProfileStatusPendingPhoto  = "pending_photo"
	ProfileStatusPendingSelfie = "pending_selfie"
	ProfileStatusPendingReview = "pending_review"
	ProfileStatusActive        = "active"
	ProfileStatusPaused        = "paused"
	ProfileStatusRestricted    = "restricted"
	ProfileStatusSuspended     = "suspended"
	ProfileStatusDeleted       = "deleted"
)

// Tune is the multi-axis personality / vibe profile (spec §6.1.6, §10).
type Tune struct {
	UserID            uuid.UUID `json:"user_id"`
	LifestyleRhythm   *int      `json:"lifestyle_rhythm,omitempty"`
	ConversationStyle *string   `json:"conversation_style,omitempty"`
	FaithWeight       *int      `json:"faith_weight,omitempty"`
	FamilyWeight      *int      `json:"family_weight,omitempty"`
	RegionWeight      *int      `json:"region_weight,omitempty"`
	FamilyPlansAxis   *int      `json:"family_plans_axis,omitempty"`
	EducationAxis     *int      `json:"education_axis,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// Photo is one media reference attached to a dating profile.
//
// ModerationReason is the human-readable note the moderator/scanner
// recorded when flipping the photo's moderation_status. Owner-only
// surfaces (GET /v1/dating/photos/me) expose this to the photo's
// owner so "Why was my photo rejected?" can render the actual reason
// rather than a generic message. §P1-2 transparency control.
type Photo struct {
	ID                uuid.UUID `json:"id"`
	UserID            uuid.UUID `json:"user_id"`
	MediaID           uuid.UUID `json:"media_id"`
	SortOrder         int       `json:"sort_order"`
	IsPrimary         bool      `json:"is_primary"`
	Visibility        string    `json:"visibility"`
	ModerationStatus  string    `json:"moderation_status"`
	ModerationReason  *string   `json:"moderation_reason,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

// Prompt is a user's answer to a static prompt-catalog item.
type Prompt struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	PromptID  int       `json:"prompt_id"`
	Answer    string    `json:"answer"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Preferences are the discovery filters chosen by a user.
type Preferences struct {
	UserID             uuid.UUID `json:"user_id"`
	MinAge             *int      `json:"min_age,omitempty"`
	MaxAge             *int      `json:"max_age,omitempty"`
	DistanceKm         int       `json:"distance_km"`
	InterestedInGender *string   `json:"interested_in_gender,omitempty"`
	IntentFilter       []string  `json:"intent_filter"`
	BlurModePref       bool      `json:"blur_mode_pref"`
	LanguageFilter     []string  `json:"language_filter"`
	UpdatedAt          time.Time `json:"updated_at"`
}
