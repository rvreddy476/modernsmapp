package service

// The address an order is fulfilled to (audit 2026-10-30 M5).
//
// Shipment booking, the invoice ship-to, the delivery partner's customer
// contact and the return pickup all read the LIVE customer_addresses row the
// order points at. That row stays editable after payment (UpdateAddress), so a
// buyer who paid for a Bengaluru delivery and then edited the saved address to
// an Assam pincode had the courier booked to Assam at the Bengaluru rate, and
// the GST invoice printed an Assam ship-to beside a Karnataka place of supply.
//
// Checkout seals a snapshot of the address into the order
// (delivery_address_snapshot_enc, ScopeOrderSnapshot). It carries every field
// those four readers use (name, phone, lines, landmark, city, state, postal
// code, country), so no new snapshot column is needed: they now read the
// snapshot. The live row is read ONLY for an order that has no usable
// snapshot at all, which is an order placed before checkout began writing one.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/store/postgres"
)

// errOrderHasNoAddress: the order has neither a snapshot nor an address row.
var errOrderHasNoAddress = errors.New("order has no delivery address")

// orderFulfilmentAddress is the address an order ships to, from its snapshot.
func (s *Service) orderFulfilmentAddress(ctx context.Context, order *postgres.Order) (*pii.Address, error) {
	return s.resolveOrderAddress(ctx, order, s.liveOrderAddress)
}

// liveOrderAddress opens the customer_addresses row the order points at.
func (s *Service) liveOrderAddress(ctx context.Context, order *postgres.Order) (*pii.Address, error) {
	if order.DeliveryAddressID == nil {
		return nil, errOrderHasNoAddress
	}
	row, err := s.store.GetAddressRow(ctx, *order.DeliveryAddressID)
	if err != nil {
		return nil, fmt.Errorf("read delivery address: %w", err)
	}
	return s.openAddressRow(ctx, row)
}

// resolveOrderAddress is orderFulfilmentAddress with the live-row reader
// passed in, so the decision is testable without a database.
//
// Order of preference:
//
//  1. the sealed snapshot. If it is present but cannot be opened, that is an
//     error, never a reason to read the live row: falling back would
//     reintroduce exactly the edit-after-payment defect this exists to stop;
//  2. the plaintext snapshot, while the PII cutover still permits plaintext
//     reads (the dual-write window, before the backfill sealed it);
//  3. the live row, only for an order with no usable snapshot (see below).
func (s *Service) resolveOrderAddress(ctx context.Context, order *postgres.Order,
	live func(context.Context, *postgres.Order) (*pii.Address, error)) (*pii.Address, error) {
	if len(order.DeliveryAddressSnapshotEnc) > 0 {
		if s.pii == nil {
			return nil, fmt.Errorf("commerce: PII cipher is not configured; cannot open the snapshot of order %s", order.ID)
		}
		plain, err := s.pii.Open(ctx, pii.ScopeOrderSnapshot, order.DeliveryAddressSnapshotEnc)
		if err != nil {
			return nil, fmt.Errorf("open the address snapshot of order %s: %w", order.ID, err)
		}
		var addr pii.Address
		if err := json.Unmarshal([]byte(plain), &addr); err != nil {
			return nil, fmt.Errorf("the address snapshot of order %s is not an address: %w", order.ID, err)
		}
		// Checkout always snapshots a street line. A sealed blob without one
		// is a legacy partial that piibackfill sealed as it found it; it is
		// handled like a missing snapshot, below.
		if addr.AddressLine1 != "" {
			return &addr, nil
		}
	} else if snap, ok := plaintextSnapshot(order.DeliveryAddressSnapshot); ok {
		if !s.piiCutover.AllowsPlaintextRead() {
			// After cutover every snapshot is sealed (piibackfill runs before
			// the scrub). One that is not is a defect to surface, and the
			// live row is no substitute for it.
			return nil, fmt.Errorf("commerce: order %s has an unsealed address snapshot and the PII cutover is complete", order.ID)
		}
		return snap, nil
	}
	// Legacy fallback. Orders placed before checkout wrote a snapshot
	// (migration 008 marks them address_snapshot_provenance =
	// 'legacy_unverified') have nothing else to ship or invoice to. Their
	// true address cannot be reconstructed, so the live row is the best
	// available, and it is what these readers always used for them.
	slog.Warn("commerce: order has no address snapshot; using the live address row",
		"order_id", order.ID)
	return live(ctx, order)
}

// plaintextSnapshot decodes a plaintext snapshot that actually holds an
// address. A legacy blob without address_line_1 (the test migration 008 used
// to mark legacy rows) is not one.
func plaintextSnapshot(raw []byte) (*pii.Address, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var addr pii.Address
	if err := json.Unmarshal(raw, &addr); err != nil || addr.AddressLine1 == "" {
		return nil, false
	}
	return &addr, true
}
