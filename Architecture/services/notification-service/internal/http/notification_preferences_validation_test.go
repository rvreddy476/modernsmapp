package http

import (
	"testing"

	"github.com/atpost/notification-service/internal/store/postgres"
)

func TestValidateDetailedNotificationPreferences(t *testing.T) {
	start, end, timezone := "22:00", "07:00", "Asia/Kolkata"
	valid := postgres.NotificationPreferences{
		EmailDigest: "daily", QuietHoursEnabled: true,
		QuietHoursStart: &start, QuietHoursEnd: &end, QuietHoursTZ: &timezone,
	}
	if err := validateDetailedNotificationPreferences(&valid); err != nil {
		t.Fatalf("valid preferences rejected: %v", err)
	}

	tests := []struct {
		name string
		edit func(*postgres.NotificationPreferences)
	}{
		{"unknown digest", func(p *postgres.NotificationPreferences) { p.EmailDigest = "hourly" }},
		{"bad start", func(p *postgres.NotificationPreferences) { value := "25:00"; p.QuietHoursStart = &value }},
		{"bad end", func(p *postgres.NotificationPreferences) { value := "7am"; p.QuietHoursEnd = &value }},
		{"bad timezone", func(p *postgres.NotificationPreferences) { value := "India"; p.QuietHoursTZ = &value }},
		{"missing timezone", func(p *postgres.NotificationPreferences) { p.QuietHoursTZ = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.edit(&candidate)
			if err := validateDetailedNotificationPreferences(&candidate); err == nil {
				t.Fatal("invalid preferences were accepted")
			}
		})
	}
}
