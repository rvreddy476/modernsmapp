package push

import "context"

// Pusher sends push notifications to device tokens.
type Pusher interface {
	Send(ctx context.Context, token, platform, title, body string, data map[string]string) error
}
