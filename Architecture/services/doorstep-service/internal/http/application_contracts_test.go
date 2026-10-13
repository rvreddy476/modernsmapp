package http

import (
	"context"
	"testing"

	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/service"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
)

type applicationContractStore struct{ *fakeProStore }

func (s *applicationContractStore) RequestAlternatePhone(context.Context, uuid.UUID, string) (string, error) {
	return "3211", nil
}
func (s *applicationContractStore) VerifyAlternatePhone(context.Context, uuid.UUID, string) (bool, error) {
	return true, nil
}

func TestContract_AlternatePhone(t *testing.T) {
	pr := newProRig(t, func(d *service.ProDeps) {
		d.Store = &applicationContractStore{d.Store.(*fakeProStore)}
		d.DevPhoneOTP = true
	})
	assertFixture(t, "pro_alternate_phone_request_200.json", pr.call(proUser, "POST", "/me/alternate-phone/request", "{}"), 200)
	assertFixture(t, "pro_alternate_phone_verify_200.json", pr.call(proUser, "POST", "/me/alternate-phone/verify", `{"code":"123456"}`), 200)
}

func (s *applicationContractStore) SubmitPartnerApplication(ctx context.Context, in store.ApplicationSubmission) (*model.Professional, error) {
	p, err := s.CreateProfessional(ctx, store.NewProfessional{UserID: in.UserID, DisplayName: in.Input.FirstName + " " + in.Input.LastName, CityCode: "HYD", Skills: in.Skills})
	if err != nil {
		return nil, err
	}
	p.Status = "pending_verification"
	p.Application = &model.PartnerApplication{FirstName: in.Input.FirstName, LastName: in.Input.LastName, DeclaredGender: in.Input.Gender, StateCode: in.Input.StateCode, StateName: in.StateName, Town: in.Input.Town, PrimaryPhoneLast4: in.PhoneLast4, AlternatePhoneLast4: in.AlternateLast4, SubmittedAt: fixtureTS}
	return p, nil
}

func TestContract_PartnerApplication(t *testing.T) {
	pr := newProRig(t, func(d *service.ProDeps) {
		d.Store = &applicationContractStore{d.Store.(*fakeProStore)}
		d.VerifiedPhone = func(context.Context, uuid.UUID) (string, error) { return "+919876543210", nil }
	})
	assertFixture(t, "pro_application_states_200.json", pr.call(proUser, "GET", "/application/states", ""), 200)
	body := jsonStr(map[string]any{"review_consent": true, "first_name": "Test", "last_name": "Partner", "gender": "others", "state_code": "36", "town": "Warangal", "skill_codes": []string{"deep_cleaning"}, "identity_kind": "passport", "identity_media_id": mediaOf(proUser, "selfie").String(), "selfie_media_id": mediaOf(proUser, "selfie").String(), "pan": "ABCPE1234F", "bank": map[string]string{"account_holder": "Test Partner", "account_number": "000123456789", "ifsc": "SBIN0001234", "bank_name": "State Bank", "proof_kind": "passbook", "proof_media_id": mediaOf(proUser, "photo").String()}})
	assertFixture(t, "pro_application_post_201.json", pr.call(proUser, "POST", "/application", body), 201)
}
