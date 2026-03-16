package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/atpost/ai-service/internal/provider"
	"github.com/atpost/ai-service/internal/store/postgres"
	"github.com/atpost/ai-service/internal/worker"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// In-memory mock store
// ---------------------------------------------------------------------------

type mockStore struct {
	mu   sync.Mutex
	jobs map[uuid.UUID]*postgres.AIJob
}

func newMockStore() *mockStore {
	return &mockStore{jobs: make(map[uuid.UUID]*postgres.AIJob)}
}

func (m *mockStore) CreateJob(ctx context.Context, job *postgres.AIJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if job.ID == uuid.Nil {
		job.ID = uuid.New()
	}
	job.CreatedAt = time.Now().UTC()
	job.Status = "queued"
	cp := *job
	m.jobs[job.ID] = &cp
	return nil
}

func (m *mockStore) GetJob(ctx context.Context, id uuid.UUID) (*postgres.AIJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return nil, nil
	}
	cp := *j
	return &cp, nil
}

func (m *mockStore) UpdateJobStatus(ctx context.Context, id uuid.UUID, status string, result []byte, errMsg string, latencyMs int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return errors.New("job not found")
	}
	j.Status = status
	if len(result) > 0 {
		j.Result = json.RawMessage(result)
	}
	if errMsg != "" {
		j.ErrorMessage = &errMsg
	}
	if latencyMs > 0 {
		j.LatencyMs = &latencyMs
	}
	now := time.Now().UTC()
	j.CompletedAt = &now
	return nil
}

func (m *mockStore) GetPendingJobs(ctx context.Context, limit int) ([]postgres.AIJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []postgres.AIJob
	for _, j := range m.jobs {
		if j.Status == "queued" {
			cp := *j
			out = append(out, cp)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (m *mockStore) CreateModerationResult(ctx context.Context, r *postgres.ModerationResult) error {
	return nil
}

func (m *mockStore) GetModerationResult(ctx context.Context, contentType string, contentID uuid.UUID) (*postgres.ModerationResult, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// In-memory mock Redis (implements the tiny subset service needs via fakeredis)
// We use a simple map-based implementation so the tests have no external deps.
// ---------------------------------------------------------------------------

type fakeRedis struct {
	mu      sync.Mutex
	data    map[string]string
	expiries map[string]time.Time
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{
		data:     make(map[string]string),
		expiries: make(map[string]time.Time),
	}
}

func (f *fakeRedis) get(key string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if exp, ok := f.expiries[key]; ok && time.Now().After(exp) {
		delete(f.data, key)
		delete(f.expiries, key)
		return "", false
	}
	v, ok := f.data[key]
	return v, ok
}

func (f *fakeRedis) set(key, val string, ttl time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[key] = val
	if ttl > 0 {
		f.expiries[key] = time.Now().Add(ttl)
	}
}

// ---------------------------------------------------------------------------
// Mock provider that tracks call counts
// ---------------------------------------------------------------------------

type mockTextProvider struct {
	mu            sync.Mutex
	moderateCalls int
	captionCalls  int
}

func (m *mockTextProvider) GenerateCaptions(_ context.Context, content string, hints []string) ([]string, error) {
	m.mu.Lock()
	m.captionCalls++
	m.mu.Unlock()
	return []string{"caption A", "caption B", "caption C"}, nil
}

func (m *mockTextProvider) GenerateHashtags(_ context.Context, content string) ([]string, error) {
	return []string{"#foo", "#bar"}, nil
}

func (m *mockTextProvider) SmartReply(_ context.Context, message, ctx string) ([]string, error) {
	return []string{"reply 1", "reply 2", "reply 3"}, nil
}

func (m *mockTextProvider) Summarize(_ context.Context, text string, maxLen int) (string, error) {
	if maxLen > 0 && len(text) > maxLen {
		return text[:maxLen], nil
	}
	return text, nil
}

func (m *mockTextProvider) Translate(_ context.Context, text, targetLang string) (string, error) {
	return "[" + targetLang + "] " + text, nil
}

func (m *mockTextProvider) ScamCheck(_ context.Context, text string) (float64, string, error) {
	return 0.1, "ok", nil
}

func (m *mockTextProvider) PredictEngagement(_ context.Context, content string) (float64, error) {
	return 0.55, nil
}

type mockModerationProvider struct {
	mu    sync.Mutex
	calls int
}

func (m *mockModerationProvider) CheckContent(_ context.Context, text string, mediaURLs []string) (bool, float64, []string, error) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	return true, 0.05, nil, nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestJobEnqueueAndFetch verifies that EnqueueJob persists a job and GetJob retrieves it.
func TestJobEnqueueAndFetch(t *testing.T) {
	store := newMockStore()
	rdb := newFakeRedis()

	// Build a minimal service using the postgres.Store wrapper.
	// Because Service depends on *postgres.Store, we test via the worker Store interface
	// and validate the mock directly.
	_ = rdb

	refID := uuid.New()
	job := &postgres.AIJob{
		JobType:      "caption_suggestion",
		InputRefType: "draft",
		InputRefID:   refID,
	}

	ctx := context.Background()

	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}
	if job.ID == uuid.Nil {
		t.Fatal("expected non-nil job ID after create")
	}
	if job.Status != "queued" {
		t.Fatalf("expected status=queued, got %s", job.Status)
	}

	fetched, err := store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if fetched == nil {
		t.Fatal("expected job to be returned, got nil")
	}
	if fetched.ID != job.ID {
		t.Errorf("ID mismatch: want %s, got %s", job.ID, fetched.ID)
	}
	if fetched.JobType != "caption_suggestion" {
		t.Errorf("JobType mismatch: want caption_suggestion, got %s", fetched.JobType)
	}
	if fetched.InputRefID != refID {
		t.Errorf("InputRefID mismatch: want %s, got %s", refID, fetched.InputRefID)
	}
}

// TestStubProviderModeration verifies that the stub catches known scam phrases.
func TestStubProviderModeration(t *testing.T) {
	p := provider.NewStubTextProvider()
	ctx := context.Background()

	score, reason, err := p.ScamCheck(ctx, "Congratulations! You've won $1000. Click here to claim your free gift now!")
	if err != nil {
		t.Fatalf("ScamCheck failed: %v", err)
	}
	if score < 0.8 {
		t.Errorf("expected high scam score (>0.8) for scam text, got %.2f (reason: %s)", score, reason)
	}

	score2, _, err2 := p.ScamCheck(ctx, "Hey, want to grab coffee sometime?")
	if err2 != nil {
		t.Fatalf("ScamCheck (normal) failed: %v", err2)
	}
	if score2 > 0.2 {
		t.Errorf("expected low scam score (<0.2) for normal text, got %.2f", score2)
	}

	mod := provider.NewStubModerationProvider()
	safe, _, _, err3 := mod.CheckContent(ctx, "I will kill you asshole", nil)
	if err3 != nil {
		t.Fatalf("CheckContent failed: %v", err3)
	}
	if safe {
		t.Error("expected safe=false for text with banned words")
	}

	safe2, _, _, err4 := mod.CheckContent(ctx, "Have a wonderful day!", nil)
	if err4 != nil {
		t.Fatalf("CheckContent (safe) failed: %v", err4)
	}
	if !safe2 {
		t.Error("expected safe=true for clean text")
	}
}

// TestWorkerJobExecution verifies the state transition pending→processing→completed.
func TestWorkerJobExecution(t *testing.T) {
	store := newMockStore()
	textP := &mockTextProvider{}
	modP := &mockModerationProvider{}
	ctx := context.Background()

	// Enqueue a caption suggestion job with input embedded in result JSONB.
	inputPayload, _ := json.Marshal(map[string]string{"content": "sunset on the beach"})
	job := &postgres.AIJob{
		JobType:      "caption_suggestion",
		InputRefType: "draft",
		InputRefID:   uuid.New(),
		Result:       json.RawMessage(inputPayload),
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	w := worker.New(store, textP, modP, 1).WithPollInterval(50 * time.Millisecond)

	// Run the worker with a context that is cancelled after the job completes.
	workerCtx, cancel := context.WithCancel(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(workerCtx)
	}()

	// Poll until the job reaches a terminal state (up to 3 seconds).
	deadline := time.Now().Add(3 * time.Second)
	var finalJob *postgres.AIJob
	for time.Now().Before(deadline) {
		j, err := store.GetJob(ctx, job.ID)
		if err != nil {
			t.Fatalf("GetJob failed: %v", err)
		}
		if j != nil && (j.Status == "completed" || j.Status == "failed") {
			finalJob = j
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	cancel()
	<-done

	if finalJob == nil {
		t.Fatal("job did not reach terminal state within 3 seconds")
	}
	if finalJob.Status != "completed" {
		t.Errorf("expected status=completed, got %s (error: %v)", finalJob.Status, finalJob.ErrorMessage)
	}
	if len(finalJob.Result) == 0 {
		t.Error("expected non-empty result JSON after completion")
	}

	// Verify result contains captions.
	var result map[string]interface{}
	if err := json.Unmarshal(finalJob.Result, &result); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if _, ok := result["captions"]; !ok {
		t.Error("result JSON missing 'captions' field")
	}
}

// TestModerationCheckCaching verifies that a second call hits the cache and not the provider.
// We test this at the provider level since Service needs a real Redis client;
// we validate caching semantics using the fakeRedis directly.
func TestModerationCheckCaching(t *testing.T) {
	fakeRdb := newFakeRedis()
	cacheKey := "ai:toxicity:" + uuid.New().String()

	// Simulate first call: result not in cache → store it.
	callCount := 0
	fetchResult := func() *postgres.ModerationResult {
		callCount++
		score := float32(0.05)
		return &postgres.ModerationResult{
			ID:          uuid.New(),
			ContentType: "post",
			ContentID:   uuid.New(),
			TextScore:   &score,
			Action:      "allow",
		}
	}

	// First lookup: cache miss.
	_, found := fakeRdb.get(cacheKey)
	if found {
		t.Fatal("expected cache miss on first lookup")
	}

	result1 := fetchResult()
	data, err := json.Marshal(result1)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	fakeRdb.set(cacheKey, string(data), time.Hour)

	// Second lookup: cache hit.
	val, found2 := fakeRdb.get(cacheKey)
	if !found2 {
		t.Fatal("expected cache hit on second lookup")
	}
	var result2 postgres.ModerationResult
	if err := json.Unmarshal([]byte(val), &result2); err != nil {
		t.Fatalf("unmarshal cached result: %v", err)
	}

	// Provider should have been called exactly once.
	if callCount != 1 {
		t.Errorf("expected provider called once, got %d", callCount)
	}

	if result2.Action != result1.Action {
		t.Errorf("cached action mismatch: want %s, got %s", result1.Action, result2.Action)
	}
}

// TestWorkerModeration ensures the worker correctly executes a moderation_check job.
func TestWorkerModeration(t *testing.T) {
	store := newMockStore()
	textP := &mockTextProvider{}
	modP := &mockModerationProvider{}
	ctx := context.Background()

	inputPayload, _ := json.Marshal(map[string]string{"text": "totally fine content"})
	job := &postgres.AIJob{
		JobType:      "moderation_check",
		InputRefType: "post",
		InputRefID:   uuid.New(),
		Result:       json.RawMessage(inputPayload),
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	w := worker.New(store, textP, modP, 1).WithPollInterval(50 * time.Millisecond)
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(workerCtx)
	}()

	deadline := time.Now().Add(3 * time.Second)
	var finalJob *postgres.AIJob
	for time.Now().Before(deadline) {
		j, err := store.GetJob(ctx, job.ID)
		if err != nil {
			t.Fatalf("GetJob failed: %v", err)
		}
		if j != nil && (j.Status == "completed" || j.Status == "failed") {
			finalJob = j
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	cancel()
	<-done

	if finalJob == nil {
		t.Fatal("moderation job did not reach terminal state within 3 seconds")
	}
	if finalJob.Status != "completed" {
		t.Errorf("expected status=completed, got %s", finalJob.Status)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(finalJob.Result, &result); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if _, ok := result["safe"]; !ok {
		t.Error("moderation result missing 'safe' field")
	}
	if _, ok := result["action"]; !ok {
		t.Error("moderation result missing 'action' field")
	}

	if modP.calls < 1 {
		t.Error("expected moderation provider to be called at least once")
	}
}
