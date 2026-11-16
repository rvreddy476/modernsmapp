package reindex

import (
	"context"
	"testing"
	"time"
)

// The users reindex stamps every document with the time its page was
// requested (user_profile_state.go), so later live events compare against
// it: an edit before the read loses, an edit after it wins.
func TestReindexUsers_StampsThePageReadTimeAsTheVersion(t *testing.T) {
	read := time.Date(2026, 11, 16, 9, 0, 0, 654321000, time.UTC)
	prev := reindexClock
	reindexClock = func() time.Time { return read }
	t.Cleanup(func() { reindexClock = prev })

	store, os := newFakeOS(t)
	res, err := ReindexUsers(context.Background(), nil, fakeProfiles(t, activeUser, deactivatedUser), "", store, nil, nil, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	if res.Indexed != 2 {
		t.Fatalf("result = %+v", res)
	}
	for _, id := range []string{activeUser, deactivatedUser} {
		got, _ := os.bulkDocs[id]["source_updated_us"].(float64)
		if int64(got) != read.UnixMicro() {
			t.Fatalf("%s source_updated_us = %v, want the page read time %d", id, os.bulkDocs[id]["source_updated_us"], read.UnixMicro())
		}
	}
}
