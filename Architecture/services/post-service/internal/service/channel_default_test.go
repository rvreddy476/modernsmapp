package service

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestDefaultChannelName(t *testing.T) {
	cases := []struct{ display, username, want string }{
		{"Raghu Varan", "raghu.v", "Raghu Varan"},
		{"  Raghu   Varan ", "", "Raghu Varan"},
		{"Al", "al.writes", "al.writes"}, // too short: falls to the username
		{"", "", "My channel"},
		{strings.Repeat("a", 60), "", strings.Repeat("a", MaxChannelNameRunes)},
	}
	for _, tc := range cases {
		if got := DefaultChannelName(tc.display, tc.username); got != tc.want {
			t.Errorf("DefaultChannelName(%q,%q) = %q, want %q", tc.display, tc.username, got, tc.want)
		}
	}
}

func TestEnsureDefaultChannelUsesUsernameAndAvoidsTakenHandles(t *testing.T) {
	ctx := context.Background()
	store := newFakeChannelStore()
	svc := newChannelTestService(store)

	first, created, err := svc.EnsureDefaultChannel(ctx, uuid.New(), "Raghu Varan", "raghu.v")
	if err != nil || !created {
		t.Fatalf("first: created=%v err=%v", created, err)
	}
	if first.Handle != "raghu.v" || first.Name != "Raghu Varan" {
		t.Fatalf("first: handle=%q name=%q", first.Handle, first.Name)
	}
	second, _, err := svc.EnsureDefaultChannel(ctx, uuid.New(), "Raghu Other", "raghu.v")
	if err != nil {
		t.Fatal(err)
	}
	if second.Handle == first.Handle {
		t.Fatalf("handles must stay unique: both %q", second.Handle)
	}
}

func TestEnsureDefaultChannelIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := newFakeChannelStore()
	svc := newChannelTestService(store)
	user := uuid.New()
	if _, err := svc.CreateChannel(ctx, user, CreateChannelInput{Name: "My Own Name", Handle: "mine"}); err != nil {
		t.Fatal(err)
	}
	ch, created, err := svc.EnsureDefaultChannel(ctx, user, "Default Name", "default")
	if err != nil || created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if ch.Name != "My Own Name" || ch.Handle != "mine" {
		t.Fatalf("an existing channel must be left as the owner made it: %+v", ch)
	}
}

// The lazy path: an account with no channel opening its own channel gets
// the default one instead of 404.
func TestGetMyChannelCreatesTheDefaultChannel(t *testing.T) {
	store := newFakeChannelStore()
	svc := newChannelTestService(store)
	user := uuid.New()
	view, err := svc.GetMyChannel(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if view == nil || store.byUser[user] == nil {
		t.Fatal("GetMyChannel must leave the account with its channel")
	}
}

// The event and a lazy path at once still leave one channel.
func TestEnsureDefaultChannelConcurrentCallsMakeOne(t *testing.T) {
	store := newFakeChannelStore()
	svc := newChannelTestService(store)
	user := uuid.New()
	var mu sync.Mutex // the fake store is not goroutine-safe
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.Lock()
			defer mu.Unlock()
			if _, _, err := svc.EnsureDefaultChannel(context.Background(), user, "Raghu Varan", "raghu.v"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := len(store.byUser); n != 1 {
		t.Fatalf("channels = %d, want 1", n)
	}
}
