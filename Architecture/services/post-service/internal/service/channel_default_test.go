package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/atpost/post-service/internal/store/postgres"
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

// Channel names are unique, ignoring case (founder, 2026-10-21).
func TestEnsureDefaultChannelPicksAFreeName(t *testing.T) {
	ctx := context.Background()
	store := newFakeChannelStore()
	svc := newChannelTestService(store)
	a, _, err := svc.EnsureDefaultChannel(ctx, uuid.New(), "Raghuvaran Nalabolu", "prvr2023")
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := svc.EnsureDefaultChannel(ctx, uuid.New(), "raghuvaran nalabolu", "reddyraghuvaran1")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "Raghuvaran Nalabolu" || b.Name != "raghuvaran nalabolu 2" {
		t.Fatalf("names: %q, %q", a.Name, b.Name)
	}
}

func TestChannelNameVariantStaysWithinTheLimit(t *testing.T) {
	long := strings.Repeat("x", MaxChannelNameRunes)
	got := channelNameVariant(long, 12)
	if n := len([]rune(got)); n != MaxChannelNameRunes || !strings.HasSuffix(got, " 12") {
		t.Fatalf("variant %q (%d runes)", got, n)
	}
}

func TestChannelNameAvailability(t *testing.T) {
	ctx := context.Background()
	store := newFakeChannelStore()
	svc := newChannelTestService(store)
	owner, other := uuid.New(), uuid.New()
	if _, err := svc.CreateChannel(ctx, owner, CreateChannelInput{Name: "Call B Studio", Handle: "call.b"}); err != nil {
		t.Fatal(err)
	}
	if ok, suggestion, err := svc.ChannelNameAvailability(ctx, other, "  call b studio "); err != nil || ok || suggestion != "call b studio 2" {
		t.Fatalf("taken: ok=%v suggestion=%q err=%v", ok, suggestion, err)
	}
	if ok, _, err := svc.ChannelNameAvailability(ctx, owner, "CALL B STUDIO"); err != nil || !ok {
		t.Fatalf("own name must be available to its owner: ok=%v err=%v", ok, err)
	}
	if ok, s, err := svc.ChannelNameAvailability(ctx, other, "Fresh Name"); err != nil || !ok || s != "Fresh Name" {
		t.Fatalf("free: ok=%v s=%q err=%v", ok, s, err)
	}
	if _, _, err := svc.ChannelNameAvailability(ctx, other, "ab"); !errors.Is(err, ErrInvalidChannelName) {
		t.Fatalf("invalid: %v", err)
	}
}

func TestCreateAndRenameRefuseATakenName(t *testing.T) {
	ctx := context.Background()
	store := newFakeChannelStore()
	svc := newChannelTestService(store)
	owner, other := uuid.New(), uuid.New()
	if _, err := svc.CreateChannel(ctx, owner, CreateChannelInput{Name: "Call B Studio", Handle: "call.b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateChannel(ctx, other, CreateChannelInput{Name: "CALL B STUDIO", Handle: "other"}); !errors.Is(err, postgres.ErrChannelNameTaken) {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.CreateChannel(ctx, other, CreateChannelInput{Name: "Other Studio", Handle: "other"}); err != nil {
		t.Fatal(err)
	}
	taken := "call b studio"
	if _, err := svc.UpdateMyChannel(ctx, other, UpdateChannelInput{Name: &taken}); !errors.Is(err, postgres.ErrChannelNameTaken) {
		t.Fatalf("rename: %v", err)
	}
	recase := "CALL B STUDIO"
	if _, err := svc.UpdateMyChannel(ctx, owner, UpdateChannelInput{Name: &recase}); err != nil {
		t.Fatalf("re-casing your own name must work: %v", err)
	}
}
