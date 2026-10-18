package service

import (
	"context"

	"github.com/google/uuid"
)

type alertToneStore interface {
	ListAlertTones(ctx context.Context, conversationID uuid.UUID) (map[uuid.UUID]string, error)
}

// recipientAlertTones maps each recipient with a non-default alert tone to
// it (CONTRACT §8: the push carries alert_tone). Fails open to "no map": a
// lookup error means default tones, never a lost notification.
func (s *Service) recipientAlertTones(ctx context.Context, conversationID uuid.UUID, recipients []string) map[string]string {
	st, ok := s.convStore.(alertToneStore)
	if !ok || len(recipients) == 0 {
		return nil
	}
	tones, err := st.ListAlertTones(ctx, conversationID)
	if err != nil || len(tones) == 0 {
		return nil
	}
	want := make(map[string]bool, len(recipients))
	for _, r := range recipients {
		want[r] = true
	}
	out := map[string]string{}
	for id, tone := range tones {
		if want[id.String()] {
			out[id.String()] = tone
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
