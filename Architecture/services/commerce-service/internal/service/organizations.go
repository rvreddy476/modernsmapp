// Phase 5 — B2B organization service. Owns CRUD, RBAC, invitations.
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

var (
	ErrOrgNotFound        = fmt.Errorf("organization not found")
	ErrOrgForbidden       = fmt.Errorf("not authorised for this organization")
	ErrOrgInvalidRole     = fmt.Errorf("invalid role")
	ErrOrgLastAdmin       = fmt.Errorf("cannot remove or demote the last admin")
	ErrOrgInviteNotFound  = fmt.Errorf("invite not found or expired")
)

const inviteTTL = 7 * 24 * time.Hour

var validOrgRoles = map[string]bool{
	"admin":    true,
	"buyer":    true,
	"approver": true,
	"finance":  true,
}

// CreateOrganizationInput captures everything the create endpoint accepts.
// All compliance fields are optional on create — admins can edit later.
type CreateOrganizationInput struct {
	Name              string
	LegalName         *string
	GSTIN             *string
	PAN               *string
	BillingEmail      *string
	BillingPhone      *string
	BillingAddressID  *uuid.UUID
	ApprovalThreshold *float64
	CreditTermsDays   int
	CreditLimit       *float64
}

func (s *Service) CreateOrganization(ctx context.Context, actorID uuid.UUID, in CreateOrganizationInput) (*postgres.Organization, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if in.CreditTermsDays < 0 || in.CreditTermsDays > 90 {
		return nil, fmt.Errorf("credit_terms_days must be 0–90")
	}
	org := &postgres.Organization{
		Name:              name,
		LegalName:         in.LegalName,
		GSTIN:             in.GSTIN,
		PAN:               in.PAN,
		BillingEmail:      in.BillingEmail,
		BillingPhone:      in.BillingPhone,
		BillingAddressID:  in.BillingAddressID,
		ApprovalThreshold: in.ApprovalThreshold,
		CreditTermsDays:   in.CreditTermsDays,
		CreditLimit:       in.CreditLimit,
	}
	if err := s.store.CreateOrganization(ctx, org, actorID); err != nil {
		return nil, fmt.Errorf("create org: %w", err)
	}
	s.publish(ctx, "commerce.organization.created", map[string]any{
		"organization_id": org.ID, "name": org.Name, "created_by": actorID,
	})
	return org, nil
}

// requireOrgRole returns nil only if actor has one of the supplied roles in
// the org. Use the variadic to accept multiple roles (e.g. admin OR approver).
func (s *Service) requireOrgRole(ctx context.Context, orgID, actorID uuid.UUID, allowed ...string) (*postgres.OrganizationMember, error) {
	m, err := s.store.GetMembership(ctx, orgID, actorID)
	if err != nil {
		return nil, err
	}
	if m == nil || m.Status != "active" {
		return nil, ErrOrgForbidden
	}
	for _, r := range allowed {
		if m.Role == r {
			return m, nil
		}
	}
	return nil, ErrOrgForbidden
}

func (s *Service) GetOrganization(ctx context.Context, orgID, actorID uuid.UUID) (*postgres.Organization, error) {
	if _, err := s.requireOrgRole(ctx, orgID, actorID, "admin", "buyer", "approver", "finance"); err != nil {
		return nil, err
	}
	o, err := s.store.GetOrganizationByID(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, ErrOrgNotFound
	}
	return o, nil
}

func (s *Service) ListMyOrganizations(ctx context.Context, userID uuid.UUID) ([]*postgres.Organization, error) {
	return s.store.ListOrganizationsForUser(ctx, userID)
}

func (s *Service) UpdateOrganization(ctx context.Context, orgID, actorID uuid.UUID, patch *postgres.Organization) (*postgres.Organization, error) {
	if _, err := s.requireOrgRole(ctx, orgID, actorID, "admin"); err != nil {
		return nil, err
	}
	if patch.CreditTermsDays < 0 || patch.CreditTermsDays > 90 {
		return nil, fmt.Errorf("credit_terms_days must be 0–90")
	}
	if err := s.store.UpdateOrganization(ctx, orgID, patch); err != nil {
		return nil, err
	}
	return s.store.GetOrganizationByID(ctx, orgID)
}

func (s *Service) ListOrganizationMembers(ctx context.Context, orgID, actorID uuid.UUID) ([]*postgres.OrganizationMember, error) {
	if _, err := s.requireOrgRole(ctx, orgID, actorID, "admin", "buyer", "approver", "finance"); err != nil {
		return nil, err
	}
	return s.store.ListOrganizationMembers(ctx, orgID)
}

// InviteMember stores an invite + returns the bearer token. Email delivery
// is left to the caller (auth-service mailer); the token URL is constructed
// in the HTTP layer.
func (s *Service) InviteMember(ctx context.Context, orgID, actorID uuid.UUID, email, role string) (*postgres.OrganizationInvite, error) {
	if _, err := s.requireOrgRole(ctx, orgID, actorID, "admin"); err != nil {
		return nil, err
	}
	if !validOrgRoles[role] {
		return nil, ErrOrgInvalidRole
	}
	if strings.TrimSpace(email) == "" {
		return nil, fmt.Errorf("email is required")
	}
	tok, err := randToken(32)
	if err != nil {
		return nil, err
	}
	inv := &postgres.OrganizationInvite{
		OrganizationID: orgID,
		Email:          strings.ToLower(strings.TrimSpace(email)),
		Role:           role,
		Token:          tok,
		InvitedBy:      actorID,
		ExpiresAt:      time.Now().Add(inviteTTL),
	}
	if err := s.store.CreateInvite(ctx, inv); err != nil {
		return nil, err
	}
	s.publish(ctx, "commerce.organization.member_invited", map[string]any{
		"organization_id": orgID, "email": inv.Email, "role": role, "invite_id": inv.ID,
	})
	return inv, nil
}

// AcceptInvite is the recipient-facing accept call. Requires the bearer
// token + the authenticated user's id (so we can stamp the membership).
func (s *Service) AcceptInvite(ctx context.Context, token string, actorID uuid.UUID) (*postgres.OrganizationInvite, error) {
	inv, err := s.store.AcceptInvite(ctx, token, actorID)
	if err != nil {
		if strings.Contains(err.Error(), "expired or already accepted") {
			return nil, ErrOrgInviteNotFound
		}
		return nil, err
	}
	s.publish(ctx, "commerce.organization.member_joined", map[string]any{
		"organization_id": inv.OrganizationID, "user_id": actorID, "role": inv.Role,
	})
	return inv, nil
}

// UpdateMemberRole guards the last-admin invariant — every org must keep at
// least one active admin so settings remain reachable.
func (s *Service) UpdateMemberRole(ctx context.Context, orgID, actorID, targetUserID uuid.UUID, role string) error {
	if _, err := s.requireOrgRole(ctx, orgID, actorID, "admin"); err != nil {
		return err
	}
	if !validOrgRoles[role] {
		return ErrOrgInvalidRole
	}
	target, err := s.store.GetMembership(ctx, orgID, targetUserID)
	if err != nil {
		return err
	}
	if target == nil {
		return ErrOrgNotFound
	}
	if target.Role == "admin" && role != "admin" {
		if err := s.assertNotLastAdmin(ctx, orgID, targetUserID); err != nil {
			return err
		}
	}
	return s.store.UpdateMemberRole(ctx, orgID, targetUserID, role)
}

func (s *Service) RemoveMember(ctx context.Context, orgID, actorID, targetUserID uuid.UUID) error {
	if _, err := s.requireOrgRole(ctx, orgID, actorID, "admin"); err != nil {
		return err
	}
	target, err := s.store.GetMembership(ctx, orgID, targetUserID)
	if err != nil {
		return err
	}
	if target == nil {
		return ErrOrgNotFound
	}
	if target.Role == "admin" {
		if err := s.assertNotLastAdmin(ctx, orgID, targetUserID); err != nil {
			return err
		}
	}
	return s.store.RemoveMember(ctx, orgID, targetUserID)
}

func (s *Service) assertNotLastAdmin(ctx context.Context, orgID, targetUserID uuid.UUID) error {
	members, err := s.store.ListOrganizationMembers(ctx, orgID)
	if err != nil {
		return err
	}
	otherAdmins := 0
	for _, m := range members {
		if m.Status == "active" && m.Role == "admin" && m.UserID != targetUserID {
			otherAdmins++
		}
	}
	if otherAdmins == 0 {
		return ErrOrgLastAdmin
	}
	return nil
}

func randToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ─── Phase 5.3 — order approval routing ──────────────────────

// ApproveOrgOrder marks an awaiting_approval B2B order as approved and
// flips the order back to payment_pending (so the buyer can pay) or
// confirmed (for credit-terms orders). Approver must hold the 'approver'
// or 'admin' role on the same org. Idempotent — re-approving is a no-op.
func (s *Service) ApproveOrgOrder(ctx context.Context, orderID, actorID uuid.UUID, notes string) (*postgres.Order, error) {
	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil || order == nil {
		return nil, ErrOrderNotFound
	}
	if order.OrganizationID == nil {
		return nil, fmt.Errorf("not a B2B order")
	}
	if _, err := s.requireOrgRole(ctx, *order.OrganizationID, actorID, "admin", "approver"); err != nil {
		return nil, err
	}
	if order.ApprovalStatus != nil && *order.ApprovalStatus == "approved" {
		return order, nil
	}
	if order.ApprovalStatus == nil || *order.ApprovalStatus != "pending" {
		return nil, fmt.Errorf("order not pending approval")
	}
	// Credit-terms orders confirm directly; gateway orders flip back to
	// payment_pending so the buyer can complete checkout.
	nextStatus := "payment_pending"
	if order.CreditTermsDays > 0 {
		nextStatus = "confirmed"
	}
	if err := s.store.ApproveOrgOrder(ctx, orderID, actorID, notes, nextStatus); err != nil {
		return nil, fmt.Errorf("approve: %w", err)
	}
	s.publish(ctx, "commerce.org.order_approved", map[string]any{
		"order_id": orderID, "actor_id": actorID, "next_status": nextStatus,
	})
	return s.store.GetOrderByID(ctx, orderID)
}

// RejectOrgOrder cancels an awaiting_approval order with an explanation.
// The reserved stock is released (best-effort) since the order won't ship.
func (s *Service) RejectOrgOrder(ctx context.Context, orderID, actorID uuid.UUID, reason string) (*postgres.Order, error) {
	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil || order == nil {
		return nil, ErrOrderNotFound
	}
	if order.OrganizationID == nil {
		return nil, fmt.Errorf("not a B2B order")
	}
	if _, err := s.requireOrgRole(ctx, *order.OrganizationID, actorID, "admin", "approver"); err != nil {
		return nil, err
	}
	if order.ApprovalStatus == nil || *order.ApprovalStatus != "pending" {
		return nil, fmt.Errorf("order not pending approval")
	}
	if err := s.store.RejectOrgOrder(ctx, orderID, actorID, reason); err != nil {
		return nil, fmt.Errorf("reject: %w", err)
	}
	s.publish(ctx, "commerce.org.order_rejected", map[string]any{
		"order_id": orderID, "actor_id": actorID, "reason": reason,
	})
	return s.store.GetOrderByID(ctx, orderID)
}

// ListOrgPendingApprovals returns approval-blocked orders for the org. Used
// by the approver dashboard. Caller must hold any active role on the org.
func (s *Service) ListOrgPendingApprovals(ctx context.Context, orgID, actorID uuid.UUID, limit, offset int) ([]*postgres.Order, error) {
	if _, err := s.requireOrgRole(ctx, orgID, actorID, "admin", "approver", "buyer", "finance"); err != nil {
		return nil, err
	}
	return s.store.ListOrgOrdersByApprovalStatus(ctx, orgID, "pending", limit, offset)
}

// ListOrgOrders returns the org's order history with optional approval +
// status filters. Procurement dashboard primary read path.
func (s *Service) ListOrgOrders(ctx context.Context, orgID, actorID uuid.UUID, status string, limit, offset int) ([]*postgres.Order, error) {
	if _, err := s.requireOrgRole(ctx, orgID, actorID, "admin", "approver", "buyer", "finance"); err != nil {
		return nil, err
	}
	return s.store.ListOrgOrders(ctx, orgID, status, limit, offset)
}

// Compile-time guard against unused imports if errors helpers move out.
var _ = errors.New
