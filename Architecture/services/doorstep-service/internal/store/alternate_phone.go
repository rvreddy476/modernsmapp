package store

import (
	"context"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"time"
)

// Rate limits and verification are serialized on the application row, across instances.
func (s *Store) RequestAlternatePhone(ctx context.Context, uid uuid.UUID, hash string) (string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	var id uuid.UUID
	var last *string
	var verified, sent, window *time.Time
	var sends int
	err = tx.QueryRow(ctx, `SELECT a.pro_id,a.alternate_phone_last4,a.alternate_phone_verified_at,a.alternate_otp_sent_at,a.alternate_otp_window_at,a.alternate_otp_sends
 FROM doorstep.pro_applications a JOIN doorstep.professionals p ON p.id=a.pro_id WHERE p.user_id=$1 FOR UPDATE OF a`, uid).Scan(&id, &last, &verified, &sent, &window, &sends)
	if err != nil {
		return "", mapErr(err)
	}
	if last == nil || verified != nil {
		return "", ErrInvalid
	}
	now := time.Now()
	if sent != nil && now.Sub(*sent) < time.Minute {
		return "", ErrConflict
	}
	if window == nil || now.Sub(*window) >= 15*time.Minute {
		window = &now
		sends = 0
	}
	if sends >= 5 {
		return "", ErrConflict
	}
	_, err = tx.Exec(ctx, `UPDATE doorstep.pro_applications SET alternate_otp_hash=$2,alternate_otp_expires_at=NOW()+INTERVAL '5 minutes',alternate_otp_attempts=0,
 alternate_otp_sent_at=NOW(),alternate_otp_window_at=$3,alternate_otp_sends=$4 WHERE pro_id=$1`, id, hash, window, sends+1)
	if err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return *last, nil
}

func (s *Store) VerifyAlternatePhone(ctx context.Context, uid uuid.UUID, code string) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	var id uuid.UUID
	var hash *string
	var expires, verified *time.Time
	var attempts int
	err = tx.QueryRow(ctx, `SELECT a.pro_id,a.alternate_otp_hash,a.alternate_otp_expires_at,a.alternate_otp_attempts,a.alternate_phone_verified_at
 FROM doorstep.pro_applications a JOIN doorstep.professionals p ON p.id=a.pro_id WHERE p.user_id=$1 FOR UPDATE OF a`, uid).Scan(&id, &hash, &expires, &attempts, &verified)
	if err != nil {
		return false, mapErr(err)
	}
	if hash == nil || expires == nil || !time.Now().Before(*expires) || attempts >= 5 || verified != nil {
		return false, nil
	}
	match := bcrypt.CompareHashAndPassword([]byte(*hash), []byte(code)) == nil
	_, err = tx.Exec(ctx, `UPDATE doorstep.pro_applications SET alternate_otp_attempts=alternate_otp_attempts+1,
 alternate_phone_verified_at=CASE WHEN $2 THEN NOW() ELSE alternate_phone_verified_at END,
 alternate_otp_hash=CASE WHEN $2 OR alternate_otp_attempts>=4 THEN NULL ELSE alternate_otp_hash END WHERE pro_id=$1`, id, match)
	if err != nil {
		return false, err
	}
	return match, tx.Commit(ctx)
}
