//go:build integration

package integration

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// tsReport is a report as its reporter sees it (trust-safety-service
// user_views.go, userReport).
type tsReport struct {
	ID         uuid.UUID `json:"id"`
	ReporterID uuid.UUID `json:"reporter_id"`
	EntityType string    `json:"entity_type"`
	EntityID   uuid.UUID `json:"entity_id"`
	Reason     string    `json:"reason"`
	Status     string    `json:"status"`
}

type tsReportList struct {
	Items []tsReport `json:"items"`
}

func (l tsReportList) has(id uuid.UUID) bool {
	for _, r := range l.Items {
		if r.ID == id {
			return true
		}
	}
	return false
}

// TestE2E_TrustSafety_FileReport exercises the abuse-reporting intake through
// the gateway, the entry point of the moderation pipeline:
//
//	a user files a report against a post → it is in THEIR list
//	(GET /v1/reports?mine=true) → it is in nobody else's → the full queue
//	(GET /v1/reports with no ?mine) is refused without the admin scope →
//	filing the same report again is 409 ACTIVE_REPORT_EXISTS
//
// The write is synchronous (one Postgres insert), so the reads assert at once.
func TestE2E_TrustSafety_FileReport(t *testing.T) {
	gw, ctx := gatewayForFlow(t)

	reporterID := uuid.New()
	reporter := NewHTTPClient(gw, reporterID)
	stranger := NewHTTPClient(gw, uuid.New())
	anonymous := NewHTTPClient(gw, uuid.Nil)

	targetPost := uuid.New()
	body := map[string]any{
		"entity_type": "post",
		"entity_id":   targetPost.String(),
		"reason":      "spam",
		"details":     "e2e trust-safety report",
	}

	var filed tsReport
	if !wantData(t, reporter.MustDo(t, ctx, "POST", "/v1/reports", body), "POST /v1/reports", http.StatusOK, &filed) {
		t.FailNow()
	}
	if filed.ID == uuid.Nil || filed.ReporterID != reporterID || filed.EntityType != "post" ||
		filed.EntityID != targetPost || filed.Reason != "spam" || filed.Status != "open" {
		t.Fatalf("filed report: got %+v, want a new id, reporter %s, post %s, reason spam, status open", filed, reporterID, targetPost)
	}

	// The reporter's own list carries it.
	var mine tsReportList
	if wantData(t, reporter.MustDo(t, ctx, "GET", "/v1/reports?mine=true", nil), "reporter GET /v1/reports?mine=true", http.StatusOK, &mine) {
		if !mine.has(filed.ID) {
			t.Errorf("reporter's own list does not carry report %s (%d items)", filed.ID, len(mine.Items))
		}
		for _, r := range mine.Items {
			if r.ReporterID != reporterID {
				t.Errorf("reporter's own list carries report %s filed by %s", r.ID, r.ReporterID)
			}
		}
	}

	// Nobody else's does.
	var theirs tsReportList
	if wantData(t, stranger.MustDo(t, ctx, "GET", "/v1/reports?mine=true", nil), "stranger GET /v1/reports?mine=true", http.StatusOK, &theirs) {
		if theirs.has(filed.ID) {
			t.Errorf("another user's own list carries report %s", filed.ID)
		}
	}

	// The full queue and a single report by id are the moderators': a user
	// without the admin scope is refused, the reporter included.
	wantRefusal(t, reporter.MustDo(t, ctx, "GET", "/v1/reports", nil),
		"reporter GET /v1/reports (the moderation queue)", http.StatusForbidden, "FORBIDDEN")
	wantRefusal(t, reporter.MustDo(t, ctx, "GET", "/v1/reports/"+filed.ID.String(), nil),
		"reporter GET /v1/reports/<id>", http.StatusForbidden, "FORBIDDEN")
	wantRefusal(t, reporter.MustDo(t, ctx, "PATCH", "/v1/reports/"+filed.ID.String(), map[string]any{"status": "dismissed"}),
		"reporter PATCH /v1/reports/<id>", http.StatusForbidden, "FORBIDDEN")

	// One active report per reporter and target.
	wantRefusal(t, reporter.MustDo(t, ctx, "POST", "/v1/reports", body),
		"the same report filed twice", http.StatusConflict, "ACTIVE_REPORT_EXISTS")

	// Filing needs a signed-in user, and a reason from the allowlist.
	wantRefusal(t, anonymous.MustDo(t, ctx, "POST", "/v1/reports", body),
		"anonymous POST /v1/reports", http.StatusUnauthorized, "UNAUTHORIZED")
	wantRefusal(t, reporter.MustDo(t, ctx, "POST", "/v1/reports", map[string]any{
		"entity_type": "post",
		"entity_id":   uuid.NewString(),
		"reason":      "e2e-not-a-category",
	}), "POST /v1/reports with an unknown reason", http.StatusBadRequest, "BAD_REQUEST")
}
