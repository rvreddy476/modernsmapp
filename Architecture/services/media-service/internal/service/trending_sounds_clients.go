package service

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

	"github.com/google/uuid"
)

// The two services the trending ranking asks (trending_sounds.go), over the
// internal service key the way this service already asks post-service
// (delivery/authz.go). Every failure is an error, never "no uses" or "not
// blocked".

const (
	// soundUsesPath is post-service's count route.
	soundUsesPath = "/v1/internal/sound-uses"
	// soundUsesPageSize is the page asked for: post-service's maximum.
	soundUsesPageSize = 1000
	// blockedAnyPath is graph-service's either-direction block lookup, the
	// one post-service uses for the same question (credits.go).
	blockedAnyPath = "/v1/internal/graph/blocked-any"
	// blockedAnyCap is graph-service's candidate ceiling per call.
	blockedAnyCap = 1024
	// trendingResponseLimit bounds a body read from either service.
	trendingResponseLimit = 8 << 20
)

// HTTPSoundUses reads GET /v1/internal/sound-uses.
type HTTPSoundUses struct {
	baseURL     string
	internalKey string
	client      *http.Client
}

// NewHTTPSoundUses asks post-service at baseURL.
func NewHTTPSoundUses(baseURL, internalKey string, client *http.Client) *HTTPSoundUses {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &HTTPSoundUses{baseURL: strings.TrimRight(baseURL, "/"), internalKey: internalKey, client: client}
}

type soundUsesWire struct {
	AsOf   *time.Time `json:"as_of"`
	Days   int        `json:"days"`
	Sounds *[]struct {
		AudioTrackID uuid.UUID `json:"audio_track_id"`
		DailyUses    []int     `json:"daily_uses"`
	} `json:"sounds"`
	NextAfter *uuid.UUID `json:"next_after"`
}

// SoundUses is one page after `after` (uuid.Nil: the first).
func (h *HTTPSoundUses) SoundUses(ctx context.Context, after uuid.UUID) (*SoundUsesPage, error) {
	q := url.Values{}
	q.Set("limit", fmt.Sprint(soundUsesPageSize))
	if after != uuid.Nil {
		q.Set("after", after.String())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.baseURL+soundUsesPath+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if h.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", h.internalKey)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post-service sound uses: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, fmt.Errorf("post-service sound uses returned %d", resp.StatusCode)
	}
	var wire soundUsesWire
	if err := json.NewDecoder(io.LimitReader(resp.Body, trendingResponseLimit)).Decode(&wire); err != nil {
		return nil, fmt.Errorf("post-service sound uses body: %w", err)
	}
	// A body that does not say what the contract says is not "no uses".
	if wire.AsOf == nil || wire.Sounds == nil {
		return nil, errors.New("post-service sound uses answered without as_of or sounds")
	}
	if wire.Days != trendingDays {
		return nil, fmt.Errorf("post-service sound uses counts %d days, want %d", wire.Days, trendingDays)
	}
	page := &SoundUsesPage{AsOf: *wire.AsOf, NextAfter: wire.NextAfter, Sounds: make([]SoundDailyUses, 0, len(*wire.Sounds))}
	for _, s := range *wire.Sounds {
		if s.AudioTrackID == uuid.Nil || len(s.DailyUses) != trendingDays {
			return nil, fmt.Errorf("post-service sound uses: malformed row for %s", s.AudioTrackID)
		}
		page.Sounds = append(page.Sounds, SoundDailyUses{AudioTrackID: s.AudioTrackID, DailyUses: s.DailyUses})
	}
	return page, nil
}

// HTTPBlockChecker asks graph-service's blocked-any.
type HTTPBlockChecker struct {
	baseURL     string
	internalKey string
	client      *http.Client
}

// NewHTTPBlockChecker asks graph-service at baseURL.
func NewHTTPBlockChecker(baseURL, internalKey string, client *http.Client) *HTTPBlockChecker {
	if client == nil {
		// On the read path: a slow graph must become a fast retryable 503.
		client = &http.Client{Timeout: 3 * time.Second}
	}
	return &HTTPBlockChecker{baseURL: strings.TrimRight(baseURL, "/"), internalKey: internalKey, client: client}
}

// BlockedAny returns the candidates with a block either way with userID.
func (h *HTTPBlockChecker) BlockedAny(ctx context.Context, userID uuid.UUID, candidates []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := make(map[uuid.UUID]bool)
	for start := 0; start < len(candidates); start += blockedAnyCap {
		end := start + blockedAnyCap
		if end > len(candidates) {
			end = len(candidates)
		}
		if err := h.blockedAnyChunk(ctx, userID, candidates[start:end], out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (h *HTTPBlockChecker) blockedAnyChunk(ctx context.Context, userID uuid.UUID, candidates []uuid.UUID, out map[uuid.UUID]bool) error {
	body, err := json.Marshal(map[string]any{"user_id": userID, "candidate_ids": candidates})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL+blockedAnyPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if h.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", h.internalKey)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("graph blocked-any: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return fmt.Errorf("graph blocked-any returned %d", resp.StatusCode)
	}
	var wire struct {
		BlockedUserIDs *[]uuid.UUID `json:"blocked_user_ids"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&wire); err != nil {
		return fmt.Errorf("graph blocked-any body: %w", err)
	}
	if wire.BlockedUserIDs == nil {
		return errors.New("graph blocked-any answered without blocked_user_ids")
	}
	for _, id := range *wire.BlockedUserIDs {
		out[id] = true
	}
	return nil
}
