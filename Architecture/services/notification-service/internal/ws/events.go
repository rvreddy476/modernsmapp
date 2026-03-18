package ws

// RoomEvent is a message sent to a WebSocket room.
type RoomEvent struct {
	Type  string      `json:"type"`            // room_event, badge_update, subscribed, error, pong
	Room  string      `json:"room"`
	Event string      `json:"event,omitempty"` // comment.created, poll.voted, rsvp.updated, typing
	Data  interface{} `json:"data,omitempty"`
}

// ClientMessage is a message received from a WebSocket client.
type ClientMessage struct {
	Type string `json:"type"` // subscribe, unsubscribe, ping, typing
	Room string `json:"room,omitempty"`
}

// Subscription limits
const MaxRoomsPerConnection = 5 // excluding the auto-subscribed notifications room
