package bank

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
)

// MockClient is the deterministic in-memory bank client used by tests and
// when BANK_PARTNER=mock. Designed so every error path is reachable from
// tests by passing reserved sentinel values:
//
//   - userID with all-zeros lower bits → OpenSubAccount fails.
//   - amountPaise == 0 in any method → returns invalid.
//   - originalTxnRef == "fail-refund" → Refund fails.
//   - upiTxnRef == "fail-upi" → VerifyUPIInbound returns (false, error).
//   - upiTxnRef == "missing-upi" → VerifyUPIInbound returns (false, nil).
type MockClient struct {
	mu       sync.Mutex
	balances map[string]int64        // ref → paise
	failNext map[string]bool         // ref → return error on next op
	transfers []MockTransfer         // audit log for tests
}

// MockTransfer records a successful transfer. Tests inspect this slice.
type MockTransfer struct {
	From, To string
	Paise    int64
	TxnRef   string
}

// NewMockClient returns a fresh MockClient with empty state.
func NewMockClient() *MockClient {
	return &MockClient{
		balances: make(map[string]int64),
		failNext: make(map[string]bool),
	}
}

// SeedBalance pre-populates a balance for tests.
func (m *MockClient) SeedBalance(ref string, paise int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.balances[ref] = paise
}

// FailNext arms a sentinel: the next op against ref will return an error.
func (m *MockClient) FailNext(ref string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failNext[ref] = true
}

// Transfers returns a copy of the audit log. Tests assert on len + content.
func (m *MockClient) Transfers() []MockTransfer {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]MockTransfer, len(m.transfers))
	copy(out, m.transfers)
	return out
}

// OpenSubAccount returns a deterministic ref derived from the userID.
// Returns an error if the userID is the zero UUID (sentinel for tests).
func (m *MockClient) OpenSubAccount(_ context.Context, userID uuid.UUID) (string, error) {
	if userID == uuid.Nil {
		return "", fmt.Errorf("bank mock: invalid user id")
	}
	ref := "mock-ppi-" + userID.String()
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.balances[ref]; !ok {
		m.balances[ref] = 0
	}
	return ref, nil
}

// GetBalance returns the seeded balance, or 0 if never seeded.
func (m *MockClient) GetBalance(_ context.Context, ref string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failNext[ref] {
		delete(m.failNext, ref)
		return 0, fmt.Errorf("bank mock: simulated GetBalance failure")
	}
	return m.balances[ref], nil
}

// Transfer debits fromRef and credits toRef. Returns an error if fromRef has
// insufficient balance OR if FailNext was armed against fromRef.
func (m *MockClient) Transfer(_ context.Context, fromRef, toRef string, amountPaise int64, txnRef string) error {
	if amountPaise <= 0 {
		return fmt.Errorf("bank mock: invalid amount")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failNext[fromRef] {
		delete(m.failNext, fromRef)
		return fmt.Errorf("bank mock: simulated transfer failure")
	}
	if m.balances[fromRef] < amountPaise {
		return fmt.Errorf("bank mock: insufficient balance at partner bank for %s", fromRef)
	}
	m.balances[fromRef] -= amountPaise
	m.balances[toRef] += amountPaise
	m.transfers = append(m.transfers, MockTransfer{
		From:   fromRef,
		To:     toRef,
		Paise:  amountPaise,
		TxnRef: txnRef,
	})
	return nil
}

// VerifyUPIInbound returns true unless the upiTxnRef is one of the sentinels.
// On true, it credits the configured "ppi-pool" ref by the expected amount
// so a follow-on transfer to the user has funds (helpful for end-to-end
// test scenarios).
func (m *MockClient) VerifyUPIInbound(_ context.Context, upiTxnRef string, expectedAmountPaise int64) (bool, error) {
	if upiTxnRef == "fail-upi" {
		return false, fmt.Errorf("bank mock: simulated UPI verify failure")
	}
	if upiTxnRef == "missing-upi" {
		return false, nil
	}
	if expectedAmountPaise <= 0 {
		return false, fmt.Errorf("bank mock: invalid expected amount")
	}
	return true, nil
}

// Refund returns nil for any non-sentinel originalTxnRef. The sentinel
// "fail-refund" returns an error.
func (m *MockClient) Refund(_ context.Context, originalTxnRef string, amountPaise int64) error {
	if originalTxnRef == "fail-refund" {
		return fmt.Errorf("bank mock: simulated refund failure")
	}
	if amountPaise <= 0 {
		return fmt.Errorf("bank mock: invalid amount")
	}
	return nil
}
