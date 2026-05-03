package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/atpost/bill-pay-service/internal/store"
	"github.com/google/uuid"
)

// ListCategories returns the static (seeded) bill categories.
func (s *Service) ListCategories(ctx context.Context) ([]store.Category, error) {
	return s.store.ListCategories(ctx)
}

// ListProviders returns providers filtered by category and (optionally) state.
func (s *Service) ListProviders(ctx context.Context, category, state string, limit int) ([]store.Provider, error) {
	return s.store.ListProviders(ctx, strings.TrimSpace(category), strings.TrimSpace(strings.ToUpper(state)), limit)
}

// GetProvider returns a single provider by id.
func (s *Service) GetProvider(ctx context.Context, providerID uuid.UUID) (*store.Provider, error) {
	if providerID == uuid.Nil {
		return nil, fmt.Errorf("invalid: provider id required")
	}
	return s.store.GetProvider(ctx, providerID)
}

// SyncProvidersForCategory pulls Setu's biller list for a category and
// upserts into our local catalog. Run from the nightly sync cron.
func (s *Service) SyncProvidersForCategory(ctx context.Context, category string) (int, error) {
	billers, err := s.setu.ListBillers(ctx, category)
	if err != nil {
		return 0, fmt.Errorf("setu list billers: %w", err)
	}
	count := 0
	for _, b := range billers {
		paramsJSON := []byte("[]")
		if len(b.CustomerParams) > 0 {
			j, err := marshalCustomerParams(b.CustomerParams)
			if err != nil {
				return count, fmt.Errorf("marshal params: %w", err)
			}
			paramsJSON = j
		}
		var short, logo *string
		if b.ShortName != "" {
			tmp := b.ShortName
			short = &tmp
		}
		if b.LogoURL != "" {
			tmp := b.LogoURL
			logo = &tmp
		}
		if _, err := s.store.UpsertProvider(ctx, store.UpsertProviderInput{
			SetuBillerID:       b.SetuBillerID,
			CategoryID:         b.CategoryID,
			Name:               b.Name,
			ShortName:          short,
			LogoURL:            logo,
			States:             b.States,
			CustomerParamsJSON: paramsJSON,
			BillFetchSupported: b.BillFetchSupported,
		}); err != nil {
			return count, fmt.Errorf("upsert provider: %w", err)
		}
		count++
	}
	return count, nil
}
