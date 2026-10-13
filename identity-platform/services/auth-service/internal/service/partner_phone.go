package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/atpost/identity-shared/events"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

var ErrPartnerPhoneUnavailable = errors.New("mobile verification is not available")
var ErrPartnerPhoneCode = errors.New("the code is incorrect or expired; request a new code if needed")
var ErrPartnerPhoneInput = errors.New("check your registration details")

// This explicitly enabled local-development transport must never become an
// implicit production SMS fallback. It shares no bypass code with normal auth.
func (s *Service) PartnerPhoneAvailable() bool {
	return s.cfg != nil && !s.cfg.Production && s.cfg.RuntimeEnvironment == "local" && os.Getenv("PARTNER_DEV_PHONE_OTP_ENABLED") == "true"
}

type PartnerPhoneInput struct {
	Mode          string `json:"mode"`
	Phone         string `json:"phone"`
	FirstName     string `json:"first_name"`
	LastName      string `json:"last_name"`
	Gender        string `json:"gender"`
	AcceptedTerms bool   `json:"accepted_terms"`
	TermsVersion  string `json:"terms_version"`
}

type PartnerPhoneChallenge struct {
	ChallengeID string `json:"challenge_id"`
	Delivery    string `json:"delivery"`
	ExpiresIn   int    `json:"expires_in"`
	ResendAfter int    `json:"resend_after"`
}

type partnerPhonePending struct {
	Input PartnerPhoneInput `json:"input"`
	Hash  string            `json:"hash"`
}

var partnerMobile = regexp.MustCompile(`^\+91[6-9][0-9]{9}$`)
var partnerCode = regexp.MustCompile(`^[0-9]{6}$`)
var partnerChallengeID = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func normalizePartnerPhone(in PartnerPhoneInput) (PartnerPhoneInput, error) {
	in.Phone = strings.TrimSpace(in.Phone)
	if len(in.Phone) == 10 {
		in.Phone = "+91" + in.Phone
	}
	if !partnerMobile.MatchString(in.Phone) {
		return in, errors.New("enter a valid 10-digit Indian mobile number")
	}
	if in.Mode != "login" && in.Mode != "register" {
		return in, errors.New("choose sign in or registration")
	}
	if in.Mode == "register" {
		in.FirstName, in.LastName = strings.TrimSpace(in.FirstName), strings.TrimSpace(in.LastName)
		for _, name := range []string{in.FirstName, in.LastName} {
			if utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 50 {
				return in, errors.New("enter first and last names, each up to 50 characters")
			}
			for _, r := range name {
				if !unicode.IsLetter(r) && !unicode.IsMark(r) && r != ' ' && r != '-' && r != '\'' && r != '.' {
					return in, errors.New("names may contain letters, spaces, apostrophes, dots and hyphens")
				}
			}
		}
		if in.Gender != "male" && in.Gender != "female" && in.Gender != "others" {
			return in, errors.New("choose Male, Female or Other")
		}
		if !in.AcceptedTerms || in.TermsVersion != CurrentTermsVersion {
			return in, errors.New("accept the current terms and privacy policy")
		}
	}
	return in, nil
}

var partnerQuota = redis.NewScript(`local n=redis.call('INCR',KEYS[1]); if n==1 then redis.call('EXPIRE',KEYS[1],ARGV[1]) end; return n`)

func (s *Service) partnerPhoneQuota(ctx context.Context, value string, limit int64, seconds int) error {
	sum := sha256.Sum256([]byte(value))
	n, err := partnerQuota.Run(ctx, s.rdb, []string{"partner_phone:quota:" + hex.EncodeToString(sum[:])}, seconds).Int64()
	if err != nil {
		return err
	}
	if n > limit {
		return &ErrThrottled{RetryAfter: time.Duration(seconds) * time.Second, Reason: "mobile verification"}
	}
	return nil
}

func (s *Service) RequestPartnerPhone(ctx context.Context, in PartnerPhoneInput, ip string) (*PartnerPhoneChallenge, error) {
	if !s.PartnerPhoneAvailable() || s.rdb == nil {
		return nil, ErrPartnerPhoneUnavailable
	}
	in, err := normalizePartnerPhone(in)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrPartnerPhoneInput, err)
	}
	if err = s.partnerPhoneQuota(ctx, "ip:"+ip, 30, 900); err != nil {
		return nil, err
	}
	if err = s.partnerPhoneQuota(ctx, "phone:"+in.Phone, 5, 900); err != nil {
		return nil, err
	}
	if err = s.partnerPhoneQuota(ctx, "cooldown:"+in.Phone, 1, 60); err != nil {
		return nil, err
	}
	var bytes [32]byte
	if _, err = rand.Read(bytes[:]); err != nil {
		return nil, err
	}
	id := base64.RawURLEncoding.EncodeToString(bytes[:])
	// Fixed length for this contract, independent of legacy OTP configuration.
	var digits [6]byte
	for i := range digits {
		for {
			var b [1]byte
			if _, err = rand.Read(b[:]); err != nil {
				return nil, err
			}
			if b[0] < 250 {
				digits[i] = '0' + b[0]%10
				break
			}
		}
	}
	code := string(digits[:])
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(partnerPhonePending{Input: in, Hash: string(hash)})
	if err != nil {
		return nil, err
	}
	if err = s.rdb.Set(ctx, "partner_phone:challenge:"+id, payload, 5*time.Minute).Err(); err != nil {
		return nil, err
	}
	s.log.Warn("PARTNER OTP — DEVELOPMENT ONLY — SMS NOT SENT", "phone", maskPhone(in.Phone), "challenge_id", id, "code", code)
	return &PartnerPhoneChallenge{ChallengeID: id, Delivery: "development_log", ExpiresIn: 300, ResendAfter: 60}, nil
}

// Atomic attempt cap and single-use consumption prevent two successful verifies
// of one challenge, including concurrent requests on different server instances.
var consumePartnerCode = redis.NewScript(`
if redis.call('GET',KEYS[1]) ~= ARGV[1] then return 0 end
local n=redis.call('INCR',KEYS[2]); if n==1 then redis.call('EXPIRE',KEYS[2],300) end
if n>5 then redis.call('DEL',KEYS[1]); return 0 end
if ARGV[2]=='yes' then redis.call('DEL',KEYS[1]); return 1 end
if n==5 then redis.call('DEL',KEYS[1]) end
return 0`)

func (s *Service) consumePartnerPhone(ctx context.Context, id, code string) (*partnerPhonePending, error) {
	if !s.PartnerPhoneAvailable() || s.rdb == nil {
		return nil, ErrPartnerPhoneUnavailable
	}
	if !partnerChallengeID.MatchString(id) || !partnerCode.MatchString(code) {
		return nil, ErrPartnerPhoneCode
	}
	key := "partner_phone:challenge:" + id
	raw, err := s.rdb.Get(ctx, key).Result()
	if err != nil {
		return nil, ErrPartnerPhoneCode
	}
	var pending partnerPhonePending
	if json.Unmarshal([]byte(raw), &pending) != nil {
		return nil, ErrPartnerPhoneCode
	}
	match := "no"
	if bcrypt.CompareHashAndPassword([]byte(pending.Hash), []byte(code)) == nil {
		match = "yes"
	}
	ok, err := consumePartnerCode.Run(ctx, s.rdb, []string{key, key + ":attempts"}, raw, match).Int()
	if err != nil || ok != 1 {
		return nil, ErrPartnerPhoneCode
	}
	return &pending, nil
}

func (s *Service) VerifyPartnerPhone(ctx context.Context, id, code, device, platform, ip, ua string) (*AuthResponse, error) {
	p, err := s.consumePartnerPhone(ctx, id, code)
	if err != nil {
		return nil, err
	}
	user, err := s.store.GetUserByPhone(ctx, p.Input.Phone)
	if err != nil {
		return nil, err
	}
	if user == nil {
		if p.Input.Mode != "register" {
			return nil, errors.New("no account uses this mobile number; please register first")
		}
		tx, err := s.store.DB().Begin(ctx)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback(ctx)
		// Serialise simultaneous registrations for the same phone. A second
		// verified challenge must never replace an existing person's profile.
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "partner-phone:"+p.Input.Phone); err != nil {
			return nil, err
		}
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM auth.users WHERE phone=$1)`, p.Input.Phone).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			return nil, errors.New("your account already exists; please sign in")
		}
		user, err = s.store.CreateUserTx(ctx, tx, p.Input.Phone)
		if err != nil {
			return nil, err
		}
		if err = s.store.CreateUserRecordTx(ctx, tx, user.ID); err != nil {
			return nil, err
		}
		if err = s.store.CreateProfileTx(ctx, tx, user.ID, p.Input.FirstName+" "+p.Input.LastName, p.Input.FirstName, p.Input.LastName, "", p.Input.Gender); err != nil {
			return nil, err
		}
		username, err := s.assignHandleTx(ctx, tx, user.ID, "", p.Input.FirstName, p.Input.LastName)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `UPDATE auth.users SET phone_verified=true WHERE user_id=$1`, user.ID); err != nil {
			return nil, err
		}
		if err = s.store.RecordConsentTx(ctx, tx, user.ID, store.RegistrationConsent{TermsVersion: p.Input.TermsVersion, AcceptedTerms: true, AcceptedPrivacy: true, IP: ip, UserAgent: ua}); err != nil {
			return nil, err
		}
		// This form asks no birth date or adult declaration. The legacy email
		// consent writer assumes an age-gated form; do not manufacture that
		// declaration for mobile intake. Age-restricted checks remain closed.
		if _, err = tx.Exec(ctx, `UPDATE auth.users SET consent_age=false WHERE user_id=$1`, user.ID); err != nil {
			return nil, err
		}
		if err = s.store.InsertOutboxEventTx(ctx, tx, events.UserRegistered, user.ID.String(), events.UserRegisteredPayload{UserID: user.ID.String(), Username: username, Phone: p.Input.Phone, FirstName: p.Input.FirstName, LastName: p.Input.LastName, Gender: p.Input.Gender, CreatedAt: time.Now()}); err != nil {
			return nil, err
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, err
		}
		user.PhoneVerified = true
		user.ConsentTerms, user.ConsentPrivacy, user.ConsentAge = true, true, false
	}
	if user.AccountStatus != store.AccountStatusActive {
		return nil, fmt.Errorf("this account is unavailable; contact support")
	}
	if !user.PhoneVerified {
		if err := s.store.MarkPhoneVerified(ctx, user.ID); err != nil {
			return nil, err
		}
		user.PhoneVerified = true
	}
	return s.createSessionForUser(ctx, user, []string{AMRSMS}, device, platform, ip, ua)
}
