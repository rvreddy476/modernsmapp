package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrProviderNotFound is returned when a provider lookup misses.
var ErrProviderNotFound = errors.New("provider: not found")

// ErrCategoryNotFound is returned when a category lookup misses.
var ErrCategoryNotFound = errors.New("category: not found")

// ListCategories returns all active categories ordered by sort_order.
func (s *Store) ListCategories(ctx context.Context) ([]Category, error) {
	const q = `
        SELECT id, name, icon, sort_order, is_active
        FROM billpay.categories
        WHERE is_active = true
        ORDER BY sort_order ASC, name ASC`
	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()
	var out []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Icon, &c.SortOrder, &c.IsActive); err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCategory looks up a single category by id.
func (s *Store) GetCategory(ctx context.Context, id string) (*Category, error) {
	const q = `
        SELECT id, name, icon, sort_order, is_active
        FROM billpay.categories WHERE id = $1`
	var c Category
	if err := s.db.QueryRow(ctx, q, id).Scan(&c.ID, &c.Name, &c.Icon, &c.SortOrder, &c.IsActive); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCategoryNotFound
		}
		return nil, fmt.Errorf("get category: %w", err)
	}
	return &c, nil
}

// ListProviders returns providers filtered by optional category + state.
// state filter applies only to providers whose `states` array is non-empty
// (state-restricted billers); national billers (empty states) are always
// returned.
func (s *Store) ListProviders(ctx context.Context, category, state string, limit int) ([]Provider, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	const q = `
        SELECT id, setu_biller_id, category_id, name, short_name, logo_url,
               states, customer_params, bill_fetch_supported, is_active, last_synced_at
        FROM billpay.providers
        WHERE is_active = true
          AND ($1 = '' OR category_id = $1)
          AND ($2 = '' OR cardinality(states) = 0 OR $2 = ANY(states))
        ORDER BY name ASC
        LIMIT $3`
	rows, err := s.db.Query(ctx, q, category, state, limit)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()
	return scanProviders(rows)
}

// GetProvider returns a single provider by id.
func (s *Store) GetProvider(ctx context.Context, id uuid.UUID) (*Provider, error) {
	const q = `
        SELECT id, setu_biller_id, category_id, name, short_name, logo_url,
               states, customer_params, bill_fetch_supported, is_active, last_synced_at
        FROM billpay.providers WHERE id = $1`
	rows, err := s.db.Query(ctx, q, id)
	if err != nil {
		return nil, fmt.Errorf("get provider: %w", err)
	}
	defer rows.Close()
	out, err := scanProviders(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrProviderNotFound
	}
	return &out[0], nil
}

// GetProviderBySetuID looks up by Setu's biller id. Used by the nightly sync.
func (s *Store) GetProviderBySetuID(ctx context.Context, setuID string) (*Provider, error) {
	const q = `
        SELECT id, setu_biller_id, category_id, name, short_name, logo_url,
               states, customer_params, bill_fetch_supported, is_active, last_synced_at
        FROM billpay.providers WHERE setu_biller_id = $1`
	rows, err := s.db.Query(ctx, q, setuID)
	if err != nil {
		return nil, fmt.Errorf("get provider by setu id: %w", err)
	}
	defer rows.Close()
	out, err := scanProviders(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrProviderNotFound
	}
	return &out[0], nil
}

// UpsertProviderInput is the inbound shape for nightly provider sync.
type UpsertProviderInput struct {
	SetuBillerID       string
	CategoryID         string
	Name               string
	ShortName          *string
	LogoURL            *string
	States             []string
	CustomerParamsJSON []byte
	BillFetchSupported bool
}

// UpsertProvider inserts or refreshes a provider. ON CONFLICT keeps the
// nightly sync idempotent. Returns the resulting row id.
func (s *Store) UpsertProvider(ctx context.Context, in UpsertProviderInput) (uuid.UUID, error) {
	if in.SetuBillerID == "" || in.CategoryID == "" || in.Name == "" {
		return uuid.Nil, fmt.Errorf("upsert provider: missing required fields")
	}
	if len(in.CustomerParamsJSON) == 0 {
		in.CustomerParamsJSON = []byte("[]")
	}
	if in.States == nil {
		in.States = []string{}
	}
	const q = `
        INSERT INTO billpay.providers (
            setu_biller_id, category_id, name, short_name, logo_url,
            states, customer_params, bill_fetch_supported, is_active, last_synced_at
        ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, true, now())
        ON CONFLICT (setu_biller_id) DO UPDATE SET
            category_id = EXCLUDED.category_id,
            name = EXCLUDED.name,
            short_name = EXCLUDED.short_name,
            logo_url = EXCLUDED.logo_url,
            states = EXCLUDED.states,
            customer_params = EXCLUDED.customer_params,
            bill_fetch_supported = EXCLUDED.bill_fetch_supported,
            last_synced_at = now()
        RETURNING id`
	var id uuid.UUID
	if err := s.db.QueryRow(ctx, q,
		in.SetuBillerID, in.CategoryID, in.Name, in.ShortName, in.LogoURL,
		in.States, in.CustomerParamsJSON, in.BillFetchSupported,
	).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("upsert provider: %w", err)
	}
	return id, nil
}

// scanProviders is the shared scanner for provider rows.
func scanProviders(rows pgx.Rows) ([]Provider, error) {
	var out []Provider
	for rows.Next() {
		var p Provider
		if err := rows.Scan(
			&p.ID, &p.SetuBillerID, &p.CategoryID, &p.Name, &p.ShortName, &p.LogoURL,
			&p.States, &p.CustomerParams, &p.BillFetchSupported, &p.IsActive, &p.LastSyncedAt,
		); err != nil {
			return nil, fmt.Errorf("scan provider: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
