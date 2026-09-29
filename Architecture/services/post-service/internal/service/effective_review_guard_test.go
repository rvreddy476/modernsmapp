package service

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
)

// P-4 source-scan guard, service side (Copyright Match plan, section 6.2).
//
// The store's SQL guard covers queries; this one covers the Go gates that
// judge a scanned Post (the direct read, feed hydration, media access,
// related-post cards, ws room admission through PostVisibleTo). Each must
// compare EffectiveReviewStatus(), never the base ReviewStatus field, or a
// post under an active copyright hold — base still 'approved' — would be
// served. The regexp matches a comparison of a `.ReviewStatus` field with
// the "approved" literal on one line, unless that line already goes
// through the method.

var baseApprovedCompare = regexp.MustCompile(`\.ReviewStatus\b[^\n]*"approved"`)

// exemptGates are functions that legitimately read the BASE status:
// writers deciding what to store, and the Hub flag that names the base
// state next to the restriction flag.
var exemptGates = map[string]string{
	"UploadFlags": "owner Hub flag: review_hold names the base state; copyright_hold names the restriction separately",
}

var funcDeclRe = regexp.MustCompile(`^func (?:\([^)]*\) )?([A-Za-z0-9_]+)\(`)

func TestViewerGatesJudgeTheEffectiveReviewStatus(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	effectiveUses := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		current := ""
		for i, line := range strings.Split(string(src), "\n") {
			if m := funcDeclRe.FindStringSubmatch(line); m != nil {
				current = m[1]
			}
			if strings.Contains(line, "EffectiveReviewStatus()") {
				effectiveUses++
				continue
			}
			if !baseApprovedCompare.MatchString(line) {
				continue
			}
			if reason, ok := exemptGates[current]; ok {
				t.Logf("%s:%d %s exempt (%s)", file, i+1, current, reason)
				continue
			}
			t.Errorf("%s:%d in %s compares the BASE ReviewStatus with \"approved\": a post under an active restriction would pass.\n"+
				"  Compare EffectiveReviewStatus() instead, or add the function to exemptGates with a reason.\n  line: %s",
				file, i+1, current, strings.TrimSpace(line))
		}
	}
	if effectiveUses == 0 {
		t.Fatal("guard found no EffectiveReviewStatus() gate — it has stopped guarding anything")
	}
	t.Logf("%d gates judge the effective status", effectiveUses)
}

// PostCreated is the other event search indexes from (the scheduled
// publish path emits it for a post that may already be held); it must
// carry the effective status too.
func TestPostCreatedCarriesTheEffectiveReviewStatus(t *testing.T) {
	src, err := os.ReadFile("schedule.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "ReviewStatus: p.EffectiveReviewStatus(),") {
		t.Fatal("buildPostCreatedPayload must stamp p.EffectiveReviewStatus(), not the base ReviewStatus")
	}
	svc := &Service{}
	pc := svc.buildPostCreatedPayload(context.Background(), &postgres.Post{ReviewStatus: "approved", ActiveRestrictionCount: 1}, nil, 0, 0, 1)
	if pc.ReviewStatus != "restricted" {
		t.Fatalf("PostCreated review_status=%q, want restricted", pc.ReviewStatus)
	}
}

// The Go mirror of the generated column: restricted while any hold is
// active, else the base — including a base that is not approved.
func TestEffectiveReviewStatusMirrorsTheGeneratedColumn(t *testing.T) {
	cases := []struct {
		base  string
		count int
		want  string
	}{
		{"approved", 0, "approved"}, {"approved", 1, "restricted"}, {"approved", 2, "restricted"},
		{"rejected", 0, "rejected"}, {"rejected", 1, "restricted"}, {"", 0, ""}, {"", 1, "restricted"},
	}
	for _, tc := range cases {
		p := &postgres.Post{ReviewStatus: tc.base, ActiveRestrictionCount: tc.count}
		if got := p.EffectiveReviewStatus(); got != tc.want {
			t.Errorf("base=%q count=%d: got %q want %q", tc.base, tc.count, got, tc.want)
		}
	}
	var nilPost *postgres.Post
	if nilPost.EffectiveReviewStatus() != "" {
		t.Error("nil post must read as empty, never approved")
	}
	state := &postgres.PostAccessState{ReviewStatus: "approved", ActiveRestrictionCount: 1}
	if state.EffectiveReviewStatus() != "restricted" {
		t.Error("access state under a hold must read restricted")
	}
	// The cache overlay carries the count, so a body cached before the
	// hold is judged by the row.
	cached := &postgres.Post{ReviewStatus: "approved"}
	applyPostAccessState(cached, state)
	if cached.EffectiveReviewStatus() != "restricted" || cached.ActiveRestrictionCount != 1 {
		t.Errorf("overlay did not carry the restriction count: %+v", cached)
	}
	// And the Hub flags name it.
	flags := UploadFlags(cached)
	if len(flags) != 1 || flags[0] != UploadFlagCopyrightHold {
		t.Errorf("flags=%v, want [copyright_hold]", flags)
	}
	cached.ReviewStatus = "rejected"
	if flags := UploadFlags(cached); len(flags) != 2 || flags[0] != UploadFlagReviewHold || flags[1] != UploadFlagCopyrightHold {
		t.Errorf("flags=%v, want [review_hold copyright_hold]", flags)
	}
}
