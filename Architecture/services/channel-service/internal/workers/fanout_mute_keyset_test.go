package workers

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Audit 2026-11-07, CH-M4: a mute stops the push, whatever notify_on says
// and even for a critical alert; the feed injection is unaffected.
func TestShouldPushRespectsMute(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	later, earlier := now.Add(time.Hour), now.Add(-time.Hour)
	plain := UpdatePublishedPayload{UpdateType: "announcement"}
	critical := UpdatePublishedPayload{UpdateType: "alert", Severity: "critical"}
	for _, tc := range []struct {
		name string
		sub  subscriberRow
		p    UpdatePublishedPayload
		want bool
	}{
		{"all, not muted", subscriberRow{NotifyOn: "all"}, plain, true},
		{"all, muted", subscriberRow{NotifyOn: "all", MutedUntil: &later}, plain, false},
		{"all, mute expired", subscriberRow{NotifyOn: "all", MutedUntil: &earlier}, plain, true},
		{"critical alert, muted", subscriberRow{NotifyOn: "all", MutedUntil: &later}, critical, false},
		{"critical alert pierces notify_on=none", subscriberRow{NotifyOn: "none"}, critical, true},
		{"highlights, plain", subscriberRow{NotifyOn: "highlights"}, plain, false},
		{"highlights, critical", subscriberRow{NotifyOn: "highlights"}, critical, true},
		{"none, plain", subscriberRow{NotifyOn: "none"}, plain, false},
	} {
		tc.sub.UserID = uuid.New()
		if got := shouldPush(tc.sub, tc.p, now); got != tc.want {
			t.Errorf("%s: shouldPush = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// CH-M4 + CH-L2: the roster read carries the mute and pages by keyset on the
// primary key, never by OFFSET.
func TestSubscriberBatchQueryKeysetAndMute(t *testing.T) {
	q := subscriberBatchQuery
	for _, want := range []string{"cm.muted_until", "cm.user_id > $2", "ORDER BY cm.user_id", "LIMIT $3"} {
		if !strings.Contains(q, want) {
			t.Errorf("subscriberBatchQuery must contain %q:\n%s", want, q)
		}
	}
	if strings.Contains(q, "OFFSET") {
		t.Error("subscriberBatchQuery must not page by OFFSET: joins and leaves shift the window")
	}
}
