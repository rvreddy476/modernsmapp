package partnerwork

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client fails closed on transport errors. In particular a timed-out acceptance
// is UNKNOWN, not a rejection: retain the local job and reconcile by Read.
type Client struct {
	base, key string
	http      *http.Client
}

func NewClient(base, key string) *Client {
	return &Client{strings.TrimRight(base, "/"), key, &http.Client{Timeout: 5 * time.Second}}
}
func (c *Client) call(ctx context.Context, in Command, out any) error {
	if c == nil || c.base == "" || c.key == "" {
		return fmt.Errorf("partner work coordinator is not configured")
	}
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/internal/partner-work", bytes.NewReader(b))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Internal-Key", c.key)
	resp, err := c.http.Do(r)
	if err != nil {
		return fmt.Errorf("partner work outcome unknown: %w", err)
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(io.LimitReader(resp.Body, 256*1024))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Code string `json:"code"`
		}
		_ = dec.Decode(&e)
		for code, typed := range map[string]error{"INVALID": ErrInvalid, "STALE": ErrStale, "BUSY": ErrBusy, "CONFLICT": ErrConflict, "OFF_DUTY": ErrOffDuty, "EXPIRED": ErrExpired, "NOT_FOUND": ErrNotFound} {
			if code == e.Code {
				return typed
			}
		}
		return fmt.Errorf("partner work unavailable (%d)", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return dec.Decode(out)
}
func (c *Client) Read(ctx context.Context, user uuid.UUID) (State, error) {
	var s State
	err := c.call(ctx, Command{UserID: user, Action: "read"}, &s)
	return s, err
}
func (c *Client) SetDuty(ctx context.Context, user uuid.UUID, category *Category, expected int64) error {
	return c.call(ctx, Command{UserID: user, Category: category, ExpectedRevision: expected, Action: "duty"}, nil)
}
func (c *Client) Offer(ctx context.Context, user uuid.UUID, category Category, job uuid.UUID, start, end time.Time) (Reservation, error) {
	var r Reservation
	err := c.call(ctx, Command{UserID: user, Category: &category, JobID: job, StartsAt: start, EndsAt: end, Action: "offer"}, &r)
	return r, err
}
func (c *Client) Decide(ctx context.Context, user uuid.UUID, category Category, reservation uuid.UUID, action string) error {
	return c.call(ctx, Command{UserID: user, Category: &category, ReservationID: reservation, Action: action}, nil)
}
