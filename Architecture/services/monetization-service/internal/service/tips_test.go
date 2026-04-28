package service

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestValidateTipInput — money-handling validation is the single most
// important property here. Re-asserting it as a table so a future
// well-meaning refactor (e.g. "let's let creators tip themselves to
// test") can't accidentally relax the gate.
func TestValidateTipInput(t *testing.T) {
	sender := uuid.New()
	recipient := uuid.New()

	cases := []struct {
		name    string
		input   SendTipInput
		wantErr string // substring match on error
	}{
		{
			"happy path",
			SendTipInput{SenderID: sender, RecipientID: recipient, AmountPaise: 5000, Message: "great vid"},
			"",
		},
		{
			"missing sender",
			SendTipInput{RecipientID: recipient, AmountPaise: 5000},
			"INVALID_SENDER",
		},
		{
			"missing recipient",
			SendTipInput{SenderID: sender, AmountPaise: 5000},
			"INVALID_RECIPIENT",
		},
		{
			"self-tip rejected",
			SendTipInput{SenderID: sender, RecipientID: sender, AmountPaise: 5000},
			"CANNOT_TIP_SELF",
		},
		{
			"below minimum",
			SendTipInput{SenderID: sender, RecipientID: recipient, AmountPaise: 50},
			"AMOUNT_TOO_SMALL",
		},
		{
			"zero amount",
			SendTipInput{SenderID: sender, RecipientID: recipient, AmountPaise: 0},
			"AMOUNT_TOO_SMALL",
		},
		{
			"above maximum",
			SendTipInput{SenderID: sender, RecipientID: recipient, AmountPaise: 600_000},
			"AMOUNT_TOO_LARGE",
		},
		{
			"exactly at maximum",
			SendTipInput{SenderID: sender, RecipientID: recipient, AmountPaise: 500_000},
			"",
		},
		{
			"exactly at minimum",
			SendTipInput{SenderID: sender, RecipientID: recipient, AmountPaise: 100},
			"",
		},
		{
			"message at limit",
			SendTipInput{SenderID: sender, RecipientID: recipient, AmountPaise: 5000, Message: strings.Repeat("a", 250)},
			"",
		},
		{
			"message over limit",
			SendTipInput{SenderID: sender, RecipientID: recipient, AmountPaise: 5000, Message: strings.Repeat("a", 251)},
			"MESSAGE_TOO_LONG",
		},
		{
			"both post_id and stream_id rejected",
			func() SendTipInput {
				p := uuid.New()
				st := uuid.New()
				return SendTipInput{SenderID: sender, RecipientID: recipient, AmountPaise: 5000, PostID: &p, StreamID: &st}
			}(),
			"INVALID_TARGET",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.input // copy so trim doesn't leak
			err := ValidateTipInput(&in)
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Errorf("expected error containing %q, got nil", tc.wantErr)
				return
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected error containing %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

// TestValidateTipInputTrimsMessage — leading/trailing whitespace
// shouldn't blow the 250-char cap or persist as junk in the DB.
func TestValidateTipInputTrimsMessage(t *testing.T) {
	in := SendTipInput{
		SenderID:    uuid.New(),
		RecipientID: uuid.New(),
		AmountPaise: 1000,
		Message:     "   thanks for the stream   ",
	}
	if err := ValidateTipInput(&in); err != nil {
		t.Fatalf("validation failed: %v", err)
	}
	if in.Message != "thanks for the stream" {
		t.Errorf("expected trimmed message, got %q", in.Message)
	}
}
