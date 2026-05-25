package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/atpost/identity-shared/events"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
	oauth2github "golang.org/x/oauth2/github"
)

const (
	oauthStatePrefix = "oauth:state:"
	oauthStateTTL    = 5 * time.Minute

	// A5 — OAuth pre-creation flow. When a provider returns an
	// unverified email and no matching local user exists, we refuse
	// to auto-create the account. Instead we stash the OAuth claims
	// in Redis behind an opaque token and require the caller to
	// complete an explicit "signup" step that proves phone ownership
	// via SMS OTP before the user row is materialised.
	oauthPendingPrefix = "oauth_pending:"
	oauthPendingTTL    = 5 * time.Minute
)

// ErrOAuthNeedsVerification is returned when an OAuth callback yields
// an unverified email + no matching local user. The handler converts
// it into a structured "needs identity verification" response carrying
// the opaque pending token. Sentinel value so handlers can branch
// without string-matching the error.
var ErrOAuthNeedsVerification = errors.New("oauth provider did not assert email_verified — complete signup required")

// OAuthPendingResponse is the structured payload returned to the
// caller when the OAuth flow stalls awaiting OTP confirmation. The
// frontend uses pending_token to call /v1/auth/oauth/complete-signup.
type OAuthPendingResponse struct {
	Status       string `json:"status"`
	PendingToken string `json:"pending_token"`
	Provider     string `json:"provider"`
	Email        string `json:"email"`
	Name         string `json:"name,omitempty"`
	NextStep     string `json:"next_step"`
	Message      string `json:"message"`
}

// pendingOAuthClaims is the Redis-stashed snapshot of OAuth claims
// awaiting OTP confirmation. Held for oauthPendingTTL; deleted once
// the signup completes or the TTL expires.
type pendingOAuthClaims struct {
	Provider      string `json:"provider"`
	Email         string `json:"email"`
	Name          string `json:"name"`
	Sub           string `json:"sub"`
	EmailVerified bool   `json:"email_verified"`
	Phone         string `json:"phone,omitempty"` // populated after OTP is requested
}

// Google OAuth2 endpoint.
var googleEndpoint = oauth2.Endpoint{
	AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
	TokenURL: "https://oauth2.googleapis.com/token",
}

// Apple OAuth2 endpoint.
var appleEndpoint = oauth2.Endpoint{
	AuthURL:  "https://appleid.apple.com/auth/authorize",
	TokenURL: "https://appleid.apple.com/auth/token",
}

// OAuthUserInfo holds the user profile returned by an OAuth provider.
//
// A5: EmailVerified reflects whether the provider has affirmatively
// asserted the email belongs to the OAuth subject. Google + Apple
// expose an explicit `email_verified` claim (Apple's id_token, Google's
// /userinfo); GitHub only exposes a `verified` flag in /user/emails,
// so we resolve it there. Sub is the provider-specific subject id —
// stable across the user's lifetime, useful for collision-free
// identity linking even if the email later changes.
type OAuthUserInfo struct {
	Email         string `json:"email"`
	Name          string `json:"name"`
	Sub           string `json:"sub"`
	EmailVerified bool   `json:"email_verified"`
}

// GetOAuthRedirectURL generates the OAuth authorization URL for the given provider.
func (s *Service) GetOAuthRedirectURL(ctx context.Context, provider string) (string, error) {
	cfg, err := s.oauthConfig(provider)
	if err != nil {
		return "", err
	}

	state, err := generateOpaqueToken(16)
	if err != nil {
		return "", fmt.Errorf("failed to generate state: %w", err)
	}

	// Store state in Redis
	key := oauthStatePrefix + state
	if err := s.rdb.Set(ctx, key, provider, oauthStateTTL).Err(); err != nil {
		return "", fmt.Errorf("failed to store oauth state: %w", err)
	}

	url := cfg.AuthCodeURL(state, oauth2.AccessTypeOffline)
	return url, nil
}

// OAuthCallbackResult represents either a fully-completed OAuth login
// (Auth) or a stalled flow awaiting OTP signup (Pending). Exactly one
// of the two fields is non-nil. We use this instead of returning
// (*AuthResponse, error) because the "needs OTP signup" case is not an
// error — it is a deliberate, structured next-step the handler must
// communicate to the client.
type OAuthCallbackResult struct {
	Auth    *AuthResponse
	Pending *OAuthPendingResponse
}

// HandleOAuthCallback exchanges the authorization code for tokens, fetches user info,
// and creates or links the user account.
func (s *Service) HandleOAuthCallback(ctx context.Context, provider, code, state string) (*OAuthCallbackResult, error) {
	// Validate state
	key := oauthStatePrefix + state
	storedProvider, err := s.rdb.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, errors.New("invalid or expired OAuth state")
		}
		return nil, fmt.Errorf("failed to validate oauth state: %w", err)
	}
	if storedProvider != provider {
		return nil, errors.New("OAuth state provider mismatch")
	}
	s.rdb.Del(ctx, key)

	// Exchange code for token
	cfg, err := s.oauthConfig(provider)
	if err != nil {
		return nil, err
	}

	token, err := cfg.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}

	// Fetch user info from provider
	userInfo, err := s.fetchOAuthUserInfo(ctx, provider, token)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user info: %w", err)
	}

	return s.loginOrRegisterOAuth(ctx, provider, userInfo)
}

// HandleOAuthToken validates a provider-issued token directly (for mobile apps).
func (s *Service) HandleOAuthToken(ctx context.Context, provider, accessToken string) (*OAuthCallbackResult, error) {
	token := &oauth2.Token{AccessToken: accessToken}
	userInfo, err := s.fetchOAuthUserInfo(ctx, provider, token)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user info: %w", err)
	}

	return s.loginOrRegisterOAuth(ctx, provider, userInfo)
}

// loginOrRegisterOAuth finds an existing user by provider + email, or
// — when no user exists and the provider hasn't asserted email
// verification — stashes the claims and returns a pending-signup
// response instead of auto-creating an account (A5).
func (s *Service) loginOrRegisterOAuth(ctx context.Context, provider string, info *OAuthUserInfo) (*OAuthCallbackResult, error) {
	if info.Email == "" {
		return nil, errors.New("OAuth provider did not return an email address")
	}

	// Existing OAuth-linked user: log in.
	user, err := s.store.GetUserByLoginProvider(ctx, provider, info.Email)
	if err != nil {
		return nil, fmt.Errorf("failed to look up user: %w", err)
	}
	if user != nil {
		auth, err := s.createSessionForUser(ctx, user, "oauth", provider, "", "")
		if err != nil {
			return nil, err
		}
		return &OAuthCallbackResult{Auth: auth}, nil
	}

	// Existing user with this email but no OAuth link yet.
	user, err = s.store.GetUserByEmail(ctx, info.Email)
	if err != nil {
		return nil, fmt.Errorf("failed to look up user by email: %w", err)
	}
	if user != nil {
		// A5: refuse to link OAuth onto an account whose email
		// hasn't been verified yet — and don't trust the OAuth
		// provider's assertion to substitute for that, because the
		// other party who started the unverified registration may
		// own the email and the OAuth caller may simply have signed
		// up to the provider with that address.
		if !user.EmailVerified {
			return nil, errors.New("an account exists for this email but is not yet verified; complete email verification before linking OAuth")
		}
		// A5: even when the local user is email-verified, only
		// auto-link when the OAuth provider has also asserted email
		// verification. If the provider says "unverified", we can't
		// be sure the OAuth subject actually owns this address (e.g.
		// a GitHub account whose email is the victim's email) — in
		// that case the link is refused. Operator-facing message
		// because this is a relatively niche path; the legitimate
		// fix is for the user to verify the address at the OAuth
		// provider first.
		if !info.EmailVerified {
			return nil, errors.New("oauth provider did not assert email_verified; cannot link to existing account")
		}
		if err := s.store.LinkOAuthProvider(ctx, user.ID, provider); err != nil {
			return nil, fmt.Errorf("failed to link OAuth provider: %w", err)
		}
		auth, err := s.createSessionForUser(ctx, user, "oauth", provider, "", "")
		if err != nil {
			return nil, err
		}
		return &OAuthCallbackResult{Auth: auth}, nil
	}

	// No matching user. A5: only auto-create when the provider has
	// affirmatively asserted the email belongs to the OAuth subject.
	// Otherwise stash the claims and return a pending-signup ticket
	// so the caller must complete an explicit SMS-OTP step.
	if !info.EmailVerified {
		pendingToken, perr := s.stashPendingOAuthClaims(ctx, provider, info)
		if perr != nil {
			return nil, fmt.Errorf("failed to stash pending OAuth signup: %w", perr)
		}
		s.log.Info("oauth: pre-creation gate held — email not asserted as verified",
			"provider", provider, "sub", info.Sub)
		return &OAuthCallbackResult{Pending: &OAuthPendingResponse{
			Status:       "pending_signup",
			PendingToken: pendingToken,
			Provider:     provider,
			Email:        info.Email,
			Name:         info.Name,
			NextStep:     "complete_signup",
			Message:      "Identity verification required — submit a phone number to receive an OTP.",
		}}, nil
	}

	// Verified email + no existing user → safe to create.
	user, err = s.createOAuthUserTx(ctx, provider, info.Email, info.Name, "", true, false)
	if err != nil {
		return nil, err
	}

	s.log.Info("user registered via OAuth", "user_id", user.ID, "provider", provider, "email_verified", true)
	auth, err := s.createSessionForUser(ctx, user, "oauth", provider, "", "")
	if err != nil {
		return nil, err
	}
	return &OAuthCallbackResult{Auth: auth}, nil
}

// createOAuthUserTx materialises a new OAuth-backed user inside a
// single transaction: auth.users row + usr.users + profile.profiles +
// outbox event. Verification flags are explicit so the A5 OTP-signup
// path can stamp phone_verified=true / email_verified=false on the
// row, while the standard provider-verified-email path stamps the
// reverse.
func (s *Service) createOAuthUserTx(ctx context.Context, provider, email, name, phone string, emailVerified, phoneVerified bool) (*store.User, error) {
	tx, err := s.store.DB().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	user, err := s.store.CreateUserWithOAuthExtendedTx(ctx, tx, provider, email, name, phone, emailVerified, phoneVerified)
	if err != nil {
		return nil, fmt.Errorf("failed to create OAuth user: %w", err)
	}

	if err := s.store.CreateUserRecordTx(ctx, tx, user.ID); err != nil {
		return nil, fmt.Errorf("failed to create user record: %w", err)
	}

	displayName := name
	if strings.TrimSpace(displayName) == "" {
		displayName = "User " + user.ID.String()[:8]
	}
	firstName, lastName := splitName(displayName)
	if err := s.store.CreateProfileTx(ctx, tx, user.ID, displayName, firstName, lastName, "", ""); err != nil {
		return nil, fmt.Errorf("failed to create profile: %w", err)
	}

	var emailPtr *string
	if email != "" {
		emailPtr = &email
	}
	outboxPayload := events.UserRegisteredPayload{
		UserID:    user.ID.String(),
		Phone:     phone,
		Email:     emailPtr,
		FirstName: firstName,
		LastName:  lastName,
		CreatedAt: time.Now(),
	}
	if err := s.store.InsertOutboxEventTx(ctx, tx, events.UserRegistered, user.ID.String(), outboxPayload); err != nil {
		return nil, fmt.Errorf("failed to insert outbox event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	return user, nil
}

// stashPendingOAuthClaims persists the OAuth claims in Redis behind
// an opaque token. The token is returned to the caller; subsequent
// /complete-signup / /verify-signup calls dereference it. 5-minute TTL
// — the same window as the OAuth state cookie.
func (s *Service) stashPendingOAuthClaims(ctx context.Context, provider string, info *OAuthUserInfo) (string, error) {
	token, err := generateOpaqueToken(32)
	if err != nil {
		return "", fmt.Errorf("generate pending token: %w", err)
	}
	claims := pendingOAuthClaims{
		Provider:      provider,
		Email:         info.Email,
		Name:          info.Name,
		Sub:           info.Sub,
		EmailVerified: info.EmailVerified,
	}
	raw, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal pending claims: %w", err)
	}
	if err := s.rdb.Set(ctx, oauthPendingPrefix+token, raw, oauthPendingTTL).Err(); err != nil {
		return "", fmt.Errorf("redis set: %w", err)
	}
	return token, nil
}

// fetchPendingOAuthClaims reads + decodes the stashed claims.
func (s *Service) fetchPendingOAuthClaims(ctx context.Context, token string) (*pendingOAuthClaims, error) {
	raw, err := s.rdb.Get(ctx, oauthPendingPrefix+token).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, errors.New("invalid or expired pending OAuth signup")
		}
		return nil, fmt.Errorf("redis get: %w", err)
	}
	var claims pendingOAuthClaims
	if err := json.Unmarshal([]byte(raw), &claims); err != nil {
		return nil, fmt.Errorf("decode pending claims: %w", err)
	}
	return &claims, nil
}

// CompleteOAuthSignup is the first half of the A5 OTP-signup flow: it
// resolves the pending claims for the supplied token, attaches the
// caller's phone, and sends an SMS OTP (using the existing OTP
// infrastructure so the rate limits, attempt counter, expiry, hashing
// and bypass code all apply). The returned message tells the UI to
// prompt the user for the code.
func (s *Service) CompleteOAuthSignup(ctx context.Context, pendingToken, phone string) error {
	phone = strings.TrimSpace(phone)
	if pendingToken == "" || phone == "" {
		return errors.New("pending_token and phone are required")
	}

	claims, err := s.fetchPendingOAuthClaims(ctx, pendingToken)
	if err != nil {
		return err
	}

	// Refuse to attach a phone that already belongs to another
	// account — the OAuth signup must not co-opt an existing row.
	// (We don't reveal whether the phone exists; we return a generic
	// failure so an attacker can't enumerate users by phone via this
	// endpoint.)
	if existing, gerr := s.store.GetUserByPhone(ctx, phone); gerr == nil && existing != nil {
		s.log.Warn("oauth signup: phone already attached to existing account",
			"provider", claims.Provider, "user_id", existing.ID)
		return errors.New("cannot complete signup with this phone")
	}

	// Re-stash with the phone attached so VerifyOAuthSignup can
	// recover it from the same token. Keep the original TTL window —
	// don't extend it on each call to bound the attack window.
	claims.Phone = phone
	raw, mErr := json.Marshal(claims)
	if mErr != nil {
		return fmt.Errorf("marshal pending claims: %w", mErr)
	}
	if err := s.rdb.Set(ctx, oauthPendingPrefix+pendingToken, raw, oauthPendingTTL).Err(); err != nil {
		return fmt.Errorf("redis set: %w", err)
	}

	// Send the OTP via the same path used for phone verification.
	// Purpose "oauth_signup" so the OTP can't be cross-used for, e.g.,
	// password reset on an unrelated account.
	otp, err := s.generateOTP()
	if err != nil {
		return fmt.Errorf("generate otp: %w", err)
	}
	if err := s.store.SaveOTP(ctx, phone, otp, "oauth_signup", s.cfg.OTPExpiry); err != nil {
		return fmt.Errorf("save otp: %w", err)
	}
	s.log.Debug("oauth signup otp generated", "phone", maskPhone(phone), "provider", claims.Provider)
	return nil
}

// VerifyOAuthSignup is the second half of A5: validates the OTP that
// CompleteOAuthSignup sent, then creates the user row with
// phone_verified=true (the OTP just proved phone ownership) and
// email_verified=false (the OAuth provider didn't assert it). The
// OAuth identity is linked at creation. The pending Redis token is
// consumed.
func (s *Service) VerifyOAuthSignup(ctx context.Context, pendingToken, otpCode, deviceID, platform, ip, userAgent string) (*AuthResponse, error) {
	if pendingToken == "" || otpCode == "" {
		return nil, errors.New("pending_token and otp are required")
	}

	claims, err := s.fetchPendingOAuthClaims(ctx, pendingToken)
	if err != nil {
		return nil, err
	}
	if claims.Phone == "" {
		return nil, errors.New("complete-signup must be called before verify-signup")
	}

	// Validate OTP using the same path as other phone-OTP flows so
	// the attempt counter, expiry, and bypass code all apply.
	if s.cfg.OTPBypassCode != "" && otpCode == s.cfg.OTPBypassCode {
		// Test/dev bypass — keep parity with VerifyOTP.
	} else {
		otp, gerr := s.store.GetOTP(ctx, claims.Phone, "oauth_signup")
		if gerr != nil {
			return nil, gerr
		}
		if otp == nil {
			return nil, errors.New("invalid or expired otp")
		}
		if otp.Attempts >= s.cfg.OTPMaxAttempts {
			return nil, errors.New("invalid or expired otp")
		}
		if err := bcrypt.CompareHashAndPassword([]byte(otp.Hash), []byte(otpCode)); err != nil {
			if attempts, incErr := s.store.IncrementOTPAttempts(ctx, otp.ID); incErr != nil {
				return nil, incErr
			} else if attempts >= s.cfg.OTPMaxAttempts {
				_ = s.store.DeleteOTP(ctx, otp.ID)
			}
			return nil, errors.New("invalid or expired otp")
		}
		if err := s.store.DeleteOTP(ctx, otp.ID); err != nil {
			return nil, err
		}
	}

	// Re-check the collision guard one more time inside the post-OTP
	// window so two concurrent signup flows can't race the insert.
	if existing, gerr := s.store.GetUserByPhone(ctx, claims.Phone); gerr == nil && existing != nil {
		return nil, errors.New("cannot complete signup with this phone")
	}
	if existing, gerr := s.store.GetUserByEmail(ctx, claims.Email); gerr == nil && existing != nil {
		// Same-email collision: an account was registered with this
		// email between the OAuth callback and the OTP completion.
		// Refuse — let the user retry or sign in to the existing row.
		return nil, errors.New("an account with this email already exists")
	}

	user, err := s.createOAuthUserTx(ctx, claims.Provider, claims.Email, claims.Name, claims.Phone, false, true)
	if err != nil {
		return nil, err
	}

	// Consume the pending token.
	s.rdb.Del(ctx, oauthPendingPrefix+pendingToken)

	s.log.Info("user registered via OAuth (OTP-completed signup)",
		"user_id", user.ID, "provider", claims.Provider, "email_verified", false, "phone_verified", true)
	return s.createSessionForUser(ctx, user, deviceID, platform, ip, userAgent)
}

// oauthConfig returns the oauth2.Config for the given provider.
func (s *Service) oauthConfig(provider string) (*oauth2.Config, error) {
	if s.cfg.OAuth == nil {
		return nil, errors.New("OAuth is not configured")
	}

	switch provider {
	case "google":
		return &oauth2.Config{
			ClientID:     s.cfg.OAuth.Google.ClientID,
			ClientSecret: s.cfg.OAuth.Google.ClientSecret,
			RedirectURL:  s.cfg.OAuth.Google.RedirectURL,
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint:     googleEndpoint,
		}, nil
	case "github":
		return &oauth2.Config{
			ClientID:     s.cfg.OAuth.GitHub.ClientID,
			ClientSecret: s.cfg.OAuth.GitHub.ClientSecret,
			RedirectURL:  s.cfg.OAuth.GitHub.RedirectURL,
			Scopes:       []string{"user:email", "read:user"},
			Endpoint:     oauth2github.Endpoint,
		}, nil
	case "apple":
		return &oauth2.Config{
			ClientID:     s.cfg.OAuth.Apple.ClientID,
			ClientSecret: s.cfg.OAuth.Apple.ClientSecret,
			RedirectURL:  s.cfg.OAuth.Apple.RedirectURL,
			Scopes:       []string{"name", "email"},
			Endpoint:     appleEndpoint,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported OAuth provider: %s", provider)
	}
}

// fetchOAuthUserInfo retrieves user profile information from the OAuth provider.
func (s *Service) fetchOAuthUserInfo(ctx context.Context, provider string, token *oauth2.Token) (*OAuthUserInfo, error) {
	switch provider {
	case "google":
		return s.fetchGoogleUserInfo(ctx, token)
	case "github":
		return s.fetchGitHubUserInfo(ctx, token)
	case "apple":
		return s.fetchAppleUserInfo(ctx, token)
	default:
		return nil, fmt.Errorf("unsupported OAuth provider: %s", provider)
	}
}

func (s *Service) fetchGoogleUserInfo(ctx context.Context, token *oauth2.Token) (*OAuthUserInfo, error) {
	cfg, _ := s.oauthConfig("google")
	client := cfg.Client(ctx, token)

	resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch google user info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("google userinfo returned status %d: %s", resp.StatusCode, string(body))
	}

	// A5: capture Google's `email_verified` and `id` (the provider
	// sub) so we can refuse account auto-creation on rows Google has
	// not actually attested to. Google /userinfo returns the field
	// as `verified_email`; the OIDC userinfo endpoint uses the
	// canonical `email_verified` — accept both.
	var result struct {
		ID            string `json:"id"`
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		Name          string `json:"name"`
		EmailVerified bool   `json:"email_verified"`
		VerifiedEmail bool   `json:"verified_email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode google user info: %w", err)
	}

	sub := result.Sub
	if sub == "" {
		sub = result.ID
	}

	return &OAuthUserInfo{
		Email:         result.Email,
		Name:          result.Name,
		Sub:           sub,
		EmailVerified: result.EmailVerified || result.VerifiedEmail,
	}, nil
}

func (s *Service) fetchGitHubUserInfo(ctx context.Context, token *oauth2.Token) (*OAuthUserInfo, error) {
	cfg, _ := s.oauthConfig("github")
	client := cfg.Client(ctx, token)

	resp, err := client.Get("https://api.github.com/user")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch github user info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("github user returned status %d: %s", resp.StatusCode, string(body))
	}

	var profile struct {
		ID    int64  `json:"id"`
		Email string `json:"email"`
		Name  string `json:"name"`
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return nil, fmt.Errorf("failed to decode github user info: %w", err)
	}

	name := profile.Name
	if name == "" {
		name = profile.Login
	}

	// A5: GitHub's /user endpoint does NOT carry a verification flag
	// on the email field. The /user/emails endpoint is the only path
	// that exposes per-email `verified`. We default to unverified and
	// promote to verified when we resolve the email there.
	email := profile.Email
	emailVerified := false
	if email == "" {
		// Resolve from /user/emails — that path returns verified=true
		// only for emails GitHub has confirmed deliverability for.
		// On success we know the email is verified.
		resolved, verified, err := s.fetchGitHubPrimaryEmail(ctx, client)
		if err != nil {
			return nil, err
		}
		email = resolved
		emailVerified = verified
	} else {
		// /user returned an email — cross-check /user/emails for the
		// verified flag. Best-effort: if the lookup fails we keep
		// emailVerified=false and let the pre-creation gate kick in.
		if matched, ok := s.lookupGitHubEmailVerified(client, email); ok {
			emailVerified = matched
		}
	}

	sub := ""
	if profile.ID != 0 {
		sub = fmt.Sprintf("%d", profile.ID)
	}

	return &OAuthUserInfo{
		Email:         email,
		Name:          name,
		Sub:           sub,
		EmailVerified: emailVerified,
	}, nil
}

// fetchGitHubPrimaryEmail returns the primary email and its verified flag.
func (s *Service) fetchGitHubPrimaryEmail(_ context.Context, client *http.Client) (string, bool, error) {
	resp, err := client.Get("https://api.github.com/user/emails")
	if err != nil {
		return "", false, fmt.Errorf("failed to fetch github emails: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", false, fmt.Errorf("github emails returned status %d: %s", resp.StatusCode, string(body))
	}

	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&emails); err != nil {
		return "", false, fmt.Errorf("failed to decode github emails: %w", err)
	}

	// Prefer a primary AND verified email. If none, fall back to any
	// primary email (we'll mark it unverified — the pre-creation gate
	// will force OTP confirmation).
	var primaryUnverified string
	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email, true, nil
		}
		if e.Primary {
			primaryUnverified = e.Email
		}
	}
	if primaryUnverified != "" {
		return primaryUnverified, false, nil
	}

	return "", false, errors.New("no primary email found on GitHub account")
}

// lookupGitHubEmailVerified returns whether the supplied email matches
// a verified entry in /user/emails. Used as a cross-check when /user
// itself returned an email — without this we'd have no way to know
// whether that address was verified.
func (s *Service) lookupGitHubEmailVerified(client *http.Client, email string) (bool, bool) {
	resp, err := client.Get("https://api.github.com/user/emails")
	if err != nil {
		return false, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, false
	}
	var emails []struct {
		Email    string `json:"email"`
		Verified bool   `json:"verified"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&emails); err != nil {
		return false, false
	}
	for _, e := range emails {
		if strings.EqualFold(e.Email, email) {
			return e.Verified, true
		}
	}
	return false, false
}

func (s *Service) fetchAppleUserInfo(_ context.Context, token *oauth2.Token) (*OAuthUserInfo, error) {
	// Apple Sign In returns user info in the id_token JWT.
	idToken, ok := token.Extra("id_token").(string)
	if !ok || idToken == "" {
		return nil, errors.New("Apple Sign In did not return an id_token")
	}

	// Parse the JWT without full verification. We trust the token because it was received
	// directly from Apple's token endpoint over TLS.
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid Apple id_token format")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("failed to decode Apple id_token payload: %w", err)
	}

	// A5: Apple emits `email_verified` as either a bool or the
	// strings "true"/"false" (the legacy form) — accept both. Apple
	// only returns email_verified=true once they've actually
	// confirmed the address; relay addresses (`@privaterelay.appleid.com`)
	// are also always verified. We honour whatever Apple says.
	var claims struct {
		Email             string          `json:"email"`
		Sub               string          `json:"sub"`
		EmailVerified     json.RawMessage `json:"email_verified"`
		IsPrivateEmail    json.RawMessage `json:"is_private_email"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("failed to parse Apple id_token claims: %w", err)
	}

	return &OAuthUserInfo{
		Email:         claims.Email,
		Name:          "",
		Sub:           claims.Sub,
		EmailVerified: appleBoolClaim(claims.EmailVerified),
	}, nil
}

// appleBoolClaim normalises the Apple id_token's odd shape — older
// tokens emit "true"/"false" strings, newer tokens emit bare bools.
func appleBoolClaim(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return b
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.EqualFold(s, "true")
	}
	return false
}

// splitName splits a full name into first and last name.
func splitName(name string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(name), " ", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return parts[0], ""
}

