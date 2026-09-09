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

	"github.com/atpost/search-service/internal/privacyclient"
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
	AvatarMediaID *string    `json:"avatar_media_id"`
	IsVerified    bool       `json:"is_verified"`
	CreatedAt     *time.Time `json:"created_at"`
}

type profileListResponse struct {
	Data struct {
		Items []profile `json:"items"`
	} `json:"data"`
}

// UsersResult summarizes a reindex run.
//
// Reported in the spirit of the product reindex's index_total/orphans: say
// what the run could not do, not only what it wrote.
type UsersResult struct {
	Fetched int
	Indexed int
	// UsernamesPreserved counts documents whose username profile-service
	// did not know and which were carried forward from the existing index
	// instead of being blanked. A large number is not a success — it is the
	// measure of how blind the reindex source is to handles.
	UsernamesPreserved int
	// UsernamesMissing counts indexed documents that ended the run with no
	// username from any source. Those accounts cannot be found by handle.
	UsernamesMissing int
}

// ReindexUsers walks profile-service's /v1/profiles/discover endpoint and
// bulk-indexes every profile into the users_v1 OpenSearch index. It is
// safe to run repeatedly — IndexUser/BulkIndexUsers upsert by user_id.
//
// profileServiceURL is the base URL (e.g. http://identity-profile:8098);
// internalKey is forwarded as X-Internal-Service-Key so the call passes
// profile-service's internal gate.
//
// privacy stamps is_private from the identity settings (private accounts);
// BulkIndexUsers is a full replace, so omitting it would reset every
// private account to public. A nil lookup indexes everyone as public and is
// only acceptable on a dev rig without the identity user-service.
func ReindexUsers(
	ctx context.Context,
	httpClient *http.Client,
	profileServiceURL, internalKey string,
	store *search.Store,
	privacy privacyclient.Lookup,
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

		// profile-service is NOT the authority for username — app.users,
		// behind user-service, is, and profile.profiles.username is NULL
		// for every row on the environments checked. BulkIndexUsers is a
		// full-document replace, so taking profile-service's answer at face
		// value does not merely leave the username absent: it ERASES a
		// username that the UserRegistered event had already indexed, and
		// the account stops being findable by handle.
		//
		// A reindex exists to repair drift, not to destroy the one field
		// its source cannot see. So an empty username from profile-service
		// is treated as "unknown", and the value already in the index is
		// carried forward. A non-empty answer still wins — that is a real
		// update. The permanent fix is for profile-service to serve the
		// username (or for this to read user-service); until then this
		// keeps a reindex from being a regression.
		ids := make([]string, 0, len(profiles))
		for _, p := range profiles {
			ids = append(ids, p.UserID)
		}
		existing := map[string]search.UserDoc{}
		if got, err := store.GetUsersByIDs(ctx, ids); err != nil {
			// Fail SAFE for the data: without the read-back we cannot tell
			// "no username" from "username we are about to delete", so skip
			// the page rather than blank a batch of handles.
			log.Warn("reindex: username read-back failed; skipping page to avoid erasing handles",
				"offset", offset, "err", err)
			if len(profiles) < profilePageSize {
				break
			}
			continue
		} else {
			existing = got
		}

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
			if doc.Username == "" {
				if prev, ok := existing[p.UserID]; ok && prev.Username != "" {
					doc.Username = prev.Username
					res.UsernamesPreserved++
				}
			}
			// Same full-replace hazard as username: profile-service's
			// discover payload carries created_at, but if it ever stops,
			// the field must not be destroyed — it drives the gauss
			// recency function in the ranked query.
			if p.CreatedAt != nil {
				doc.CreatedAt = p.CreatedAt
			} else if prev, ok := existing[p.UserID]; ok && prev.CreatedAt != nil {
				doc.CreatedAt = prev.CreatedAt
			}
			if p.AvatarMediaID != nil {
				doc.AvatarMediaID = *p.AvatarMediaID
			}
			if privacy != nil {
				private, err := privacy.IsPrivate(ctx, p.UserID)
				if err != nil {
					// Do not guess "public" for a user we could not resolve:
					// skip the document and let the next run (or the
					// settings-changed event) write it with the real value.
					log.Warn("reindex: account_visibility unresolved; skipping user", "user_id", p.UserID, "err", err)
					continue
				}
				doc.IsPrivate = private
			}
			if doc.Username == "" {
				res.UsernamesMissing++
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

	log.Info("reindex: users complete",
		"fetched", res.Fetched, "indexed", res.Indexed,
		"usernames_preserved", res.UsernamesPreserved,
		"usernames_missing", res.UsernamesMissing)
	if res.UsernamesMissing > 0 {
		log.Warn("reindex: users indexed with no username — unfindable by handle",
			"count", res.UsernamesMissing,
			"why", "profile-service does not serve usernames (profile.profiles.username is NULL); "+
				"the authority is app.users behind user-service",
			"fix", "profile-service should include username in /v1/profiles/discover, or this "+
				"reindex should read handles from user-service")
	}
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
	privacy privacyclient.Lookup,
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
		if _, err := ReindexUsers(healCtx, httpClient, profileServiceURL, internalKey, store, privacy, log); err != nil {
			log.Error("reindex: startup auto-heal failed", "err", err)
		}
	}()
}
