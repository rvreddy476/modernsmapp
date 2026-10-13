package partnerwork

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// State is authenticated readback; callers must never infer duty from a cached
// domain-specific online flag.
type State struct {
	Session      Session       `json:"session"`
	Reservations []Reservation `json:"reservations"`
}

type Command struct {
	UserID           uuid.UUID `json:"user_id"`
	Category         *Category `json:"category"`
	ExpectedRevision int64     `json:"expected_revision"`
	JobID            uuid.UUID `json:"job_id"`
	ReservationID    uuid.UUID `json:"reservation_id"`
	StartsAt         time.Time `json:"starts_at"`
	EndsAt           time.Time `json:"ends_at"`
	Action           string    `json:"action"`
}

func Snapshot(ctx context.Context, db *pgxpool.Pool, user uuid.UUID) (State, error) {
	s, rs, err := Read(ctx, db, user)
	return State{Session: s, Reservations: rs}, err
}

// InternalHandler is mounted only behind the service-to-service key. Domain
// services validate approval, coverage and job ownership before these calls.
// No end-user request may choose another user_id or arbitrary job time bounds.
func InternalHandler(db *pgxpool.Pool, key string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if key == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Internal-Key")), []byte(key)) != 1 {
			http.Error(w, `{"code":"UNAUTHORIZED"}`, http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var in Command
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
		d.DisallowUnknownFields()
		if err := d.Decode(&in); err != nil || in.UserID == uuid.Nil {
			writeError(w, ErrInvalid)
			return
		}
		if in.Action == "read" {
			s, err := Snapshot(r.Context(), db, in.UserID)
			if err != nil {
				writeError(w, err)
				return
			}
			json.NewEncoder(w).Encode(s)
			return
		}
		tx, err := db.Begin(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		defer tx.Rollback(r.Context())
		var result any = map[string]bool{"ok": true}
		switch in.Action {
		case "duty":
			err = SetDutyTx(r.Context(), tx, in.UserID, in.Category, in.ExpectedRevision)
		case "offer":
			if in.Category == nil {
				err = ErrInvalid
			} else {
				result, err = OfferTx(r.Context(), tx, in.UserID, *in.Category, in.JobID, in.StartsAt, in.EndsAt)
			}
		case "accept", "start", "release":
			if in.Category == nil {
				err = ErrInvalid
			} else {
				err = DecideTx(r.Context(), tx, in.UserID, *in.Category, in.ReservationID, in.Action)
			}
		default:
			err = ErrInvalid
		}
		if err == nil {
			err = tx.Commit(r.Context())
		}
		if err != nil {
			writeError(w, err)
			return
		}
		json.NewEncoder(w).Encode(result)
	})
}

func ErrorCode(err error) string {
	for code, e := range map[string]error{"INVALID": ErrInvalid, "STALE": ErrStale, "BUSY": ErrBusy, "CONFLICT": ErrConflict, "OFF_DUTY": ErrOffDuty, "EXPIRED": ErrExpired, "NOT_FOUND": ErrNotFound} {
		if errors.Is(err, e) {
			return code
		}
	}
	return "UNAVAILABLE"
}
func writeError(w http.ResponseWriter, err error) {
	code := ErrorCode(err)
	status := http.StatusConflict
	if code == "INVALID" {
		status = http.StatusBadRequest
	}
	if code == "NOT_FOUND" {
		status = http.StatusNotFound
	}
	if code == "UNAVAILABLE" {
		status = http.StatusServiceUnavailable
	}
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"code": code})
}
