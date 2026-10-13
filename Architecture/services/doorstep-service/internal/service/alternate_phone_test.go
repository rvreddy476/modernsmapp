package service

import (
	"context"
	"github.com/google/uuid"
	"testing"
)

type alternateTestStore struct {
	ProStore
	called bool
}

func (s *alternateTestStore) RequestAlternatePhone(context.Context, uuid.UUID, string) (string, error) {
	s.called = true
	return "1234", nil
}
func (s *alternateTestStore) VerifyAlternatePhone(context.Context, uuid.UUID, string) (bool, error) {
	s.called = true
	return true, nil
}
func TestAlternatePhoneDisabledAndValidation(t *testing.T) {
	st := &alternateTestStore{}
	s := &Service{pro: ProDeps{Store: st}}
	if _, err := s.RequestAlternatePhone(context.Background(), uuid.New()); err == nil || st.called {
		t.Fatal("disabled transport accepted request")
	}
	if _, err := s.VerifyAlternatePhone(context.Background(), uuid.New(), AlternatePhoneCode{Code: "123456"}); err == nil || st.called {
		t.Fatal("disabled transport accepted verification")
	}
	s.pro.DevPhoneOTP = true
	if _, err := s.VerifyAlternatePhone(context.Background(), uuid.New(), AlternatePhoneCode{Code: "1"}); err == nil || st.called {
		t.Fatal("invalid code reached store")
	}
}
