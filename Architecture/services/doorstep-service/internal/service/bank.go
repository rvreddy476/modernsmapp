package service

import (
	"context"
	"github.com/atpost/doorstep-service/internal/apperr"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/shared/kyc"
	"github.com/google/uuid"
	"strings"
)

func validateBank(in model.BankInput) (store.BankSubmission, string, error) {
	out := store.BankSubmission{}
	if in.AccountHolder == nil || !holderRe.MatchString(strings.TrimSpace(*in.AccountHolder)) {
		return out, "", apperr.Invalid("account_holder", "Enter the name shown on your bank account.")
	}
	out.Holder = strings.Join(strings.Fields(*in.AccountHolder), " ")
	if in.AccountNumber == nil {
		return out, "", apperr.Invalid("account_number", "Enter your account number.")
	}
	number, err := kyc.NormalizeBankAccountNumber(*in.AccountNumber)
	if err != nil {
		return out, "", apperr.Invalid("account_number", "Enter a 9–18 digit account number.")
	}
	if in.IFSC == nil {
		return out, "", apperr.Invalid("ifsc", "Enter your IFSC.")
	}
	out.IFSC, err = kyc.NormalizeIFSC(*in.IFSC)
	if err != nil {
		return out, "", apperr.Invalid("ifsc", "Enter a valid 11-character IFSC.")
	}
	if in.BankName == nil {
		return out, "", apperr.Invalid("bank_name", "Enter your bank name.")
	}
	var aerr *apperr.Error
	out.BankName, aerr = cleanText("bank_name", *in.BankName, 2, 100)
	if aerr != nil {
		return out, "", aerr
	}
	if in.BranchName != nil && strings.TrimSpace(*in.BranchName) != "" {
		out.BranchName, aerr = cleanText("branch_name", *in.BranchName, 2, 100)
		if aerr != nil {
			return out, "", aerr
		}
	}
	if in.ProofKind == nil || (*in.ProofKind != "cancelled_cheque" && *in.ProofKind != "bank_statement" && *in.ProofKind != "passbook") {
		return out, "", apperr.Invalid("proof_kind", "Choose a cancelled cheque, bank statement or passbook.")
	}
	out.ProofKind = *in.ProofKind
	if in.ProofMediaID == nil {
		return out, "", apperr.Invalid("proof_media_id", "Upload your bank proof.")
	}
	out.ProofMediaID, err = uuid.Parse(*in.ProofMediaID)
	if err != nil || out.ProofMediaID == uuid.Nil {
		return out, "", apperr.Invalid("proof_media_id", "Upload your bank proof.")
	}
	out.Last4 = kyc.BankAccountLast4(number)
	return out, number, nil
}

func (s *Service) prepareBank(ctx context.Context, uid uuid.UUID, in model.BankInput) (store.BankSubmission, error) {
	out, number, err := validateBank(in)
	if err != nil {
		return out, err
	}
	if s.pro.Documents == nil {
		return out, apperr.New(503, "DOCUMENT_UNAVAILABLE", "Document verification is unavailable. Please retry.")
	}
	if err = s.pro.Documents.VerifyOwnedImage(ctx, uid, out.ProofMediaID); err != nil {
		return out, apperr.New(422, "DOCUMENT_NOT_READY", "Upload a private, processed bank proof image and retry.")
	}
	sealed, err := s.pro.PII.SealAccountNumber(ctx, number)
	if err != nil {
		return out, piiUnavailable()
	}
	out.Sealed, out.KeyVersion = sealed.Blob, sealed.KeyVersion
	return out, nil
}
