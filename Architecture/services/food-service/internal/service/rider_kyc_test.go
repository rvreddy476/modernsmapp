package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/digilocker"
	"github.com/atpost/food-service/internal/foodpii"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/kyc"
	"github.com/google/uuid"
)

// Wave 1 B4 service rules, against a fake store and a scripted provider.
// Synthetic identifiers only.

var kycPartner = uuid.MustParse("7d0c1e2a-4b5f-4a6e-9c1d-0000000000b4")

type kycFakeStore struct {
	Store
	createdHash    string
	createdSealed  []byte
	createdVersion uint32
	consumeCalls   int
	consumeErr     error
	recorded       *postgres.DigiLockerVerification
}

func (f *kycFakeStore) CreateDigiLockerAuthState(_ context.Context, _ uuid.UUID, hash string, sealed []byte, version uint32, _ time.Time) (uuid.UUID, error) {
	f.createdHash, f.createdSealed, f.createdVersion = hash, sealed, version
	return kycPartner, nil
}

func (f *kycFakeStore) ConsumeDigiLockerAuthState(_ context.Context, _ uuid.UUID, hash string) (*postgres.DigiLockerAuthState, error) {
	f.consumeCalls++
	if f.consumeErr != nil {
		return nil, f.consumeErr
	}
	if hash != f.createdHash {
		return nil, postgres.ErrDigiLockerStateNotFound
	}
	return &postgres.DigiLockerAuthState{PartnerID: kycPartner, VerifierSealed: f.createdSealed}, nil
}

func (f *kycFakeStore) RecordDigiLockerVerification(_ context.Context, _ uuid.UUID, v postgres.DigiLockerVerification) error {
	f.recorded = &v
	return nil
}

func (f *kycFakeStore) DeliveryPartnerKYCForUser(context.Context, uuid.UUID) (*postgres.DeliveryPartnerKYC, error) {
	return &postgres.DeliveryPartnerKYC{PartnerID: kycPartner, Missing: []string{}}, nil
}

// scriptedClient returns fixed documents and remembers the verifier it got.
type scriptedClient struct {
	docs        []digilocker.IssuedDocument
	gotVerifier string
}

func (c *scriptedClient) ExchangeCode(_ context.Context, _, verifier string) (*digilocker.Session, error) {
	c.gotVerifier = verifier
	return &digilocker.Session{}, nil
}

func (c *scriptedClient) IssuedDocuments(context.Context, *digilocker.Session) ([]digilocker.IssuedDocument, error) {
	return c.docs, nil
}

func kycCrypto(t *testing.T) *foodpii.Crypto {
	t.Helper()
	c, err := foodpii.New(context.Background(), []foodpii.VersionedKey{{Version: 1, Key: bytes.Repeat([]byte{9}, 32)}}, []byte("service-kyc-unit-test-salt-00001"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func kycSynthAadhaar(t *testing.T) string {
	t.Helper()
	for c := '0'; c <= '9'; c++ {
		if n := "34567890123" + string(c); kyc.LooksLikeAadhaar(n) {
			return n
		}
	}
	t.Fatal("no synthetic Aadhaar-shaped number")
	return ""
}

func validUntil(y int) *time.Time {
	t := time.Date(y, time.June, 30, 0, 0, 0, 0, time.UTC)
	return &t
}

func issuedSet() []digilocker.IssuedDocument {
	return []digilocker.IssuedDocument{
		{Kind: digilocker.KindAadhaar, Reference: "ref-aadhaar-abc", DocType: "ADHAR", NameOnDocument: "Test Rider"},
		{Kind: digilocker.KindDrivingLicence, Reference: "ref-dl-abc", DocType: "DRVLC", Number: "KA0120200000001", NameOnDocument: "Test Rider", ValidUntil: validUntil(2040)},
		{Kind: digilocker.KindVehicleRC, Reference: "ref-rc-abc", DocType: "RVCER", Number: "KA01AB1234", NameOnDocument: "Test Rider", ValidUntil: validUntil(2038)},
	}
}

func httpSettings(c digilocker.Client) digilocker.Settings {
	return digilocker.Settings{Mode: digilocker.ModeHTTP, Client: c, AuthorizeURL: "https://digilocker.example.test/authorize",
		ClientID: "food-client", PublicBaseURL: "https://api.example.test", AppLinkURL: "https://rider.example.test/kyc"}
}

func TestStartDigiLockerStoresOnlyAHashAndASealedVerifier(t *testing.T) {
	st := &kycFakeStore{}
	crypto := kycCrypto(t)
	svc := New(st).WithPII(crypto).WithDigiLocker(httpSettings(&scriptedClient{}))
	out, err := svc.StartDigiLocker(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if !digilocker.ValidState(out.State) || st.createdHash != digilocker.HashState(out.State) || strings.Contains(st.createdHash, out.State) {
		t.Fatal("the store did not receive only the state's hash")
	}
	verifier, err := crypto.OpenCodeVerifier(context.Background(), st.createdSealed)
	if err != nil || st.createdVersion != 1 || bytes.Contains(st.createdSealed, []byte(verifier)) {
		t.Fatalf("verifier is not sealed: %v", err)
	}
	u, err := url.Parse(out.AuthorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Host != "digilocker.example.test" || q.Get("state") != out.State || q.Get("code_challenge_method") != "S256" ||
		q.Get("code_challenge") != digilocker.CodeChallengeS256(verifier) || q.Get("redirect_uri") != "https://api.example.test"+digilocker.ReturnPath ||
		q.Get("client_id") != "food-client" || q.Get("response_type") != "code" {
		t.Fatal("authorize URL does not carry the S256 challenge of the sealed verifier")
	}
	if strings.Contains(out.AuthorizeURL, verifier) {
		t.Fatal("the verifier leaked into the authorize URL")
	}

	mock := New(&kycFakeStore{}).WithPII(crypto).WithDigiLocker(digilocker.Settings{Mode: digilocker.ModeMock, Client: digilocker.NewMockClient(), PublicBaseURL: "https://api.example.test"})
	m, err := mock.StartDigiLocker(context.Background(), uuid.New())
	if err != nil || m.AuthorizeURL != "https://api.example.test"+digilocker.DevAuthorizePath+"?state="+m.State {
		t.Fatalf("mock authorize url = %v", err)
	}
}

func TestStartDigiLockerFailsClosed(t *testing.T) {
	ctx := context.Background()
	if _, err := New(&kycFakeStore{}).WithPII(kycCrypto(t)).StartDigiLocker(ctx, uuid.New()); !errors.Is(err, ErrDigiLockerNotConfigured) {
		t.Fatalf("no provider: %v", err)
	}
	if _, err := New(&kycFakeStore{}).WithDigiLocker(httpSettings(&scriptedClient{})).StartDigiLocker(ctx, uuid.New()); !errors.Is(err, ErrPIINotConfigured) {
		t.Fatalf("no PII keys: %v", err)
	}
}

func startThen(t *testing.T, client digilocker.Client) (*Service, *kycFakeStore, string) {
	t.Helper()
	st := &kycFakeStore{}
	svc := New(st).WithPII(kycCrypto(t)).WithDigiLocker(httpSettings(client))
	out, err := svc.StartDigiLocker(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	return svc, st, out.State
}

func TestCompleteDigiLockerRecordsAadhaarAsReferenceOnlyAndSealsDLAndRC(t *testing.T) {
	client := &scriptedClient{docs: issuedSet()}
	svc, st, state := startThen(t, client)
	if _, err := svc.CompleteDigiLocker(context.Background(), uuid.New(), "the-code", state); err != nil {
		t.Fatal(err)
	}
	if client.gotVerifier == "" || digilocker.HashState(state) != st.createdHash {
		t.Fatal("the provider did not receive the stored verifier")
	}
	rec := st.recorded
	if rec == nil || len(rec.Checks) != 3 || len(rec.Documents) != 2 {
		t.Fatalf("recorded = %+v", rec)
	}
	aadhaar := rec.Checks[0]
	if aadhaar.Kind != postgres.KYCKindAadhaar || aadhaar.AssertionRef != "ref-aadhaar-abc" ||
		aadhaar.DocTypeHash != digilocker.HashDocumentType("ADHAR") || aadhaar.NameOnDocumentMasked != "T*** R***" {
		t.Fatalf("aadhaar check = %+v", aadhaar)
	}
	for _, d := range rec.Documents {
		if d.Status != postgres.DocumentStatusApproved || d.NumberLookup == "" || len(d.NumberSealed) == 0 || d.ExpiresAt == nil {
			t.Fatalf("document %s not sealed and approved", d.DocumentType)
		}
	}
	if rec.Documents[0].NumberMasked != "****0001" || rec.Documents[1].NumberMasked != "****1234" {
		t.Fatal("masks are wrong")
	}
	raw, _ := json.Marshal(rec)
	for _, plain := range []string{"KA0120200000001", "KA01AB1234", "Test Rider", state, client.gotVerifier} {
		if strings.Contains(string(raw), plain) {
			t.Fatal("the persisted record carries a plaintext value")
		}
	}
}

func TestCompleteDigiLockerRefusesAadhaarNumbersAnywhere(t *testing.T) {
	aadhaar := kycSynthAadhaar(t)
	cases := map[string]func([]digilocker.IssuedDocument){
		"number on the aadhaar document":  func(d []digilocker.IssuedDocument) { d[0].Number = aadhaar },
		"aadhaar-shaped reference":        func(d []digilocker.IssuedDocument) { d[0].Reference = "uidai-" + aadhaar },
		"aadhaar number as the DL number": func(d []digilocker.IssuedDocument) { d[1].Number = aadhaar },
		"reference embeds the DL number":  func(d []digilocker.IssuedDocument) { d[1].Reference = "in.gov.transport-DRVLC-KA0120200000001" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			docs := issuedSet()
			mutate(docs)
			svc, st, state := startThen(t, &scriptedClient{docs: docs})
			if _, err := svc.CompleteDigiLocker(context.Background(), uuid.New(), "the-code", state); !errors.Is(err, ErrDigiLockerProvider) {
				t.Fatalf("err = %v, want ErrDigiLockerProvider", err)
			}
			if st.recorded != nil {
				t.Fatal("something was persisted")
			}
		})
	}
}

func TestCompleteDigiLockerSkipsAMalformedLicence(t *testing.T) {
	docs := issuedSet()
	docs[1].Number = "KA01"
	svc, st, state := startThen(t, &scriptedClient{docs: docs})
	if _, err := svc.CompleteDigiLocker(context.Background(), uuid.New(), "the-code", state); err != nil {
		t.Fatal(err)
	}
	if len(st.recorded.Checks) != 2 || len(st.recorded.Documents) != 1 || st.recorded.Documents[0].DocumentType != "VEHICLE_RC" {
		t.Fatalf("recorded = %+v", st.recorded)
	}
}

func TestCompleteDigiLockerRejectsBadInputBeforeConsuming(t *testing.T) {
	svc, st, state := startThen(t, &scriptedClient{docs: issuedSet()})
	ctx := context.Background()
	if _, err := svc.CompleteDigiLocker(ctx, uuid.New(), "the-code", "not-a-state"); !errors.Is(err, postgres.ErrDigiLockerStateNotFound) {
		t.Fatalf("malformed state: %v", err)
	}
	if _, err := svc.CompleteDigiLocker(ctx, uuid.New(), " ", state); err == nil {
		t.Fatal("empty code accepted")
	}
	if st.consumeCalls != 0 {
		t.Fatal("a malformed callback consumed a state")
	}
	st.consumeErr = postgres.ErrDigiLockerStateUsed
	if _, err := svc.CompleteDigiLocker(ctx, uuid.New(), "the-code", state); !errors.Is(err, postgres.ErrDigiLockerStateUsed) {
		t.Fatalf("used state: %v", err)
	}
	if st.recorded != nil {
		t.Fatal("a refused state still persisted documents")
	}
}

func TestDigiLockerRedirects(t *testing.T) {
	state, _ := digilocker.NewState()
	dev := New(&kycFakeStore{}).WithDigiLocker(digilocker.Settings{Mode: digilocker.ModeMock, Client: digilocker.NewMockClient(), PublicBaseURL: "https://api.example.test"})
	loc, err := dev.DigiLockerDevReturnURL(state)
	if err != nil || !strings.HasPrefix(loc, "https://api.example.test"+digilocker.ReturnPath+"?") {
		t.Fatalf("dev return = %v", err)
	}
	u, _ := url.Parse(loc)
	if u.Query().Get("state") != state || !strings.HasPrefix(u.Query().Get("code"), "mock-") {
		t.Fatal("dev return does not carry a mock code and the state")
	}
	prod := New(&kycFakeStore{}).WithDigiLocker(digilocker.Settings{Mode: digilocker.ModeMock, Client: digilocker.NewMockClient(), Production: true})
	if _, err := prod.DigiLockerDevReturnURL(state); !errors.Is(err, ErrDigiLockerNotConfigured) {
		t.Fatalf("mock authorize outside dev: %v", err)
	}
	app := New(&kycFakeStore{}).WithDigiLocker(httpSettings(nil))
	loc, err = app.DigiLockerAppLinkURL(url.Values{"code": {"c1"}, "state": {state}, "other": {"dropped"}})
	if err != nil {
		t.Fatal(err)
	}
	u, _ = url.Parse(loc)
	if u.Host != "rider.example.test" || u.Query().Get("code") != "c1" || u.Query().Get("state") != state || u.Query().Get("other") != "" {
		t.Fatal("app link does not pass code and state through (and only them)")
	}
}

// ─── Customer cancellation of a paid order (B4 follow-up) ──────────────────

type cancelFakeStore struct {
	*refundFakeStore
	out *postgres.Order
}

func (f *cancelFakeStore) CancelOrder(context.Context, uuid.UUID, uuid.UUID, string) (*postgres.Order, error) {
	o := *f.out
	return &o, nil
}

func TestCustomerCancelSubmitsTheRefundOfAPaidOrder(t *testing.T) {
	orderID := uuid.New()
	plan := refundPlanFor(orderID)
	st := &cancelFakeStore{refundFakeStore: &refundFakeStore{plans: map[uuid.UUID]*postgres.RefundPlan{orderID: plan}},
		out: &postgres.Order{ID: orderID, Status: "REFUND_PENDING"}}
	pay := &fakePayments{}
	if _, err := New(st).WithPayments(pay).CancelOrder(context.Background(), uuid.New(), orderID, "changed my mind"); err != nil {
		t.Fatal(err)
	}
	if len(pay.refunds) != 1 || pay.refunds[0].amount != plan.AmountMinor || len(st.submitted) != 1 || st.submitted[0] != plan.RefundID {
		t.Fatalf("refunds = %+v, submitted = %v", pay.refunds, st.submitted)
	}

	unpaid := uuid.New()
	st2 := &cancelFakeStore{refundFakeStore: &refundFakeStore{}, out: &postgres.Order{ID: unpaid, Status: "CANCELLED_BY_CUSTOMER"}}
	pay2 := &fakePayments{}
	if _, err := New(st2).WithPayments(pay2).CancelOrder(context.Background(), uuid.New(), unpaid, "changed my mind"); err != nil {
		t.Fatal(err)
	}
	if len(pay2.refunds) != 0 {
		t.Fatal("an unpaid cancellation submitted a refund")
	}
}
