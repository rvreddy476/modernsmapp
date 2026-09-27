package service

import (
	"context"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

/*
	Chapters on the post detail (MTube, 2026-09-27).

	POST/GET /v1/posts/:id/chapters keep their shape. The post detail now
	carries `chapters: [{start_ms, title}]`:

	  * the saved media_chapters rows when there are any;
	  * otherwise, for a long video, chapters DERIVED from timestamps in the
	    description the way YouTube does it — a line that starts with
	    "0:00 Intro", "1:23 Title", "1:23:45 Title"; the first must be 0:00,
	    there must be at least three, and they must ascend. Derived chapters
	    are computed at read time and never persisted, so editing the
	    description changes them and saving chapters replaces them.
*/

// ChapterRef is one chapter as the post detail carries it.
type ChapterRef struct {
	StartMs int    `json:"start_ms"`
	Title   string `json:"title"`
}

// MinDerivedChapters is the fewest description timestamps that count as a
// chapter list; fewer are just timestamps in prose.
const MinDerivedChapters = 3

// descriptionChapterRe matches one chapter line: optional list marker, a
// timestamp (m:ss, mm:ss, h:mm:ss, optionally in brackets), then the title.
// Anchored at line start so "at 1:23 we ..." mid-sentence does not count.
var descriptionChapterRe = regexp.MustCompile(`^\s*(?:[-*•]\s*)?[\[(]?(\d{1,2}):(\d{2})(?::(\d{2}))?[\])]?\s*[-–—:]?\s*(.*?)\s*$`)

// ParseDescriptionChapters is the pure parser. It returns nil unless the
// text yields a valid list: first at 0:00, at least MinDerivedChapters,
// strictly ascending, every title non-empty. A line that does not parse as
// a chapter is skipped (it is prose); a timestamp that breaks the ascent
// invalidates the whole list, because a list that is not in order is not a
// chapter list.
func ParseDescriptionChapters(text string) []ChapterRef {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var out []ChapterRef
	for _, line := range strings.Split(text, "\n") {
		m := descriptionChapterRe.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		startMs, ok := timestampToMs(m[1], m[2], m[3])
		if !ok {
			continue
		}
		title := strings.TrimSpace(m[4])
		if title == "" {
			continue
		}
		if len(out) == 0 && startMs != 0 {
			// The first chapter must be 0:00; a list that starts later is
			// a set of references, not chapters.
			return nil
		}
		if len(out) > 0 && startMs <= out[len(out)-1].StartMs {
			return nil
		}
		out = append(out, ChapterRef{StartMs: startMs, Title: title})
	}
	if len(out) < MinDerivedChapters {
		return nil
	}
	return out
}

// timestampToMs turns (a, b, c) = (m, ss) or (h, mm, ss) into milliseconds.
// Seconds (and minutes when hours are given) must be < 60.
func timestampToMs(a, b, c string) (int, bool) {
	x, err1 := strconv.Atoi(a)
	y, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || y >= 60 {
		return 0, false
	}
	if c == "" {
		return (x*60 + y) * 1000, true
	}
	z, err := strconv.Atoi(c)
	if err != nil || z >= 60 || y >= 60 {
		return 0, false
	}
	return ((x*60+y)*60 + z) * 1000, true
}

// chaptersForDetail is what the post detail carries: saved chapters when
// present, else derived ones for a long video, else an empty list. Never
// nil, so the key is always an array on the wire.
func (s *Service) chaptersForDetail(ctx context.Context, p *postgres.Post) []ChapterRef {
	out := []ChapterRef{}
	if p == nil {
		return out
	}
	if s.pgStore != nil {
		saved, err := s.pgStore.GetChapters(ctx, p.ID)
		if err != nil {
			slog.WarnContext(ctx, "post chapters skipped", "post_id", p.ID, "err", err)
		}
		if len(saved) > 0 {
			for _, ch := range saved {
				out = append(out, ChapterRef{StartMs: ch.StartMs, Title: ch.Title})
			}
			return out
		}
	}
	if isLongVideoContentType(p.ContentType) {
		if derived := ParseDescriptionChapters(p.Text); derived != nil {
			return derived
		}
	}
	return out
}

// GetChaptersFor is the exported form for callers outside the detail path
// (kept small so a handler test can pin the derivation without a store).
func (s *Service) GetChaptersFor(ctx context.Context, postID uuid.UUID, p *postgres.Post) []ChapterRef {
	return s.chaptersForDetail(ctx, p)
}
