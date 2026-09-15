package service

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/google/uuid"
)

// ChangeHandle over a fake store: the one write it makes carries the username
// and nothing else.

type fakeHandleStore struct {
	profile *store.Profile
	updates []store.UpdateProfileParams
	history int
}

func (f *fakeHandleStore) GetProfile(context.Context, uuid.UUID) (*store.Profile, error) {
	cp := *f.profile
	return &cp, nil
}

func (f *fakeHandleStore) GetProfileByUsername(context.Context, string) (*store.Profile, error) {
	return nil, nil
}

func (f *fakeHandleStore) GetLatestHandleChange(context.Context, uuid.UUID) (*store.HandleHistoryEntry, error) {
	return nil, nil
}

func (f *fakeHandleStore) UpdateProfile(_ context.Context, _ uuid.UUID, p store.UpdateProfileParams) (*store.Profile, error) {
	f.updates = append(f.updates, p)
	next := *f.profile
	if p.Username != nil {
		u := *p.Username
		next.Username = &u
	}
	f.profile = &next
	return &next, nil
}

func (f *fakeHandleStore) InsertHandleHistory(context.Context, uuid.UUID, string, string) (*store.HandleHistoryEntry, error) {
	f.history++
	return &store.HandleHistoryEntry{}, nil
}

func TestChangeHandle_WritesOnlyTheUsername(t *testing.T) {
	f := &fakeHandleStore{profile: &store.Profile{
		UserID: testUserID, Username: strOf("old_handle"), DisplayName: "Asha K",
		FirstName: strOf("Asha"), LastName: strOf("Surname"), PreferredName: strOf("Ash"),
		Pronouns: strOf("she/her"), Gender: strOf("female"), Bio: "bio", Category: "creator",
		Profession: "Engineer", Website: "https://example.test", Location: "Hyderabad",
		ProfileThemeColor: "#112233", DoB: dateOf("1990-03-17"),
	}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(nil, nil, nil, nil, logger).WithHandleChangeStore(f)

	if _, err := svc.ChangeHandle(context.Background(), testUserID, "@new_handle"); err != nil {
		t.Fatalf("ChangeHandle: %v", err)
	}
	if len(f.updates) != 1 {
		t.Fatalf("store writes = %d, want 1", len(f.updates))
	}
	p := f.updates[0]
	if p.Username == nil || *p.Username != "new_handle" {
		t.Fatalf("username param = %v, want new_handle", p.Username)
	}
	v := reflect.ValueOf(p)
	for i := 0; i < v.NumField(); i++ {
		name := v.Type().Field(i).Name
		if name == "Username" {
			continue
		}
		if !v.Field(i).IsZero() {
			t.Errorf("ChangeHandle wrote %s; a handle change must leave every other column alone", name)
		}
	}
	if f.history != 1 {
		t.Fatalf("handle history rows = %d, want 1", f.history)
	}
}
