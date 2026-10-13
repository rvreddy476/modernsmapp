package service

import (
	"bytes"
	"context"
	"errors"
	"github.com/atpost/doorstep-service/internal/config"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/propii"
	"github.com/atpost/shared/ownedmedia"
	"github.com/google/uuid"
	"testing"
)

func TestBankDetailsValidation(t *testing.T) {
	if _, _, err := validateBank(validBank()); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*model.BankInput){
		"holder":     func(in *model.BankInput) { in.AccountHolder = nil },
		"number":     func(in *model.BankInput) { in.AccountNumber = nil },
		"ifsc":       func(in *model.BankInput) { in.IFSC = nil },
		"bank":       func(in *model.BankInput) { in.BankName = nil },
		"proof_type": func(in *model.BankInput) { in.ProofKind = nil },
		"proof":      func(in *model.BankInput) { in.ProofMediaID = nil },
	} {
		t.Run(name, func(t *testing.T) {
			in := validBank()
			mutate(&in)
			if _, _, err := validateBank(in); err == nil {
				t.Fatal("invalid bank accepted")
			}
		})
	}
}
func TestBankProofOwnershipAndEncryptionFailClosed(t *testing.T) {
	for _, mediaErr := range []error{errors.New("public or foreign media"), nil} {
		s := &Service{pro: ProDeps{Documents: ownedmedia.Func(func(context.Context, uuid.UUID, uuid.UUID) error { return mediaErr })}}
		if mediaErr != nil {
			crypto, err := propii.New(context.Background(), []config.PIIKey{{Version: 1, Key: bytes.Repeat([]byte{0x42}, 32)}})
			if err != nil {
				t.Fatal(err)
			}
			s.pro.PII = crypto
		}
		if _, err := s.prepareBank(context.Background(), uuid.New(), validBank()); err == nil {
			t.Fatal("unowned or unencrypted bank details accepted")
		}
	}
}
