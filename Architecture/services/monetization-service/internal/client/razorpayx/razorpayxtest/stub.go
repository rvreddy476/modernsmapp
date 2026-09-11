// Package razorpayxtest is an httptest stand-in for RazorpayX, mimicking
// the contacts, fund_accounts, payouts and webhook contracts documented on
// the client (see razorpayx/client.go for each endpoint's request and
// response shape and its reference URL). It exists because no real
// RazorpayX credentials exist yet; the controlled Rs 1 transfer in the
// plan's acceptance table waits for test-mode keys.
//
// Beyond the happy path it can do the two things the rail has to survive:
// answer a CreatePayout with a 5xx while still creating the payout
// (a lost response), and hold a second payout under the same reference
// (an ambiguity no machine should resolve).
package razorpayxtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/atpost/monetization-service/internal/client/razorpayx"
)

const (
	StubKeyID         = "rzp_test_stub"
	StubKeySecret     = "stub_secret"
	StubAccountNumber = "2323230041626905"
)

var ifscRe = regexp.MustCompile(`^[A-Z]{4}0[A-Z0-9]{6}$`)

type contact struct {
	ID          string `json:"id"`
	Entity      string `json:"entity"`
	Name        string `json:"name"`
	Email       string `json:"email,omitempty"`
	Type        string `json:"type"`
	ReferenceID string `json:"reference_id"`
	Active      bool   `json:"active"`
	CreatedAt   int64  `json:"created_at"`
}

type bankAccount struct {
	Name          string `json:"name"`
	IFSC          string `json:"ifsc"`
	AccountNumber string `json:"account_number"`
	BankName      string `json:"bank_name"`
}

type fundAccount struct {
	ID          string      `json:"id"`
	Entity      string      `json:"entity"`
	ContactID   string      `json:"contact_id"`
	AccountType string      `json:"account_type"`
	BankAccount bankAccount `json:"bank_account"`
	Active      bool        `json:"active"`
	CreatedAt   int64       `json:"created_at"`
}

type statusDetails struct {
	Description string `json:"description"`
	Source      string `json:"source"`
	Reason      string `json:"reason"`
}

type payout struct {
	ID            string         `json:"id"`
	Entity        string         `json:"entity"`
	FundAccountID string         `json:"fund_account_id"`
	Amount        int64          `json:"amount"`
	Currency      string         `json:"currency"`
	Fees          int64          `json:"fees"`
	Tax           int64          `json:"tax"`
	Status        string         `json:"status"`
	Purpose       string         `json:"purpose"`
	UTR           *string        `json:"utr"`
	Mode          string         `json:"mode"`
	ReferenceID   string         `json:"reference_id"`
	Narration     string         `json:"narration"`
	FailureReason *string        `json:"failure_reason"`
	StatusDetails *statusDetails `json:"status_details"`
	CreatedAt     int64          `json:"created_at"`
}

func (p payout) public() razorpayx.Payout {
	out := razorpayx.Payout{
		ID: p.ID, Status: p.Status, Mode: p.Mode, ReferenceID: p.ReferenceID,
		FundAccountID: p.FundAccountID, AmountPaise: p.Amount,
	}
	if p.UTR != nil {
		out.UTR = *p.UTR
	}
	if p.FailureReason != nil {
		out.FailureReason = *p.FailureReason
	}
	return out
}

type failNext struct {
	status       int
	createAnyway bool
}

// Server is the stub. Every accessor is safe to call from tests while
// the rail is hitting it.
type Server struct {
	*httptest.Server

	mu           sync.Mutex
	seq          int
	contacts     []contact
	fundAccounts []fundAccount
	payouts      map[string]*payout
	payoutOrder  []string
	idem         map[string]string // idempotency key -> payout id
	idemKeys     []string          // every X-Payout-Idempotency received, in order
	createCalls  int
	failCreate   *failNext
}

// NewServer starts the stub. Close it when done.
func NewServer() *Server {
	s := &Server{payouts: map[string]*payout{}, idem: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/contacts", s.auth(s.handleContacts))
	mux.HandleFunc("/v1/fund_accounts", s.auth(s.handleFundAccounts))
	mux.HandleFunc("/v1/fund_accounts/validations", s.auth(s.handleValidations))
	mux.HandleFunc("/v1/payouts", s.auth(s.handlePayouts))
	mux.HandleFunc("/v1/payouts/", s.auth(s.handlePayoutByID))
	s.Server = httptest.NewServer(mux)
	return s
}

// Config is a client configuration pointed at the stub.
func (s *Server) Config(webhookSecret string) razorpayx.Config {
	return razorpayx.Config{
		KeyID:         StubKeyID,
		KeySecret:     StubKeySecret,
		AccountNumber: StubAccountNumber,
		WebhookSecret: webhookSecret,
		BaseURL:       s.URL,
		HTTPClient:    &http.Client{Timeout: 5 * time.Second},
	}
}

// ---------------------------------------------------------------------------
// Knobs and accessors
// ---------------------------------------------------------------------------

// FailNextCreatePayout makes the next POST /v1/payouts answer `status`.
// With createAnyway the payout is still created before the error is
// returned — the request reached the provider and the answer was lost.
func (s *Server) FailNextCreatePayout(status int, createAnyway bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failCreate = &failNext{status: status, createAnyway: createAnyway}
}

// AddPayout seeds a payout that bypasses idempotency, e.g. a second one
// under a reference the rail already used.
func (s *Server) AddPayout(referenceID string, amountPaise int64, status string) razorpayx.Payout {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.newPayoutLocked("fa_seeded", amountPaise, razorpayx.ModeFor(amountPaise), referenceID)
	p.Status = status
	return p.public()
}

// SetPayoutStatus moves a payout to a provider status, with the UTR and
// failure reason the provider would attach.
func (s *Server) SetPayoutStatus(id, status, utr, failureReason string) razorpayx.Payout {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.payouts[id]
	if !ok {
		panic("razorpayxtest: SetPayoutStatus: unknown payout " + id)
	}
	p.Status = status
	p.UTR = nil
	if utr != "" {
		u := utr
		p.UTR = &u
	}
	p.FailureReason = nil
	p.StatusDetails = nil
	if failureReason != "" {
		f := failureReason
		p.FailureReason = &f
		p.StatusDetails = &statusDetails{Description: failureReason, Source: "beneficiary_bank", Reason: "bank_rejected"}
	}
	return p.public()
}

// Payouts returns every payout in creation order.
func (s *Server) Payouts() []razorpayx.Payout {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]razorpayx.Payout, 0, len(s.payoutOrder))
	for _, id := range s.payoutOrder {
		out = append(out, s.payouts[id].public())
	}
	return out
}

// PayoutsByReference returns the payouts carrying referenceID.
func (s *Server) PayoutsByReference(referenceID string) []razorpayx.Payout {
	var out []razorpayx.Payout
	for _, p := range s.Payouts() {
		if p.ReferenceID == referenceID {
			out = append(out, p)
		}
	}
	return out
}

// CreatePayoutCalls counts POST /v1/payouts requests, failed ones included.
func (s *Server) CreatePayoutCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createCalls
}

// IdempotencyKeys is every X-Payout-Idempotency header received.
func (s *Server) IdempotencyKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.idemKeys...)
}

// Contacts and FundAccounts count what has been created.
func (s *Server) Contacts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.contacts)
}

func (s *Server) FundAccounts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.fundAccounts)
}

// WebhookRequest builds the body and headers RazorpayX would deliver for
// eventType about p, signed with secret.
func (s *Server) WebhookRequest(secret, eventID, eventType string, p razorpayx.Payout) ([]byte, http.Header) {
	entity := payout{
		ID: p.ID, Entity: "payout", FundAccountID: p.FundAccountID, Amount: p.AmountPaise,
		Currency: "INR", Status: p.Status, Purpose: "payout", Mode: p.Mode,
		ReferenceID: p.ReferenceID, Narration: "Creator payout", CreatedAt: time.Now().Unix(),
	}
	if p.UTR != "" {
		u := p.UTR
		entity.UTR = &u
	}
	if p.FailureReason != "" {
		f := p.FailureReason
		entity.FailureReason = &f
		entity.StatusDetails = &statusDetails{Description: f, Source: "beneficiary_bank", Reason: "bank_rejected"}
	}
	body, _ := json.Marshal(map[string]any{
		"entity":     "event",
		"account_id": "acc_stub",
		"event":      eventType,
		"contains":   []string{"payout"},
		"payload":    map[string]any{"payout": map[string]any{"entity": entity}},
		"created_at": time.Now().Unix(),
	})
	hdr := http.Header{}
	hdr.Set("Content-Type", "application/json")
	hdr.Set(razorpayx.HeaderSignature, razorpayx.Sign(secret, body))
	hdr.Set(razorpayx.HeaderEventID, eventID)
	return body, hdr
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func (s *Server) nextID(prefix string) string {
	s.seq++
	return fmt.Sprintf("%s_%014d", prefix, s.seq)
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != StubKeyID || pass != StubKeySecret {
			writeErr(w, http.StatusUnauthorized, "BAD_REQUEST_ERROR", "Authentication failed")
			return
		}
		next(w, r)
	}
}

func writeErr(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "description": description, "source": "business", "step": "payout_initiation", "reason": "input_validation_failed"},
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func collection[T any](items []T) map[string]any {
	if items == nil {
		items = []T{}
	}
	return map[string]any{"entity": "collection", "count": len(items), "items": items}
}

func (s *Server) handleContacts(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		ref := r.URL.Query().Get("reference_id")
		var items []contact
		for _, c := range s.contacts {
			if ref == "" || c.ReferenceID == ref {
				items = append(items, c)
			}
		}
		writeJSON(w, collection(items))
	case http.MethodPost:
		var in struct {
			Name        string `json:"name"`
			Email       string `json:"email"`
			Type        string `json:"type"`
			ReferenceID string `json:"reference_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Name) == "" {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST_ERROR", "The name field is required.")
			return
		}
		c := contact{ID: s.nextID("cont"), Entity: "contact", Name: in.Name, Email: in.Email, Type: in.Type, ReferenceID: in.ReferenceID, Active: true, CreatedAt: time.Now().Unix()}
		s.contacts = append(s.contacts, c)
		writeJSON(w, c)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "BAD_REQUEST_ERROR", "method not allowed")
	}
}

func (s *Server) handleFundAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "BAD_REQUEST_ERROR", "method not allowed")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var in struct {
		ContactID   string      `json:"contact_id"`
		AccountType string      `json:"account_type"`
		BankAccount bankAccount `json:"bank_account"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ContactID == "" || in.AccountType != "bank_account" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST_ERROR", "contact_id and account_type are required")
		return
	}
	found := false
	for _, c := range s.contacts {
		if c.ID == in.ContactID {
			found = true
		}
	}
	if !found {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST_ERROR", "The id provided does not exist")
		return
	}
	if !ifscRe.MatchString(in.BankAccount.IFSC) {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST_ERROR", "The ifsc field is invalid.")
		return
	}
	if l := len(in.BankAccount.AccountNumber); l < 5 || l > 35 || in.BankAccount.Name == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST_ERROR", "The account number or name is invalid.")
		return
	}
	// The provider de-duplicates identical details under a contact.
	for _, fa := range s.fundAccounts {
		if fa.ContactID == in.ContactID && fa.BankAccount.IFSC == in.BankAccount.IFSC && fa.BankAccount.AccountNumber == in.BankAccount.AccountNumber {
			writeJSON(w, fa)
			return
		}
	}
	fa := fundAccount{ID: s.nextID("fa"), Entity: "fund_account", ContactID: in.ContactID, AccountType: "bank_account", Active: true, CreatedAt: time.Now().Unix()}
	fa.BankAccount = in.BankAccount
	fa.BankAccount.BankName = "Stub Bank"
	s.fundAccounts = append(s.fundAccounts, fa)
	writeJSON(w, fa)
}

func (s *Server) handleValidations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "BAD_REQUEST_ERROR", "method not allowed")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var in struct {
		AccountNumber string `json:"account_number"`
		FundAccount   struct {
			ID string `json:"id"`
		} `json:"fund_account"`
		Amount   int64  `json:"amount"`
		Currency string `json:"currency"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.FundAccount.ID == "" || in.AccountNumber != StubAccountNumber {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST_ERROR", "fund_account.id and account_number are required")
		return
	}
	var fa *fundAccount
	for i := range s.fundAccounts {
		if s.fundAccounts[i].ID == in.FundAccount.ID {
			fa = &s.fundAccounts[i]
		}
	}
	if fa == nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST_ERROR", "The id provided does not exist")
		return
	}
	writeJSON(w, map[string]any{
		"id":           s.nextID("fav"),
		"entity":       "fund_account.validation",
		"fund_account": fa,
		"status":       "completed",
		"amount":       in.Amount,
		"currency":     "INR",
		"notes":        []any{},
		"results":      map[string]any{"account_status": "active", "registered_name": fa.BankAccount.Name},
		"created_at":   time.Now().Unix(),
		"utr":          "STUBVALID" + fmt.Sprint(s.seq),
	})
}

func (s *Server) newPayoutLocked(fundAccountID string, amount int64, mode, referenceID string) *payout {
	p := &payout{
		ID: s.nextID("pout"), Entity: "payout", FundAccountID: fundAccountID, Amount: amount,
		Currency: "INR", Fees: 590, Tax: 90, Status: "processing", Purpose: "payout", Mode: mode,
		ReferenceID: referenceID, Narration: "Creator payout", CreatedAt: time.Now().Unix(),
	}
	s.payouts[p.ID] = p
	s.payoutOrder = append(s.payoutOrder, p.ID)
	return p
}

func (s *Server) handlePayouts(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query()
		if q.Get("account_number") != StubAccountNumber {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST_ERROR", "The account number field is required.")
			return
		}
		ref := q.Get("reference_id")
		var items []payout
		for _, id := range s.payoutOrder {
			p := s.payouts[id]
			if ref == "" || p.ReferenceID == ref {
				items = append(items, *p)
			}
		}
		writeJSON(w, collection(items))
	case http.MethodPost:
		s.createCalls++
		key := r.Header.Get("X-Payout-Idempotency")
		s.idemKeys = append(s.idemKeys, key)
		var in struct {
			AccountNumber string `json:"account_number"`
			FundAccountID string `json:"fund_account_id"`
			Amount        int64  `json:"amount"`
			Currency      string `json:"currency"`
			Mode          string `json:"mode"`
			Purpose       string `json:"purpose"`
			ReferenceID   string `json:"reference_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.AccountNumber != StubAccountNumber || in.FundAccountID == "" || in.Amount <= 0 || in.Currency != "INR" {
			writeErr(w, http.StatusBadRequest, "BAD_REQUEST_ERROR", "account_number, fund_account_id, amount and currency are required")
			return
		}
		if f := s.failCreate; f != nil {
			s.failCreate = nil
			if f.createAnyway {
				p := s.newPayoutLocked(in.FundAccountID, in.Amount, in.Mode, in.ReferenceID)
				if key != "" {
					s.idem[key] = p.ID
				}
			}
			writeErr(w, f.status, "SERVER_ERROR", http.StatusText(f.status))
			return
		}
		if key != "" {
			if id, ok := s.idem[key]; ok {
				writeJSON(w, s.payouts[id])
				return
			}
		}
		p := s.newPayoutLocked(in.FundAccountID, in.Amount, in.Mode, in.ReferenceID)
		if key != "" {
			s.idem[key] = p.ID
		}
		writeJSON(w, p)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "BAD_REQUEST_ERROR", "method not allowed")
	}
}

func (s *Server) handlePayoutByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "BAD_REQUEST_ERROR", "method not allowed")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := strings.TrimPrefix(r.URL.Path, "/v1/payouts/")
	p, ok := s.payouts[id]
	if !ok {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST_ERROR", "The id provided does not exist")
		return
	}
	writeJSON(w, p)
}
