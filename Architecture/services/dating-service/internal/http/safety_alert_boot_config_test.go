package http

import (
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/service"
)

func TestResolveSafetyConfig_PanicAlertWindow(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "DATING_PANIC_ALERT_WINDOW_HOURS" {
				return v
			}
			return ""
		}
	}
	cfg, err := ResolveSafetyConfig(env(""))
	if err != nil || cfg.PanicAlertWindow != service.DefaultPanicAlertWindow {
		t.Fatalf("default: window=%s err=%v", cfg.PanicAlertWindow, err)
	}
	cfg, err = ResolveSafetyConfig(env("6"))
	if err != nil || cfg.PanicAlertWindow != 6*time.Hour {
		t.Fatalf("6 hours: window=%s err=%v", cfg.PanicAlertWindow, err)
	}
	for _, bad := range []string{"0", "73", "1.5", "day"} {
		if _, err := ResolveSafetyConfig(env(bad)); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}
