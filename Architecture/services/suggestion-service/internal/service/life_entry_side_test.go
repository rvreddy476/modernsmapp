package service

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The candidate side of the batch must read PUBLIC entries only; the
// viewer side keeps reading its own. Comments are stripped before matching
// so a sentence discussing the rule cannot satisfy it.
func TestBatchUsesThePublicReaderForCandidates(t *testing.T) {
	raw, err := os.ReadFile("batch.go")
	if err != nil {
		t.Fatal(err)
	}
	src := regexp.MustCompile(`(?m)//.*$`).ReplaceAllString(
		regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(string(raw), " "), "")

	if !strings.Contains(src, "GetPublicLifeEntries(ctx, candidateID)") {
		t.Fatal("batch.go does not read a candidate's life entries through GetPublicLifeEntries; " +
			"a private school would become SAME_SCHOOL and explain text for a stranger")
	}
	if strings.Contains(src, "GetUserLifeEntries(ctx, candidateID)") {
		t.Fatal("batch.go still reads a CANDIDATE's entries with the all-visibilities reader")
	}
	if !strings.Contains(src, "GetUserLifeEntries(ctx, viewerID)") {
		t.Fatal("batch.go no longer reads the VIEWER's own entries with the all-visibilities reader; " +
			"a viewer's private school should still find them people from that school")
	}
}
