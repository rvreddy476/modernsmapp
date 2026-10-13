package service

import (
	"context"
	"encoding/json"
	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/shared/kyc"
	"github.com/atpost/shared/ownedmedia"
	"github.com/google/uuid"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type ApplicationState struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// GST state codes match the service-city catalogue. Applications do not require
// a launch city: this is a declared working location, not a serviceability check.
var ApplicationStates = []ApplicationState{
	{"01", "Jammu and Kashmir"}, {"02", "Himachal Pradesh"}, {"03", "Punjab"}, {"04", "Chandigarh"}, {"05", "Uttarakhand"}, {"06", "Haryana"}, {"07", "Delhi"}, {"08", "Rajasthan"}, {"09", "Uttar Pradesh"}, {"10", "Bihar"}, {"11", "Sikkim"}, {"12", "Arunachal Pradesh"}, {"13", "Nagaland"}, {"14", "Manipur"}, {"15", "Mizoram"}, {"16", "Tripura"}, {"17", "Meghalaya"}, {"18", "Assam"}, {"19", "West Bengal"}, {"20", "Jharkhand"}, {"21", "Odisha"}, {"22", "Chhattisgarh"}, {"23", "Madhya Pradesh"}, {"24", "Gujarat"}, {"26", "Dadra and Nagar Haveli and Daman and Diu"}, {"27", "Maharashtra"}, {"29", "Karnataka"}, {"30", "Goa"}, {"31", "Lakshadweep"}, {"32", "Kerala"}, {"33", "Tamil Nadu"}, {"34", "Puducherry"}, {"35", "Andaman and Nicobar Islands"}, {"36", "Telangana"}, {"37", "Andhra Pradesh"}, {"38", "Ladakh"},
}

type applicationStore interface {
	SubmitPartnerApplication(context.Context, store.ApplicationSubmission) (*model.Professional, error)
}

var applicationPhone = regexp.MustCompile(`^\+91[6-9][0-9]{9}$`)

func validateApplication(in *model.PartnerApplicationInput) (string, error) {
	if !in.ReviewConsent {
		return "", apperr.Invalid("review_consent", "confirm that these are your documents and consent to manual verification")
	}
	pan, panErr := kyc.ValidatePAN(in.PAN)
	if panErr != nil || pan.HolderType != kyc.PANHolderType('P') {
		return "", apperr.Invalid("pan", "Enter your individual PAN number.")
	}
	in.PAN = pan.Normalized
	if _, _, err := validateBank(in.Bank); err != nil {
		return "", err
	}
	var err *apperr.Error
	if in.FirstName, err = cleanText("first_name", in.FirstName, 1, 50); err != nil {
		return "", err
	}
	if in.LastName, err = cleanText("last_name", in.LastName, 1, 50); err != nil {
		return "", err
	}
	if in.Gender != "male" && in.Gender != "female" && in.Gender != "others" {
		return "", apperr.Invalid("gender", "choose Male, Female or Other")
	}
	if in.Town, err = cleanText("town", in.Town, 2, 100); err != nil {
		return "", err
	}
	state := ""
	for _, v := range ApplicationStates {
		if v.Code == in.StateCode {
			state = v.Name
		}
	}
	if state == "" {
		return "", apperr.Invalid("state_code", "choose an Indian state or union territory")
	}
	if len(in.SkillCodes) == 0 || len(in.SkillCodes) > 20 {
		return "", apperr.Invalid("skill_codes", "choose between 1 and 20 services")
	}
	seen := map[string]bool{}
	for _, v := range in.SkillCodes {
		if !skillCodeRe.MatchString(v) || seen[v] {
			return "", apperr.Invalid("skill_codes", "choose distinct services from the list")
		}
		seen[v] = true
	}
	if in.IdentityKind != "passport" && in.IdentityKind != "voter_id" && in.IdentityKind != "driving_license" {
		return "", apperr.Invalid("identity_kind", "choose Passport, Voter ID or Driving Licence")
	}
	for field, raw := range map[string]string{"identity_media_id": in.IdentityMediaID, "selfie_media_id": in.SelfieMediaID, "experience_media_id": in.ExperienceMediaID, "introduction_media_id": in.IntroductionMediaID} {
		if (field == "experience_media_id" || field == "introduction_media_id") && raw == "" {
			continue
		}
		id, e := uuid.Parse(raw)
		if e != nil || id == uuid.Nil {
			return "", apperr.Invalid(field, "upload a document image")
		}
	}
	in.AlternatePhone = strings.TrimSpace(in.AlternatePhone)
	if len(in.AlternatePhone) == 10 {
		in.AlternatePhone = "+91" + in.AlternatePhone
	}
	if in.AlternatePhone != "" && !applicationPhone.MatchString(in.AlternatePhone) {
		return "", apperr.Invalid("alternate_phone", "enter a valid Indian mobile number")
	}
	return state, nil
}

func (s *Service) SubmitPartnerApplication(ctx context.Context, uid uuid.UUID, in model.PartnerApplicationInput) (*model.Professional, error) {
	state, err := validateApplication(&in)
	if err != nil {
		return nil, err
	}
	st, ok := s.pro.Store.(applicationStore)
	if !ok || s.pro.VerifiedPhone == nil || s.pro.Documents == nil {
		return nil, apperr.New(503, "APPLICATION_UNAVAILABLE", "Applications are temporarily unavailable. Please try again.")
	}
	phone, err := s.pro.VerifiedPhone(ctx, uid)
	if err != nil {
		return nil, err
	}
	if !applicationPhone.MatchString(phone) {
		return nil, apperr.New(403, "VERIFIED_MOBILE_REQUIRED", "Please verify your mobile number through partner sign-in.")
	}
	if in.AlternatePhone == phone {
		return nil, apperr.Invalid("alternate_phone", "use a different number, or leave this blank")
	}
	skills, err := s.declarations(ctx, in.SkillCodes, "")
	if err != nil {
		return nil, err
	}
	for _, raw := range []string{in.IdentityMediaID, in.SelfieMediaID, in.ExperienceMediaID} {
		if raw == "" {
			continue
		}
		if err = s.pro.Documents.VerifyOwnedImage(ctx, uid, uuid.MustParse(raw)); err != nil {
			return nil, apperr.New(422, "DOCUMENT_NOT_READY", "Your document must be a private, processed image you uploaded. Wait a moment and retry without uploading again.")
		}
	}
	sub := store.ApplicationSubmission{UserID: uid, Input: in, StateName: state, GSTStateCode: in.StateCode, PhoneLast4: phone[len(phone)-4:], Skills: skills}
	if in.IntroductionMediaID != "" {
		if err = ownedmedia.VerifyIntroduction(ctx, s.pro.Documents, uid, uuid.MustParse(in.IntroductionMediaID)); err != nil {
			return nil, apperr.Invalid("introduction_media_id", "Upload your own processed video, up to 60 seconds and 64 MB, or remove it to continue.")
		}
	}
	sub.Bank, err = s.prepareBank(ctx, uid, in.Bank)
	if err != nil {
		return nil, err
	}
	panSealed, err := s.pro.PII.SealPAN(ctx, in.PAN)
	if err != nil {
		return nil, piiUnavailable()
	}
	sub.PANSealed, sub.PANLast4 = panSealed.Blob, in.PAN[len(in.PAN)-4:]
	// The store receives sealed values only, not plaintext payout/PAN fields.
	sub.Input.PAN = ""
	sub.Input.Bank = model.BankInput{}
	if in.AlternatePhone != "" {
		sealed, e := s.pro.PII.SealTrustedContact(ctx, uid.String()+":"+in.AlternatePhone)
		if e != nil {
			return nil, piiUnavailable()
		}
		sub.AlternateSealed = sealed.Blob
		last := in.AlternatePhone[len(in.AlternatePhone)-4:]
		sub.AlternateLast4 = &last
	}
	out, err := st.SubmitPartnerApplication(ctx, sub)
	if err != nil {
		return nil, proAdminErr(ctx, "submit partner application", err)
	}
	return out, nil
}

// Resolve the authenticated user's verified contact on the server. Never trust
// browser-supplied phone_verified, phone, identity status or reviewed gender.
func VerifiedPhoneClient(base, key string) func(context.Context, uuid.UUID) (string, error) {
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(ctx context.Context, id uuid.UUID) (string, error) {
		unavailable := apperr.New(503, "CONTACT_UNAVAILABLE", "We could not confirm your mobile number. Please try again.")
		if base == "" || key == "" || id == uuid.Nil {
			return "", unavailable
		}
		req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(base, "/")+"/v1/auth/internal/users/"+id.String(), nil)
		if err != nil {
			return "", unavailable
		}
		req.Header.Set("X-Internal-Service-Key", key)
		resp, err := client.Do(req)
		if err != nil {
			return "", unavailable
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return "", unavailable
		}
		var out struct {
			Data struct {
				ID       uuid.UUID `json:"user_id"`
				Phone    string    `json:"phone"`
				Verified bool      `json:"phone_verified"`
			} `json:"data"`
		}
		if err = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&out); err != nil || out.Data.ID != id {
			return "", unavailable
		}
		if !out.Data.Verified || !applicationPhone.MatchString(out.Data.Phone) {
			return "", apperr.New(403, "VERIFIED_MOBILE_REQUIRED", "Verify your mobile number through partner sign-in before applying.")
		}
		return out.Data.Phone, nil
	}
}
