package service

import (
	"context"
	"testing"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// countingCompare wraps bcrypt so a test can see how many comparisons a login
// paid for, and against which hash.
func countingCompare(f *adminFixture) *[][]byte {
	var hashes [][]byte
	f.svc.comparePassword = func(hash, password []byte) error {
		hashes = append(hashes, hash)
		return bcrypt.CompareHashAndPassword(hash, password)
	}
	return &hashes
}

// A login for an address with no account returned before any bcrypt work, so
// its response time told an attacker which addresses have accounts. Every
// refusal now pays for exactly one comparison, the unknown ones against a hash
// at the service's own cost.
func TestLoginUnknownAccountStillComparesAPassword(t *testing.T) {
	f := newAdminFixture(t)
	hashes := countingCompare(f)

	_, err := f.svc.LoginWithPassword(context.Background(), "nobody@example.test", "guess", "d", "web", "1.1.1.1", "ua")
	if err == nil {
		t.Fatal("login for an unknown account succeeded")
	}
	if len(*hashes) != 1 {
		t.Fatalf("unknown account: %d password comparisons, want 1", len(*hashes))
	}
	if cost, cerr := bcrypt.Cost((*hashes)[0]); cerr != nil || cost != f.svc.cfg.BcryptCost {
		t.Fatalf("dummy hash cost %d (%v), want the service's %d", cost, cerr, f.svc.cfg.BcryptCost)
	}
}

// An account with no password (signed up with Google) is refused the same way.
func TestLoginPasswordlessAccountStillComparesAPassword(t *testing.T) {
	f := newAdminFixture(t)
	email := uuid.NewString() + "@example.test"
	u := &store.User{ID: uuid.New(), Email: &email, AccountStatus: store.AccountStatusActive}
	f.st.users[u.ID] = u
	hashes := countingCompare(f)

	if _, err := f.svc.LoginWithPassword(context.Background(), email, "guess", "d", "web", "1.1.1.1", "ua"); err == nil {
		t.Fatal("password login for a passwordless account succeeded")
	}
	if len(*hashes) != 1 {
		t.Fatalf("passwordless account: %d password comparisons, want 1", len(*hashes))
	}
}

// The real account's wrong password is the reference: one comparison.
func TestLoginWrongPasswordComparesOnce(t *testing.T) {
	f := newAdminFixture(t)
	u := f.addUser(t, false)
	hashes := countingCompare(f)

	if _, err := f.svc.LoginWithPassword(context.Background(), *u.Email, "wrong", "d", "web", "1.1.1.1", "ua"); err == nil {
		t.Fatal("wrong password accepted")
	}
	if len(*hashes) != 1 || string((*hashes)[0]) != u.PasswordHash {
		t.Fatalf("wrong password: %d comparisons, want 1 against the account's hash", len(*hashes))
	}
}
