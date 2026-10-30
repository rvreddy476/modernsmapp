package onboarding

import (
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// CodeMediaIDInvalid refuses an optional media id that is not an uploaded
// media's UUID (the same code the rider document route uses).
const CodeMediaIDInvalid = "FOOD_MEDIA_ID_INVALID"

// ParseOptionalMediaID applies the media check the FSSAI route applies in
// ValidateFSSAI: the value must be the uploaded media's UUID. Blank means no
// media; anything else that does not parse (or is the nil UUID) is a 422.
func ParseOptionalMediaID(field, raw string) (*uuid.UUID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return nil, fieldErr(CodeMediaIDInvalid, field, field+" must be an uploaded media id")
	}
	return &id, nil
}

// MediaServeURL is the URL a client renders an uploaded image from:
// media-service's public serve route, which applies its delivery gate. base is
// FOOD_MEDIA_PUBLIC_BASE_URL; empty keeps the gateway-relative path.
func MediaServeURL(base string, id uuid.UUID) string {
	return strings.TrimRight(base, "/") + "/v1/media/" + id.String() + "/serve"
}

// CodeImageURLInvalid refuses an image_url that is not platform media
// (audit L6): a menu photo is drawn on every customer's menu, so it must be
// an upload served by media-service's delivery gate, never an arbitrary
// host (a tracking pixel, or another brand's photo).
const CodeImageURLInvalid = "FOOD_IMAGE_URL_INVALID"

// mediaServePath is the platform's own media path, as clients draw it:
// /v1/media/<id>/serve, optionally with a variant (/serve/thumb). The same
// shape post-service accepts for a playlist cover.
var mediaServePath = regexp.MustCompile(`^/v1/media/([0-9a-fA-F-]{36})/serve(?:/[a-z0-9_-]+)?$`)

// ParsePlatformMediaRef reads an image reference a client may store: a bare
// media id, this platform's /v1/media/<id>/serve path, or that path on base
// (FOOD_MEDIA_PUBLIC_BASE_URL, the form MediaServeURL writes). Anything
// else, an https URL on another host included, is not one.
func ParsePlatformMediaRef(base, raw string) (uuid.UUID, bool) {
	v := strings.TrimSpace(raw)
	if id, err := uuid.Parse(v); err == nil && len(v) == 36 {
		return id, id != uuid.Nil
	}
	if b := strings.TrimRight(base, "/"); b != "" && strings.HasPrefix(v, b+"/") {
		v = strings.TrimPrefix(v, b)
	}
	m := mediaServePath.FindStringSubmatch(v)
	if m == nil {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(m[1])
	if err != nil || id == uuid.Nil {
		return uuid.Nil, false
	}
	return id, true
}

// ImageURLError is the 422 for an image_url that is not platform media.
func ImageURLError(field string) error {
	return fieldErr(CodeImageURLInvalid, field, field+" must be an uploaded image (a media id or /v1/media/<id>/serve)")
}
