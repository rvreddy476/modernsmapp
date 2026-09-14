// Package paymentsclient is the typed client for payments-service's
// service-authority API, the /v1/payments/internal route family.
//
// payments-service owns every provider interaction. A caller (commerce-service,
// food-service, and later any other domain that takes money) uses this client
// to open an intent for something it owns, read that intent, check a client
// checkout callback ADVISORILY, and file a durable refund command. Money moves
// to "paid" or "refunded" only through the payment events (see the sibling
// package paymentevents), never through a response from this client.
//
// # One caller, one reference type
//
// A Client is bound to one calling service (Config.Service) and the one
// payments reference type that service owns (Config.ReferenceType, e.g.
// servicetoken.RefOrder for commerce, servicetoken.RefFoodOrder for food).
// That is how payments-service registers callers in SERVICE_CALLERS, and it
// keeps a token minted for one domain from being usable against another's
// intents.
//
// # Applications
//
// Every payment, refund and transaction belongs to one APPLICATION (Feast,
// MStore, …) so it can be listed and filtered per application rather than per
// whole app. CreateIntentRequest.ApplicationID and RefundRequest.ApplicationID
// are required and must match ^[a-z][a-z0-9_]{1,31}$ (ValidateApplicationID);
// the client refuses anything else before sending. Each service supplies its
// id from its own configuration. Application settings will come from a
// database registry that does not exist yet, so the ids in use are provisional.
//
// Wire status: the client sends `application_id` in the create-intent and
// refund bodies. payments-service does not store it yet. Its internal handlers
// bind request bodies with gin's ShouldBindJSON, which ignores unknown fields,
// so the field is accepted and dropped there today. The ApplicationID on the
// read results (Intent, CallbackVerdict, RefundAccepted) is empty until
// payments-service stores and echoes it.
//
// # Authentication
//
// Every call carries a fresh Ed25519 service token in
// `X-Service-Authorization: Bearer <token>`: audience payments, issuer and
// subject Config.Service, exactly one operation and the client's reference
// type, a two-minute TTL (TokenTTL). A token is minted per request, not per
// process, because a short TTL is the only control against replay.
//
// The shared `X-Internal-Service-Key` survives ONLY as a legacy credential for
// a local or dev stack that has not issued the caller a signing key. The
// client never decides what "local or dev" means: each service keeps its own
// env var names and its own rule, and passes the verdict in as
// Auth.LegacyAllowed. Without a token key and without that verdict, New fails
// with ErrServiceTokenRequired. The two credentials are never sent together.
//
// # Errors, timeouts and retries
//
// A failed call returns *Error, classified as KindNetwork (transport failure),
// KindServer (5xx) or KindRefused (4xx). errors.Is(err, ErrUnavailable) holds
// for the first two and errors.Is(err, ErrRefused) for the third. An
// unavailable result means "unknown", never "did not happen": the request may
// have been applied and the response lost, which is why every mutating call
// carries a deterministic idempotency key.
//
// The client does not retry. The default HTTP client has a 15 second timeout
// (DefaultTimeout); a caller that needs tracing passes its own *http.Client.
//
// # What is never exposed
//
// The client does not log. Tokens and keys live only in request headers.
// A 4xx error keeps at most 300 bytes of payments-service's own error body
// (its JSON error envelope, not a provider body) so an operator can see why a
// refund was refused. client_session is decoded into ClientSession, whose
// only fields are the three public checkout values; anything else payments or
// a provider adapter attaches (a key secret, an amount) is dropped at decode.
package paymentsclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

const (
	// InternalBase is the service-authority route family on payments-service.
	InternalBase = "/v1/payments/internal"
	// DefaultTimeout is the per-request timeout of the default HTTP client.
	DefaultTimeout = 15 * time.Second
	// TokenTTL is the lifetime of each minted service token.
	TokenTTL = 2 * time.Minute
	// DefaultCurrency is sent when a CreateIntentRequest names none.
	DefaultCurrency = "INR"

	// HeaderServiceAuthorization carries the service token.
	HeaderServiceAuthorization = "X-Service-Authorization"
	// HeaderInternalServiceKey carries the legacy shared key.
	HeaderInternalServiceKey = "X-Internal-Service-Key"

	// errorDetailLimit bounds how much of a 4xx body an Error keeps.
	errorDetailLimit = 300
)

var (
	// ErrUnavailable matches a transport failure or a 5xx. Treat it as
	// "unknown", never as "did not happen".
	ErrUnavailable = errors.New("payments service unavailable")
	// ErrRefused matches a 4xx: payments-service rejected the request.
	ErrRefused = errors.New("payments refused the request")
	// ErrServiceTokenRequired: no token key was configured and the caller
	// did not allow the legacy key.
	ErrServiceTokenRequired = errors.New("payments client: a service token key is required outside local/dev")
	// ErrInvalidApplicationID: an application id is missing or malformed.
	ErrInvalidApplicationID = errors.New("payments client: application_id must match ^[a-z][a-z0-9_]{1,31}$")
)

var applicationIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)

// ValidateApplicationID reports whether id is a well-formed application id:
// a lowercase letter, then 1-31 lowercase letters, digits or underscores.
func ValidateApplicationID(id string) error {
	if !applicationIDPattern.MatchString(id) {
		return fmt.Errorf("%w: got %q", ErrInvalidApplicationID, id)
	}
	return nil
}

// Kind classifies a failed call.
type Kind int

const (
	// KindNetwork: the request did not complete (connection, timeout, context).
	KindNetwork Kind = iota + 1
	// KindServer: payments-service answered 5xx.
	KindServer
	// KindRefused: payments-service answered 4xx.
	KindRefused
)

func (k Kind) String() string {
	switch k {
	case KindNetwork:
		return "network"
	case KindServer:
		return "server"
	case KindRefused:
		return "refused"
	}
	return "unknown"
}

// Error is a failed call to payments-service.
type Error struct {
	Kind Kind
	// StatusCode is the HTTP status; zero for KindNetwork.
	StatusCode int
	// Detail is payments-service's error body for KindRefused, truncated to
	// 300 bytes. Empty otherwise.
	Detail string
	// Cause is the transport error for KindNetwork.
	Cause error
}

// Error renders the same text the per-service clients produced, which food
// relays in its FOOD_PAYMENTS_* error messages and commerce stores as a
// refund command's last_error.
func (e *Error) Error() string {
	switch e.Kind {
	case KindNetwork:
		return fmt.Sprintf("%v: %v", ErrUnavailable, e.Cause)
	case KindServer:
		return fmt.Sprintf("%v: status %d", ErrUnavailable, e.StatusCode)
	default:
		return fmt.Sprintf("%v: status %d: %s", ErrRefused, e.StatusCode, e.Detail)
	}
}

// Is lets errors.Is match ErrUnavailable (network, 5xx) and ErrRefused (4xx).
func (e *Error) Is(target error) bool {
	switch target {
	case ErrUnavailable:
		return e.Kind == KindNetwork || e.Kind == KindServer
	case ErrRefused:
		return e.Kind == KindRefused
	}
	return false
}

// Auth is the credential configuration. The caller reads its own env vars
// and states its own legacy rule; the client only applies the result.
type Auth struct {
	// TokenKey is the caller's base64 Ed25519 private key. When set, the
	// client authenticates with service tokens and never with the legacy key.
	TokenKey string
	// TokenKID is the key id payments-service registered for TokenKey.
	TokenKID string
	// InternalKey is the legacy shared X-Internal-Service-Key.
	InternalKey string
	// LegacyAllowed is the caller's verdict that its environment is local or
	// dev. InternalKey is used only when TokenKey is empty AND this is true.
	LegacyAllowed bool
}

// Config builds a Client.
type Config struct {
	// BaseURL of payments-service, e.g. http://payments-service:8102.
	BaseURL string
	// Service is the caller's identity: token issuer and subject.
	Service string
	// ReferenceType is the one payments reference type this caller owns.
	ReferenceType string
	Auth          Auth
	// HTTPClient is used when set (a tracing transport, say). Nil means a
	// client with DefaultTimeout over http.DefaultTransport.
	HTTPClient *http.Client
	// MaxResponseBytes caps how much of a response body is read. Zero reads
	// the whole body.
	MaxResponseBytes int64
	// ValidateMethod, when set, refuses a CreateIntent whose method it
	// rejects, before anything is sent.
	ValidateMethod func(method string) error
	// VerifyReferenceEcho refuses an intent whose echoed reference_type or
	// reference_id is not the one requested. The amount echo is always
	// checked.
	VerifyReferenceEcho bool
	// RequirePositiveRefund refuses a refund of zero or less before anything
	// is sent. Without it such a refund is left to payments-service to refuse
	// (a 4xx, which a refund worker treats as terminal).
	RequirePositiveRefund bool
}

// Client talks to payments-service. It is safe for concurrent use.
type Client struct {
	cfg         Config
	baseURL     string
	signer      *servicetoken.Signer
	internalKey string
	http        *http.Client
}

// New validates cfg and builds a Client.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("payments client: base URL is required")
	}
	if strings.TrimSpace(cfg.Service) == "" {
		return nil, fmt.Errorf("payments client: calling service name is required")
	}
	if strings.TrimSpace(cfg.ReferenceType) == "" {
		return nil, fmt.Errorf("payments client: reference type is required")
	}
	c := &Client{cfg: cfg, baseURL: strings.TrimRight(cfg.BaseURL, "/"), http: cfg.HTTPClient}
	if c.http == nil {
		c.http = &http.Client{Timeout: DefaultTimeout}
	}
	switch {
	case cfg.Auth.TokenKey != "":
		signer, err := servicetoken.NewSignerFromBase64(cfg.Service, cfg.Auth.TokenKID, cfg.Auth.TokenKey)
		if err != nil {
			return nil, fmt.Errorf("payments client: signing key: %w", err)
		}
		c.signer = signer
	case !cfg.Auth.LegacyAllowed:
		return nil, ErrServiceTokenRequired
	case strings.TrimSpace(cfg.Auth.InternalKey) == "":
		return nil, fmt.Errorf("payments client: internal service key is required in legacy mode")
	default:
		c.internalKey = cfg.Auth.InternalKey
	}
	return c, nil
}

// LegacyAuth reports whether this client presents the shared internal key
// rather than a service token.
func (c *Client) LegacyAuth() bool { return c != nil && c.signer == nil }

// ReferenceType is the reference type this client is bound to.
func (c *Client) ReferenceType() string { return c.cfg.ReferenceType }

// ClientSession is what a client SDK needs to open checkout: the provider,
// its order handle and the PUBLISHABLE key. It is a struct, not a map, so no
// other value can be relayed; a key secret is never here.
type ClientSession struct {
	Provider string `json:"provider"`
	OrderID  string `json:"order_id"`
	KeyID    string `json:"key_id"`
}

// AsMap renders the session as the map shape commerce and food carry on
// their own intent types: exactly the three public keys, or nil.
func (s *ClientSession) AsMap() map[string]string {
	if s == nil {
		return nil
	}
	return map[string]string{"provider": s.Provider, "order_id": s.OrderID, "key_id": s.KeyID}
}

// Intent is a payments-service intent as a caller sees it.
type Intent struct {
	ID            uuid.UUID `json:"id"`
	Status        string    `json:"status"`
	AmountMinor   int64     `json:"amount_minor"`
	Currency      string    `json:"currency"`
	Method        string    `json:"method"`
	ProviderRef   string    `json:"provider_ref"`
	ReferenceType string    `json:"reference_type"`
	ReferenceID   uuid.UUID `json:"reference_id"`
	PayerID       uuid.UUID `json:"payer_id"`
	PayeeID       uuid.UUID `json:"payee_id"`
	// ApplicationID is the application the intent belongs to. Empty until
	// payments-service stores and echoes it.
	ApplicationID string `json:"application_id,omitempty"`
	// ClientSession is present only when payments-service has a provider
	// adapter that can derive one (Razorpay). Absent for the stub gateway and
	// for providers whose session is not derivable from the order id alone.
	ClientSession *ClientSession `json:"client_session,omitempty"`
}

// CreateIntentRequest is a server-authored payment request. The reference
// type is the client's own.
type CreateIntentRequest struct {
	// ApplicationID is required: the application this payment belongs to.
	ApplicationID string
	ReferenceID   uuid.UUID
	PayerID       uuid.UUID
	PayeeID       uuid.UUID
	// AmountMinor is integer minor units, from the caller's own record.
	AmountMinor int64
	// Currency defaults to DefaultCurrency.
	Currency string
	Method   string
	// IdempotencyKey must be deterministic for the referenced record, so a
	// retry collapses to one intent.
	IdempotencyKey string
}

// CreateIntent opens a payment. The echoed amount (and, with
// VerifyReferenceEcho, the echoed reference) is checked rather than trusted.
func (c *Client) CreateIntent(ctx context.Context, in CreateIntentRequest) (*Intent, error) {
	if in.AmountMinor <= 0 {
		return nil, fmt.Errorf("payments client: refusing an intent for %d paise", in.AmountMinor)
	}
	if c.cfg.ValidateMethod != nil {
		if err := c.cfg.ValidateMethod(in.Method); err != nil {
			return nil, fmt.Errorf("payments client: %w", err)
		}
	}
	if err := ValidateApplicationID(in.ApplicationID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return nil, fmt.Errorf("payments client: an intent idempotency key is required")
	}
	currency := in.Currency
	if currency == "" {
		currency = DefaultCurrency
	}
	body := map[string]any{
		"application_id":  in.ApplicationID,
		"payer_id":        in.PayerID,
		"payee_id":        in.PayeeID,
		"reference_type":  c.cfg.ReferenceType,
		"reference_id":    in.ReferenceID,
		"amount_minor":    in.AmountMinor,
		"currency":        currency,
		"method":          in.Method,
		"idempotency_key": in.IdempotencyKey,
	}
	var out Intent
	if err := c.do(ctx, http.MethodPost, InternalBase+"/intents", servicetoken.OpIntentCreate, body, &out); err != nil {
		return nil, err
	}
	if out.AmountMinor != in.AmountMinor {
		return nil, fmt.Errorf("payments client: intent amount %d does not match the order total %d", out.AmountMinor, in.AmountMinor)
	}
	if c.cfg.VerifyReferenceEcho && (out.ReferenceType != c.cfg.ReferenceType || out.ReferenceID != in.ReferenceID) {
		return nil, fmt.Errorf("payments client: intent reference %s/%s is not %s/%s",
			out.ReferenceType, out.ReferenceID, c.cfg.ReferenceType, in.ReferenceID)
	}
	return &out, nil
}

// GetIntent reads an intent's current state.
func (c *Client) GetIntent(ctx context.Context, id uuid.UUID) (*Intent, error) {
	var out Intent
	if err := c.do(ctx, http.MethodGet, InternalBase+"/intents/"+id.String(), servicetoken.OpIntentRead, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CallbackRequest is a client-returned checkout payload.
type CallbackRequest struct {
	ProviderOrderID   string
	ProviderPaymentID string
	Signature         string
	// ExpectedAmountMinor is the caller's own total, never the client's.
	ExpectedAmountMinor int64
}

// CallbackVerdict is ADVISORY. The parties and reference are echoed so the
// caller can REFUSE a genuine signature that belongs to another record or
// another payer; a positive verdict never means "paid".
type CallbackVerdict struct {
	Verified      bool      `json:"verified"`
	Advisory      bool      `json:"advisory"`
	Status        string    `json:"status"`
	AmountMinor   int64     `json:"amount_minor"`
	PayerID       uuid.UUID `json:"payer_id"`
	PayeeID       uuid.UUID `json:"payee_id"`
	ReferenceType string    `json:"reference_type"`
	ReferenceID   uuid.UUID `json:"reference_id"`
	// ApplicationID is empty until payments-service stores and echoes it.
	ApplicationID string `json:"application_id,omitempty"`
}

// VerifyCallback checks a client checkout callback against an intent.
func (c *Client) VerifyCallback(ctx context.Context, intentID uuid.UUID, in CallbackRequest) (*CallbackVerdict, error) {
	body := map[string]any{
		"razorpay_order_id":   in.ProviderOrderID,
		"razorpay_payment_id": in.ProviderPaymentID,
		"razorpay_signature":  in.Signature,
		"amount_minor":        in.ExpectedAmountMinor,
	}
	var out CallbackVerdict
	if err := c.do(ctx, http.MethodPost, InternalBase+"/intents/"+intentID.String()+"/verify", servicetoken.OpIntentRead, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RefundRequest asks for a refund of an explicit amount.
type RefundRequest struct {
	// ApplicationID is required: the application the refunded payment
	// belongs to.
	ApplicationID string
	AmountMinor   int64
	Reason        string
	// IdempotencyKey is required and must be the SAME on every retry, so an
	// ambiguous timeout followed by a retry produces one refund at the PSP.
	IdempotencyKey string
}

// RefundAccepted acknowledges a durable refund command. Money has NOT moved
// when this returns; payment.refunded says when it has.
type RefundAccepted struct {
	CommandID   uuid.UUID `json:"command_id"`
	IntentID    uuid.UUID `json:"intent_id"`
	AmountMinor int64     `json:"amount_minor"`
	Status      string    `json:"status"`
	// ApplicationID is empty until payments-service stores and echoes it.
	ApplicationID string `json:"application_id,omitempty"`
}

// Refund files a durable refund command.
func (c *Client) Refund(ctx context.Context, intentID uuid.UUID, in RefundRequest) (*RefundAccepted, error) {
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return nil, fmt.Errorf("payments client: a refund idempotency key is required")
	}
	if err := ValidateApplicationID(in.ApplicationID); err != nil {
		return nil, err
	}
	if c.cfg.RequirePositiveRefund && in.AmountMinor <= 0 {
		return nil, fmt.Errorf("payments client: refusing a refund of %d paise", in.AmountMinor)
	}
	body := map[string]any{
		"application_id":  in.ApplicationID,
		"amount_minor":    in.AmountMinor,
		"reason":          in.Reason,
		"idempotency_key": in.IdempotencyKey,
	}
	var out RefundAccepted
	if err := c.do(ctx, http.MethodPost, InternalBase+"/intents/"+intentID.String()+"/refund", servicetoken.OpRefundCreate, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// do performs one authenticated call and decodes the shared {"data": …}
// envelope, falling back to a bare body.
func (c *Client) do(ctx context.Context, method, path, op string, body, out any) error {
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
	req.Header.Set("Content-Type", "application/json")
	if err := c.authenticate(req, op); err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return &Error{Kind: KindNetwork, Cause: err}
	}
	defer resp.Body.Close()
	var src io.Reader = resp.Body
	if c.cfg.MaxResponseBytes > 0 {
		src = io.LimitReader(resp.Body, c.cfg.MaxResponseBytes)
	}
	raw, _ := io.ReadAll(src)

	if resp.StatusCode >= 500 {
		return &Error{Kind: KindServer, StatusCode: resp.StatusCode}
	}
	if resp.StatusCode >= 400 {
		return &Error{Kind: KindRefused, StatusCode: resp.StatusCode, Detail: truncate(string(raw), errorDetailLimit)}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err == nil && len(env.Data) > 0 {
		return json.Unmarshal(env.Data, out)
	}
	return json.Unmarshal(raw, out)
}

// authenticate stamps exactly one credential.
func (c *Client) authenticate(req *http.Request, op string) error {
	if c.signer == nil {
		req.Header.Set(HeaderInternalServiceKey, c.internalKey)
		return nil
	}
	token, err := c.signer.Mint(servicetoken.AudiencePayments, c.cfg.Service,
		[]string{op}, []string{c.cfg.ReferenceType}, TokenTTL)
	if err != nil {
		return fmt.Errorf("payments client: mint token: %w", err)
	}
	req.Header.Set(HeaderServiceAuthorization, "Bearer "+token)
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
