package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

type fakeSafetyDeps struct {
	mu          sync.Mutex
	claimed     map[uuid.UUID]bool
	ctx         *datingPanicContext
	ctxErr      error
	ctxCalls    int
	responders  []uuid.UUID
	firstNames  map[uuid.UUID]string
	alerts      []service.SafetyAlert
	delivered   map[string]bool
	ops         []postgres.OpsAlert
	emails      []string
}

func newFakeSafetyDeps() *fakeSafetyDeps {
	return &fakeSafetyDeps{claimed: map[uuid.UUID]bool{}, delivered: map[string]bool{}, firstNames: map[uuid.UUID]string{}}
}

func (f *fakeSafetyDeps) ClaimEventDedup(_ context.Context, id uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.claimed[id] {
		return false, nil
	}
	f.claimed[id] = true
	return true, nil
}

func (f *fakeSafetyDeps) PanicContext(context.Context, uuid.UUID) (*datingPanicContext, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ctxCalls++
	return f.ctx, f.ctxErr
}

func (f *fakeSafetyDeps) FirstName(_ context.Context, id uuid.UUID) string { return f.firstNames[id] }

func (f *fakeSafetyDeps) Responders(context.Context) []uuid.UUID { return f.responders }

func (f *fakeSafetyDeps) SendSafetyAlert(_ context.Context, a service.SafetyAlert) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.delivered[a.Identity] {
		return nil
	}
	f.delivered[a.Identity] = true
	f.alerts = append(f.alerts, a)
	return nil
}

func (f *fakeSafetyDeps) RecordOpsAlert(_ context.Context, a postgres.OpsAlert) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops = append(f.ops, a)
	return nil
}

func (f *fakeSafetyDeps) EmailOps(_ context.Context, subject, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.emails = append(f.emails, subject+"\n"+body)
	return nil
}

func (f *fakeSafetyDeps) alertsOfType(t string) []service.SafetyAlert {
	var out []service.SafetyAlert
	for _, a := range f.alerts {
		if a.NotifType == t {
			out = append(out, a)
		}
	}
	return out
}

func panicEvent(t *testing.T, incident, user uuid.UUID) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"incident_id": incident.String(), "user_id": user.String(),
		"created_at": time.Now().UTC(), "has_location": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const optedLat, optedLng = 12.971599, 77.594566

func float(v float64) *float64 { return &v }

// The consumer pages every responder with no coordinates, and notifies each
// trusted contact by first name — with the point only for the contact the
// user opted in to share it with.
func TestDatingPanicPagesRespondersAndTrustedContacts(t *testing.T) {
	incident, user := uuid.New(), uuid.New()
	r1, r2 := uuid.New(), uuid.New()
	opted, notOpted := uuid.New(), uuid.New()
	deps := newFakeSafetyDeps()
	deps.responders = []uuid.UUID{r1, r2}
	deps.ctx = &datingPanicContext{
		IncidentID: incident.String(), UserID: user.String(), FirstName: "Asha", Status: "open", HasLocation: true,
		TrustedContacts: []datingPanicContact{
			{UserID: opted.String(), Latitude: float(optedLat), Longitude: float(optedLng)},
			{UserID: notOpted.String()},
		},
	}
	if err := processDatingPanic(context.Background(), deps, panicEvent(t, incident, user)); err != nil {
		t.Fatalf("process: %v", err)
	}

	responders := deps.alertsOfType(NotifDatingPanicResponder)
	if len(responders) != 2 {
		t.Fatalf("responder pages = %d, want 2", len(responders))
	}
	for _, a := range responders {
		if a.Recipient != r1 && a.Recipient != r2 {
			t.Fatalf("paged a non-responder %s", a.Recipient)
		}
		if blob := fmt.Sprintf("%+v", a); strings.Contains(blob, "12.97") || strings.Contains(blob, "lat=") {
			t.Fatalf("responder page carries coordinates: %s", blob)
		}
	}

	contacts := deps.alertsOfType(NotifDatingPanicTrustedContact)
	if len(contacts) != 2 {
		t.Fatalf("trusted contact notifications = %d, want 2", len(contacts))
	}
	for _, a := range contacts {
		if a.Title != "Asha triggered a safety alert" {
			t.Fatalf("contact title = %q", a.Title)
		}
		if !strings.HasPrefix(a.DeepLink, "/dating/safety/alerts/"+incident.String()) {
			t.Fatalf("contact deep link = %q", a.DeepLink)
		}
		hasPoint := strings.Contains(a.DeepLink, "lat=12.971599") && strings.Contains(a.DeepLink, "lng=77.594566")
		switch a.Recipient {
		case opted:
			if !hasPoint {
				t.Fatalf("opted-in contact did not get the point: %q", a.DeepLink)
			}
		case notOpted:
			if blob := fmt.Sprintf("%+v", a); strings.Contains(blob, "12.97") || strings.Contains(blob, "lat=") {
				t.Fatalf("contact who was not opted in got coordinates: %s", blob)
			}
		default:
			t.Fatalf("notified an unexpected contact %s", a.Recipient)
		}
	}

	if len(deps.ops) != 1 || deps.ops[0].Kind != OpsAlertDatingPanicPaged || deps.ops[0].SubjectID != incident {
		t.Fatalf("ops alerts = %+v, want one dating_panic_paged for the incident", deps.ops)
	}
	opsJSON, _ := json.Marshal(deps.ops)
	if strings.Contains(string(opsJSON), "12.97") || strings.Contains(string(opsJSON), user.String()) {
		t.Fatalf("ops alert carries a location or the user id: %s", opsJSON)
	}
	for _, m := range deps.emails {
		if strings.Contains(m, "12.97") || strings.Contains(m, user.String()) {
			t.Fatalf("ops email carries a location or the user id: %s", m)
		}
	}
}

func TestDatingPanicDuplicateEventIgnored(t *testing.T) {
	incident, user := uuid.New(), uuid.New()
	deps := newFakeSafetyDeps()
	deps.responders = []uuid.UUID{uuid.New()}
	deps.ctx = &datingPanicContext{Status: "open", FirstName: "Asha",
		TrustedContacts: []datingPanicContact{{UserID: uuid.NewString()}}}
	ev := panicEvent(t, incident, user)
	for i := 0; i < 3; i++ {
		if err := processDatingPanic(context.Background(), deps, ev); err != nil {
			t.Fatalf("process %d: %v", i, err)
		}
	}
	if len(deps.alerts) != 2 || deps.ctxCalls != 1 || len(deps.ops) != 1 || len(deps.emails) != 1 {
		t.Fatalf("after 3 deliveries: alerts=%d context calls=%d ops=%d emails=%d, want 2/1/1/1",
			len(deps.alerts), deps.ctxCalls, len(deps.ops), len(deps.emails))
	}
}

func TestDatingPanicSuspectedAbuseIsNotPaged(t *testing.T) {
	deps := newFakeSafetyDeps()
	deps.responders = []uuid.UUID{uuid.New()}
	deps.ctx = &datingPanicContext{Status: "open", SuspectedAbuse: true,
		TrustedContacts: []datingPanicContact{{UserID: uuid.NewString()}}}
	if err := processDatingPanic(context.Background(), deps, panicEvent(t, uuid.New(), uuid.New())); err != nil {
		t.Fatalf("process: %v", err)
	}
	if len(deps.alerts) != 0 {
		t.Fatalf("suspected abuse paged %d recipients", len(deps.alerts))
	}
	if len(deps.ops) != 1 || deps.ops[0].Kind != OpsAlertDatingPanicSuspectedAbuse {
		t.Fatalf("ops = %+v, want one suspected-abuse alert", deps.ops)
	}
}

// With no responder configured the trusted contacts are still told, and the
// incident raises a critical no-responder ops alert.
func TestDatingPanicWithoutRespondersRaisesCriticalOpsAlert(t *testing.T) {
	deps := newFakeSafetyDeps()
	deps.ctx = &datingPanicContext{Status: "open", FirstName: "Asha",
		TrustedContacts: []datingPanicContact{{UserID: uuid.NewString()}}}
	if err := processDatingPanic(context.Background(), deps, panicEvent(t, uuid.New(), uuid.New())); err != nil {
		t.Fatalf("process: %v", err)
	}
	if len(deps.alertsOfType(NotifDatingPanicTrustedContact)) != 1 {
		t.Fatalf("trusted contact not notified without responders")
	}
	if len(deps.ops) != 1 || deps.ops[0].Kind != OpsAlertDatingPanicNoResponder || deps.ops[0].Severity != "critical" {
		t.Fatalf("ops = %+v, want one critical dating_panic_no_responder", deps.ops)
	}
	if len(deps.emails) != 1 || !strings.HasPrefix(deps.emails[0], "UNPAGED") {
		t.Fatalf("emails = %v, want one UNPAGED ops email", deps.emails)
	}
}

func TestDatingPanicContextUnavailableStillPagesResponders(t *testing.T) {
	deps := newFakeSafetyDeps()
	deps.responders = []uuid.UUID{uuid.New()}
	deps.ctxErr = errors.New("dating-service down")
	if err := processDatingPanic(context.Background(), deps, panicEvent(t, uuid.New(), uuid.New())); err != nil {
		t.Fatalf("process: %v", err)
	}
	if len(deps.alertsOfType(NotifDatingPanicResponder)) != 1 {
		t.Fatalf("responders not paged when the context is unavailable")
	}
}

func TestDatingLocationSharedNotifiesRecipientWithoutCoordinates(t *testing.T) {
	deps := newFakeSafetyDeps()
	sharer, recipient, share := uuid.New(), uuid.New(), uuid.New()
	deps.firstNames[sharer] = "Asha"
	raw, _ := json.Marshal(map[string]any{
		"user_id": sharer.String(), "contact_id": recipient.String(), "share_id": share.String(),
		"expires_at": time.Now().Add(time.Hour), "started_at": time.Now(),
	})
	for i := 0; i < 2; i++ {
		if err := processDatingLocationShared(context.Background(), deps, raw); err != nil {
			t.Fatalf("process: %v", err)
		}
	}
	if len(deps.alerts) != 1 {
		t.Fatalf("alerts = %d, want 1 (the redelivery is ignored)", len(deps.alerts))
	}
	a := deps.alerts[0]
	if a.Recipient != recipient || a.Title != "Asha is sharing their live location with you" ||
		a.DeepLink != "/dating/safety/shared-locations/"+share.String() {
		t.Fatalf("alert = %+v", a)
	}
}

func TestDatingPanicContextUsesServiceOnlyPath(t *testing.T) {
	incident := uuid.New()
	want := fmt.Sprintf("/v1/dating/internal/safety/panic/%s/notify-context", incident)
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		if r.URL.Path != want {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprintf(w, `{"data":{"incident_id":%q,"first_name":"Asha","status":"open","trusted_contacts":[]}}`, incident)
	}))
	defer srv.Close()
	c := &datingClient{baseURL: srv.URL, internalKey: "k", http: srv.Client()}
	pctx, err := c.getPanicContext(context.Background(), incident)
	if err != nil || pctx.FirstName != "Asha" {
		t.Fatalf("context = %+v, err = %v", pctx, err)
	}
	if got.Get("X-Internal-Service-Key") != "k" {
		t.Fatalf("service key not sent")
	}
	for _, h := range []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "X-Admin-Role"} {
		if got.Get(h) != "" {
			t.Fatalf("service call carried %s", h)
		}
	}
}

type fakeRoles map[string][]string

func (f fakeRoles) Roles(_ context.Context, id string) ([]string, error) {
	roles, ok := f[id]
	if !ok {
		return nil, errors.New("identity unavailable")
	}
	return roles, nil
}

func TestResponderDirectoryPagesOnlyStaff(t *testing.T) {
	mod, user, unknown := uuid.New(), uuid.New(), uuid.New()
	ids, invalid := ParseResponderIDs(mod.String() + ", " + user.String() + " not-a-uuid," + unknown.String() + "," + mod.String())
	if len(ids) != 3 || len(invalid) != 1 {
		t.Fatalf("parse ids=%v invalid=%v", ids, invalid)
	}
	d := NewResponderDirectory(ids, fakeRoles{mod.String(): {"user", "moderator"}, user.String(): {"user"}})
	got := d.Responders(context.Background())
	// The moderator is paged, the plain user is dropped, and the id whose
	// lookup failed is still paged (configured by an operator).
	if len(got) != 2 || got[0] != mod || got[1] != unknown {
		t.Fatalf("responders = %v, want [%s %s]", got, mod, unknown)
	}
}
