package service

// Echo types define how a post is shared/echoed and each has different notification behavior.
//
// | Echo Type    | Who Gets Notified              | Notification                        |
// |--------------|--------------------------------|-------------------------------------|
// | feed         | Original post author           | "{actor} shared your post"          |
// | direct       | Chat recipient(s)              | Chat message (handled by chat svc)  |
// | copy_link    | Nobody                         | Analytics event only                |
// | external     | Nobody                         | Analytics event only                |

// ShouldNotifyOnEcho determines whether the original post author should receive
// a notification for this echo type. Only "feed" echoes notify the author;
// direct echoes are handled by the chat service, and copy_link/external are
// analytics-only events.
func ShouldNotifyOnEcho(echoType string) bool {
	switch echoType {
	case "feed":
		return true // echo_to_feed → notify author with "post.shared"
	case "direct":
		return false // echo_to_direct → chat service handles recipient notification
	case "copy_link":
		return false // clipboard copy → no notification, analytics event only
	case "external":
		return false // OS share sheet → no notification, analytics event only
	default:
		return false
	}
}

// EchoEventType returns the canonical event type string used for echo/share
// notifications. This maps to the "post.shared" template in the registry.
func EchoEventType() string {
	return "post.shared"
}

// EchoAnalyticsEvent returns the analytics-only event type for non-notifying echo types.
// These events are published to Kafka for analytics tracking but never create notifications.
func EchoAnalyticsEvent(echoType string) string {
	switch echoType {
	case "copy_link":
		return "post.link_copied"
	case "external":
		return "post.shared_external"
	default:
		return ""
	}
}
