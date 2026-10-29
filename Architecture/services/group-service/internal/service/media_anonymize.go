package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/atpost/group-service/internal/mediaclaims"
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

// attachmentIDs reads the media ids out of the attachments JSON
// (mediaclaims.AttachmentIDs: ["id", …] or [{"media_id": …}]).
func attachmentIDs(raw json.RawMessage) ([]uuid.UUID, error) {
	return mediaclaims.AttachmentIDs(raw)
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
