// Package reindex rebuilds OpenSearch indices from a source-of-truth
// service when the event stream alone can't be trusted.
//
// Why this exists: search-service's users_v1 index is normally kept
// current by Kafka events (UserRegistered / UserProfileUpdated /
// HandleChanged). That works while the event stream is unbroken — but
// if OpenSearch is wiped, or search-service is offline longer than
// Kafka's retention window, or the broker volume is reset, the index
// silently drifts from reality and there is no way back. That exact
// failure happened in May 2026: a Redpanda volume reset dropped every
// historical event and search returned nothing for users who plainly
// existed.
//
// ReindexUsers closes that gap: it pulls the full profile set from
// profile-service (the source of truth for username / display name /
// bio / avatar / verified) and bulk-indexes it. It runs on demand via
// an admin endpoint and automatically on startup when the index is
// found empty.
package reindex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/atpost/search-service/internal/store/search"
)

// profilePageSize is the page size used when walking profile-service.
// Offset pagination is fine here — reconciliation is an occasional job,
// not a hot path.
const profilePageSize = 200

// defaultHTTPClient is used when a caller passes a nil client.
var defaultHTTPClient = &http.Client{Timeout: 15 * time.Second}

func orDefaultClient(c *http.Client) *http.Client {
	if c == nil {
		return defaultHTTPClient
	}
	return c
}

// profile mirrors the subset of profile-service's Profile JSON that the
// users_v1 index needs. Unknown fields are ignored by encoding/json.
type profile struct {
	UserID        string  `json:"user_id"`
	Username      *string `json:"username"`
	DisplayName   string  `json:"display_name"`
	Bio           string  `json:"bio"`
	AvatarMediaID *string `json:"avatar_media_id"`
	IsVerified    bool    `json:"is_verified"`
}

type profileListResponse struct {
	Data struct {
		Items []profile `json:"items"`
	} `json:"data"`
}

// UsersResult summarizes a reindex run.
type UsersResult struct {
	Fetched int
	Indexed int
}

// ReindexUsers walks profile-service's /v1/profiles/discover endpoint and
// bulk-indexes every profile into the users_v1 OpenSearch index. It is
// safe to run repeatedly — IndexUser/BulkIndexUsers upsert by user_id.
//
// profileServiceURL is the base URL (e.g. http://identity-profile:8098);
// internalKey is forwarded as X-Internal-Service-Key so the call passes
// profile-service's internal gate.
func ReindexUsers(
	ctx context.Context,
	httpClient *http.Client,
	profileServiceURL, internalKey string,
	store *search.Store,
	log *slog.Logger,
) (UsersResult, error) {
	var res UsersResult
	if profileServiceURL == "" {
		return res, fmt.Errorf("reindex: PROFILE_SERVICE_URL not configured")
	}
	httpClient = orDefaultClient(httpClient)

	for offset := 0; ; offset += profilePageSize {
		profiles, err := fetchProfilePage(ctx, httpClient, profileServiceURL, internalKey, profilePageSize, offset)
		if err != nil {
			return res, fmt.Errorf("reindex: fetch page at offset %d: %w", offset, err)
		}
		if len(profiles) == 0 {
			break
		}
		res.Fetched += len(profiles)

		docs := make([]search.UserDoc, 0, len(profiles))
		for _, p := range profiles {
			doc := search.UserDoc{
				UserID:      p.UserID,
				DisplayName: p.DisplayName,
				Bio:         p.Bio,
				IsVerified:  p.IsVerified,
			}
			if p.Username != nil {
				doc.Username = *p.Username
			}
			if p.AvatarMediaID != nil {
				doc.AvatarMediaID = *p.AvatarMediaID
			}
			docs = append(docs, doc)
		}

		n, err := store.BulkIndexUsers(ctx, docs)
		if err != nil {
			// Log and continue — a partial reindex is better than none,
			// and the next run will pick up whatever this one missed.
			log.Warn("reindex: bulk index page failed", "offset", offset, "err", err)
		}
		res.Indexed += n

		if len(profiles) < profilePageSize {
			break
		}
	}

	log.Info("reindex: users complete", "fetched", res.Fetched, "indexed", res.Indexed)
	return res, nil
}

func fetchProfilePage(
	ctx context.Context,
	httpClient *http.Client,
	profileServiceURL, internalKey string,
	limit, offset int,
) ([]profile, error) {
	url := fmt.Sprintf("%s/v1/profiles/discover?limit=%d&offset=%d", profileServiceURL, limit, offset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", internalKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("profile-service returned %d: %s", resp.StatusCode, string(body))
	}

	var parsed profileListResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal profile list: %w", err)
	}
	return parsed.Data.Items, nil
}

// AutoHealUsersOnStartup runs ReindexUsers in the background when the
// users_v1 index is empty at boot — the signature of a wiped index or
// a brand-new OpenSearch volume. A populated index is left untouched;
// steady-state indexing is the Kafka consumers' job.
func AutoHealUsersOnStartup(
	ctx context.Context,
	httpClient *http.Client,
	profileServiceURL, internalKey string,
	store *search.Store,
	log *slog.Logger,
) {
	count, err := store.CountUsers(ctx)
	if err != nil {
		log.Warn("reindex: startup user-count check failed; skipping auto-heal", "err", err)
		return
	}
	if count > 0 {
		log.Info("reindex: users_v1 already populated; skipping startup auto-heal", "count", count)
		return
	}
	log.Warn("reindex: users_v1 is empty at startup — triggering auto-heal from profile-service")
	go func() {
		healCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if _, err := ReindexUsers(healCtx, httpClient, profileServiceURL, internalKey, store, log); err != nil {
			log.Error("reindex: startup auto-heal failed", "err", err)
		}
	}()
}
