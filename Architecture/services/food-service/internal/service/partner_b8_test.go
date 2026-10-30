package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/food-service/internal/onboarding"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
)

type menuImageStore struct {
	Store
	created, updated []postgres.MenuItemInput
	current          string // the stored image_url GetPartnerMenuItem returns
}

func (f *menuImageStore) GetPartnerMenuItem(context.Context, uuid.UUID, uuid.UUID) (*postgres.PartnerMenuItem, error) {
	return &postgres.PartnerMenuItem{MenuItem: postgres.MenuItem{ImageURL: f.current}}, nil
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

	// Audit L6: without a media id, image_url must itself be platform media.
	// The serve URL (relative, or on the media base) is kept as that media's
	// URL; another host is refused before the store is reached.
	for _, given := range []string{
		"/v1/media/" + media.String() + "/serve",
		"https://api.example.test/v1/media/" + media.String() + "/serve",
		media.String(),
	} {
		if _, err := svc.CreateMenuItem(ctx, uuid.New(), uuid.New(), uuid.New(), postgres.MenuItemInput{Name: "Vada", ImageURL: given}); err != nil {
			t.Fatalf("platform image_url %q refused: %v", given, err)
		}
		got := st.created[len(st.created)-1]
		if got.ImageURL != want || got.ImageMediaID == nil || *got.ImageMediaID != media {
			t.Fatalf("platform image_url %q stored as %+v, want %q", given, got, want)
		}
	}
	before := len(st.created)
	_, err := svc.CreateMenuItem(ctx, uuid.New(), uuid.New(), uuid.New(), postgres.MenuItemInput{Name: "Vada", ImageURL: "https://cdn.test/vada.jpg"})
	var fe *onboarding.FieldError
	if !errors.As(err, &fe) || fe.Code != onboarding.CodeImageURLInvalid || fe.Field != "image_url" {
		t.Fatalf("foreign image_url: err = %v, want %s on image_url", err, onboarding.CodeImageURLInvalid)
	}
	if len(st.created) != before {
		t.Fatal("a foreign image_url reached the store")
	}
}

// An update may send back the image already stored (an item saved before L6
// with another host's URL stays editable), but may not set a new foreign one.
func TestUpdateMenuItemImageURL_ForeignOnlyIfUnchanged(t *testing.T) {
	legacy := "https://images.example.test/old.jpg"
	st := &menuImageStore{current: legacy}
	svc := New(st).WithMediaBaseURL("https://api.example.test")
	ctx := context.Background()
	if _, err := svc.UpdateMenuItem(ctx, uuid.New(), uuid.New(), postgres.MenuItemInput{Name: "Idli", ImageURL: legacy}); err != nil {
		t.Fatalf("unchanged legacy image refused: %v", err)
	}
	if st.updated[0].ImageURL != legacy {
		t.Fatalf("unchanged legacy image rewritten: %q", st.updated[0].ImageURL)
	}
	_, err := svc.UpdateMenuItem(ctx, uuid.New(), uuid.New(), postgres.MenuItemInput{Name: "Idli", ImageURL: "https://tracker.example.test/pixel.gif"})
	var fe *onboarding.FieldError
	if !errors.As(err, &fe) || fe.Code != onboarding.CodeImageURLInvalid {
		t.Fatalf("new foreign image: err = %v, want %s", err, onboarding.CodeImageURLInvalid)
	}
	if len(st.updated) != 1 {
		t.Fatal("a new foreign image_url reached the store")
	}
	// Clearing the image is always allowed.
	if _, err := svc.UpdateMenuItem(ctx, uuid.New(), uuid.New(), postgres.MenuItemInput{Name: "Idli"}); err != nil {
		t.Fatalf("clearing the image refused: %v", err)
	}
}
