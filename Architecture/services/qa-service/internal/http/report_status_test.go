package http

import (
	"net/http"
	"testing"
)

// A new report is 201; the reporter's open report returned again is 200.
func TestReportStatus(t *testing.T) {
	if got := reportStatus(true); got != http.StatusCreated {
		t.Fatalf("new report: %d, want 201", got)
	}
	if got := reportStatus(false); got != http.StatusOK {
		t.Fatalf("repeat: %d, want 200", got)
	}
}
