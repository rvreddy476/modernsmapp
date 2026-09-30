package service

import "github.com/google/uuid"

// buildPushData is the device push's copy and data payload for one
// notification.
//
// entity_type, entity_id and deep_link ride the data payload so the client
// can open the exact destination from a tap — including background taps,
// where FCM hands these keys to the launch intent as extras. title and body
// ride it too, so a client that renders its own notification (a data-only
// delivery) shows the same words the system tray would. No message content is
// ever included: chat pushes stay generic by construction, which is what
// keeps previews and lock screens privacy-safe regardless of client settings.
//
// `render` overrides the generic per-type copy and the collapse key when the
// caller rendered them (uploads, orders). It used to be accepted by
// deliverWithDecision and then ignored, so every upload push said "New
// Notification".
func buildPushData(userID uuid.UUID, notifType, entityType string, entityID uuid.UUID, deepLink string, render RenderOverride) (title, body string, data map[string]string) {
	title, body = notifTitleBody(notifType)
	if render.Title != "" {
		title = render.Title
		if render.Body != "" {
			body = render.Body
		}
	}
	data = map[string]string{
		"type":        notifType,
		"entity_type": entityType,
		"entity_id":   entityID.String(),
		"deep_link":   deepLink,
		"title":       title,
		"body":        body,
	}
	// Collapse key so repeated notifications (e.g. many likes) replace each
	// other on the device instead of flooding.
	ck := render.CollapseKey
	if ck == "" {
		ck = GetCollapseKey(notifType, entityID.String(), userID.String())
	}
	if ck != "" {
		data["collapse_key"] = ck
	}
	return title, body, data
}
