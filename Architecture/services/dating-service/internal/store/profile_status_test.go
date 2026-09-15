package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// parseState reads "status<prior+p" (prior and +p optional).
func parseState(s string) ProfileStatusState {
	var st ProfileStatusState
	if strings.HasSuffix(s, "+p") {
		st.Paused = true
		s = strings.TrimSuffix(s, "+p")
	}
	if i := strings.Index(s, "<"); i >= 0 {
		st.Status, st.Prior = s[:i], s[i+1:]
	} else {
		st.Status = s
	}
	return st
}

var defaultActor = map[ProfileEvent]ProfileActor{
	ProfileEventBasicsComplete: ProfileActorSystem,
	ProfileEventPhotoApproved:  ProfileActorSystem,
	ProfileEventSelfiePassed:   ProfileActorSystem,
	ProfileEventPause:          ProfileActorUser,
	ProfileEventUnpause:        ProfileActorUser,
	ProfileEventReview:         ProfileActorAdmin,
	ProfileEventRestrict:       ProfileActorAdmin,
	ProfileEventSuspend:        ProfileActorAdmin,
	ProfileEventReinstate:      ProfileActorAdmin,
	ProfileEventDelete:         ProfileActorUser,
}

var allEvents = []ProfileEvent{
	ProfileEventBasicsComplete, ProfileEventPhotoApproved, ProfileEventSelfiePassed,
	ProfileEventPause, ProfileEventUnpause,
	ProfileEventReview, ProfileEventRestrict, ProfileEventSuspend, ProfileEventReinstate,
	ProfileEventDelete,
}

// allowedTransitions is the lane D2 table, written out by hand. Every
// (from, event) pair not listed must be refused with
// ErrProfileTransitionNotAllowed. Event actors are defaultActor.
var allowedTransitions = map[string]string{
	// draft
	"draft|basics_complete": "pending_photo",
	"draft|pause":           "paused<draft+p",
	"draft|unpause":         "draft",
	"draft|review":          "pending_review<draft",
	"draft|restrict":        "restricted<draft",
	"draft|suspend":         "suspended<draft",
	"draft|delete":          "deleted<draft+p",
	// pending_photo
	"pending_photo|photo_approved": "pending_selfie",
	"pending_photo|pause":          "paused<pending_photo+p",
	"pending_photo|unpause":        "pending_photo",
	"pending_photo|review":         "pending_review<pending_photo",
	"pending_photo|restrict":       "restricted<pending_photo",
	"pending_photo|suspend":        "suspended<pending_photo",
	"pending_photo|delete":         "deleted<pending_photo+p",
	// pending_selfie
	"pending_selfie|selfie_passed": "active",
	"pending_selfie|pause":         "paused<pending_selfie+p",
	"pending_selfie|unpause":       "pending_selfie",
	"pending_selfie|review":        "pending_review<pending_selfie",
	"pending_selfie|restrict":      "restricted<pending_selfie",
	"pending_selfie|suspend":       "suspended<pending_selfie",
	"pending_selfie|delete":        "deleted<pending_selfie+p",
	// active
	"active|pause":    "paused<active+p",
	"active|unpause":  "active",
	"active|review":   "pending_review<active",
	"active|restrict": "restricted<active",
	"active|suspend":  "suspended<active",
	"active|delete":   "deleted<active+p",
	// paused from active
	"paused<active+p|pause":    "paused<active+p",
	"paused<active+p|unpause":  "active",
	"paused<active+p|review":   "pending_review<active+p",
	"paused<active+p|restrict": "restricted<active+p",
	"paused<active+p|suspend":  "suspended<active+p",
	"paused<active+p|delete":   "deleted<active+p",
	// paused mid-onboarding: unpause returns to the step, onboarding
	// evidence advances the remembered step.
	"paused<draft+p|basics_complete": "paused<pending_photo+p",
	"paused<draft+p|pause":           "paused<draft+p",
	"paused<draft+p|unpause":         "draft",
	"paused<draft+p|review":          "pending_review<draft+p",
	"paused<draft+p|restrict":        "restricted<draft+p",
	"paused<draft+p|suspend":         "suspended<draft+p",
	"paused<draft+p|delete":          "deleted<draft+p",
	"paused<pending_selfie+p|selfie_passed": "paused<active+p",
	"paused<pending_selfie+p|pause":         "paused<pending_selfie+p",
	"paused<pending_selfie+p|unpause":       "pending_selfie",
	"paused<pending_selfie+p|review":        "pending_review<pending_selfie+p",
	"paused<pending_selfie+p|restrict":      "restricted<pending_selfie+p",
	"paused<pending_selfie+p|suspend":       "suspended<pending_selfie+p",
	"paused<pending_selfie+p|delete":        "deleted<pending_selfie+p",
	// holds over an active profile
	"pending_review<active|pause":     "pending_review<active+p",
	"pending_review<active|unpause":   "pending_review<active",
	"pending_review<active|review":    "pending_review<active",
	"pending_review<active|restrict":  "restricted<active",
	"pending_review<active|suspend":   "suspended<active",
	"pending_review<active|reinstate": "active",
	"pending_review<active|delete":    "deleted<active+p",
	"restricted<active|pause":         "restricted<active+p",
	"restricted<active|unpause":       "restricted<active",
	"restricted<active|review":        "pending_review<active",
	"restricted<active|restrict":      "restricted<active",
	"restricted<active|suspend":       "suspended<active",
	"restricted<active|reinstate":     "active",
	"restricted<active|delete":        "deleted<active+p",
	"suspended<active|pause":          "suspended<active+p",
	"suspended<active|unpause":        "suspended<active",
	"suspended<active|review":         "pending_review<active",
	"suspended<active|restrict":       "restricted<active",
	"suspended<active|suspend":        "suspended<active",
	"suspended<active|reinstate":      "active",
	"suspended<active|delete":         "deleted<active+p",
	// a hold mid-onboarding: reinstate returns to the step, not active
	"restricted<pending_photo|photo_approved": "restricted<pending_selfie",
	"restricted<pending_photo|pause":          "restricted<pending_photo+p",
	"restricted<pending_photo|unpause":        "restricted<pending_photo",
	"restricted<pending_photo|review":         "pending_review<pending_photo",
	"restricted<pending_photo|restrict":       "restricted<pending_photo",
	"restricted<pending_photo|suspend":        "suspended<pending_photo",
	"restricted<pending_photo|reinstate":      "pending_photo",
	"restricted<pending_photo|delete":         "deleted<pending_photo+p",
	"suspended<draft|basics_complete":         "suspended<pending_photo",
	"suspended<draft|pause":                   "suspended<draft+p",
	"suspended<draft|unpause":                 "suspended<draft",
	"suspended<draft|review":                  "pending_review<draft",
	"suspended<draft|restrict":                "restricted<draft",
	"suspended<draft|suspend":                 "suspended<draft",
	"suspended<draft|reinstate":               "draft",
	"suspended<draft|delete":                  "deleted<draft+p",
	// a hold over a paused profile: reinstate returns to paused
	"suspended<active+p|pause":     "suspended<active+p",
	"suspended<active+p|unpause":   "suspended<active",
	"suspended<active+p|review":    "pending_review<active+p",
	"suspended<active+p|restrict":  "restricted<active+p",
	"suspended<active+p|suspend":   "suspended<active+p",
	"suspended<active+p|reinstate": "paused<active+p",
	"suspended<active+p|delete":    "deleted<active+p",
	// deleted is terminal
	"deleted<active+p|delete": "deleted<active+p",
}

var tableFromStates = []string{
	"draft", "pending_photo", "pending_selfie", "active",
	"paused<active+p", "paused<draft+p", "paused<pending_selfie+p",
	"pending_review<active", "restricted<active", "suspended<active",
	"restricted<pending_photo", "suspended<draft", "suspended<active+p",
	"deleted<active+p",
}

func TestProfileTransitionTable(t *testing.T) {
	for _, from := range tableFromStates {
		for _, ev := range allEvents {
			key := from + "|" + string(ev)
			got, err := NextProfileStatus(parseState(from), ev, defaultActor[ev])
			want, allowed := allowedTransitions[key]
			if allowed {
				if err != nil {
					t.Errorf("%s: want %s, got error %v", key, want, err)
					continue
				}
				if got != parseState(want) {
					t.Errorf("%s: want %s, got %s", key, want, got)
				}
				continue
			}
			if !errors.Is(err, ErrProfileTransitionNotAllowed) {
				t.Errorf("%s: want refusal (ErrProfileTransitionNotAllowed), got state=%s err=%v", key, got, err)
			}
		}
	}
	for key := range allowedTransitions {
		from := key[:strings.Index(key, "|")]
		found := false
		for _, s := range tableFromStates {
			if s == from {
				found = true
			}
		}
		if !found {
			t.Errorf("allowedTransitions has %s but %s is not in tableFromStates", key, from)
		}
	}
}

// TestUnpauseNeverLiftsHoldOrSkipsSteps is the brief's unpause contract
// spelled out on its own.
func TestUnpauseNeverLiftsHoldOrSkipsSteps(t *testing.T) {
	cases := map[string]string{
		"suspended<active":        "suspended",
		"suspended<active+p":      "suspended",
		"restricted<active+p":     "restricted",
		"pending_review<active+p": "pending_review",
		"paused<draft+p":          "draft",
		"paused<pending_photo+p":  "pending_photo",
		"paused<pending_selfie+p": "pending_selfie",
		"paused<active+p":         "active",
	}
	for _, actor := range []ProfileActor{ProfileActorUser, ProfileActorLifecycle} {
		for from, wantStatus := range cases {
			got, err := NextProfileStatus(parseState(from), ProfileEventUnpause, actor)
			if err != nil {
				t.Fatalf("%s unpause from %s: %v", actor, from, err)
			}
			if got.Status != wantStatus || got.Paused {
				t.Errorf("%s unpause from %s = %s, want status %s and not paused", actor, from, got, wantStatus)
			}
		}
	}
	// A paused row with no remembered step never unpauses to active.
	got, err := NextProfileStatus(ProfileStatusState{Status: ProfileStatusPaused, Paused: true}, ProfileEventUnpause, ProfileActorUser)
	if err != nil || got.Status != ProfileStatusDraft {
		t.Fatalf("unpause of paused with no prior = %s, %v; want draft", got, err)
	}
}

func TestProfileTransitionActors(t *testing.T) {
	refused := []struct {
		ev    ProfileEvent
		actor ProfileActor
		from  string
	}{
		{ProfileEventRestrict, ProfileActorUser, "active"},
		{ProfileEventSuspend, ProfileActorSystem, "active"},
		{ProfileEventReinstate, ProfileActorUser, "suspended<active"},
		{ProfileEventReinstate, ProfileActorLifecycle, "suspended<active"},
		{ProfileEventSelfiePassed, ProfileActorUser, "pending_selfie"},
		{ProfileEventBasicsComplete, ProfileActorAdmin, "draft"},
		{ProfileEventPause, ProfileActorAdmin, "active"},
		{ProfileEventPause, ProfileActorSystem, "active"},
		{ProfileEventDelete, ProfileActorAdmin, "active"},
	}
	for _, tc := range refused {
		_, err := NextProfileStatus(parseState(tc.from), tc.ev, tc.actor)
		if !errors.Is(err, ErrProfileTransitionActor) {
			t.Errorf("%s by %s from %s: want ErrProfileTransitionActor, got %v", tc.ev, tc.actor, tc.from, err)
		}
	}
	// Account lifecycle redelivers hide/unhide on a deleted profile: no-op.
	for _, ev := range []ProfileEvent{ProfileEventPause, ProfileEventUnpause} {
		from := parseState("deleted<active+p")
		got, err := NextProfileStatus(from, ev, ProfileActorLifecycle)
		if err != nil || got != from {
			t.Errorf("lifecycle %s on deleted = %s, %v; want unchanged no-op", ev, got, err)
		}
	}
}

func TestAgeOn(t *testing.T) {
	d := func(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }
	cases := []struct {
		birth, at time.Time
		want      int
	}{
		// Birthday tomorrow does not count yet. 2 March in a common year
		// and 1 March in a leap year share day-of-year 61.
		{d(2001, time.March, 2), d(2024, time.March, 1), 22},
		{d(2006, time.September, 16), d(2024, time.September, 15), 17},
		// Birthday today counts. 1 March 2000 (leap) is day 61; 1 March
		// 2025 is day 60.
		{d(2000, time.March, 1), d(2025, time.March, 1), 25},
		{d(2006, time.September, 15), d(2024, time.September, 15), 18},
		// 29 February birthdays turn over on 1 March in common years.
		{d(2004, time.February, 29), d(2022, time.February, 28), 17},
		{d(2004, time.February, 29), d(2022, time.March, 1), 18},
		// Future and zero clamp to 0.
		{d(2030, time.January, 1), d(2024, time.January, 1), 0},
		{time.Time{}, d(2024, time.January, 1), 0},
	}
	for _, tc := range cases {
		if got := AgeOn(tc.birth, tc.at); got != tc.want {
			t.Errorf("AgeOn(%s, %s) = %d, want %d", tc.birth.Format("2006-01-02"), tc.at.Format("2006-01-02"), got, tc.want)
		}
	}
	// A local evening timestamp still uses its own calendar date.
	ist := time.FixedZone("IST", 5*3600+1800)
	if got := AgeOn(d(2006, time.September, 16), time.Date(2024, time.September, 15, 23, 30, 0, 0, ist)); got != 17 {
		t.Errorf("AgeOn at 23:30 IST the day before the 18th birthday = %d, want 17", got)
	}
}

func Example_profileTransition() {
	st, _ := NextProfileStatus(parseState("suspended<active+p"), ProfileEventUnpause, ProfileActorUser)
	fmt.Println(st)
	// Output: suspended<active
}
