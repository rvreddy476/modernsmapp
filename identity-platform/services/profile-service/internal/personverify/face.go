package personverify

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// The selfie step: liveness plus a face match of the selfie against the
// DigiLocker (Aadhaar) photo. Ported from the rider-service selfie check
// (internal/service/facecompare.go) and dating's liveness client: a provider
// behind an interface, a 0-100 similarity against a configurable threshold,
// a mock refused in production, and never a pass on an error — an
// unavailable provider leaves the attempt waiting, it never verifies.
//
// Difference from rider and dating: the reference is the DigiLocker photo as
// BYTES, not a media-service id. The Aadhaar photo is not the user's upload
// and must not become one of their media, so it is handed to the provider
// directly and never stored in the clear (it waits sealed on the attempt).

// SelfieCheck names the user's selfie video (a short blink recording,
// uploaded to media-service like any video) and carries the reference photo.
type SelfieCheck struct {
	UserID        uuid.UUID
	SelfieMediaID uuid.UUID
	// ReferencePhoto is the DigiLocker photo (JPEG). Never logged.
	ReferencePhoto []byte
}

func (c SelfieCheck) String() string { return "SelfieCheck{" + c.UserID.String() + ", redacted}" }

// SelfieResult is the provider's answer.
type SelfieResult struct {
	// Live is true when the provider saw a live, single, consistent face.
	Live bool
	// Similarity of the selfie face to the reference, 0-100.
	Similarity float64
	Provider   string
}

var (
	// ErrFaceUnavailable: no answer (provider down, not configured). Never a
	// pass and never a refusal: the attempt keeps waiting.
	ErrFaceUnavailable = errors.New("face check is unavailable")
	// ErrSelfieInvalid: the selfie media is not the user's ready video, or
	// cannot be analysed. The user may submit another.
	ErrSelfieInvalid = errors.New("selfie cannot be checked")
)

// FaceVerifier runs liveness and the face match in one call.
type FaceVerifier interface {
	VerifySelfie(ctx context.Context, c SelfieCheck) (*SelfieResult, error)
}

// MockFaceVerifier answers a fixed result. Development only: FaceFromEnv
// refuses it in production, where it would verify every selfie.
type MockFaceVerifier struct {
	Result SelfieResult
	Err    error
}

// VerifySelfie implements FaceVerifier.
func (m *MockFaceVerifier) VerifySelfie(_ context.Context, c SelfieCheck) (*SelfieResult, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	if c.SelfieMediaID == uuid.Nil || len(c.ReferencePhoto) == 0 {
		return nil, ErrSelfieInvalid
	}
	r := m.Result
	return &r, nil
}

// Face provider environment.
const (
	EnvFaceMode          = "PERSON_VERIFICATION_FACE_MODE"
	EnvMinSimilarity     = "PERSON_VERIFICATION_MIN_SIMILARITY"
	DefaultMinSimilarity = 90.0

	FaceModeDisabled = "disabled"
	FaceModeMock     = "mock"
)

// FaceFromEnv selects the face provider:
//
//	PERSON_VERIFICATION_FACE_MODE       disabled (default) | mock (refused
//	                                    in production)
//	PERSON_VERIFICATION_MIN_SIMILARITY  1-100, default 90
//
// No production provider exists yet: media-service's liveness and compare
// routes take media ids only, and the reference here is the DigiLocker
// photo, which must not become a user upload. A real provider is a new
// FaceVerifier (a media-service route that accepts a reference image, or a
// direct liveness + compare vendor) selected by a new mode value here.
func FaceFromEnv(getenv func(string) string, production bool) (FaceVerifier, float64, error) {
	min := DefaultMinSimilarity
	if raw := strings.TrimSpace(getenv(EnvMinSimilarity)); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v < 1 || v > 100 {
			return nil, 0, fmt.Errorf("%s must be a number between 1 and 100", EnvMinSimilarity)
		}
		min = v
	}
	switch mode := strings.ToLower(strings.TrimSpace(getenv(EnvFaceMode))); mode {
	case "", FaceModeDisabled:
		return nil, min, nil
	case FaceModeMock:
		if production {
			return nil, 0, fmt.Errorf("%s=mock is refused unless the environment is local, dev or development", EnvFaceMode)
		}
		return &MockFaceVerifier{Result: SelfieResult{Live: true, Similarity: 96, Provider: "mock"}}, min, nil
	default:
		return nil, 0, fmt.Errorf("unknown %s %q (want disabled or mock; no production provider is built yet)", EnvFaceMode, mode)
	}
}
