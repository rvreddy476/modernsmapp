package onboarding

import (
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
