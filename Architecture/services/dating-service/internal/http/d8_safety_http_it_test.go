// Lane D8 HTTP tests on dating_it_test: report reason codes, the admin
// panic queue (no coordinates) versus the audited detail view, the admin
// report action target check, and the service-only notify context.
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

func TestD8HTTP_ReportInvalidReason400(t *testing.T) {
	env := setupAuthzIT(t)
	user := uuid.New()
	body := `{"target_id":"` + uuid.NewString() + `","reason":"creepy","details":"x"}`
	w := serve(env.r, http.MethodPost, "/v1/dating/safety/report", body, gatewayUser(user, ""))
	if w.Code != http.StatusBadRequest || errorCode(t, w) != "INVALID_REPORT_REASON" {
		t.Fatalf("status=%d body=%s, want 400 INVALID_REPORT_REASON", w.Code, w.Body.String())
	}
}

func TestD8HTTP_AdminPanicListHasNoCoordinatesDetailIsAudited(t *testing.T) {
	env := setupAuthzIT(t)
	ctx := context.Background()
	user := uuid.New()
	lat, lng := 12.971599, 77.594566
	out, err := env.st.RecordPanicIncident(ctx, store.RecordPanicParams{
		UserID: user, Source: store.PanicSourcePanic, Latitude: &lat, Longitude: &lng,
	})
	if err != nil {
		t.Fatalf("seed incident: %v", err)
	}
	admin := uuid.New()
	id := out.Incident.ID.String()

	list := serve(env.r, http.MethodGet, "/v1/dating/admin/safety/panic?limit=200", "", gatewayUser(admin, "moderator"))
	if list.Code != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", list.Code, list.Body.String())
	}
	if !strings.Contains(list.Body.String(), id) {
		t.Fatalf("list does not include the incident")
	}
	if strings.Contains(list.Body.String(), "latitude") || strings.Contains(list.Body.String(), "12.971599") ||
		strings.Contains(list.Body.String(), "77.594566") {
		t.Fatalf("admin list carries coordinates: %s", list.Body.String())
	}
	if n := len(auditFor(t, env.st, "panic_incident:"+id)); n != 0 {
		t.Fatalf("list wrote %d audit rows", n)
	}

	detail := serve(env.r, http.MethodGet, "/v1/dating/admin/safety/panic/"+id, "", gatewayUser(admin, "moderator"))
	if detail.Code != http.StatusOK {
		t.Fatalf("detail: status=%d body=%s", detail.Code, detail.Body.String())
	}
	var env2 struct {
		Data store.PanicIncident `json:"data"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &env2); err != nil || env2.Data.Latitude == nil || *env2.Data.Latitude != lat {
		t.Fatalf("detail = %s err=%v, want the coordinates", detail.Body.String(), err)
	}
	rows := auditFor(t, env.st, "panic_incident:"+id)
	if len(rows) != 1 || rows[0].Action != "panic_viewed" || rows[0].ActorAdminID != admin || rows[0].TargetUserID != user {
		t.Fatalf("detail audit rows=%d actor=%v, want 1 panic_viewed by %s", len(rows), actorOf(rows), admin)
	}
	if w := serve(env.r, http.MethodGet, "/v1/dating/admin/safety/panic/"+id, "", gatewayUser(uuid.New(), "")); w.Code != http.StatusForbidden {
		t.Fatalf("plain user detail: status=%d, want 403", w.Code)
	}

	resolve := serve(env.r, http.MethodPost, "/v1/dating/admin/safety/panic/"+id+"/resolve", `{"note":"called the user; safe"}`, gatewayUser(admin, "admin"))
	if resolve.Code != http.StatusOK || !strings.Contains(resolve.Body.String(), `"status":"resolved"`) || strings.Contains(resolve.Body.String(), "12.97") {
		t.Fatalf("resolve: status=%d body=%s", resolve.Code, resolve.Body.String())
	}
	resolved := 0
	for _, r := range auditFor(t, env.st, "panic_incident:"+id) {
		if r.Action == "panic_resolved" && r.InternalNotes == "called the user; safe" {
			resolved++
		}
	}
	if resolved != 1 {
		t.Fatalf("panic_resolved audit rows = %d, want 1", resolved)
	}
}

func TestD8HTTP_AdminReportActionMismatchedTarget409(t *testing.T) {
	env := setupAuthzIT(t)
	ctx := context.Background()
	target, bystander := uuid.New(), uuid.New()
	seedProfile(t, env.st, bystander)
	report, err := env.st.CreateReport(ctx, uuid.New(), target, "spam", "d8")
	if err != nil {
		t.Fatalf("seed report: %v", err)
	}
	body := `{"action":"suspend","target_user_id":"` + bystander.String() + `"}`
	w := serve(env.r, http.MethodPost, "/v1/dating/admin/reports/"+report.ID.String()+"/action", body, gatewayUser(uuid.New(), "admin"))
	if w.Code != http.StatusConflict || errorCode(t, w) != "REPORT_TARGET_MISMATCH" {
		t.Fatalf("status=%d body=%s, want 409 REPORT_TARGET_MISMATCH", w.Code, w.Body.String())
	}
	if n := len(auditFor(t, env.st, "report:"+report.ID.String())); n != 0 {
		t.Fatalf("refused action wrote %d audit rows", n)
	}
}

func TestD8HTTP_PanicNotifyContextIsServiceOnlyAndOptInScoped(t *testing.T) {
	env := setupAuthzIT(t)
	ctx := context.Background()
	user, opted, notOpted := uuid.New(), uuid.New(), uuid.New()
	seedProfile(t, env.st, user)
	lat, lng := 12.971599, 77.594566
	out, err := env.st.RecordPanicIncident(ctx, store.RecordPanicParams{UserID: user, Source: store.PanicSourcePanic, Latitude: &lat, Longitude: &lng})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := env.st.UpsertTrustedContact(ctx, user, opted, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := env.st.UpsertTrustedContact(ctx, user, notOpted, false); err != nil {
		t.Fatal(err)
	}
	path := "/v1/dating/internal/safety/panic/" + out.Incident.ID.String() + "/notify-context"
	if w := serve(env.r, http.MethodGet, path, "", gatewayUser(uuid.New(), "admin")); w.Code != http.StatusForbidden {
		t.Fatalf("gateway user on notify context: status=%d, want 403", w.Code)
	}
	w := serve(env.r, http.MethodGet, path, "", map[string]string{headerInternalKey: testInternalKey})
	if w.Code != http.StatusOK {
		t.Fatalf("service call: status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			TrustedContacts []struct {
				UserID    uuid.UUID `json:"user_id"`
				Latitude  *float64  `json:"latitude"`
				Longitude *float64  `json:"longitude"`
			} `json:"trusted_contacts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data.TrustedContacts) != 2 {
		t.Fatalf("contacts = %s", w.Body.String())
	}
	for _, c := range resp.Data.TrustedContacts {
		switch c.UserID {
		case opted:
			if c.Latitude == nil || *c.Latitude != lat {
				t.Fatalf("opted-in contact lacks the point: %s", w.Body.String())
			}
		case notOpted:
			if c.Latitude != nil || c.Longitude != nil {
				t.Fatalf("contact who did not opt in got the point: %s", w.Body.String())
			}
		}
	}
}
