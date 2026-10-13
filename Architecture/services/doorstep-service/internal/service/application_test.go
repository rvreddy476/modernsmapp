package service

import (
	"bytes"
	"context"
	"errors"
	"github.com/atpost/doorstep-service/internal/config"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/prokyc"
	"github.com/atpost/doorstep-service/internal/propii"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/shared/ownedmedia"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"testing"
)

func validApplication() model.PartnerApplicationInput {
	return model.PartnerApplicationInput{PAN: "ABCPE1234F", Bank: validBank(), ReviewConsent: true, FirstName: "Test", LastName: "Partner", Gender: "others", StateCode: "36", Town: "Warangal", SkillCodes: []string{"deep_cleaning"}, IdentityKind: "passport", IdentityMediaID: uuid.NewString(), SelfieMediaID: uuid.NewString()}
}
func validBank() model.BankInput {
	p := func(s string) *string { return &s }
	return model.BankInput{AccountHolder: p("Test Partner"), AccountNumber: p("000123456789"), IFSC: p("SBIN0001234"), BankName: p("State Bank"), ProofKind: p("passbook"), ProofMediaID: p(uuid.NewString())}
}

type applicationTestStore struct {
	ProStore
	saved bool
}

func (s *applicationTestStore) SkillRules(context.Context, []string) (map[string]*store.SkillRule, error) {
	return map[string]*store.SkillRule{"deep_cleaning": {}}, nil
}
func (s *applicationTestStore) SubmitPartnerApplication(_ context.Context, in store.ApplicationSubmission) (*model.Professional, error) {
	if in.Input.PAN != "" || in.Input.Bank.AccountNumber != nil || len(in.Bank.Sealed) == 0 || len(in.PANSealed) == 0 {
		return nil, errors.New("unsealed application")
	}
	s.saved = true
	return &model.Professional{Status: "pending_verification"}, nil
}
func TestApplicationValidation(t *testing.T) {
	for name, mutate := range map[string]func(*model.PartnerApplicationInput){"state": func(i *model.PartnerApplicationInput) { i.StateCode = "XX" }, "town": func(i *model.PartnerApplicationInput) { i.Town = "" }, "gender": func(i *model.PartnerApplicationInput) { i.Gender = "" }, "skills": func(i *model.PartnerApplicationInput) { i.SkillCodes = nil }, "proof": func(i *model.PartnerApplicationInput) { i.IdentityKind = "aadhaar" }, "phone": func(i *model.PartnerApplicationInput) { i.AlternatePhone = "12345" }, "media": func(i *model.PartnerApplicationInput) { i.IdentityMediaID = "" }} {
		t.Run(name, func(t *testing.T) {
			in := validApplication()
			mutate(&in)
			if _, err := validateApplication(&in); err == nil {
				t.Fatal("invalid application accepted")
			}
		})
	}
	if _, err := validateApplication(ptrApplication(validApplication())); err != nil {
		t.Fatal(err)
	}
}
func ptrApplication(v model.PartnerApplicationInput) *model.PartnerApplicationInput { return &v }
func TestApplicationSelfieRequiredIntroductionOptional(t *testing.T) {
	in := validApplication()
	in.SelfieMediaID = ""
	if _, err := validateApplication(&in); err == nil {
		t.Fatal("missing selfie accepted")
	}
	in = validApplication()
	if _, err := validateApplication(&in); err != nil {
		t.Fatal("optional video required", err)
	}
	in.IntroductionMediaID = "not-a-media-id"
	if _, err := validateApplication(&in); err == nil {
		t.Fatal("invalid introduction accepted")
	}
}

func TestApplicationChecksSelfieOwnerBeforeSaving(t *testing.T) {
	in := validApplication()
	st := &applicationTestStore{}
	crypto, err := propii.New(context.Background(), []config.PIIKey{{Version: 1, Key: bytes.Repeat([]byte{0x42}, 32)}})
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{pro: ProDeps{Store: st, PII: crypto, VerifiedPhone: func(context.Context, uuid.UUID) (string, error) { return "+919876543210", nil }, Documents: ownedmedia.Func(func(_ context.Context, _ uuid.UUID, id uuid.UUID) error {
		if id.String() == in.SelfieMediaID {
			return ownedmedia.ErrInvalid
		}
		return nil
	})}}
	if _, err = s.SubmitPartnerApplication(context.Background(), uuid.New(), in); err == nil || st.saved {
		t.Fatal("foreign selfie saved")
	}
}
func TestApplicationRequiresVerifiedPhoneAndPrivateDocument(t *testing.T) {
	for _, tc := range []struct {
		name, phone string
		mediaErr    error
		want        bool
	}{{"unverified", "", nil, false}, {"foreign or public media", "+919876543210", errors.New("not owned"), false}, {"valid", "+919876543210", nil, true}} {
		t.Run(tc.name, func(t *testing.T) {
			st := &applicationTestStore{}
			crypto, cryptoErr := propii.New(context.Background(), []config.PIIKey{{Version: 1, Key: bytes.Repeat([]byte{0x42}, 32)}})
			if cryptoErr != nil {
				t.Fatal(cryptoErr)
			}
			s := &Service{pro: ProDeps{Store: st, VerifiedPhone: func(context.Context, uuid.UUID) (string, error) { return tc.phone, nil }, Documents: ownedmedia.Func(func(context.Context, uuid.UUID, uuid.UUID) error { return tc.mediaErr })}}
			s.pro.PII = crypto
			out, err := s.SubmitPartnerApplication(context.Background(), uuid.New(), validApplication())
			if st.saved != tc.want || (err == nil) != tc.want {
				t.Fatalf("saved=%v err=%v", st.saved, err)
			}
			if tc.want && out.Status != "pending_verification" {
				t.Fatal("application self-approved")
			}
		})
	}
}
func TestVerifiedPhoneClientRejectsUnverifiedAndWrongUser(t *testing.T) {
	id := uuid.New()
	for _, body := range []string{`{"data":{"user_id":"` + id.String() + `","phone":"+919876543210","phone_verified":false}}`, `{"data":{"user_id":"` + uuid.NewString() + `","phone":"+919876543210","phone_verified":true}}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		_, err := VerifiedPhoneClient(server.URL, "key")(context.Background(), id)
		server.Close()
		if err == nil {
			t.Fatal("untrusted contact accepted")
		}
	}
}
func TestApplicationApprovalDoesNotEnableDuty(t *testing.T) {
	st := &store.ProState{Status: "approved", Facts: prokyc.Facts{SimpleApplication: true, SelfieMatched: true, HasPAN: true, HasPayoutAccount: true, IdentityReviewed: true, IdentityDocumentApproved: true, VerifiedSkills: 1}}
	if len(prokyc.MissingSteps(st.Facts)) != 0 {
		t.Fatal("identity approval blocked by work setup")
	}
	if readinessOf(st).CanGoOnDuty {
		t.Fatal("approved identity enabled duty without operational checks")
	}
	if err := approvalCheck(st, []prokyc.SkillCategory{{Skill: "salon", Family: prokyc.FamilySalon, GenderRule: prokyc.GenderFemaleOnly}}); err == nil {
		t.Fatal("salon restrictions bypassed")
	}
}
