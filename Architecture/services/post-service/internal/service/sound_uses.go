package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

/*
	Trending sounds (2026-10-24): the uses of each sound, for media-service.

	media-service owns sounds and the trending ranking; this service owns the
	posts that use them and every rule about who may see a post. So the
	split is: this service COUNTS, media-service RANKS.

	    GET /v1/internal/sound-uses?after=<sound id>&limit=<n>   internal key

	answers, for every sound used in the last 14 days, the number of uses on
	each of those days. A use is counted once per (sound, author, day): one
	person posting 50 reels over a sound on one day is one use.

	WHAT COUNTS (contract "What counts as a use")

	A post that links the sound and plays to everybody: exactly the posts the
	SIGNED-OUT playback rule admits (anonymousMayAccessPost, media_access.go)
	— public, effectively approved (no hold, no restriction), live, not
	scheduled, not members-only, not 18+, by an author whose account is
	public and not hidden (deactivated / pending deletion). A reel or a long
	video, never a feed post; never the sound's own source reel. The SQL
	(store/postgres/sound_uses.go) narrows the rows; the rule below is
	judged on every one of them anyway, so a sound trends only on posts a
	stranger could actually play.

	DAYS

	Day 0 is the 24 hours before as_of, day 1 the 24 hours before that, and
	so on to day 13. They are windows counted back from as_of, not calendar
	days, so "the last 24 h" in the ranking is exactly day 0.

	FAIL CLOSED

	The author gate asks graph-service and the hidden-author list. When
	either cannot answer, the page is unavailable (503): media-service keeps
	the ranking it has rather than computing one in which every unanswered
	author silently counted for nothing.
*/

// SoundUsesDays is how many days back the uses are counted: this week and
// the week before, which the ranking compares.
const SoundUsesDays = 14

// DefaultSoundUsesPage is the page size when the caller names none.
const DefaultSoundUsesPage = 500

// ErrSoundUsesUnavailable: the store or the author gate could not answer.
var ErrSoundUsesUnavailable = errors.New("sound uses are unavailable right now")

// soundUsesStore is the storage slice the count reads.
type soundUsesStore interface {
	SoundUseRows(ctx context.Context, since, until time.Time, after uuid.UUID, limit int) ([]postgres.SoundUse, *uuid.UUID, error)
}

// SoundDailyUses is one sound's uses, per day. DailyUses[0] is the 24 hours
// before as_of; it always has SoundUsesDays entries.
type SoundDailyUses struct {
	AudioTrackID uuid.UUID `json:"audio_track_id"`
	DailyUses    []int     `json:"daily_uses"`
}

// SoundUsesPage is one page of GET /v1/internal/sound-uses.
type SoundUsesPage struct {
	AsOf time.Time `json:"as_of"`
	Days int       `json:"days"`
	// Sounds holds the sounds of the page with at least one use, by id.
	Sounds []SoundDailyUses `json:"sounds"`
	// NextAfter is the `after` of the next page; null on the last one.
	NextAfter *uuid.UUID `json:"next_after"`
}

func (s *Service) soundUsesSource() soundUsesStore {
	if s.soundUses != nil {
		return s.soundUses
	}
	if s.pgStore != nil {
		return s.pgStore
	}
	return nil
}

// NormalizeSoundUsesLimit resolves the caller's page size.
func NormalizeSoundUsesLimit(limit int) int {
	if limit <= 0 {
		return DefaultSoundUsesPage
	}
	if limit > postgres.MaxSoundUsesPage {
		return postgres.MaxSoundUsesPage
	}
	return limit
}

// soundUseDay is the day a use falls on, counted back from asOf: 0 for the
// 24 hours before it. -1 outside the window.
func soundUseDay(asOf, publishedAt time.Time) int {
	if publishedAt.After(asOf) {
		return -1
	}
	day := int(asOf.Sub(publishedAt) / (24 * time.Hour))
	if day >= SoundUsesDays {
		return -1
	}
	return day
}

// SoundUses is one page of every sound's daily uses.
func (s *Service) SoundUses(ctx context.Context, after uuid.UUID, limit int) (*SoundUsesPage, error) {
	store := s.soundUsesSource()
	if store == nil {
		return nil, fmt.Errorf("%w: no store", ErrSoundUsesUnavailable)
	}
	limit = NormalizeSoundUsesLimit(limit)
	asOf := s.clock().UTC()
	since := asOf.Add(-SoundUsesDays * 24 * time.Hour)

	rows, next, err := store.SoundUseRows(ctx, since, asOf, after, limit)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSoundUsesUnavailable, err)
	}

	authors := make([]uuid.UUID, 0, len(rows))
	seenAuthor := make(map[uuid.UUID]bool, len(rows))
	for _, u := range rows {
		if !seenAuthor[u.AuthorID] {
			seenAuthor[u.AuthorID] = true
			authors = append(authors, u.AuthorID)
		}
	}
	// The account gate as the signed-out stranger sees it: a private or a
	// hidden author's posts are not public uses.
	authorOK, resolved := s.canViewPostsResolved(ctx, nil, authors)
	if !resolved {
		return nil, fmt.Errorf("%w: the author gate did not answer", ErrSoundUsesUnavailable)
	}

	type useKey struct {
		sound, author uuid.UUID
		day           int
	}
	counted := make(map[useKey]bool, len(rows))
	perSound := map[uuid.UUID][]int{}
	for _, u := range rows {
		if !anonymousMayAccessPost(u.AsPost(), authorOK[u.AuthorID]) {
			continue
		}
		day := soundUseDay(asOf, u.PublishedAt)
		if day < 0 {
			continue
		}
		key := useKey{u.SoundID, u.AuthorID, day}
		if counted[key] {
			continue
		}
		counted[key] = true
		days, ok := perSound[u.SoundID]
		if !ok {
			days = make([]int, SoundUsesDays)
			perSound[u.SoundID] = days
		}
		days[day]++
	}

	page := &SoundUsesPage{AsOf: asOf, Days: SoundUsesDays, Sounds: make([]SoundDailyUses, 0, len(perSound)), NextAfter: next}
	for id, days := range perSound {
		page.Sounds = append(page.Sounds, SoundDailyUses{AudioTrackID: id, DailyUses: days})
	}
	sort.Slice(page.Sounds, func(i, j int) bool {
		return page.Sounds[i].AudioTrackID.String() < page.Sounds[j].AudioTrackID.String()
	})
	return page, nil
}
