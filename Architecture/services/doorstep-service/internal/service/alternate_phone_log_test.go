package service

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Security sweep #26 (21 Oct 2026): RequestAlternatePhone logged the
// alternate-contact OTP itself at Warn. The code must never reach a log line:
// no "code" attribute, and no six-digit run anywhere in what was written.
func TestAlternatePhoneOTPIsNeverLogged(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	st := &alternateTestStore{}
	s := &Service{pro: ProDeps{Store: st, DevPhoneOTP: true}}
	for i := 0; i < 5; i++ {
		if _, err := s.RequestAlternatePhone(context.Background(), uuid.New()); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	if !st.called || buf.Len() == 0 {
		t.Fatal("the request path did not run or logged nothing; the test proves nothing")
	}
	sixDigits := regexp.MustCompile(`(^|[^0-9])[0-9]{6}([^0-9]|$)`)
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		if _, ok := rec["code"]; ok {
			t.Fatalf("a log line carries the OTP code: %s", line)
		}
		// user_id (a UUID) and time (fractional seconds) carry digit runs of
		// their own; strip them so they cannot pass for a code.
		delete(rec, "user_id")
		delete(rec, "time")
		rest, _ := json.Marshal(rec)
		if sixDigits.Match(rest) {
			t.Fatalf("a log line carries a six-digit value: %s", line)
		}
	}
}
