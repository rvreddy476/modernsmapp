// Customer addresses: sealing identifying fields for write, and adding,
// listing, updating, deleting and defaulting addresses. Moved out of
// service.go unchanged.

package service

import (
	"context"
	"fmt"

	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ─── Addresses ───────────────────────────────────────────────

// sealAddressForWrite encrypts an address's identifying fields.
//
// B4. Every client-reachable address write goes through here, and it FAILS the
// whole write when KMS, the key ring or the cipher is unavailable. That is
// deliberate: the alternative is storing the customer's name, phone and street
// in plaintext because the key service had a bad minute, and a row written
// that way is indistinguishable afterwards from one written before the
// cutover.
func (s *Service) sealAddressForWrite(ctx context.Context, addr *postgres.CustomerAddress) (postgres.SealedAddressWrite, error) {
	if s.pii == nil {
		return postgres.SealedAddressWrite{}, fmt.Errorf(
			"commerce: the PII cipher is not configured; refusing to store an address in plaintext")
	}
	sealed, err := s.pii.SealAddress(ctx, pii.ScopeProfile, pii.Address{
		ContactName:  addr.ContactName,
		Phone:        addr.Phone,
		AddressLine1: addr.AddressLine1,
		AddressLine2: derefOrEmpty(addr.AddressLine2),
		Landmark:     derefOrEmpty(addr.Landmark),
		City:         addr.City,
		State:        addr.State,
		PostalCode:   addr.PostalCode,
		Country:      addr.Country,
	})
	if err != nil {
		return postgres.SealedAddressWrite{}, fmt.Errorf("commerce: sealing the address: %w", err)
	}
	return postgres.SealedAddressWrite{
		ContactName:    sealed.ContactName,
		Phone:          sealed.Phone,
		AddressLine1:   sealed.AddressLine1,
		AddressLine2:   sealed.AddressLine2,
		Landmark:       sealed.Landmark,
		KeyVersion:     sealed.KeyVersion,
		LookupHash:     sealed.LookupHash,
		WritePlaintext: s.piiCutover.WritesPlaintext(),
	}, nil
}

func (s *Service) AddAddress(ctx context.Context, addr *postgres.CustomerAddress) error {
	sealed, err := s.sealAddressForWrite(ctx, addr)
	if err != nil {
		return err
	}
	return s.store.CreateAddress(ctx, addr, sealed)
}

// GetAddresses returns the customer's address book with its identifying
// fields OPENED.
//
// It used to return the plaintext columns as stored. After the PII cutover
// those are ”, so every saved address came back with no name, phone or
// street. A row that cannot be opened fails the whole read rather than being
// served blank: a nameless address the customer then checks out with is a
// parcel nobody can deliver.
func (s *Service) GetAddresses(ctx context.Context, userID uuid.UUID) ([]*postgres.CustomerAddress, error) {
	rows, err := s.store.GetAddressRowsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	var out []*postgres.CustomerAddress
	for _, row := range rows {
		a, err := s.openAddressRow(ctx, row)
		if err != nil {
			return nil, err
		}
		out = append(out, customerAddressFrom(row, a))
	}
	return out, nil
}

// customerAddressFrom is the wire shape of an opened address row.
func customerAddressFrom(row *postgres.AddressRow, a *pii.Address) *postgres.CustomerAddress {
	c := &postgres.CustomerAddress{
		ID: row.ID, UserID: row.UserID, Label: row.Label,
		ContactName: a.ContactName, Phone: a.Phone, AddressLine1: a.AddressLine1,
		City: row.City, State: row.State, Country: row.Country, PostalCode: row.PostalCode,
		AddressType: row.AddressType, IsDefault: row.IsDefault, CreatedAt: row.CreatedAt,
	}
	if line2 := a.AddressLine2; line2 != "" {
		c.AddressLine2 = &line2
	}
	if landmark := a.Landmark; landmark != "" {
		c.Landmark = &landmark
	}
	return c
}

func (s *Service) UpdateAddress(ctx context.Context, id, userID uuid.UUID, addr *postgres.CustomerAddress) error {
	sealed, err := s.sealAddressForWrite(ctx, addr)
	if err != nil {
		return err
	}
	return s.store.UpdateAddress(ctx, id, userID, addr, sealed)
}

func (s *Service) DeleteAddress(ctx context.Context, id, userID uuid.UUID) error {
	return s.store.DeleteAddress(ctx, id, userID)
}

func (s *Service) SetDefaultAddress(ctx context.Context, id, userID uuid.UUID) error {
	return s.store.SetDefaultAddress(ctx, id, userID)
}
