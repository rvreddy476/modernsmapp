package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

const (
	pendingSessionPrefix = "2fa:pending:"
	recoveryCodesPrefix  = "2fa:recovery:"
	pendingSessionTTL    = 5 * time.Minute
	recoveryCodeCount    = 8
)

// TwoFASetupResponse is returned when a user initiates 2FA setup.
type TwoFASetupResponse struct {
	Secret        string   `json:"secret"`
	QRCodeURL     string   `json:"qr_code_url"`
	RecoveryCodes []string `json:"recovery_codes"`
}

// PendingTwoFASession is stored in Redis while awaiting 2FA verification during login.
type PendingTwoFASession struct {
	UserID    string `json:"user_id"`
	DeviceID  string `json:"device_id"`
	Platform  string `json:"platform"`
	IP        string `json:"ip"`
	UserAgent string `json:"user_agent"`
}

// Setup2FA generates a TOTP secret and recovery codes for the user.
func (s *Service) Setup2FA(ctx context.Context, userID uuid.UUID) (*TwoFASetupResponse, error) {
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	if user == nil {
		return nil, errors.New("user not found")
	}
	if user.TwoFactorEnabled {
		return nil, errors.New("2FA is already enabled")
	}

	// Determine account name for the TOTP key
	accountName := user.Phone
	if user.Email != nil && *user.Email != "" {
		accountName = *user.Email
	}
	if accountName == "" {
		accountName = userID.String()
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      s.cfg.TwoFAIssuer,
		AccountName: accountName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to generate TOTP key: %w", err)
	}

	// Store the secret temporarily in Redis until verification completes
	if err := s.rdb.Set(ctx, "2fa:setup:"+userID.String(), key.Secret(), 10*time.Minute).Err(); err != nil {
		return nil, fmt.Errorf("failed to store setup secret: %w", err)
	}

	// Generate recovery codes
	codes, err := generateRecoveryCodes(recoveryCodeCount)
	if err != nil {
		return nil, fmt.Errorf("failed to generate recovery codes: %w", err)
	}

	// Store hashed recovery codes temporarily in Redis until verification
	if err := s.storeRecoveryCodesTemp(ctx, userID, codes); err != nil {
		return nil, fmt.Errorf("failed to store recovery codes: %w", err)
	}

	return &TwoFASetupResponse{
		Secret:        key.Secret(),
		QRCodeURL:     key.URL(),
		RecoveryCodes: codes,
	}, nil
}

// Verify2FASetup confirms the user can generate valid TOTP codes and enables 2FA.
func (s *Service) Verify2FASetup(ctx context.Context, userID uuid.UUID, code string) error {
	// Retrieve the pending secret from Redis
	secret, err := s.rdb.Get(ctx, "2fa:setup:"+userID.String()).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return errors.New("no pending 2FA setup found; please call setup first")
		}
		return fmt.Errorf("failed to retrieve setup secret: %w", err)
	}

	// Validate the TOTP code
	if !totp.Validate(code, secret) {
		return errors.New("invalid TOTP code")
	}

	// Enable 2FA in the database
	if err := s.store.Enable2FA(ctx, userID, secret); err != nil {
		return fmt.Errorf("failed to enable 2FA: %w", err)
	}

	// Move recovery codes from temp to permanent storage
	if err := s.promoteRecoveryCodes(ctx, userID); err != nil {
		s.log.Warn("failed to promote recovery codes", "err", err, "user_id", userID)
	}

	// Clean up temp setup key
	s.rdb.Del(ctx, "2fa:setup:"+userID.String())

	s.log.Info("2FA enabled", "user_id", userID)
	return nil
}

// Disable2FA turns off 2FA for a user after verifying their password and TOTP code.
func (s *Service) Disable2FA(ctx context.Context, userID uuid.UUID, password, code string) error {
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}
	if user == nil {
		return errors.New("user not found")
	}
	if !user.TwoFactorEnabled {
		return errors.New("2FA is not enabled")
	}

	// Verify password
	if user.PasswordHash == "" {
		return errors.New("user has no password set")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return errors.New("invalid password")
	}

	// Verify TOTP code
	secret, err := s.store.Get2FASecret(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get 2FA secret: %w", err)
	}
	if !totp.Validate(code, secret) {
		return errors.New("invalid TOTP code")
	}

	// Disable 2FA
	if err := s.store.Disable2FA(ctx, userID); err != nil {
		return fmt.Errorf("failed to disable 2FA: %w", err)
	}

	// Remove recovery codes from Redis
	s.rdb.Del(ctx, recoveryCodesPrefix+userID.String())

	s.log.Info("2FA disabled", "user_id", userID)
	return nil
}

// Verify2FA validates a TOTP code or recovery code during login and creates a full session.
func (s *Service) Verify2FA(ctx context.Context, userID uuid.UUID, code, pendingToken string) (*AuthResponse, error) {
	// Retrieve pending session from Redis
	key := pendingSessionPrefix + pendingToken
	data, err := s.rdb.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, errors.New("invalid or expired pending session")
		}
		return nil, fmt.Errorf("failed to retrieve pending session: %w", err)
	}

	var pending PendingTwoFASession
	if err := json.Unmarshal([]byte(data), &pending); err != nil {
		return nil, fmt.Errorf("failed to unmarshal pending session: %w", err)
	}

	parsedUserID, err := uuid.Parse(pending.UserID)
	if err != nil {
		return nil, errors.New("invalid user ID in pending session")
	}

	// Verify the userID matches
	if parsedUserID != userID {
		return nil, errors.New("user ID mismatch")
	}

	// Get the TOTP secret
	secret, err := s.store.Get2FASecret(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get 2FA secret: %w", err)
	}

	// Try TOTP validation first
	valid := totp.Validate(code, secret)

	// If TOTP fails, try recovery code
	if !valid {
		used, err := s.useRecoveryCode(ctx, userID, code)
		if err != nil {
			return nil, fmt.Errorf("failed to check recovery code: %w", err)
		}
		if !used {
			return nil, errors.New("invalid 2FA code")
		}
	}

	// Delete the pending session
	s.rdb.Del(ctx, key)

	// Look up the user for session creation
	user, err := s.store.GetUserByID(ctx, parsedUserID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	if user == nil {
		return nil, errors.New("user not found")
	}

	// Create the real session
	return s.createSessionForUser(ctx, user, pending.DeviceID, pending.Platform, pending.IP, pending.UserAgent)
}

// StorePending2FASession stores a pending 2FA session in Redis and returns a temporary token.
func (s *Service) StorePending2FASession(ctx context.Context, userID uuid.UUID, deviceID, platform, ip, userAgent string) (string, error) {
	token, err := generateOpaqueToken(32)
	if err != nil {
		return "", fmt.Errorf("failed to generate pending token: %w", err)
	}

	pending := PendingTwoFASession{
		UserID:    userID.String(),
		DeviceID:  deviceID,
		Platform:  platform,
		IP:        ip,
		UserAgent: userAgent,
	}

	data, err := json.Marshal(pending)
	if err != nil {
		return "", fmt.Errorf("failed to marshal pending session: %w", err)
	}

	key := pendingSessionPrefix + token
	if err := s.rdb.Set(ctx, key, data, pendingSessionTTL).Err(); err != nil {
		return "", fmt.Errorf("failed to store pending session: %w", err)
	}

	return token, nil
}


// generateRecoveryCodes generates random hex recovery codes.
func generateRecoveryCodes(count int) ([]string, error) {
	codes := make([]string, count)
	for i := 0; i < count; i++ {
		buf := make([]byte, 4)
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}
		codes[i] = hex.EncodeToString(buf)
	}
	return codes, nil
}

// storeRecoveryCodesTemp stores bcrypt-hashed recovery codes in Redis temporarily during setup.
func (s *Service) storeRecoveryCodesTemp(ctx context.Context, userID uuid.UUID, codes []string) error {
	hashed := make([]string, len(codes))
	for i, code := range codes {
		h, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		hashed[i] = string(h)
	}

	data, err := json.Marshal(hashed)
	if err != nil {
		return err
	}

	return s.rdb.Set(ctx, "2fa:setup:recovery:"+userID.String(), data, 10*time.Minute).Err()
}

// promoteRecoveryCodes moves temp recovery codes to permanent storage after 2FA is verified.
func (s *Service) promoteRecoveryCodes(ctx context.Context, userID uuid.UUID) error {
	data, err := s.rdb.Get(ctx, "2fa:setup:recovery:"+userID.String()).Result()
	if err != nil {
		return err
	}

	// Move to permanent key
	if err := s.rdb.Set(ctx, recoveryCodesPrefix+userID.String(), data, 0).Err(); err != nil {
		return err
	}

	// Clean up temp key
	s.rdb.Del(ctx, "2fa:setup:recovery:"+userID.String())
	return nil
}

// useRecoveryCode checks if the code matches any stored hashed recovery code and removes it.
func (s *Service) useRecoveryCode(ctx context.Context, userID uuid.UUID, code string) (bool, error) {
	key := recoveryCodesPrefix + userID.String()
	data, err := s.rdb.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return false, nil
		}
		return false, err
	}

	var hashed []string
	if err := json.Unmarshal([]byte(data), &hashed); err != nil {
		return false, err
	}

	for i, h := range hashed {
		if err := bcrypt.CompareHashAndPassword([]byte(h), []byte(code)); err == nil {
			// Remove the used code
			hashed = append(hashed[:i], hashed[i+1:]...)
			updated, err := json.Marshal(hashed)
			if err != nil {
				return false, err
			}
			if err := s.rdb.Set(ctx, key, updated, 0).Err(); err != nil {
				return false, err
			}
			return true, nil
		}
	}

	return false, nil
}
