// Registration route: the request shape, person-name and gender validation,
// and the Register handler. Pure move out of handler.go; behaviour is
// unchanged.

package http

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/atpost/identity-auth-service/internal/rollout"
	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
)

type RegisterRequest struct {
	// Phone is accepted but is no longer sufficient on its own — see the
	// EMAIL_REQUIRED check in Register. SMS delivery does not exist, so a
	// phone-only account could never be verified or recovered.
	Phone    string `json:"phone"`
	Email    string `json:"email"`
	Password string `json:"password" binding:"required"`
	// Mandatory. See validatePersonName and the checks in Register: binding
	// alone would accept a string of spaces, so both are re-checked there.
	FirstName string `json:"first_name" binding:"required"`
	LastName  string `json:"last_name" binding:"required"`
	// DOB is MANDATORY (SR-6). It was optional, and the age check returned nil
	// when it was absent, so omitting the field bypassed the gate entirely.
	DOB    string `json:"dob"`
	Gender string `json:"gender"`
	// AcceptedTerms must be explicitly true. A bool defaulting to false means a
	// client that omits it is refused — a consent that defaults to granted is
	// not consent.
	AcceptedTerms bool `json:"accepted_terms"`
	// TermsVersion records WHICH text the user was shown, so a later audit can
	// answer that question from data rather than from a deployment timeline.
	TermsVersion string `json:"terms_version"`
}

// validatePersonName enforces what a human name may contain.
//
// Unicode letters, not ASCII: the product is India-first, so Devanagari,
// Telugu and Tamil names must pass exactly as Latin ones do. Spaces, hyphens
// and apostrophes are allowed for "O'Brien", "Devi Prasad", "Anne-Marie".
//
// unicode.IsMark is required, and its absence was a real defect. IsLetter
// covers Unicode category L only; Indic vowel signs (matras), viramas and
// nuktas are category M. Without IsMark this function accepted the consonant
// stems of an Indic name and rejected its vowels — "रघुवरन" failed on U+0941,
// "ரகுவரன்" on U+0BC1 — so only mark-free names such as "कमल" got through.
// Since first and last name became mandatory, that blocked most Devanagari,
// Telugu and Tamil speakers from registering under their real name at all.
//
// Digits are rejected deliberately. A name field accepting "user123" is how
// people end up putting handles in it, and the handle is a separate,
// uniqueness-checked concept. Widening to category M does not widen to
// symbols, digits or emoji — see TestValidatePersonName.
func validatePersonName(name string) error {
	const maxLen = 50
	if utf8.RuneCountInString(name) > maxLen {
		return fmt.Errorf("name must be %d characters or fewer", maxLen)
	}
	for _, r := range name {
		switch {
		case unicode.IsLetter(r), unicode.IsMark(r), unicode.IsSpace(r), r == '-', r == '\'':
			continue
		default:
			return errors.New("name may contain letters, spaces, hyphens and apostrophes only")
		}
	}
	return nil
}

// genderValues is the closed set a registration may record.
//
// Stored tokens, deliberately lowercase and stable. Display wording lives in
// the clients; changing "Others" to "Prefer to self-describe" must never
// change what is already in the database.
var genderValues = []string{"male", "female", "other"}

func allowedGenders() []string {
	out := make([]string, len(genderValues))
	copy(out, genderValues)
	return out
}

func isAllowedGender(v string) bool {
	for _, allowed := range genderValues {
		if v == allowed {
			return true
		}
	}
	return false
}

func (h *Handler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warn("invalid request payload", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Validation failed: "+err.Error(), nil, nil)
		return
	}

	// SR-6: EMAIL is the launch identifier. Phone registration is retired
	// because no SMS delivery exists — a phone-only account could never
	// receive a verification or recovery code, so it could never be recovered.
	if strings.TrimSpace(req.Email) == "" {
		api.Error(c.Writer, http.StatusBadRequest, "EMAIL_REQUIRED",
			"An email address is required. Phone registration is not available: "+
				"SMS delivery is not enabled, so a phone-only account could never "+
				"receive a verification or password-reset code.", nil, nil)
		return
	}
	// M1/H2 (registration audit): one normal form, checked at the boundary.
	// The service re-checks (a second handler cannot skip it); this gives the
	// client a field-specific code before any other validation runs.
	if norm, err := service.NormalizeEmail(req.Email); err != nil {
		api.Error(c.Writer, http.StatusUnprocessableEntity, "INVALID_EMAIL",
			"Enter a valid email address.", nil, nil)
		return
	} else {
		req.Email = norm
	}
	if strings.TrimSpace(req.Phone) != "" {
		if _, err := service.NormalizePhoneE164(req.Phone); err != nil {
			api.Error(c.Writer, http.StatusUnprocessableEntity, "INVALID_PHONE",
				service.ErrInvalidPhone.Error(), nil, nil)
			return
		}
	}

	if req.Password == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Password cannot be empty", nil, nil)
		return
	}

	// Real first and last names are MANDATORY.
	//
	// They were previously optional, which is why accounts exist with no name
	// at all and the app falls back to showing "anonymous". Validated here
	// rather than by binding tags alone, because `binding:"required"` accepts
	// a string of spaces.
	//
	// Distinct codes per field so the client can mark the offending input
	// instead of showing one message for both.
	req.FirstName = strings.TrimSpace(req.FirstName)
	req.LastName = strings.TrimSpace(req.LastName)

	if req.FirstName == "" {
		api.Error(c.Writer, http.StatusUnprocessableEntity, "FIRST_NAME_REQUIRED",
			"First name is required.", nil, nil)
		return
	}
	if req.LastName == "" {
		api.Error(c.Writer, http.StatusUnprocessableEntity, "LAST_NAME_REQUIRED",
			"Last name is required.", nil, nil)
		return
	}
	if err := validatePersonName(req.FirstName); err != nil {
		api.Error(c.Writer, http.StatusUnprocessableEntity, "NAME_INVALID", err.Error(), nil, nil)
		return
	}
	if err := validatePersonName(req.LastName); err != nil {
		api.Error(c.Writer, http.StatusUnprocessableEntity, "NAME_INVALID", err.Error(), nil, nil)
		return
	}

	// Gender: expand/contract rollout.
	//
	// The column is `gender TEXT DEFAULT ''` with no enum and no CHECK, so
	// nothing below the API enforces anything. "Required" enforced only in one
	// app is not required at all — a second client, or a direct API call,
	// walks straight past it.
	//
	// EXPAND (flag off, the default): an absent gender is accepted, but a
	// supplied one must be valid. Clients can start sending the field before
	// anything depends on it, and existing clients that omit it keep working.
	//
	// CONTRACT (flag on): absence is rejected too.
	//
	// The split matters because promoting an optional field to required is a
	// breaking change for every client that predates it — including the
	// Flutter app, which sends no gender at all. Enforcing both halves at once
	// takes those clients down with no way back short of a redeploy.
	req.Gender = strings.ToLower(strings.TrimSpace(req.Gender))
	if req.Gender == "" {
		if h.flags.Enabled(c.Request.Context(), rollout.FlagRegisterRequireGender) {
			api.Error(c.Writer, http.StatusUnprocessableEntity, "GENDER_REQUIRED",
				"Gender is required.",
				map[string]any{"allowed": allowedGenders()}, nil)
			return
		}
	} else if !isAllowedGender(req.Gender) {
		// Enforced in BOTH phases. Accepting arbitrary text into a column that
		// will later be constrained just moves the problem to the backfill.
		api.Error(c.Writer, http.StatusUnprocessableEntity, "GENDER_INVALID",
			"Gender must be one of the supported values.",
			map[string]any{"allowed": allowedGenders()}, nil)
		return
	}

	resp, err := h.svc.RegisterWithConsent(c.Request.Context(), req.Phone, req.Email, req.Password,
		req.FirstName, req.LastName, req.DOB, req.Gender,
		service.RegistrationConsent{
			Accepted: req.AcceptedTerms,
			Version:  req.TermsVersion,
		})
	if err != nil {
		h.log.Error("registration failed", "err", err, "phone", maskPhone(req.Phone), "email", maskEmail(req.Email), "request_id", RequestIDFromContext(c))
		switch {
		case errors.Is(err, store.ErrUserExists):
			api.Error(c.Writer, http.StatusConflict, "USER_EXISTS", "User already exists", nil, nil)
		case errors.Is(err, service.ErrInvalidEmail):
			api.Error(c.Writer, http.StatusUnprocessableEntity, "INVALID_EMAIL", "Enter a valid email address.", nil, nil)
		case errors.Is(err, service.ErrInvalidPhone):
			api.Error(c.Writer, http.StatusUnprocessableEntity, "INVALID_PHONE", err.Error(), nil, nil)
		case errors.Is(err, service.ErrPasswordTooShort) || errors.Is(err, service.ErrPasswordTooWeak):
			api.Error(c.Writer, http.StatusUnprocessableEntity, "WEAK_PASSWORD", err.Error(), nil, nil)
		// SR-6: the age and consent failures carry their own codes and the
		// real reason. A generic "Registration failed" would leave a user
		// retrying a form that can never succeed.
		case errors.Is(err, service.ErrUnderage):
			api.Error(c.Writer, http.StatusUnprocessableEntity, "UNDERAGE", err.Error(), nil, nil)
		case errors.Is(err, service.ErrDOBRequired) || errors.Is(err, service.ErrDOBMalformed) ||
			errors.Is(err, service.ErrDOBInFuture):
			api.Error(c.Writer, http.StatusUnprocessableEntity, "INVALID_DOB", err.Error(), nil, nil)
		case errors.Is(err, service.ErrConsentRequired) || errors.Is(err, service.ErrConsentVersionMismatch):
			api.Error(c.Writer, http.StatusUnprocessableEntity, "CONSENT_REQUIRED", err.Error(),
				map[string]any{"current_terms_version": service.CurrentTermsVersion}, nil)
		default:
			api.Error(c.Writer, http.StatusBadRequest, "REGISTRATION_FAILED", "Registration failed", nil, nil)
		}
		return
	}

	// LB-5: registration issues NO session, so there are no cookies to set.
	// The account is pending until the emailed code is verified — calling
	// setAuthCookies here would write empty credentials and, worse, imply the
	// client is signed in.
	api.JSON(c.Writer, http.StatusCreated, resp, nil)
}
