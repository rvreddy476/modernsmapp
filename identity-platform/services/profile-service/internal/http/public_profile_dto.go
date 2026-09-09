package http

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/google/uuid"
)

// Module 3 M3-P0-4 / SR-4 — private-by-construction public profiles.
//
// THE DEFECT
//
// `GET /v1/profiles/:userId` is unauthenticated, and it serialised
// store.Profile directly. That struct carries, among other things:
//
//	DoB       *time.Time  — the user's EXACT date of birth
//	Gender    *string
//	Timezone  *string     — narrows physical location
//	PreferredName, Pronouns
//	UpdatedAt             — reveals activity timing
//
// So anyone on the internet could enumerate user IDs and harvest full dates of
// birth. A date of birth is not a display field: it is an identity-verification
// answer, a password-reset security question, and the input to the 18+ gate.
//
// THE FIX, AND WHY IT IS AN ALLOWLIST
//
// PublicProfile lists what MAY be published. The alternative — deleting the
// sensitive fields from the response — fails the moment someone adds a column.
// A denylist is a promise to remember; an allowlist is a decision that a new
// field is private until a human says otherwise.
//
// This is also why the conversion is written field by field rather than with
// reflection or struct embedding: embedding store.Profile would silently
// re-expose every field added to it later, which is the exact failure being
// fixed.

// PublicProfile is the ONLY shape returned to an unauthenticated or non-owner
// caller. Adding a field here is a privacy decision — make it deliberately.
type PublicProfile struct {
	UserID      uuid.UUID  `json:"user_id"`
	Username    *string    `json:"username,omitempty"`
	DisplayName string     `json:"display_name"`
	Bio         string     `json:"bio"`
	Pronouns    *string    `json:"pronouns,omitempty"`
	AvatarMedia *uuid.UUID `json:"avatar_media_id,omitempty"`
	CoverMedia  *uuid.UUID `json:"cover_media_id,omitempty"`

	Category   string `json:"category"`
	Profession string `json:"profession"`
	Website    string `json:"website"`
	// Location is the free-text "where I am" a user typed into their profile.
	// It is published because the user wrote it FOR publication. Timezone is
	// not: it is inferred from the device and the user never chose to share it.
	Location string `json:"location"`

	BadgeFlags        int    `json:"badge_flags"`
	IsVerified        bool   `json:"is_verified"`
	VerificationLevel string `json:"verification_level"`

	StatusText      *string    `json:"status_text,omitempty"`
	StatusEmoji     *string    `json:"status_emoji,omitempty"`
	StatusExpiresAt *time.Time `json:"status_expires_at,omitempty"`

	ProfileThemeColor string  `json:"profile_theme_color"`
	IntroMediaURL     *string `json:"intro_media_url,omitempty"`
	IntroMediaType    *string `json:"intro_media_type,omitempty"`
	CTALabel          *string `json:"cta_label,omitempty"`
	CTAURL            *string `json:"cta_url,omitempty"`
	MemberSinceBadge  bool    `json:"member_since_badge"`

	FollowerCount  int64 `json:"follower_count"`
	FollowingCount int64 `json:"following_count"`
	FriendCount    int64 `json:"friend_count"`
	PostCount      int64 `json:"post_count"`

	// IsPrivate mirrors identity user-service account_visibility. Display
	// only: the profile stays reachable; post-service gates the posts.
	IsPrivate bool `json:"is_private"`
	// FollowStatus is the viewer-side edge toward this profile: "none",
	// "requested" (pending follow request) or "following". Present only when
	// a signed-in viewer who is not the owner reads a single profile.
	FollowStatus string `json:"follow_status,omitempty"`

	// CreatedAt supports the "member since" affordance the product shows.
	CreatedAt time.Time `json:"created_at"`
}

// privateProfileFields documents what is deliberately WITHHELD, and why. It is
// referenced by the test that asserts none of these appear in a public
// response, so the reasoning and the enforcement cannot drift apart.
var privateProfileFields = map[string]string{
	"dob":            "exact date of birth: an identity-verification answer and the input to the 18+ gate",
	"gender":         "sensitive personal characteristic the user did not choose to publish",
	"timezone":       "inferred from the device, narrows physical location, never explicitly shared",
	"first_name":     "legal-name fragment; DisplayName is the field the user chose to show",
	"last_name":      "legal-name fragment; DisplayName is the field the user chose to show",
	"preferred_name": "internal personalisation, not a published field",
	"updated_at":     "reveals activity timing to anyone polling the endpoint",
	"email":          "contact detail; never published",
	"phone":          "contact detail; never published",
}

// ToPublicProfile converts a stored profile to the publishable shape.
//
// Written out field by field on purpose: a new column on store.Profile must be
// added here explicitly before it can ever reach an unauthenticated caller.
func ToPublicProfile(p *store.Profile) *PublicProfile {
	if p == nil {
		return nil
	}
	return &PublicProfile{
		UserID:      p.UserID,
		Username:    p.Username,
		DisplayName: p.DisplayName,
		Bio:         p.Bio,
		Pronouns:    p.Pronouns,
		AvatarMedia: p.AvatarMediaID,
		CoverMedia:  p.CoverMediaID,

		Category:   p.Category,
		Profession: p.Profession,
		// LB-4: user-supplied URLs are scheme-validated before publication.
		// A profile is a place a user types a URL and other people click it,
		// so an unvalidated value is a stored injection vector —
		// `javascript:` executes in the viewer's session, `data:text/html`
		// renders attacker markup from this origin in some webviews. An
		// unsafe value is blanked, not published.
		Website:  safePublicURLString(p.Website),
		Location: p.Location,

		BadgeFlags:        p.BadgeFlags,
		IsVerified:        p.IsVerified,
		VerificationLevel: p.VerificationLevel,

		StatusText:      p.StatusText,
		StatusEmoji:     p.StatusEmoji,
		StatusExpiresAt: p.StatusExpiresAt,

		ProfileThemeColor: p.ProfileThemeColor,
		// LB-4: intro-media and CTA URLs are user-supplied and clickable.
		IntroMediaURL:    SanitizePublicURLPointer(p.IntroMediaURL),
		IntroMediaType:   p.IntroMediaType,
		CTALabel:         p.CTALabel,
		CTAURL:           SanitizePublicURLPointer(p.CTAURL),
		MemberSinceBadge: p.MemberSinceBadge,

		FollowerCount:  p.FollowerCount,
		FollowingCount: p.FollowingCount,
		FriendCount:    p.FriendCount,
		PostCount:      p.PostCount,

		CreatedAt: p.CreatedAt,
	}
}

// ---------------------------------------------------------------
// Rendered media URLs
// ---------------------------------------------------------------
//
// # THE GAP THIS CLOSES
//
// A profile published `avatar_media_id` and nothing else, so every client had
// to know how media-service turns an id into bytes before it could draw a
// face. The web client did not, and rendered initials for everyone.
//
// # WHY A STABLE PATH AND NOT A SIGNED URL
//
// The obvious fix — resolve each avatar at media-service and inline the signed
// URL — is wrong twice over. media-service signs profile media for
// delivery.MaxProtectedTTL (5 minutes), because that TTL IS the revocation
// window for a photo whose owner blocks you or goes private. Inlining it puts
// a five-minute fuse on every avatar in a feed page: the reader who leaves a
// tab open comes back to broken images. And it costs a signing round trip per
// author on every profile read, which is exactly the N+1 a feed page cannot
// afford.
//
// This path is derived from an id the payload already carries, so it costs no
// lookup at all — not one per author, not one per page. Each render re-asks
// the privacy authority through media-service and gets a URL signed at that
// moment, which also means a revoked photo stops rendering on the next load
// instead of at the end of somebody's cached signature.
//
// The path is gateway-relative for the same reason `hls_url` and
// `playback_url` are: the caller knows its own gateway origin, and this
// service does not need one configured to answer.
//
// `avatar` is a rendition ALIAS, resolved server-side against the renditions
// the asset actually owns (media-service: service.AvatarVariant). A hard-coded
// `thumb_150` here would 404 for any avatar the image pipeline skipped a
// rendition for.
const profileMediaPathFormat = "/v1/media/%s/serve/avatar"

func profileMediaURL(mediaID *uuid.UUID) *string {
	if mediaID == nil || *mediaID == uuid.Nil {
		return nil
	}
	u := fmt.Sprintf(profileMediaPathFormat, *mediaID)
	return &u
}

// MarshalJSON publishes avatar_url / cover_url alongside the ids they are
// derived from.
//
// # COMPUTED AT THE WIRE, NOT STORED ON THE STRUCT
//
// The photo-privacy gate redacts a profile by nil-ing AvatarMedia and
// CoverMedia (profile_photo_gate.go). A URL held in its own field would be a
// second place that redaction has to remember, and forgetting it would publish
// a working link to the photo the gate just withheld. Deriving the URL here
// makes that unrepresentable: no id on the wire, no URL on the wire.
func (p PublicProfile) MarshalJSON() ([]byte, error) {
	// The alias sheds this method, so the embedded value marshals as a plain
	// struct instead of recursing into MarshalJSON forever.
	type alias PublicProfile
	return json.Marshal(struct {
		alias
		AvatarURL *string `json:"avatar_url,omitempty"`
		CoverURL  *string `json:"cover_url,omitempty"`
	}{
		alias:     alias(p),
		AvatarURL: profileMediaURL(p.AvatarMedia),
		CoverURL:  profileMediaURL(p.CoverMedia),
	})
}

// ToPublicProfiles converts a list, preserving order.
func ToPublicProfiles(ps []store.Profile) []*PublicProfile {
	out := make([]*PublicProfile, 0, len(ps))
	for i := range ps {
		out = append(out, ToPublicProfile(&ps[i]))
	}
	return out
}

// ToPublicProfileMap converts the batch-lookup result.
//
// The batch endpoint is the widest leak of the four: one request with a
// hundred user IDs returned a hundred full dates of birth.
func ToPublicProfileMap(m map[uuid.UUID]*store.Profile) map[uuid.UUID]*PublicProfile {
	out := make(map[uuid.UUID]*PublicProfile, len(m))
	for id, p := range m {
		out[id] = ToPublicProfile(p)
	}
	return out
}
