package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"log/slog"
	"math/big"
	"regexp"
)

type alternatePhoneStore interface {
	RequestAlternatePhone(context.Context, uuid.UUID, string) (string, error)
	VerifyAlternatePhone(context.Context, uuid.UUID, string) (bool, error)
}
type AlternatePhoneChallenge struct {
	Delivery    string `json:"delivery"`
	ExpiresIn   int    `json:"expires_in"`
	ResendAfter int    `json:"resend_after"`
}
type AlternatePhoneCode struct {
	Code string `json:"code"`
}
type AlternatePhoneResult struct {
	Verified bool `json:"verified"`
}

var alternateCodePattern = regexp.MustCompile(`^[0-9]{6}$`)

func (s *Service) RequestAlternatePhone(ctx context.Context, uid uuid.UUID) (*AlternatePhoneChallenge, error) {
	st, ok := s.pro.Store.(alternatePhoneStore)
	if !s.pro.DevPhoneOTP || !ok {
		return nil, apperr.New(503, "SMS_NOT_AVAILABLE", "Contact verification is not available yet.")
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return nil, err
	}
	code := fmt.Sprintf("%06d", n.Int64())
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	last, err := st.RequestAlternatePhone(ctx, uid, string(hash))
	if errors.Is(err, store.ErrConflict) {
		return nil, apperr.New(429, "RATE_LIMITED", "Please wait before requesting another code. Up to five requests every 15 minutes.")
	}
	if errors.Is(err, store.ErrInvalid) || errors.Is(err, store.ErrNotFound) {
		return nil, apperr.Invalid("alternate_phone", "Add an alternate contact in your application; a verified contact does not need another code.")
	}
	if err != nil {
		return nil, internal(ctx, "alternate phone request", err)
	}
	slog.Warn("ALTERNATE CONTACT OTP — DEVELOPMENT ONLY — SMS NOT SENT", "user_id", uid, "phone_last4", last, "code", code)
	return &AlternatePhoneChallenge{Delivery: "development_log", ExpiresIn: 300, ResendAfter: 60}, nil
}

func (s *Service) VerifyAlternatePhone(ctx context.Context, uid uuid.UUID, in AlternatePhoneCode) (*AlternatePhoneResult, error) {
	st, ok := s.pro.Store.(alternatePhoneStore)
	if !s.pro.DevPhoneOTP || !ok {
		return nil, apperr.New(503, "SMS_NOT_AVAILABLE", "Contact verification is not available yet.")
	}
	if !alternateCodePattern.MatchString(in.Code) {
		return nil, apperr.Invalid("code", "Enter the six-digit code.")
	}
	verified, err := st.VerifyAlternatePhone(ctx, uid, in.Code)
	if err != nil {
		return nil, proAdminErr(ctx, "verify alternate contact", err)
	}
	if !verified {
		return nil, apperr.New(422, "OTP_INVALID", "The code is incorrect or expired. Request another code if needed.")
	}
	return &AlternatePhoneResult{Verified: true}, nil
}
