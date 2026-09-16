package http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/approvals"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	adminA = "11111111-1111-4111-8111-111111111111"
	adminB = "22222222-2222-4222-8222-222222222222"
	nobody = "33333333-3333-4333-8333-333333333333"
)

// commerceAll is every commerce permission the routes use.
var commerceAll = []string{
	permCatalogueEdit, permCODSettle, permKYCVerify, permPayoutsRead,
	permProductsModerate, permSellerApprove, permSellerSuspend,
}

// fakePerms is identity's permission route.
type fakePerms struct {
	mu     sync.Mutex
	byUser map[string]adminauth.Permissions
	err    error
	calls  int
}

func (f *fakePerms) UserPermissions(_ context.Context, id string) (adminauth.Permissions, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return adminauth.Permissions{}, f.err
	}
	p, ok := f.byUser[id]
	if !ok {
		return adminauth.Permissions{UserID: id, Apps: map[string][]string{}, Platform: []string{}}, nil
	}
	return p, nil
}

func (f *fakePerms) grant(user string, perms ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.byUser[user]
	if !ok {
		p = adminauth.Permissions{UserID: user, Apps: map[string][]string{}, Platform: []string{}}
	}
	for _, perm := range perms {
		app := adminauth.AppOf(perm)
		if app == "platform" {
			p.Platform = append(p.Platform, perm)
		} else {
			p.Apps[app] = append(p.Apps[app], perm)
		}
	}
	f.byUser[user] = p
}

// fakeHolders is identity's holders-count route.
type fakeHolders struct {
	mu                    sync.Mutex
	n                     int
	err                   error
	calls                 int
	lastPerm, lastExclude string
}

func (f *fakeHolders) OtherTOTPHolders(_ context.Context, perm, exclude string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastPerm, f.lastExclude = perm, exclude
	return f.n, f.err
}

// memStore is an in-memory approvals.Store with the same compare-and-set
// transitions as the postgres store.
type memStore struct {
	mu   sync.Mutex
	rows map[string]*approvals.Approval
}

func newMemStore() *memStore { return &memStore{rows: map[string]*approvals.Approval{}} }

func clone(a *approvals.Approval) *approvals.Approval {
	c := *a
	c.Payload = append([]byte(nil), a.Payload...)
	return &c
}

func (m *memStore) CreateApproval(_ context.Context, a *approvals.Approval) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a.ID = uuid.NewString()
	a.Status = approvals.StatusPending
	a.CreatedAt = time.Now()
	a.ExpiresAt = a.CreatedAt.Add(approvals.TTL)
	m.rows[a.ID] = clone(a)
	return nil
}

func (m *memStore) GetApproval(_ context.Context, id string) (*approvals.Approval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.rows[id]
	if !ok {
		return nil, approvals.ErrNotFound
	}
	return clone(a), nil
}

func (m *memStore) ClaimApproval(_ context.Context, id, approver, reason string) (*approvals.Approval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.rows[id]
	if !ok || a.Status != approvals.StatusPending || !time.Now().Before(a.ExpiresAt) {
		return nil, approvals.ErrNotClaimable
	}
	now := time.Now()
	a.Status, a.Approver, a.DecisionReason, a.DecidedAt = approvals.StatusApproved, &approver, &reason, &now
	return clone(a), nil
}

func (m *memStore) FinishApproval(_ context.Context, id string, status int, outcome string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.rows[id]
	if !ok || a.Status != approvals.StatusApproved {
		return approvals.ErrNotClaimable
	}
	now := time.Now()
	a.Status, a.ResultStatus, a.ResultOutcome, a.ExecutedAt = approvals.StatusExecuted, &status, &outcome, &now
	return nil
}

func (m *memStore) CloseApproval(_ context.Context, id, decider, reason string) (*approvals.Approval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.rows[id]
	if !ok || !(a.Status == approvals.StatusApproved || (a.Status == approvals.StatusPending && time.Now().Before(a.ExpiresAt))) {
		return nil, approvals.ErrNotClaimable
	}
	a.Status, a.Approver, a.DecisionReason = approvals.StatusRejected, &decider, &reason
	return clone(a), nil
}

func (m *memStore) ExpireApproval(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.rows[id]; ok && a.Status == approvals.StatusPending && !time.Now().Before(a.ExpiresAt) {
		a.Status = approvals.StatusExpired
	}
	return nil
}

func (m *memStore) ListPendingApprovals(_ context.Context, perms []string, exclude string, _ int) ([]approvals.Approval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []approvals.Approval{}
	for _, a := range m.rows {
		if a.Status != approvals.StatusPending || a.Requester == exclude || !time.Now().Before(a.ExpiresAt) {
			continue
		}
		for _, p := range perms {
			if p == a.RequiredPermission {
				out = append(out, *clone(a))
				break
			}
		}
	}
	return out, nil
}

// mutate edits a stored row, as a tampering database user would.
func (m *memStore) mutate(id string, fn func(*approvals.Approval)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn(m.rows[id])
}

type upstreamSeen struct {
	mu      sync.Mutex
	hits    int
	actor   string
	request string
	paths   []string
	bodies  []string
	userHdr bool
}

func (u *upstreamSeen) snapshot() (int, string, []string, []string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.hits, u.actor, append([]string(nil), u.paths...), append([]string(nil), u.bodies...)
}

type rig struct {
	r        *gin.Engine
	gate     *Gate
	rec      *fakeRecorder
	perms    *fakePerms
	holders  *fakeHolders
	store    approvals.Store
	seen     *upstreamSeen
	upstream *httptest.Server
}

type rigOpts struct {
	status     int // commerce stub answer; default 200
	store      approvals.Store
	requireMFA *bool
}

func newRig(t *testing.T, o rigOpts) *rig {
	t.Helper()
	if o.status == 0 {
		o.status = http.StatusOK
	}
	if o.store == nil {
		o.store = newMemStore()
	}
	requireMFA := true
	if o.requireMFA != nil {
		requireMFA = *o.requireMFA
	}
	seen := &upstreamSeen{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen.mu.Lock()
		seen.hits++
		seen.actor = r.Header.Get("X-User-Id")
		seen.request = r.Header.Get("X-Request-Id")
		seen.paths = append(seen.paths, r.URL.Path)
		seen.bodies = append(seen.bodies, string(b))
		seen.mu.Unlock()
		w.WriteHeader(o.status)
	}))
	t.Cleanup(upstream.Close)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequestID())
	rec := &fakeRecorder{}
	perms := &fakePerms{byUser: map[string]adminauth.Permissions{}}
	holders := &fakeHolders{}
	gate := NewGate(perms, rec, requireMFA)
	h := New(&stubAdminService{}, gate, approvals.NewService(o.store, holders))
	if err := h.RegisterAllRoutes(r, service.NewCommerceClient(upstream.URL, "test-internal-key")); err != nil {
		t.Fatalf("route table refused: %v", err)
	}
	return &rig{r: r, gate: gate, rec: rec, perms: perms, holders: holders, store: o.store, seen: seen, upstream: upstream}
}

type reqOpt func(*http.Request)

func noMFA(r *http.Request)    { r.Header.Del("X-Admin-MFA") }
func noStepUp(r *http.Request) { r.Header.Del("X-Step-Up-At") }
func stepUpAgo(d time.Duration) reqOpt {
	return func(r *http.Request) {
		r.Header.Set("X-Step-Up-At", strconv.FormatInt(time.Now().Add(-d).Unix(), 10))
	}
}

// do sends a request as actor with MFA and a fresh step-up unless opts remove them.
func (rg *rig) do(method, path, body, actor string, opts ...reqOpt) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", "req-123")
	if actor != "" {
		req.Header.Set("X-User-Id", actor)
	}
	req.Header.Set("X-Admin-MFA", "true")
	req.Header.Set("X-Step-Up-At", strconv.FormatInt(time.Now().Unix(), 10))
	for _, o := range opts {
		o(req)
	}
	w := httptest.NewRecorder()
	rg.r.ServeHTTP(w, req)
	return w
}

func (rg *rig) entries() []postgres.AdminAuditEntry {
	rg.rec.mu.Lock()
	defer rg.rec.mu.Unlock()
	return append([]postgres.AdminAuditEntry(nil), rg.rec.entries...)
}

func (rg *rig) onlyEntry(t *testing.T) postgres.AdminAuditEntry {
	t.Helper()
	e := rg.entries()
	if len(e) != 1 {
		t.Fatalf("audit rows = %d, want 1: %+v", len(e), e)
	}
	return e[0]
}

func hasCode(w *httptest.ResponseRecorder, code string) bool {
	return strings.Contains(w.Body.String(), `"code":"`+code+`"`)
}

var errIdentityDown = errors.New("identity down")
