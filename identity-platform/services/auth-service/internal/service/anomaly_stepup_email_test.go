package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
)

// failingOutbox refuses every message, like SES in sandbox for an
// unverified address.
type failingOutbox struct{ tries int }

func (f *failingOutbox) Send(_ context.Context, _ EmailMessage) error {
	f.tries++
	return errors.New("ses: MessageRejected")
}

// highRiskLogin drives createSessionForUser with a novel IP and device so
// the anomaly gate fires under enforce mode.
func highRiskLogin(t *testing.T, svc *Service, user *store.User) (*AuthResponse, error) {
	t.Helper()
	return svc.createSessionForUser(context.Background(), user, []string{AMRPassword}, "new-device-1", "android", "198.51.100.99", "ua")
}

func anomalyUser(t *testing.T, fstore *fakeAnomalyStore, verified, totp bool) *store.User {
	t.Helper()
	email := "u@example.test"
	u := &store.User{ID: uuid.New(), Email: &email, EmailVerified: verified}
	if totp {
		u.TwoFactorEnabled, u.TwoFactorMethod = true, store.TwoFactorMethodTOTP
	}
	fstore.users = map[uuid.UUID]*store.User{u.ID: u}
	return u
}

// The unusual-sign-in code is mailed to the verified address. Before the
// fix it was saved and never sent, so email-only accounts could not finish
// signing in.
func TestAnomalyStepUp_MailsTheCode(t *testing.T) {
	svc, fstore, mr := newTestService(t, "enforce")
	box := &outbox{}
	svc.WithEmailSender(box)
	user := anomalyUser(t, fstore, true, false)
	mr.Set("last_ip:"+user.ID.String(), "203.0.113.10")

	resp, err := highRiskLogin(t, svc, user)
	if !errors.Is(err, ErrAnomalyStepUpRequired) {
		t.Fatalf("want ErrAnomalyStepUpRequired, got %v", err)
	}
	if len(box.msgs) != 1 {
		t.Fatalf("want 1 email, got %d", len(box.msgs))
	}
	if box.msgs[0].To != "u@example.test" {
		t.Fatalf("email went to %q", box.msgs[0].To)
	}
	code, subject := box.lastCode(t)
	if code == "" || !strings.Contains(subject, "signing in") {
		t.Fatalf("unexpected email: subject %q", subject)
	}
	if !containsMethod(resp.StepUpMethods, StepUpMethodEmail) {
		t.Fatalf("email should stay on offer once sent: %v", resp.StepUpMethods)
	}
}

// A failed send with no other channel is refused outright rather than
// handing back a pending login nobody can finish.
func TestAnomalyStepUp_SendFailsNoOtherChannel_Unavailable(t *testing.T) {
	svc, fstore, mr := newTestService(t, "enforce")
	box := &failingOutbox{}
	svc.WithEmailSender(box)
	user := anomalyUser(t, fstore, true, false)
	mr.Set("last_ip:"+user.ID.String(), "203.0.113.10")

	resp, err := highRiskLogin(t, svc, user)
	if !errors.Is(err, ErrAnomalyStepUpUnavailable) {
		t.Fatalf("want ErrAnomalyStepUpUnavailable, got %v (resp %+v)", err, resp)
	}
	if box.tries != 1 {
		t.Fatalf("want one send attempt, got %d", box.tries)
	}
	if len(fstore.sessions) != 0 {
		t.Fatalf("no session may be minted; got %d", len(fstore.sessions))
	}
}

// A failed send with an authenticator enrolled drops email and keeps TOTP.
func TestAnomalyStepUp_SendFailsWithTOTP_OffersTOTPOnly(t *testing.T) {
	svc, fstore, mr := newTestService(t, "enforce")
	svc.WithEmailSender(&failingOutbox{})
	user := anomalyUser(t, fstore, true, true)
	mr.Set("last_ip:"+user.ID.String(), "203.0.113.10")

	// An authenticator account normally meets the 2FA gate first, so drive
	// the step-up directly.
	resp, err := svc.startAnomalyStepUp(context.Background(), user, []string{AMRPassword}, "new-device-1", "android", "198.51.100.99", "ua")
	if !errors.Is(err, ErrAnomalyStepUpRequired) {
		t.Fatalf("want ErrAnomalyStepUpRequired, got %v", err)
	}
	if len(resp.StepUpMethods) != 1 || resp.StepUpMethods[0] != StepUpMethod2FA {
		t.Fatalf("want [totp], got %v", resp.StepUpMethods)
	}
}

// No sender wired at all behaves like a failed send.
func TestAnomalyStepUp_NoSender_Unavailable(t *testing.T) {
	svc, fstore, mr := newTestService(t, "enforce")
	user := anomalyUser(t, fstore, true, false)
	mr.Set("last_ip:"+user.ID.String(), "203.0.113.10")

	if _, err := highRiskLogin(t, svc, user); !errors.Is(err, ErrAnomalyStepUpUnavailable) {
		t.Fatalf("want ErrAnomalyStepUpUnavailable, got %v", err)
	}
}
