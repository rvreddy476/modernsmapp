package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/google/uuid"
)

// The HTTP layer maps refusals with errors.Is. These pin that each refusal the
// service can return is the sentinel, not an ad-hoc string a handler would have
// to match on or echo.

type refusingHandleStore struct {
	fakeHandleStore
	latest *store.HandleHistoryEntry
	taken  *store.Profile
}

func (f *refusingHandleStore) GetLatestHandleChange(context.Context, uuid.UUID) (*store.HandleHistoryEntry, error) {
	return f.latest, nil
}

func (f *refusingHandleStore) GetProfileByUsername(context.Context, string) (*store.Profile, error) {
	return f.taken, nil
}

func handleService(f HandleChangeStore) *Service {
	return New(nil, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil))).WithHandleChangeStore(f)
}

func TestChangeHandleRefusalsAreSentinels(t *testing.T) {
	base := fakeHandleStore{profile: &store.Profile{UserID: testUserID, Username: strOf("old_handle")}}
	until := time.Now().Add(48 * time.Hour)

	cases := []struct {
		name  string
		store *refusingHandleStore
		input string
		want  error
	}{
		{"empty", &refusingHandleStore{fakeHandleStore: base}, "@", ErrUsernameEmpty},
		{"cooldown", &refusingHandleStore{fakeHandleStore: base, latest: &store.HandleHistoryEntry{CooldownUntil: until}}, "new_handle", ErrHandleCooldown},
		{"taken", &refusingHandleStore{fakeHandleStore: base, taken: &store.Profile{UserID: uuid.New()}}, "new_handle", ErrUsernameTaken},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := handleService(tc.store).ChangeHandle(context.Background(), testUserID, tc.input)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	_, err := handleService(&refusingHandleStore{fakeHandleStore: base, latest: &store.HandleHistoryEntry{CooldownUntil: until}}).
		ChangeHandle(context.Background(), testUserID, "new_handle")
	var cooldown *HandleCooldownError
	if !errors.As(err, &cooldown) || !cooldown.Until.Equal(until) {
		t.Fatalf("cooldown error = %#v, want Until %v", err, until)
	}
}

func TestSelfFollowAndSelfBlockAreSentinels(t *testing.T) {
	svc := New(nil, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := svc.FollowUser(context.Background(), testUserID, testUserID); !errors.Is(err, ErrCannotFollowSelf) {
		t.Fatalf("FollowUser self err = %v", err)
	}
	if err := svc.BlockUser(context.Background(), testUserID, testUserID); !errors.Is(err, ErrCannotBlockSelf) {
		t.Fatalf("BlockUser self err = %v", err)
	}
	for _, call := range []func() error{
		func() error { _, err := svc.GetModuleProfile(context.Background(), testUserID, "nope"); return err },
		func() error {
			_, err := svc.UpsertModuleProfile(context.Background(), testUserID, "nope", store.UpsertModuleProfileParams{})
			return err
		},
		func() error { return svc.DeleteModuleProfile(context.Background(), testUserID, "nope") },
	} {
		if err := call(); !errors.Is(err, ErrInvalidModule) {
			t.Fatalf("module err = %v, want ErrInvalidModule", err)
		}
	}
}
