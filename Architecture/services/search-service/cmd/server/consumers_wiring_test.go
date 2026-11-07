package main

import (
	"os"
	"strings"
	"testing"
)

// The community consumer is on by default (2026-11-08), on the topic
// community-service publishes to, in its own group, starting at the end of
// the topic. Off, community.* never reached search and an archived or
// private community stayed listed until the next backfill.
func TestCommunityConsumerIsWiredOnByDefault(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	for _, want := range []string{
		`env("COMMUNITY_KAFKA_TOPIC", "community-events")`,
		`brokerList, "search-service-community-group", communityTopic, searchStore, kafkaDialer, kafka.LastOffset,`,
		`go communityConsumer.Start(consumerCtx)`,
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("main.go no longer contains %q", want)
		}
	}

	// The default must be the topic community-service writes.
	producer, err := os.ReadFile("../../../community-service/cmd/server/main.go")
	if err != nil {
		t.Skipf("community-service not checked out next to search-service: %v", err)
	}
	if !strings.Contains(string(producer), `env("KAFKA_TOPIC", "community-events")`) {
		t.Fatal("community-service no longer publishes to community-events by default; move search's default with it")
	}
}
