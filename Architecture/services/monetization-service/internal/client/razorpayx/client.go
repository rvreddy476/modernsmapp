// Package razorpayx is the outbound payout rail (plan Phase 4B): a thin
// client for RazorpayX contacts, fund accounts and payouts, plus webhook
// verification.
//
// Why it lives here and not in payments-service: payments-service is a
// separate Go module whose internal/gateway cannot be imported, its
// Provider port is pay-in shaped, and the ledger a payout settles against
// lives in this service. Two patterns are copied rather than shared — the
// Basic-auth do() plumbing
// (payments-service/internal/gateway/razorpay_provider.go:436-474) and
// raw-body HMAC verification with the required X-Razorpay-Event-Id header
// (:149-172).
//
// Every endpoint's request and response shape is documented next to the
// method that calls it, from Razorpay's public API reference:
//
//	Contacts        https://razorpay.com/docs/api/x/contacts/
//	Fund accounts   https://razorpay.com/docs/api/x/fund-accounts/
//	Validation      https://razorpay.com/docs/api/x/fund-accounts/validation/
//	Payouts         https://razorpay.com/docs/api/x/payouts/
//	Idempotency     https://razorpay.com/docs/api/x/payout-idempotency/
//	Webhooks        https://razorpay.com/docs/webhooks/payloads/x/payouts/
//	Errors          https://razorpay.com/docs/api/x/errors/
//
// No real RazorpayX credentials exist yet; the client is exercised against
// the httptest stub in razorpayxtest, which mimics those contracts.
package razorpayx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is RazorpayX's live and test-mode host (the key prefix
// selects the mode).
const DefaultBaseURL = "https://api.razorpay.com"

// Env variable names. The first two are shared with payments-service; the
// last two are the rail's own.
const (
	EnvKeyID         = "RAZORPAY_KEY_ID"
	EnvKeySecret     = "RAZORPAY_KEY_SECRET"
	EnvAccountNumber = "RAZORPAYX_ACCOUNT_NUMBER"
	EnvWebhookSecret = "RAZORPAYX_WEBHOOK_SECRET"
)

// impsCeilingPaise: IMPS under Rs 2 lakh, NEFT at and above it.
const impsCeilingPaise int64 = 20_000_000

// Config is what the client needs. All four credential fields are
// required; BaseURL and HTTPClient default.
type Config struct {
	KeyID         string
	KeySecret     string
	AccountNumber string // the RazorpayX current/virtual account payouts are debited from
	WebhookSecret string
	BaseURL       string
	HTTPClient    *http.Client
}

// ConfigFromEnv reads the four credentials through getenv (os.Getenv in
// main, a map in tests) and returns the names of any that are unset. The
// caller constructs the client only when missing is empty.
func ConfigFromEnv(getenv func(string) string) (Config, []string) {
	cfg := Config{
		KeyID:         strings.TrimSpace(getenv(EnvKeyID)),
		KeySecret:     strings.TrimSpace(getenv(EnvKeySecret)),
		AccountNumber: strings.TrimSpace(getenv(EnvAccountNumber)),
		WebhookSecret: strings.TrimSpace(getenv(EnvWebhookSecret)),
	}
	var missing []string
	for _, kv := range []struct{ name, value string }{
		{EnvKeyID, cfg.KeyID}, {EnvKeySecret, cfg.KeySecret},
		{EnvAccountNumber, cfg.AccountNumber}, {EnvWebhookSecret, cfg.WebhookSecret},
	} {
		if kv.value == "" {
			missing = append(missing, kv.name)
		}
	}
	return cfg, missing
}

// API is the surface the service uses, so tests can substitute a fake
// without a server if they ever need to. *Client satisfies it.
type API interface {
	EnsureContact(ctx context.Context, userID, name, email string) (string, error)
	EnsureFundAccount(ctx context.Context, contactID string, b BankDetails) (string, error)
	ValidateFundAccount(ctx context.Context, fundAccountID string) (Validation, error)
	CreatePayout(ctx context.Context, idempotencyKey string, amountPaise int64, fundAccountID, mode, referenceID string) (Payout, error)
	FetchPayout(ctx context.Context, id string) (Payout, error)
	ListPayoutsByReference(ctx context.Context, referenceID string) ([]Payout, error)
}

// Client talks to one RazorpayX account.
type Client struct {
	baseURL       string
	keyID         string
	keySecret     string
	accountNumber string
	webhookSecret string
	client        *http.Client
}

var _ API = (*Client)(nil)

// New builds a client. It does not validate credentials; the first call
// does.
func New(cfg Config) *Client {
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{
		baseURL:       base,
		keyID:         cfg.KeyID,
		keySecret:     cfg.KeySecret,
		accountNumber: cfg.AccountNumber,
		webhookSecret: cfg.WebhookSecret,
		client:        hc,
	}
}

// WebhookSecret is the secret the service verifies deliveries with.
func (c *Client) WebhookSecret() string { return c.webhookSecret }

// BankDetails is what a fund account is created from.
type BankDetails struct {
	HolderName    string
	AccountNumber string
	IFSC          string
}

// Payout is the provider's payout entity, reduced to what the rail
// records.
type Payout struct {
	ID            string
	Status        string // the provider's word: queued|pending|processing|processed|reversed|cancelled|rejected|failed
	UTR           string
	Mode          string
	ReferenceID   string
	FundAccountID string
	AmountPaise   int64
	FailureReason string
}

// Validation is the result of a fund-account validation (penny drop).
type Validation struct {
	ID             string
	Status         string // created|completed|failed
	AccountStatus  string // active|invalid (results.account_status)
	RegisteredName string
}

// Error is a provider or transport failure. Ambiguous means the request
// may have been processed even though no usable answer came back — a
// timeout, a connection failure, a 5xx, 408 or 429 — and the caller must
// look before retrying (the ambiguous-response rule, plan Phase 4C).
type Error struct {
	Method    string
	Path      string
	Status    int
	Body      string
	Ambiguous bool
	Err       error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("razorpayx: %s %s: %v", e.Method, e.Path, e.Err)
	}
	return fmt.Sprintf("razorpayx: %s %s returned %d: %s", e.Method, e.Path, e.Status, e.Body)
}

func (e *Error) Unwrap() error { return e.Err }

// IsAmbiguous reports whether err is an ambiguous provider failure: the
// request may or may not have been processed.
func IsAmbiguous(err error) bool {
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Ambiguous
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// ModeFor picks the transfer mode: IMPS under Rs 2 lakh, NEFT at and
// above it.
func ModeFor(amountPaise int64) string {
	if amountPaise < impsCeilingPaise {
		return "IMPS"
	}
	return "NEFT"
}

// MapStatus translates the provider's payout status into the rail's
// state machine, exactly as the plan lists:
//
//	queued | pending | processing  -> processing
//	processed                      -> paid   (UTR captured)
//	rejected | cancelled | failed  -> failed
//	reversed                       -> reversed
//
// Anything else is unknown and ok is false.
func MapStatus(providerStatus string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(providerStatus)) {
	case "queued", "pending", "processing":
		return "processing", true
	case "processed":
		return "paid", true
	case "rejected", "cancelled", "failed":
		return "failed", true
	case "reversed":
		return "reversed", true
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Wire shapes
// ---------------------------------------------------------------------------

// contactEntity — https://razorpay.com/docs/api/x/contacts/create
//
//	POST /v1/contacts
//	  {"name":"Gaurav Kumar","email":"gaurav.kumar@example.com","contact":"9123456789",
//	   "type":"vendor","reference_id":"Acme Contact ID 12345","notes":{}}
//	200 {"id":"cont_00000000000001","entity":"contact","name":"Gaurav Kumar",
//	     "contact":"9123456789","email":"gaurav.kumar@example.com","type":"vendor",
//	     "reference_id":"Acme Contact ID 12345","batch_id":null,"active":true,
//	     "notes":{},"created_at":1545320320}
//
// Fetch all — https://razorpay.com/docs/api/x/contacts/fetch-all
//
//	GET /v1/contacts?reference_id=...   200 {"entity":"collection","count":1,"items":[{...}]}
type contactEntity struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Email       string `json:"email,omitempty"`
	Type        string `json:"type"`
	ReferenceID string `json:"reference_id"`
	Active      bool   `json:"active"`
}

// fundAccountEntity — https://razorpay.com/docs/api/x/fund-accounts/create/bank-account
//
//	POST /v1/fund_accounts
//	  {"contact_id":"cont_00000000000001","account_type":"bank_account",
//	   "bank_account":{"name":"Gaurav Kumar","ifsc":"HDFC0000053","account_number":"765432123456789"}}
//	200 {"id":"fa_00000000000001","entity":"fund_account","contact_id":"cont_00000000000001",
//	     "account_type":"bank_account","bank_account":{"ifsc":"HDFC0000053","bank_name":"HDFC Bank",
//	     "name":"Gaurav Kumar","notes":[],"account_number":"765432123456789"},
//	     "batch_id":null,"active":true,"created_at":1543650891}
//
// RazorpayX returns the existing fund account when one with identical
// details already exists under the contact.
type fundAccountEntity struct {
	ID          string `json:"id"`
	ContactID   string `json:"contact_id"`
	AccountType string `json:"account_type"`
	BankAccount struct {
		Name          string `json:"name"`
		IFSC          string `json:"ifsc"`
		AccountNumber string `json:"account_number"`
		BankName      string `json:"bank_name,omitempty"`
	} `json:"bank_account"`
	Active bool `json:"active"`
}

// validationEntity — https://razorpay.com/docs/api/x/fund-accounts/validation/
//
//	POST /v1/fund_accounts/validations
//	  {"account_number":"<X account number>","fund_account":{"id":"fa_00000000000001"},
//	   "amount":100,"currency":"INR","notes":{}}
//	200 {"id":"fav_00000000000001","entity":"fund_account.validation",
//	     "fund_account":{...},"status":"completed","amount":100,"currency":"INR",
//	     "notes":[],"results":{"account_status":"active","registered_name":"Gaurav Kumar"},
//	     "created_at":1543650891,"utr":"..."}
//
// status is created until the penny drop settles; the reconciler is not
// wired to poll it, so verified_at is set only when the response already
// says completed/active (test mode answers synchronously).
type validationEntity struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Results struct {
		AccountStatus  string `json:"account_status"`
		RegisteredName string `json:"registered_name"`
	} `json:"results"`
}

// payoutEntity — https://razorpay.com/docs/api/x/payouts/create/bank-account
//
//	POST /v1/payouts    header X-Payout-Idempotency: <key>
//	  {"account_number":"<X account number>","fund_account_id":"fa_00000000000001",
//	   "amount":1000000,"currency":"INR","mode":"IMPS","purpose":"payout",
//	   "queue_if_low_balance":true,"reference_id":"Acme Transaction ID 12345",
//	   "narration":"Acme Corp Fund Transfer","notes":{}}
//	200 {"id":"pout_00000000000001","entity":"payout","fund_account_id":"fa_00000000000001",
//	     "amount":1000000,"currency":"INR","notes":{},"fees":590,"tax":90,
//	     "status":"processing","purpose":"payout","utr":null,"mode":"IMPS",
//	     "reference_id":"Acme Transaction ID 12345","narration":"Acme Corp Fund Transfer",
//	     "batch_id":null,"failure_reason":null,"created_at":1545383037,
//	     "status_details":{"description":"...","source":"beneficiary_bank","reason":"..."}}
//
// Fetch — https://razorpay.com/docs/api/x/payouts/fetch-with-id
//
//	GET /v1/payouts/:id          200 {payout entity}
//
// Fetch all — https://razorpay.com/docs/api/x/payouts/fetch-all
//
//	GET /v1/payouts?account_number=...&reference_id=...&count=10
//	200 {"entity":"collection","count":1,"items":[{payout entity}]}
//
// Idempotency — https://razorpay.com/docs/api/x/payout-idempotency/
// the same X-Payout-Idempotency key within 24 h returns the original
// payout rather than creating another.
type payoutEntity struct {
	ID            string  `json:"id"`
	FundAccountID string  `json:"fund_account_id"`
	Amount        int64   `json:"amount"`
	Currency      string  `json:"currency"`
	Status        string  `json:"status"`
	UTR           *string `json:"utr"`
	Mode          string  `json:"mode"`
	ReferenceID   string  `json:"reference_id"`
	FailureReason *string `json:"failure_reason"`
	StatusDetails *struct {
		Description string `json:"description"`
		Reason      string `json:"reason"`
	} `json:"status_details"`
}

func (p payoutEntity) toPayout() Payout {
	out := Payout{
		ID:            p.ID,
		Status:        p.Status,
		Mode:          p.Mode,
		ReferenceID:   p.ReferenceID,
		FundAccountID: p.FundAccountID,
		AmountPaise:   p.Amount,
	}
	if p.UTR != nil {
		out.UTR = *p.UTR
	}
	if p.FailureReason != nil {
		out.FailureReason = *p.FailureReason
	}
	if out.FailureReason == "" && p.StatusDetails != nil && p.StatusDetails.Description != "" {
		out.FailureReason = p.StatusDetails.Description
	}
	return out
}

type collection[T any] struct {
	Count int `json:"count"`
	Items []T `json:"items"`
}

// ---------------------------------------------------------------------------
// Methods
// ---------------------------------------------------------------------------

// EnsureContact returns the contact for userID, creating it when the
// lookup by reference_id finds none. The reference is our user id, so a
// creator never gets a second contact.
func (c *Client) EnsureContact(ctx context.Context, userID, name, email string) (string, error) {
	if strings.TrimSpace(userID) == "" {
		return "", errors.New("razorpayx: EnsureContact: user id is required")
	}
	q := url.Values{"reference_id": {userID}, "count": {"2"}}
	var found collection[contactEntity]
	if err := c.do(ctx, http.MethodGet, "/v1/contacts?"+q.Encode(), nil, nil, &found); err != nil {
		return "", err
	}
	for _, item := range found.Items {
		if item.ReferenceID == userID && item.ID != "" {
			return item.ID, nil
		}
	}
	name = strings.TrimSpace(name)
	if len(name) < 3 {
		name = "Creator " + name
	}
	body := map[string]any{
		"name":         name,
		"type":         "vendor",
		"reference_id": userID,
	}
	if email = strings.TrimSpace(email); email != "" {
		body["email"] = email
	}
	var created contactEntity
	if err := c.do(ctx, http.MethodPost, "/v1/contacts", body, nil, &created); err != nil {
		return "", err
	}
	if created.ID == "" {
		return "", errors.New("razorpayx: create contact returned no id")
	}
	return created.ID, nil
}

// EnsureFundAccount creates (or, per the provider's own de-duplication,
// returns) the bank fund account for contactID.
func (c *Client) EnsureFundAccount(ctx context.Context, contactID string, b BankDetails) (string, error) {
	if contactID == "" || b.AccountNumber == "" || b.IFSC == "" || strings.TrimSpace(b.HolderName) == "" {
		return "", errors.New("razorpayx: EnsureFundAccount: contact, holder name, account number and IFSC are required")
	}
	body := map[string]any{
		"contact_id":   contactID,
		"account_type": "bank_account",
		"bank_account": map[string]string{
			"name":           strings.TrimSpace(b.HolderName),
			"ifsc":           b.IFSC,
			"account_number": b.AccountNumber,
		},
	}
	var fa fundAccountEntity
	if err := c.do(ctx, http.MethodPost, "/v1/fund_accounts", body, nil, &fa); err != nil {
		return "", err
	}
	if fa.ID == "" {
		return "", errors.New("razorpayx: create fund account returned no id")
	}
	return fa.ID, nil
}

// ValidateFundAccount starts a penny-drop validation of the fund account.
func (c *Client) ValidateFundAccount(ctx context.Context, fundAccountID string) (Validation, error) {
	if fundAccountID == "" {
		return Validation{}, errors.New("razorpayx: ValidateFundAccount: fund account id is required")
	}
	body := map[string]any{
		"account_number": c.accountNumber,
		"fund_account":   map[string]string{"id": fundAccountID},
		"amount":         100,
		"currency":       "INR",
	}
	var v validationEntity
	if err := c.do(ctx, http.MethodPost, "/v1/fund_accounts/validations", body, nil, &v); err != nil {
		return Validation{}, err
	}
	return Validation{ID: v.ID, Status: v.Status, AccountStatus: v.Results.AccountStatus, RegisteredName: v.Results.RegisteredName}, nil
}

// CreatePayout submits one payout under idempotencyKey. referenceID is
// stored on the provider's side and is what ListPayoutsByReference finds
// after an ambiguous response.
func (c *Client) CreatePayout(ctx context.Context, idempotencyKey string, amountPaise int64, fundAccountID, mode, referenceID string) (Payout, error) {
	switch {
	case strings.TrimSpace(idempotencyKey) == "":
		return Payout{}, errors.New("razorpayx: CreatePayout: idempotency key is required")
	case fundAccountID == "":
		return Payout{}, errors.New("razorpayx: CreatePayout: fund account id is required")
	case amountPaise <= 0:
		return Payout{}, errors.New("razorpayx: CreatePayout: amount must be positive")
	case referenceID == "":
		return Payout{}, errors.New("razorpayx: CreatePayout: reference id is required")
	}
	if mode == "" {
		mode = ModeFor(amountPaise)
	}
	body := map[string]any{
		"account_number":       c.accountNumber,
		"fund_account_id":      fundAccountID,
		"amount":               amountPaise,
		"currency":             "INR",
		"mode":                 mode,
		"purpose":              "payout",
		"queue_if_low_balance": true,
		"reference_id":         referenceID,
		"narration":            "Creator payout",
	}
	var p payoutEntity
	if err := c.do(ctx, http.MethodPost, "/v1/payouts", body, map[string]string{"X-Payout-Idempotency": idempotencyKey}, &p); err != nil {
		return Payout{}, err
	}
	if p.ID == "" {
		return Payout{}, &Error{Method: http.MethodPost, Path: "/v1/payouts", Ambiguous: true, Err: errors.New("create payout returned no id")}
	}
	return p.toPayout(), nil
}

// FetchPayout reads one payout by the provider's id.
func (c *Client) FetchPayout(ctx context.Context, id string) (Payout, error) {
	if id == "" {
		return Payout{}, errors.New("razorpayx: FetchPayout: id is required")
	}
	var p payoutEntity
	if err := c.do(ctx, http.MethodGet, "/v1/payouts/"+url.PathEscape(id), nil, nil, &p); err != nil {
		return Payout{}, err
	}
	return p.toPayout(), nil
}

// ListPayoutsByReference is the ambiguous-timeout lookup: every payout on
// the account that carries referenceID.
func (c *Client) ListPayoutsByReference(ctx context.Context, referenceID string) ([]Payout, error) {
	if referenceID == "" {
		return nil, errors.New("razorpayx: ListPayoutsByReference: reference id is required")
	}
	q := url.Values{"account_number": {c.accountNumber}, "reference_id": {referenceID}, "count": {"10"}}
	var found collection[payoutEntity]
	if err := c.do(ctx, http.MethodGet, "/v1/payouts?"+q.Encode(), nil, nil, &found); err != nil {
		return nil, err
	}
	out := make([]Payout, 0, len(found.Items))
	for _, item := range found.Items {
		if item.ReferenceID == referenceID {
			out = append(out, item.toPayout())
		}
	}
	return out, nil
}

// do is the Basic-auth request plumbing, copied from
// payments-service/internal/gateway/razorpay_provider.go:436-474 with one
// addition: the error carries whether the outcome is ambiguous.
func (c *Client) do(ctx context.Context, method, path string, body any, headers map[string]string, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.keyID, c.keySecret)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := c.client.Do(req)
	if err != nil {
		// No answer at all: the request may have been processed.
		return &Error{Method: method, Path: path, Ambiguous: true, Err: err}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		// The body is returned so an operator can see the provider's own
		// error; it is never logged alongside credentials.
		return &Error{
			Method:    method,
			Path:      path,
			Status:    resp.StatusCode,
			Body:      string(raw),
			Ambiguous: resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests,
		}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("razorpayx: decode %s response: %w", path, err)
		}
	}
	return nil
}
