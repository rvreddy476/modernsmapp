package service

import (
	"context"
	"testing"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
)

type menuImageStore struct {
	Store
	created, updated []postgres.MenuItemInput
}

func (f *menuImageStore) CreateMenuItem(_ context.Context, _, _, _ uuid.UUID, in postgres.MenuItemInput) (*postgres.MenuItem, error) {
	f.created = append(f.created, in)
	return &postgres.MenuItem{ImageURL: in.ImageURL, ImageMediaID: in.ImageMediaID}, nil
}

func (f *menuImageStore) UpdateMenuItem(_ context.Context, _, _ uuid.UUID, in postgres.MenuItemInput) (*postgres.MenuItem, error) {
	f.updated = append(f.updated, in)
	return &postgres.MenuItem{ImageURL: in.ImageURL, ImageMediaID: in.ImageMediaID}, nil
}

// A media id decides image_url; without one, image_url is kept as given.
func TestMenuItemImageMediaIDResolvesToAURL(t *testing.T) {
	st := &menuImageStore{}
	svc := New(st).WithMediaBaseURL("https://api.example.test")
	media := uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0007")
	ctx := context.Background()

	if _, err := svc.CreateMenuItem(ctx, uuid.New(), uuid.New(), uuid.New(), postgres.MenuItemInput{Name: "Idli", ImageURL: "https://ignored.test/x.jpg", ImageMediaID: &media}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateMenuItem(ctx, uuid.New(), uuid.New(), postgres.MenuItemInput{Name: "Idli", ImageMediaID: &media}); err != nil {
		t.Fatal(err)
	}
	want := "https://api.example.test/v1/media/" + media.String() + "/serve"
	if st.created[0].ImageURL != want || st.updated[0].ImageURL != want {
		t.Fatalf("image_url = %q / %q, want %q", st.created[0].ImageURL, st.updated[0].ImageURL, want)
	}

	if _, err := svc.CreateMenuItem(ctx, uuid.New(), uuid.New(), uuid.New(), postgres.MenuItemInput{Name: "Vada", ImageURL: "https://cdn.test/vada.jpg"}); err != nil {
		t.Fatal(err)
	}
	if st.created[1].ImageURL != "https://cdn.test/vada.jpg" || st.created[1].ImageMediaID != nil {
		t.Fatalf("plain image_url changed: %+v", st.created[1])
	}
}
