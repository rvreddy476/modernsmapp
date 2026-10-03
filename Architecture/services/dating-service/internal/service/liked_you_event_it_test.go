package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// Mechanic M4: the spark notification names its actor, so a recipient the
// gate locks gets dating.spark.created without the sender (or the note); a
// pass holder, or anyone with the gate off, gets it as before.

func sparkCreatedFor(t *testing.T, rec *recordingWriter, to uuid.UUID) map[string]any {
	t.Helper()
	for _, ev := range rec.events(t) {
		if ev.EventType != "dating.spark.created" {
			continue
		}
		var p map[string]any
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p["to_user_id"] == to.String() {
			return p
		}
	}
	t.Fatalf("no dating.spark.created for %s", to)
	return nil
}

func TestLikedYou_SparkEventHidesTheSenderFromALockedRecipient(t *testing.T) {
	for _, tc := range []struct {
		name       string
		gate, pass bool
		wantSender bool
	}{
		{name: "gate on, no pass", gate: true, wantSender: false},
		{name: "gate on, pass", gate: true, pass: true, wantSender: true},
		{name: "gate off", gate: false, wantSender: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, st, rec := newD3Svc(t)
			svc.SetMechanicsConfig(MechanicsConfig{LikedYouGate: tc.gate})
			ctx := context.Background()
			from, to := uuid.New(), uuid.New()
			seedActiveProfile(t, st, from)
			seedActiveProfile(t, st, to)
			if tc.pass {
				d8Exec(t, st, `INSERT INTO dating_premium_subscriptions (user_id, plan, started_at, expires_at, source)
                    VALUES ($1, 'pass_30d', now(), now() + interval '10 days', 'test')`, to)
			}
			if _, _, err := svc.CreateSpark(ctx, from, to, "prompt", "m4", "Loved your answer"); err != nil {
				t.Fatalf("spark: %v", err)
			}
			p := sparkCreatedFor(t, rec, to)
			_, hasSender := p["from_user_id"]
			if tc.wantSender {
				if p["from_user_id"] != from.String() || p["note"] != "Loved your answer" {
					t.Fatalf("payload = %v, want the sender and the note", p)
				}
			} else if (hasSender && p["from_user_id"] != "") || p["note"] != nil {
				t.Fatalf("payload = %v, want no sender and no note", p)
			}
		})
	}
}
