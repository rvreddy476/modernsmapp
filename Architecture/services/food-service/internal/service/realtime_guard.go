package service

import (
	"log/slog"

	"github.com/atpost/food-service/internal/foodevents"
)

// otpFieldIn reports the first OTP key found anywhere in data's JSON form.
// None may ever appear in a realtime frame or an outbox event: topics fan out
// to every holder of a token and outbox events go to Kafka consumers, while
// the codes are shown only on the rider's assignment (pickup_code) and the
// customer's order detail (delivery_code). The store's in-transaction outbox
// writes use the same guard (foodevents.Marshal).
func otpFieldIn(data any) (string, bool) {
	return foodevents.OTPField(data)
}

// payloadSafe refuses (and logs at ERROR) any event whose payload carries an
// OTP field. Refusing, rather than stripping, keeps the bug visible: the event
// does not go out at all, and the tests that expect it fail.
func payloadSafe(topic, eventType string, data any) bool {
	if field, bad := otpFieldIn(data); bad {
		slog.Error("food-service: refused to publish an event carrying a delivery code",
			"topic", topic, "event", eventType, "field", field)
		return false
	}
	return true
}
