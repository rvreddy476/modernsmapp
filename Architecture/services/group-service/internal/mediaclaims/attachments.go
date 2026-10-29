package mediaclaims

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// AttachmentIDs reads the media ids out of a group post's attachments JSON
// (group_posts.attachments). Both shapes the composer has ever sent are
// accepted: ["id", …] and [{"media_id": …}]. Empty, absent and null hold
// nothing. Anything else is refused: a writer must not store a list whose
// media nobody can check or claim.
func AttachmentIDs(raw json.RawMessage) ([]uuid.UUID, error) {
	return attachmentIDs(raw, true)
}

// StoredAttachmentIDs is AttachmentIDs for a row already written, for the
// reconcile tool: an element that names no media id is skipped rather than
// failing the row, so the row's other media are still claimed.
func StoredAttachmentIDs(raw json.RawMessage) []uuid.UUID {
	ids, _ := attachmentIDs(raw, false)
	return ids
}

func attachmentIDs(raw json.RawMessage, strict bool) ([]uuid.UUID, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(trimmed, &items); err != nil {
		if strict {
			return nil, fmt.Errorf("invalid: attachments must be a list")
		}
		return nil, nil
	}
	var out []uuid.UUID
	for _, it := range items {
		id, err := attachmentID(it)
		if err != nil {
			if strict {
				return nil, err
			}
			continue
		}
		out = append(out, id)
	}
	return out, nil
}

func attachmentID(it json.RawMessage) (uuid.UUID, error) {
	var asString string
	if err := json.Unmarshal(it, &asString); err == nil {
		id, err := uuid.Parse(asString)
		if err != nil {
			return uuid.Nil, fmt.Errorf("invalid: attachment %q is not a media id", asString)
		}
		return id, nil
	}
	var asObject struct {
		MediaID string `json:"media_id"`
	}
	if err := json.Unmarshal(it, &asObject); err != nil || asObject.MediaID == "" {
		return uuid.Nil, fmt.Errorf("invalid: attachment is not a media id")
	}
	id, err := uuid.Parse(asObject.MediaID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid: attachment %q is not a media id", asObject.MediaID)
	}
	return id, nil
}
