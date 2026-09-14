package service

// Applications (migration 010): the registry checks a new payment must pass,
// and the per-application reads.

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/payments-service/internal/store/postgres"
)

var (
	// ErrApplicationUnknown: the application is not in the registry.
	ErrApplicationUnknown = errors.New("payments: application is not registered")
	// ErrApplicationDisabled: the application exists but takes no new payments.
	ErrApplicationDisabled = errors.New("payments: application is disabled")
	// ErrMethodNotEnabledForApplication: the application does not accept this
	// payment method.
	ErrMethodNotEnabledForApplication = errors.New("payments: payment method is not enabled for this application")
)

// requireApplicationForPayment is the registry gate on every new intent: the
// application exists, is active, and accepts the method. It reads the registry
// on each call rather than caching it, so a PUT that disables an application
// takes effect on every replica at once.
func (s *Service) requireApplicationForPayment(ctx context.Context, key, method string) (*postgres.Application, error) {
	if !postgres.ValidApplicationKey(key) {
		return nil, fmt.Errorf("%w: %q", ErrApplicationUnknown, key)
	}
	app, err := s.store.GetApplication(ctx, key)
	switch {
	case errors.Is(err, postgres.ErrApplicationNotFound):
		return nil, fmt.Errorf("%w: %q", ErrApplicationUnknown, key)
	case err != nil:
		return nil, err
	}
	if !app.Active() {
		return nil, fmt.Errorf("%w: %q", ErrApplicationDisabled, key)
	}
	if !app.MethodEnabled(method) {
		return nil, fmt.Errorf("%w: %q does not accept %q", ErrMethodNotEnabledForApplication, key, method)
	}
	return app, nil
}

// GetApplication reads one registry entry.
func (s *Service) GetApplication(ctx context.Context, key string) (*postgres.Application, error) {
	return s.store.GetApplication(ctx, key)
}

// ListApplications reads the registry.
func (s *Service) ListApplications(ctx context.Context) ([]postgres.Application, error) {
	return s.store.ListApplications(ctx)
}

// PutApplication creates or replaces a registry entry, audited.
func (s *Service) PutApplication(ctx context.Context, in postgres.PutApplicationInput) (*postgres.ApplicationWrite, error) {
	return s.store.PutApplication(ctx, in)
}

// ListApplicationTransactions pages an application's payments and refunds.
func (s *Service) ListApplicationTransactions(ctx context.Context, f postgres.TransactionFilter) ([]postgres.Transaction, *postgres.RefundCursor, error) {
	return s.store.ListApplicationTransactions(ctx, f)
}
