package middleware

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/rider-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// fakeAuditWriter captures RecordAudit calls in memory for assertion.
type fakeAuditWriter struct {
	mu      sync.Mutex
	records []store.RecordAuditInput
}

func (f *fakeAuditWriter) RecordAudit(_ context.Context, in store.RecordAuditInput) (*store.AuditLog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, in)
	return &store.AuditLog{}, nil
}

func (f *fakeAuditWriter) latest() *store.RecordAuditInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.records) == 0 {
		return nil
	}
	r := f.records[len(f.records)-1]
	return &r
}

func newTestRouter(w *fakeAuditWriter) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	admin := r.Group("/v1/rider/admin")
	admin.Use(AdminGuard())
	admin.Use(AuditAdmin(w))
	admin.POST("/partners/:id/approve", func(c *gin.Context) {
		c.Set(AuditActionKey, "partner.approve")
		c.Set(AuditTargetKindKey, "partner")
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	admin.GET("/dashboard", func(c *gin.Context) {
		c.Set(AuditActionKey, "dashboard.view")
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	admin.POST("/no-label", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

// TestAdminGuard_RejectsMissingUser — no X-User-ID -> 401.
func TestAdminGuard_RejectsMissingUser(t *testing.T) {
	w := &fakeAuditWriter{}
	r := newTestRouter(w)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/rider/admin/dashboard", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", rec.Code)
	}
}

// TestAdminGuard_RejectsBadUser — bad uuid -> 400.
func TestAdminGuard_RejectsBadUser(t *testing.T) {
	w := &fakeAuditWriter{}
	r := newTestRouter(w)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/rider/admin/dashboard", nil)
	req.Header.Set("X-User-ID", "not-a-uuid")
	req.Header.Set(AdminRoleHeader, AdminRoleValue)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400", rec.Code)
	}
}

// TestAdminGuard_RejectsMissingRole — uuid present but no rider:admin -> 403.
func TestAdminGuard_RejectsMissingRole(t *testing.T) {
	w := &fakeAuditWriter{}
	r := newTestRouter(w)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/rider/admin/dashboard", nil)
	req.Header.Set("X-User-ID", uuid.NewString())
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d; want 403", rec.Code)
	}
}

// TestAdminGuard_RejectsWrongRole — role header present but wrong value -> 403.
func TestAdminGuard_RejectsWrongRole(t *testing.T) {
	w := &fakeAuditWriter{}
	r := newTestRouter(w)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/rider/admin/dashboard", nil)
	req.Header.Set("X-User-ID", uuid.NewString())
	req.Header.Set(AdminRoleHeader, "guest")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d; want 403", rec.Code)
	}
}

// TestAuditAdmin_WritesRow_PartnerApprove — successful admin call writes
// one audit row carrying handler-supplied action + target_kind + target_id.
func TestAuditAdmin_WritesRow_PartnerApprove(t *testing.T) {
	w := &fakeAuditWriter{}
	r := newTestRouter(w)
	adminID := uuid.New()
	partnerID := uuid.New()
	rec := httptest.NewRecorder()
	body := []byte(`{"foo":"bar"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/rider/admin/partners/"+partnerID.String()+"/approve", bytes.NewReader(body))
	req.Header.Set("X-User-ID", adminID.String())
	req.Header.Set(AdminRoleHeader, AdminRoleValue)
	req.Header.Set("User-Agent", "test/1.0")
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	got := w.latest()
	if got == nil {
		t.Fatalf("no audit record written")
	}
	if got.AdminUserID != adminID {
		t.Errorf("admin id = %s; want %s", got.AdminUserID, adminID)
	}
	if got.Action != "partner.approve" {
		t.Errorf("action = %q; want partner.approve", got.Action)
	}
	if got.EntityType != "partner" {
		t.Errorf("entity_type = %q; want partner", got.EntityType)
	}
	if got.EntityID == nil || *got.EntityID != partnerID {
		t.Errorf("entity_id = %v; want %s", got.EntityID, partnerID)
	}
	if got.RequestPath == nil || !strings.Contains(*got.RequestPath, "/approve") {
		t.Errorf("request_path missing or mismatched: %v", got.RequestPath)
	}
	if got.RequestMethod == nil || *got.RequestMethod != http.MethodPost {
		t.Errorf("request_method = %v", got.RequestMethod)
	}
	if got.ResponseStatus == nil || *got.ResponseStatus != http.StatusOK {
		t.Errorf("response_status = %v", got.ResponseStatus)
	}
	if got.LatencyMS == nil || *got.LatencyMS < 0 {
		t.Errorf("latency_ms missing or negative: %v", got.LatencyMS)
	}
	if got.UserAgent == nil || *got.UserAgent != "test/1.0" {
		t.Errorf("user_agent = %v", got.UserAgent)
	}
	if got.RequestBody == nil || !strings.Contains(*got.RequestBody, "foo") {
		t.Errorf("request_body = %v; want to contain foo", got.RequestBody)
	}
}

// TestAuditAdmin_FallbackLabels — handler that doesn't set any audit_*
// keys still produces a row with method+path action and derived target_kind.
func TestAuditAdmin_FallbackLabels(t *testing.T) {
	w := &fakeAuditWriter{}
	r := newTestRouter(w)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/rider/admin/no-label", nil)
	req.Header.Set("X-User-ID", uuid.NewString())
	req.Header.Set(AdminRoleHeader, AdminRoleValue)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	got := w.latest()
	if got == nil {
		t.Fatalf("no audit record written")
	}
	if !strings.Contains(got.Action, "/v1/rider/admin/no-label") {
		t.Errorf("fallback action = %q; expected method+path", got.Action)
	}
	// no-label doesn't match a known bucket; deriveTargetKind returns
	// "no-label" which is not a known plural -> kept as-is.
	if got.EntityType == "" {
		t.Errorf("entity_type empty; expected fallback derivation")
	}
}

// TestDeriveTargetKind — known plural buckets map to singular forms.
func TestDeriveTargetKind(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/v1/rider/admin/partners/abc/approve", "partner"},
		{"/v1/rider/admin/documents/abc/verify", "document"},
		{"/v1/rider/admin/vehicles/abc/verify", "vehicle"},
		{"/v1/rider/admin/payments/abc/verify", "payment"},
		{"/v1/rider/admin/rides/live", "ride"},
		{"/v1/rider/admin/complaints/abc/update-status", "complaint"},
		{"/v1/rider/admin/safety-incidents/abc/resolve", "safety_incident"},
		{"/v1/rider/admin/cities", "city"},
		{"/v1/rider/admin/zones/abc", "zone"},
		{"/v1/rider/admin/fare-rules", "fare_rule"},
		{"/v1/rider/admin/audit-logs", "audit_log"},
		{"/v1/rider/admin/dashboard", "dashboard"},
		{"/v1/rider/admin/random-thing", "random-thing"},
		{"/not/admin", "rider"},
	}
	for _, c := range cases {
		if got := deriveTargetKind(c.path); got != c.want {
			t.Errorf("deriveTargetKind(%q) = %q; want %q", c.path, got, c.want)
		}
	}
}

// TestTruncateBody — long bodies get truncated marker appended.
func TestTruncateBody(t *testing.T) {
	if got := truncateBody(nil, 10); got != "" {
		t.Errorf("nil body should be empty: %q", got)
	}
	if got := truncateBody([]byte("hello"), 10); got != "hello" {
		t.Errorf("short body should pass through: %q", got)
	}
	long := bytes.Repeat([]byte("x"), 2048)
	got := truncateBody(long, 1024)
	if !strings.HasSuffix(got, "...[truncated]") {
		t.Errorf("long body should be truncated: tail=%q", got[len(got)-20:])
	}
	if len(got) <= 1024 {
		t.Errorf("truncated body should be > 1024 (1024 + suffix), got %d", len(got))
	}
}
