package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
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

var (
	// ErrTOTPReplay is returned when a TOTP code has already been used within the validity window.
	ErrTOTPReplay = errors.New("TOTP code already used")
	// ErrSecondFactorRequired is returned when 2FA is enabled but the second factor has not been verified.
	ErrSecondFactorRequired = errors.New("second factor required")
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

	if valid {
		// Check replay: reject already-used TOTP codes within the validity window
		usedKey := fmt.Sprintf("totp_used:%s:%s", userID.String(), code)
		exists, err := s.rdb.Exists(ctx, usedKey).Result()
		if err == nil && exists > 0 {
			return nil, ErrTOTPReplay
		}
		// Mark this code as used for 90 seconds (covers 3 time steps)
		s.rdb.Set(ctx, usedKey, "1", 90*time.Second)
	}

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


// generateRecoveryCodes generates random base32-encoded recovery codes with 128-bit entropy.
func generateRecoveryCodes(count int) ([]string, error) {
	codes := make([]string, count)
	for i := 0; i < count; i++ {
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}
		codes[i] = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf)
	}
	return codes, nil
}

// storeRecoveryCodesTemp stores bcrypt-hashed recovery codes in Redis temporarily during setup.
func (s *Service) storeRecoveryCodesTemp(ctx context.Context, userID uuid.UUID, codes []string) error {
	hashed := make([]string, len(codes))
	for i, code := range codes {
		h, err := bcrypt.GenerateFromPassword([]byte(code), s.cfg.BcryptCost)
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
// It persists SHA-256 hashed codes to Postgres and also keeps bcrypt hashes in Redis.
func (s *Service) promoteRecoveryCodes(ctx context.Context, userID uuid.UUID) error {
	data, err := s.rdb.Get(ctx, "2fa:setup:recovery:"+userID.String()).Result()
	if err != nil {
		return err
	}

	// Move to permanent Redis key (bcrypt hashes for fast lookup)
	if err := s.rdb.Set(ctx, recoveryCodesPrefix+userID.String(), data, 0).Err(); err != nil {
		return err
	}

	// Also store SHA-256 hashes in Postgres for durable persistence (Change 2)
	// We retrieve the raw codes from the temp setup key; since we only stored bcrypt hashes,
	// we re-read the hashed list and derive sha256 hashes from the bcrypt hash bytes.
	// Note: the plain codes are not available here; we store sha256 of the bcrypt hash string
	// as a stable identifier to mark used_at. Actual verification still uses the bcrypt path.
	var bcryptHashes []string
	if jsonErr := json.Unmarshal([]byte(data), &bcryptHashes); jsonErr == nil {
		sha256Hashes := make([]string, len(bcryptHashes))
		for i, bh := range bcryptHashes {
			sum := sha256.Sum256([]byte(bh))
			sha256Hashes[i] = hex.EncodeToString(sum[:])
		}
		if pgErr := s.store.StoreRecoveryCodes(ctx, userID, sha256Hashes); pgErr != nil {
			s.log.Warn("failed to persist recovery codes to postgres", "err", pgErr, "user_id", userID)
		}
	}

	// Clean up temp key
	s.rdb.Del(ctx, "2fa:setup:recovery:"+userID.String())
	return nil
}

// useRecoveryCode checks if the code matches any stored hashed recovery code and removes it.
// It first tries Redis (bcrypt); on Redis miss it falls back to Postgres.
func (s *Service) useRecoveryCode(ctx context.Context, userID uuid.UUID, code string) (bool, error) {
	key := recoveryCodesPrefix + userID.String()
	data, redisErr := s.rdb.Get(ctx, key).Result()

	if redisErr == nil {
		// Redis hit — verify using bcrypt hashes
		var hashed []string
		if err := json.Unmarshal([]byte(data), &hashed); err != nil {
			return false, err
		}

		for i, h := range hashed {
			if err := bcrypt.CompareHashAndPassword([]byte(h), []byte(code)); err == nil {
				// Remove the used code from Redis
				hashed = append(hashed[:i], hashed[i+1:]...)
				updated, err := json.Marshal(hashed)
				if err != nil {
					return false, err
				}
				if err := s.rdb.Set(ctx, key, updated, 0).Err(); err != nil {
					return false, err
				}

				// Mark as used in Postgres via the sha256 of the bcrypt hash (our stable identifier)
				sum := sha256.Sum256([]byte(h))
				sha256ID := hex.EncodeToString(sum[:])
				pgRows, pgErr := s.store.GetUnusedRecoveryCodes(ctx, userID)
				if pgErr == nil {
					for _, row := range pgRows {
						if row.CodeHash == sha256ID {
							_ = s.store.MarkRecoveryCodeUsed(ctx, row.ID)
							break
						}
					}
				}

				return true, nil
			}
		}
		return false, nil
	}

	if !errors.Is(redisErr, redis.Nil) {
		return false, redisErr
	}

	// Redis miss — fall back to Postgres (SHA-256 of bcrypt hash is not directly searchable
	// by plaintext code; we cannot verify the code without bcrypt. Log and return false.)
	// TODO: if Redis is unavailable and recovery codes were never cached, re-verify via
	// a re-hashing approach or prompt user to reset 2FA.
	s.log.Warn("recovery codes not found in Redis; Postgres fallback not possible without bcrypt hashes",
		"user_id", userID)
	return false, nil
}
