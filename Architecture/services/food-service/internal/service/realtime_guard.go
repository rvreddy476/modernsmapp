package service

import (
	"encoding/json"
	"log/slog"
)

// otpFieldNames are the JSON keys that carry a delivery OTP. None of them may
// ever appear in a realtime frame or an outbox event: topics fan out to every
// holder of a token and outbox events go to Kafka consumers, while the codes
// are shown only on the rider's assignment (pickup_code) and the customer's
// order detail (delivery_code).
var otpFieldNames = map[string]struct{}{
	"pickup_code":   {},
	"delivery_code": {},
	"pickup_otp":    {},
	"delivery_otp":  {},
}

// otpFieldIn reports the first OTP key found anywhere in data's JSON form.
func otpFieldIn(data any) (string, bool) {
	raw, err := json.Marshal(data)
	if err != nil {
		return "", false
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", false
	}
	return findOTPField(v)
}

func findOTPField(v any) (string, bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if _, bad := otpFieldNames[k]; bad {
				return k, true
			}
			if f, bad := findOTPField(child); bad {
				return f, true
			}
		}
	case []any:
		for _, child := range t {
			if f, bad := findOTPField(child); bad {
				return f, true
			}
		}
	}
	return "", false
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
