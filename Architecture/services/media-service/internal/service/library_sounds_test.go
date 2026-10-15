package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Library sounds (library_sounds.go): which audio_url values are
// deliverable, how a library row is answered on the wire, and the /use
// decision over a faked store. The SQL (one row per library id, concurrent
// first uses) is library_sounds_integration_test.go in the store package.

func TestLibraryAudioSource(t *testing.T) {
	for _, tc := range []struct {
		raw, want string
		ok        bool
	}{
		{"library/monsoon.m4a", "library/monsoon.m4a", true},
		{"/library/monsoon.m4a", "library/monsoon.m4a", true},
		{"  audio-library/a/b.mp3 ", "audio-library/a/b.mp3", true},
		{"https://cdn.example.com/lib/a.m4a", "https://cdn.example.com/lib/a.m4a", true},
		{"HTTPS://cdn.example.com/lib/a.m4a?sig=1", "https://cdn.example.com/lib/a.m4a?sig=1", true},
		{"http://cdn.example.com/a.m4a", "", false},
		{"ftp://cdn.example.com/a.m4a", "", false},
		{"//cdn.example.com/a.m4a", "", false},
		{"https://user:pw@cdn.example.com/a.m4a", "", false},
		{"https:///a.m4a", "", false},
		{"javascript:alert(1)", "", false},
		{"data:audio/mp4;base64,AAAA", "", false},
		{"library/../secrets/key", "", false},
		{"library/./a.m4a", "", false},
		{"library//a.m4a", "", false},
		{"library/a.m4a?x=1", "", false},
		{"library\\a.m4a", "", false},
		{"", "", false},
		{"   ", "", false},
		{"/", "", false},
	} {
		got, ok := LibraryAudioSource(tc.raw)
		if ok != tc.ok || got != tc.want {
			t.Errorf("LibraryAudioSource(%q) = %q, %v; want %q, %v", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

func TestPresentLibraryTrackNeverAnswersAStorageKey(t *testing.T) {
	id := uuid.New()
	for raw, want := range map[string]string{
		"library/monsoon.m4a":               "/v1/audio-library/" + id.String() + "/serve",
		"https://cdn.example.com/a.m4a":     "https://cdn.example.com/a.m4a",
		"ftp://cdn.example.com/a.m4a":       "",
		"library/../../etc/passwd":          "",
		"javascript:alert(document.domain)": "",
	} {
		got := PresentLibraryTrack(postgres.AudioLibraryTrack{ID: id, AudioURL: raw})
		if got.AudioURL != want {
			t.Errorf("audio_url %q answered as %q, want %q", raw, got.AudioURL, want)
		}
	}
}

// fakeLibraryStore is the store slice /use reads and writes.
type fakeLibraryStore struct {
	rows      map[uuid.UUID]*postgres.AudioLibraryTrack
	tracks    map[uuid.UUID]*postgres.AudioTrack // by library id
	getErr    error
	ensureErr error
	keys      []string
}

func (f *fakeLibraryStore) GetAudioLibraryTrack(_ context.Context, id uuid.UUID) (*postgres.AudioLibraryTrack, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	r, ok := f.rows[id]
	if !ok || !r.IsActive {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (f *fakeLibraryStore) EnsureLibrarySound(_ context.Context, libraryID uuid.UUID, audioKey string) (*postgres.AudioTrack, error) {
	f.keys = append(f.keys, audioKey)
	if f.ensureErr != nil {
		return nil, f.ensureErr
	}
	if t, ok := f.tracks[libraryID]; ok {
		cp := *t
		return &cp, nil
	}
	r, ok := f.rows[libraryID]
	if !ok || !r.IsActive {
		return nil, pgx.ErrNoRows
	}
	lib := libraryID
	t := &postgres.AudioTrack{ID: uuid.New(), LibraryID: &lib, Title: r.Title, Artist: r.Artist,
		AudioKey: audioKey, DurationMs: r.DurationMs, Status: "ready", LicenseType: "standard"}
	f.tracks[libraryID] = t
	cp := *t
	return &cp, nil
}

func TestLibrarySoundsUse(t *testing.T) {
	ctx := context.Background()
	user := uuid.New()
	active, inactive, unstored := uuid.New(), uuid.New(), uuid.New()
	newStore := func() *fakeLibraryStore {
		return &fakeLibraryStore{
			rows: map[uuid.UUID]*postgres.AudioLibraryTrack{
				active:   {ID: active, Title: "Monsoon Groove", Artist: "Momentum", DurationMs: 30_000, AudioURL: "/library/monsoon.m4a", IsActive: true},
				inactive: {ID: inactive, Title: "Old", AudioURL: "library/old.m4a", IsActive: false},
				unstored: {ID: unstored, Title: "Elsewhere", AudioURL: "ftp://files.example/a.m4a", IsActive: true},
			},
			tracks: map[uuid.UUID]*postgres.AudioTrack{},
		}
	}

	store := newStore()
	lib := NewLibrarySounds(store, nil)
	first, err := lib.Use(ctx, user, active)
	if err != nil {
		t.Fatalf("first use: %v", err)
	}
	if !first.IsLibrarySound() || *first.LibraryID != active || first.Title != "Monsoon Groove" ||
		first.AudioKey != "library/monsoon.m4a" || first.SourceMediaID != nil || first.CreatorUserID != nil {
		t.Fatalf("first use answered %+v", first)
	}
	second, err := lib.Use(ctx, uuid.New(), active)
	if err != nil || second.ID != first.ID {
		t.Fatalf("second use answered %v (%v), want the first row %s", second, err, first.ID)
	}

	for name, tc := range map[string]struct {
		user, id uuid.UUID
		setup    func(*fakeLibraryStore)
		want     error
	}{
		"no such row":         {user, uuid.New(), nil, ErrLibrarySoundNotFound},
		"an inactive row":     {user, inactive, nil, ErrLibrarySoundNotFound},
		"undeliverable audio": {user, unstored, nil, ErrLibrarySoundNotStored},
		"no caller":           {uuid.Nil, active, nil, ErrLibrarySoundNotFound},
		"the read fails":      {user, active, func(s *fakeLibraryStore) { s.getErr = errors.New("conn reset") }, ErrLibrarySoundUnavailable},
		"the insert fails":    {user, active, func(s *fakeLibraryStore) { s.ensureErr = errors.New("conn reset") }, ErrLibrarySoundUnavailable},
		"deactivated mid-way": {user, active, func(s *fakeLibraryStore) { s.ensureErr = pgx.ErrNoRows }, ErrLibrarySoundNotFound},
		"its sound withdrawn": {user, active, func(s *fakeLibraryStore) {
			l := active
			s.tracks[active] = &postgres.AudioTrack{ID: uuid.New(), LibraryID: &l, AudioKey: "library/monsoon.m4a", Status: "deleted"}
		}, ErrLibrarySoundNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			s := newStore()
			if tc.setup != nil {
				tc.setup(s)
			}
			got, err := NewLibrarySounds(s, nil).Use(ctx, tc.user, tc.id)
			if !errors.Is(err, tc.want) || got != nil {
				t.Fatalf("Use = %+v, %v; want %v", got, err, tc.want)
			}
		})
	}
}
