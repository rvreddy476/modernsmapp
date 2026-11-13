package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/atpost/identity-shared/verification"
	"github.com/google/uuid"
)

// nameWorld is profiles plus reserved names, with the store's semantics:
// UpdateProfile clears a mark exactly when renameClearsTick would, and the
// reserved-name lookup applies the store's query rule (fuzzy for Official
// and Government, exact for a person, never the changer's own rows).
type nameWorld struct {
	profiles  map[uuid.UUID]*store.Profile
	reserved  map[uuid.UUID][]string
	kinds     map[uuid.UUID]string
	refreshed []uuid.UUID
	findErr   error
}

func newNameWorld() *nameWorld {
	return &nameWorld{profiles: map[uuid.UUID]*store.Profile{}, reserved: map[uuid.UUID][]string{}, kinds: map[uuid.UUID]string{}}
}

func (w *nameWorld) add(p store.Profile) {
	cp := p
	w.profiles[p.UserID] = &cp
	_ = w.RefreshReservedNames(context.Background(), p.UserID)
	w.refreshed = nil
}

func (w *nameWorld) GetProfile(_ context.Context, id uuid.UUID) (*store.Profile, error) {
	p, ok := w.profiles[id]
	if !ok {
		return nil, nil
	}
	cp := *p
	return &cp, nil
}

func (w *nameWorld) GetRegistrationDOB(context.Context, uuid.UUID) (*time.Time, error) {
	return nil, nil
}

func (w *nameWorld) UpdateProfile(_ context.Context, id uuid.UUID, p store.UpdateProfileParams) (*store.Profile, error) {
	cur, ok := w.profiles[id]
	if !ok {
		return nil, nil
	}
	if p.DisplayName != nil {
		if p.ClearVerificationOnRename && *p.DisplayName != cur.DisplayName && isMarkedLevel(cur.VerificationLevel) {
			cur.IsVerified, cur.VerificationLevel = false, ""
			cur.BadgeFlags &^= verification.BadgeAllMarks
		}
		cur.DisplayName = *p.DisplayName
	}
	if p.Username != nil {
		u := *p.Username
		cur.Username = &u
	}
	cp := *cur
	return &cp, nil
}

func (w *nameWorld) GetProfileByUsername(_ context.Context, u string) (*store.Profile, error) {
	for _, p := range w.profiles {
		if p.Username != nil && *p.Username == u {
			cp := *p
			return &cp, nil
		}
	}
	return nil, nil
}

func (w *nameWorld) GetLatestHandleChange(context.Context, uuid.UUID) (*store.HandleHistoryEntry, error) {
	return nil, nil
}

func (w *nameWorld) InsertHandleHistory(context.Context, uuid.UUID, string, string) (*store.HandleHistoryEntry, error) {
	return nil, nil
}

func (w *nameWorld) FindReservedNameHolder(_ context.Context, exclude uuid.UUID, candidate string) (*store.ReservedNameHit, error) {
	if w.findErr != nil {
		return nil, w.findErr
	}
	keys := verification.CandidateKeys(candidate)
	fuzzy := verification.FuzzyVariantsOf(keys)
	for id, variants := range w.reserved {
		if id == exclude {
			continue
		}
		look := fuzzy
		if w.kinds[id] == verification.LevelPerson {
			look = keys
		}
		set := map[string]bool{}
		for _, v := range variants {
			set[v] = true
		}
		for _, v := range look {
			if set[v] {
				return &store.ReservedNameHit{UserID: id, Kind: w.kinds[id]}, nil
			}
		}
	}
	return nil, nil
}

func (w *nameWorld) RefreshReservedNames(_ context.Context, id uuid.UUID) error {
	w.refreshed = append(w.refreshed, id)
	p := w.profiles[id]
	names := []string{p.DisplayName}
	if p.Username != nil {
		names = append(names, *p.Username)
	}
	if p.VerificationLevel == verification.LevelOrganisation || p.VerificationLevel == verification.LevelGovernment {
		names = append(names, p.OrganisationName)
	}
	v, kind := store.ReservedNameRows(p.VerificationLevel, p.IsVerified, names...)
	w.reserved[id], w.kinds[id] = v, kind
	return nil
}

type stubPerson struct {
	keep    bool
	renamed []bool
}

func (s *stubPerson) RenameKeepsTick(context.Context, uuid.UUID, string) bool { return s.keep }
func (s *stubPerson) AfterRename(_ context.Context, _ uuid.UUID, _ *store.Profile, was bool) {
	s.renamed = append(s.renamed, was)
}

var (
	orgUser    = uuid.MustParse("0a000000-0000-0000-0000-000000000001")
	govUser    = uuid.MustParse("0b000000-0000-0000-0000-000000000002")
	personUser = uuid.MustParse("0c000000-0000-0000-0000-000000000003")
	someone    = uuid.MustParse("0d000000-0000-0000-0000-000000000004")
)

func ptr(s string) *string { return &s }

func nameService(w *nameWorld) (*Service, *stubPerson) {
	person := &stubPerson{}
	svc := New(nil, nil, nil, nil, nil).
		WithProfileWriteStore(w, nil).
		WithHandleChangeStore(w).
		WithNameReservations(w).
		WithPersonVerification(person)
	return svc, person
}

func populated() *nameWorld {
	w := newNameWorld()
	w.add(store.Profile{UserID: orgUser, DisplayName: "Infosys Limited", Username: ptr("infosys"), IsVerified: true,
		VerificationLevel: verification.LevelOrganisation, BadgeFlags: verification.BadgePersonVerified | verification.BadgeOrganisation,
		OrganisationName: "Infosys Limited"})
	w.add(store.Profile{UserID: govUser, DisplayName: "Ministry of Example Affairs", Username: ptr("moea_gov"), IsVerified: true,
		VerificationLevel: verification.LevelGovernment, BadgeFlags: verification.BadgeGovernment,
		OrganisationName: "Ministry of Example Affairs"})
	w.add(store.Profile{UserID: personUser, DisplayName: "Asha Rao", Username: ptr("asharao"), IsVerified: true,
		VerificationLevel: verification.LevelPerson, BadgeFlags: verification.BadgePersonVerified})
	w.add(store.Profile{UserID: someone, DisplayName: "Some One", Username: ptr("someone")})
	return w
}

func TestImpersonation_DisplayNameChangeIsHeld(t *testing.T) {
	for _, name := range []string{"Infosys Official", "lnfosys", "INF0SYS", "Ministry of Example Affairs", "Asha Rao", "rao asha"} {
		w := populated()
		svc, _ := nameService(w)
		_, err := svc.UpdateProfile(context.Background(), someone, store.UpdateProfileParams{DisplayName: ptr(name)})
		if !errors.Is(err, ErrNameReserved) {
			t.Errorf("%q: err = %v, want ErrNameReserved", name, err)
		}
		if w.profiles[someone].DisplayName != "Some One" {
			t.Errorf("%q: the held name was written", name)
		}
	}
}

func TestImpersonation_PersonNamesAreExactOnly(t *testing.T) {
	w := populated()
	svc, _ := nameService(w)
	if _, err := svc.UpdateProfile(context.Background(), someone, store.UpdateProfileParams{DisplayName: ptr("Asha Rai")}); err != nil {
		t.Fatalf("a different person's name was held: %v", err)
	}
}

func TestImpersonation_OwnNamesNeverHoldTheOwner(t *testing.T) {
	w := populated()
	svc, _ := nameService(w)
	// The org renames to its own brand form: allowed, and the mark stays.
	p, err := svc.UpdateProfile(context.Background(), orgUser, store.UpdateProfileParams{DisplayName: ptr("Infosys")})
	if err != nil || !p.IsVerified || p.VerificationLevel != verification.LevelOrganisation {
		t.Fatalf("own name: %+v %v", p, err)
	}
}

func TestImpersonation_UnchangedNameIsNotChecked(t *testing.T) {
	w := populated()
	// A pre-existing account already carrying a name that is now reserved
	// can still save its profile with that name.
	w.add(store.Profile{UserID: uuid.New(), DisplayName: "Infosys"})
	var legacy uuid.UUID
	for id, p := range w.profiles {
		if p.DisplayName == "Infosys" && id != orgUser {
			legacy = id
		}
	}
	svc, _ := nameService(w)
	if _, err := svc.UpdateProfile(context.Background(), legacy, store.UpdateProfileParams{DisplayName: ptr("Infosys"), Bio: ptr("hi")}); err != nil {
		t.Fatalf("unchanged name refused: %v", err)
	}
}

func TestImpersonation_HandleChangeIsHeld(t *testing.T) {
	for _, h := range []string{"infosys_official", "lnfosys", "moea.gov", "asha_rao"} {
		w := populated()
		svc, _ := nameService(w)
		if _, err := svc.ChangeHandle(context.Background(), someone, h); !errors.Is(err, ErrNameReserved) {
			t.Errorf("%q: err = %v", h, err)
		}
		if *w.profiles[someone].Username != "someone" {
			t.Errorf("%q: handle written", h)
		}
	}
	w := populated()
	svc, _ := nameService(w)
	if _, err := svc.ChangeHandle(context.Background(), someone, "some_one_else"); err != nil {
		t.Fatal(err)
	}
}

func TestImpersonation_LookupFailureFailsClosed(t *testing.T) {
	w := populated()
	w.findErr = errors.New("db down")
	svc, _ := nameService(w)
	if _, err := svc.UpdateProfile(context.Background(), someone, store.UpdateProfileParams{DisplayName: ptr("Anything")}); err == nil {
		t.Fatal("rename went through without the hold check")
	}
	if _, err := svc.ChangeHandle(context.Background(), someone, "anything"); err == nil {
		t.Fatal("handle change went through without the hold check")
	}
}

func TestImpersonation_SignupNameIsHeld(t *testing.T) {
	w := populated()
	svc, _ := nameService(w)
	newUser := uuid.MustParse("0e000000-0000-0000-0000-000000000005")
	got, err := svc.signupDisplayName(context.Background(), newUser, "Infosys", "Official")
	if err != nil || got != "User 0e000000" {
		t.Fatalf("held sign-up name = %q, %v", got, err)
	}
	got, err = svc.signupDisplayName(context.Background(), newUser, "Asha", "Rao")
	if err != nil || got != "User 0e000000" {
		t.Fatalf("verified person's name at sign-up = %q, %v", got, err)
	}
	got, err = svc.signupDisplayName(context.Background(), newUser, "Ravi", "Kumar")
	if err != nil || got != "Ravi Kumar" {
		t.Fatalf("ordinary sign-up name = %q, %v", got, err)
	}
	w.findErr = errors.New("db down")
	if _, err := svc.signupDisplayName(context.Background(), newUser, "Ravi", "Kumar"); err == nil {
		t.Fatal("sign-up hold check error swallowed")
	}
}

func TestRename_ClearsOrganisationAndGovernmentMarks(t *testing.T) {
	for _, id := range []uuid.UUID{orgUser, govUser} {
		w := populated()
		svc, person := nameService(w)
		p, err := svc.UpdateProfile(context.Background(), id, store.UpdateProfileParams{DisplayName: ptr("Totally New Name")})
		if err != nil {
			t.Fatal(err)
		}
		if p.IsVerified || p.VerificationLevel != "" || p.BadgeFlags&verification.BadgeAllMarks != 0 {
			t.Fatalf("rename kept the mark: %+v", p)
		}
		if len(person.renamed) != 1 || !person.renamed[0] {
			t.Fatalf("AfterRename = %v, want one call with wasMarked", person.renamed)
		}
		// The names it held are released.
		if len(w.reserved[id]) != 0 {
			t.Fatalf("reserved names kept after the mark went: %v", w.reserved[id])
		}
	}
}

func TestRename_OrganisationKeepsItsMarkForItsOwnName(t *testing.T) {
	w := populated()
	svc, person := nameService(w)
	person.keep = true // a person's answer must not decide an organisation's mark
	p, err := svc.UpdateProfile(context.Background(), orgUser, store.UpdateProfileParams{DisplayName: ptr("INFOSYS LTD.")})
	if err != nil || !p.IsVerified || p.VerificationLevel != verification.LevelOrganisation {
		t.Fatalf("own-name rename lost the mark: %+v %v", p, err)
	}
	p, err = svc.UpdateProfile(context.Background(), orgUser, store.UpdateProfileParams{DisplayName: ptr("Infosys Fan Club")})
	if err != nil || p.IsVerified {
		t.Fatalf("a different name kept the mark: %+v %v", p, err)
	}
}

func TestRename_RefreshesReservedNames(t *testing.T) {
	w := populated()
	svc, _ := nameService(w)
	if _, err := svc.UpdateProfile(context.Background(), someone, store.UpdateProfileParams{Bio: ptr("x")}); err != nil {
		t.Fatal(err)
	}
	if len(w.refreshed) != 0 {
		t.Fatal("a bio edit rebuilt reserved names")
	}
	if _, err := svc.ChangeHandle(context.Background(), personUser, "asha.rao.writes"); err != nil {
		t.Fatal(err)
	}
	if len(w.refreshed) != 1 || w.refreshed[0] != personUser {
		t.Fatalf("handle change refreshed %v", w.refreshed)
	}
}
