package main

import "testing"

// The command refuses before it touches Kafka: a missing or wrong topic,
// -write with nothing chosen, and production without the confirmation.

func TestParseFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		ok   bool
	}{
		{[]string{}, false},
		{[]string{"-dlq", "media.events"}, false},
		{[]string{"-dlq", "media.events.dlq"}, true},
		{[]string{"-dlq", "media.events.dlq", "-write"}, false},
		{[]string{"-dlq", "media.events.dlq", "-all", "-write"}, true},
		{[]string{"-dlq", "media.events.dlq", "-offsets", "0/3", "-write"}, true},
	} {
		_, err := parseFlags(tc.args)
		if (err == nil) != tc.ok {
			t.Errorf("%v: err %v, want ok %v", tc.args, err, tc.ok)
		}
	}
}

func TestRunRefusesProductionWithoutConfirmation(t *testing.T) {
	t.Setenv("ENV", "prod")
	t.Setenv("KAFKA_BROKERS", "127.0.0.1:1") // never dialled: the guard comes first
	if code := run([]string{"-dlq", "media.events.dlq"}); code != 2 {
		t.Fatalf("exit %d, want 2 (refused)", code)
	}
	if code := run([]string{"-dlq", "media.events.dlq", "-confirm-production", "social.events.v1.dlq"}); code != 2 {
		t.Fatalf("a confirmation naming another topic: exit %d, want 2", code)
	}
}
