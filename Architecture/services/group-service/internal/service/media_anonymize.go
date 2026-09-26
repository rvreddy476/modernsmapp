package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
)

/*
	An anonymous post's attachments must be as anonymous as the post.

	media-service delivers bytes by redirecting to a signed object URL, and
	an object's key names its uploader (user/<id>/<media>/…). So a photo on
	an anonymous post would tell any member who took it — through the
	Location header for a direct client, through GET /v1/media/:id for
	anyone. Before the post row is written, every attached asset is handed
	to media-service's internal anonymize route, which puts the asset into
	the 'anonymous' access scope: its record becomes its uploader's alone
	and its bytes are streamed by media-service itself, never a URL that
	carries a name.

	Fail closed: if any attachment cannot be scoped the post is refused
	rather than published with a name attached to its picture.
*/

var ErrMediaAnonymizeUnavailable = fmt.Errorf("unavailable: attachments could not be made anonymous; try again")

func (s *Service) SetMediaServiceURL(url string) {
	s.mediaServiceURL = url
	if s.mediaClient == nil {
		s.mediaClient = &http.Client{Timeout: 5 * time.Second}
	}
}

// attachmentIDs reads the media ids out of the attachments JSON. Both shapes
// the composer has ever sent are accepted: ["id", …] and [{"media_id": …}].
func attachmentIDs(raw json.RawMessage) ([]uuid.UUID, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("invalid: attachments must be a list")
	}
	var out []uuid.UUID
	for _, it := range items {
		var asString string
		if err := json.Unmarshal(it, &asString); err == nil {
			id, err := uuid.Parse(asString)
			if err != nil {
				return nil, fmt.Errorf("invalid: attachment %q is not a media id", asString)
			}
			out = append(out, id)
			continue
		}
		var asObject struct {
			MediaID string `json:"media_id"`
		}
		if err := json.Unmarshal(it, &asObject); err != nil || asObject.MediaID == "" {
			return nil, fmt.Errorf("invalid: attachment is not a media id")
		}
		id, err := uuid.Parse(asObject.MediaID)
		if err != nil {
			return nil, fmt.Errorf("invalid: attachment %q is not a media id", asObject.MediaID)
		}
		out = append(out, id)
	}
	return out, nil
}

// anonymizeAttachments scopes every attached asset as anonymous, or fails.
func (s *Service) anonymizeAttachments(ctx context.Context, attachments json.RawMessage) error {
	ids, err := attachmentIDs(attachments)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	if s.mediaServiceURL == "" || s.mediaClient == nil {
		return ErrMediaAnonymizeUnavailable
	}
	for _, id := range ids {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			fmt.Sprintf("%s/v1/media/internal/%s/anonymize", s.mediaServiceURL, id), nil)
		if err != nil {
			return ErrMediaAnonymizeUnavailable
		}
		if s.internalServiceKey != "" {
			req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
		}
		resp, err := s.mediaClient.Do(req)
		if err != nil {
			return ErrMediaAnonymizeUnavailable
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return ErrMediaAnonymizeUnavailable
		}
	}
	return nil
}
