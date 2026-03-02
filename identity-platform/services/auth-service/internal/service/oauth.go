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

	"github.com/identity-platform/shared/events"
	"github.com/redis/go-redis/v9"
	"golang.org/x/oauth2"
	oauth2github "golang.org/x/oauth2/github"
)

const (
	oauthStatePrefix = "oauth:state:"
	oauthStateTTL    = 5 * time.Minute
)

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
type OAuthUserInfo struct {
	Email string `json:"email"`
	Name  string `json:"name"`
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

// HandleOAuthCallback exchanges the authorization code for tokens, fetches user info,
// and creates or links the user account.
func (s *Service) HandleOAuthCallback(ctx context.Context, provider, code, state string) (*AuthResponse, error) {
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
func (s *Service) HandleOAuthToken(ctx context.Context, provider, accessToken string) (*AuthResponse, error) {
	token := &oauth2.Token{AccessToken: accessToken}
	userInfo, err := s.fetchOAuthUserInfo(ctx, provider, token)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user info: %w", err)
	}

	return s.loginOrRegisterOAuth(ctx, provider, userInfo)
}

// loginOrRegisterOAuth finds an existing user by provider + email, or creates a new one.
func (s *Service) loginOrRegisterOAuth(ctx context.Context, provider string, info *OAuthUserInfo) (*AuthResponse, error) {
	if info.Email == "" {
		return nil, errors.New("OAuth provider did not return an email address")
	}

	// Try to find existing user with this provider + email
	user, err := s.store.GetUserByLoginProvider(ctx, provider, info.Email)
	if err != nil {
		return nil, fmt.Errorf("failed to look up user: %w", err)
	}

	if user != nil {
		return s.createSessionForUser(ctx, user, "oauth", provider, "", "")
	}

	// Check if user exists with this email but different provider
	user, err = s.store.GetUserByEmail(ctx, info.Email)
	if err != nil {
		return nil, fmt.Errorf("failed to look up user by email: %w", err)
	}

	if user != nil {
		// Link OAuth provider to existing account
		if err := s.store.LinkOAuthProvider(ctx, user.ID, provider); err != nil {
			return nil, fmt.Errorf("failed to link OAuth provider: %w", err)
		}
		return s.createSessionForUser(ctx, user, "oauth", provider, "", "")
	}

	// Create a new user with OAuth
	tx, err := s.store.DB().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	user, err = s.store.CreateUserWithOAuthTx(ctx, tx, provider, info.Email, info.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to create OAuth user: %w", err)
	}

	if err := s.store.CreateUserRecordTx(ctx, tx, user.ID); err != nil {
		return nil, fmt.Errorf("failed to create user record: %w", err)
	}

	displayName := info.Name
	if strings.TrimSpace(displayName) == "" {
		displayName = "User " + user.ID.String()[:8]
	}
	firstName, lastName := splitName(displayName)
	if err := s.store.CreateProfileTx(ctx, tx, user.ID, displayName, firstName, lastName, "", ""); err != nil {
		return nil, fmt.Errorf("failed to create profile: %w", err)
	}

	outboxPayload := events.UserRegisteredPayload{
		UserID:    user.ID.String(),
		Email:     &info.Email,
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

	s.log.Info("user registered via OAuth", "user_id", user.ID, "provider", provider)
	return s.createSessionForUser(ctx, user, "oauth", provider, "", "")
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

	var result struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode google user info: %w", err)
	}

	return &OAuthUserInfo{Email: result.Email, Name: result.Name}, nil
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

	email := profile.Email
	if email == "" {
		email, err = s.fetchGitHubPrimaryEmail(ctx, client)
		if err != nil {
			return nil, err
		}
	}

	return &OAuthUserInfo{Email: email, Name: name}, nil
}

func (s *Service) fetchGitHubPrimaryEmail(_ context.Context, client *http.Client) (string, error) {
	resp, err := client.Get("https://api.github.com/user/emails")
	if err != nil {
		return "", fmt.Errorf("failed to fetch github emails: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("github emails returned status %d: %s", resp.StatusCode, string(body))
	}

	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&emails); err != nil {
		return "", fmt.Errorf("failed to decode github emails: %w", err)
	}

	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email, nil
		}
	}

	return "", errors.New("no verified primary email found on GitHub account")
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

	var claims struct {
		Email string `json:"email"`
		Sub   string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("failed to parse Apple id_token claims: %w", err)
	}

	return &OAuthUserInfo{Email: claims.Email, Name: ""}, nil
}

// splitName splits a full name into first and last name.
func splitName(name string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(name), " ", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return parts[0], ""
}

