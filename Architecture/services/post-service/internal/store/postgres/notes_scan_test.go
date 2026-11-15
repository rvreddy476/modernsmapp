package postgres

import (
	"strings"
	"testing"
)

// Every note query selects noteCols and scans noteScanDestinations; the two
// lists must stay the same length, or each read fails with "number of
// field descriptions must equal number of destinations".
func TestNoteColsMatchTheirScanList(t *testing.T) {
	cols := 0
	for _, c := range strings.Split(noteCols, ",") {
		if strings.TrimSpace(c) != "" {
			cols++
		}
	}
	if got := len(noteScanDestinations(&Note{})); got != cols {
		t.Fatalf("noteCols has %d columns, noteScanDestinations has %d", cols, got)
	}
}
