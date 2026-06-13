// Phase 5 — B2B / organizations data access. Keeps the org RBAC + invite
// surface in its own file so commerce-service's main store.go stays focused
// on retail commerce.
package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CreateOrganization inserts a new org and inserts the creator as an admin
// member in a single tx so a half-created org without an owner is never
// observable.
func (s *Store) CreateOrganization(ctx context.Context, org *Organization, creatorUserID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := tx.QueryRow(ctx, `
		INSERT INTO organizations (
			name, legal_name, gstin, pan, billing_email, billing_phone,
			billing_address_id, approval_threshold, credit_terms_days, credit_limit,
			status, created_by_user_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'active',$11)
		RETURNING id, created_at, updated_at`,
		org.Name, org.LegalName, org.GSTIN, org.PAN, org.BillingEmail, org.BillingPhone,
		org.BillingAddressID, org.ApprovalThreshold, org.CreditTermsDays, org.CreditLimit,
		creatorUserID,
	).Scan(&org.ID, &org.CreatedAt, &org.UpdatedAt); err != nil {
		return err
	}
	org.Status = "active"
	now := time.Now()
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_members
			(organization_id, user_id, role, status, joined_at)
		VALUES ($1, $2, 'admin', 'active', $3)`,
		org.ID, creatorUserID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) GetOrganizationByID(ctx context.Context, id uuid.UUID) (*Organization, error) {
	o := &Organization{}
	err := s.db.QueryRow(ctx, `
		SELECT id, name, legal_name, gstin, pan, billing_email, billing_phone,
		       billing_address_id, approval_threshold, credit_terms_days, credit_limit,
		       status, created_by_user_id, created_at, updated_at
		FROM organizations WHERE id = $1`, id).Scan(
		&o.ID, &o.Name, &o.LegalName, &o.GSTIN, &o.PAN, &o.BillingEmail, &o.BillingPhone,
		&o.BillingAddressID, &o.ApprovalThreshold, &o.CreditTermsDays, &o.CreditLimit,
		&o.Status, &o.CreatedByUserID, &o.CreatedAt, &o.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return o, nil
}

// UpdateOrganization is a sparse update — non-nil fields on the supplied
// struct overwrite, nil fields leave the row untouched. Returns the
// post-update row.
func (s *Store) UpdateOrganization(ctx context.Context, id uuid.UUID, patch *Organization) error {
	_, err := s.db.Exec(ctx, `
		UPDATE organizations SET
			name              = COALESCE(NULLIF($2,''), name),
			legal_name        = COALESCE($3, legal_name),
			gstin             = COALESCE($4, gstin),
			pan               = COALESCE($5, pan),
			billing_email     = COALESCE($6, billing_email),
			billing_phone     = COALESCE($7, billing_phone),
			billing_address_id= COALESCE($8, billing_address_id),
			approval_threshold= COALESCE($9, approval_threshold),
			credit_terms_days = COALESCE($10, credit_terms_days),
			credit_limit      = COALESCE($11, credit_limit),
			updated_at        = NOW()
		WHERE id = $1`,
		id, patch.Name, patch.LegalName, patch.GSTIN, patch.PAN,
		patch.BillingEmail, patch.BillingPhone, patch.BillingAddressID,
		patch.ApprovalThreshold, patch.CreditTermsDays, patch.CreditLimit,
	)
	return err
}

// ListOrganizationsForUser returns every org the user is an active member of.
func (s *Store) ListOrganizationsForUser(ctx context.Context, userID uuid.UUID) ([]*Organization, error) {
	rows, err := s.db.Query(ctx, `
		SELECT o.id, o.name, o.legal_name, o.gstin, o.pan, o.billing_email, o.billing_phone,
		       o.billing_address_id, o.approval_threshold, o.credit_terms_days, o.credit_limit,
		       o.status, o.created_by_user_id, o.created_at, o.updated_at
		FROM organizations o
		JOIN organization_members m ON m.organization_id = o.id
		WHERE m.user_id = $1 AND m.status = 'active' AND o.status = 'active'
		ORDER BY o.name ASC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Organization
	for rows.Next() {
		o := &Organization{}
		if err := rows.Scan(&o.ID, &o.Name, &o.LegalName, &o.GSTIN, &o.PAN, &o.BillingEmail,
			&o.BillingPhone, &o.BillingAddressID, &o.ApprovalThreshold, &o.CreditTermsDays,
			&o.CreditLimit, &o.Status, &o.CreatedByUserID, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// GetMembership returns the user's role + status for an org, or nil if not a
// member. The service layer uses this for RBAC checks before every write.
func (s *Store) GetMembership(ctx context.Context, orgID, userID uuid.UUID) (*OrganizationMember, error) {
	m := &OrganizationMember{}
	err := s.db.QueryRow(ctx, `
		SELECT id, organization_id, user_id, role, status, invited_email, invited_at, joined_at
		FROM organization_members
		WHERE organization_id = $1 AND user_id = $2`, orgID, userID).Scan(
		&m.ID, &m.OrganizationID, &m.UserID, &m.Role, &m.Status,
		&m.InvitedEmail, &m.InvitedAt, &m.JoinedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return m, nil
}

func (s *Store) ListOrganizationMembers(ctx context.Context, orgID uuid.UUID) ([]*OrganizationMember, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, organization_id, user_id, role, status, invited_email, invited_at, joined_at
		FROM organization_members
		WHERE organization_id = $1
		ORDER BY status ASC, joined_at ASC NULLS LAST`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*OrganizationMember
	for rows.Next() {
		m := &OrganizationMember{}
		if err := rows.Scan(&m.ID, &m.OrganizationID, &m.UserID, &m.Role, &m.Status,
			&m.InvitedEmail, &m.InvitedAt, &m.JoinedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) UpdateMemberRole(ctx context.Context, orgID, userID uuid.UUID, role string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE organization_members SET role = $3
		WHERE organization_id = $1 AND user_id = $2`, orgID, userID, role)
	return err
}

func (s *Store) RemoveMember(ctx context.Context, orgID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE organization_members SET status = 'removed'
		WHERE organization_id = $1 AND user_id = $2`, orgID, userID)
	return err
}

// CreateInvite stores a pending invitation. The token is the bearer secret
// the recipient uses to accept; callers generate it (so they can include it
// in the invite email URL without a follow-up query).
func (s *Store) CreateInvite(ctx context.Context, inv *OrganizationInvite) error {
	return s.db.QueryRow(ctx, `
		INSERT INTO organization_invites
			(organization_id, email, role, token, invited_by, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id, created_at`,
		inv.OrganizationID, inv.Email, inv.Role, inv.Token, inv.InvitedBy, inv.ExpiresAt,
	).Scan(&inv.ID, &inv.CreatedAt)
}

func (s *Store) GetInviteByToken(ctx context.Context, token string) (*OrganizationInvite, error) {
	inv := &OrganizationInvite{}
	err := s.db.QueryRow(ctx, `
		SELECT id, organization_id, email, role, token, invited_by, expires_at, accepted_at, created_at
		FROM organization_invites WHERE token = $1`, token).Scan(
		&inv.ID, &inv.OrganizationID, &inv.Email, &inv.Role, &inv.Token,
		&inv.InvitedBy, &inv.ExpiresAt, &inv.AcceptedAt, &inv.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return inv, nil
}

// ApproveOrgOrder stamps the approver + sets the order back into payment
// flow (or confirmed for credit-terms orders). Phase 5.3.
func (s *Store) ApproveOrgOrder(ctx context.Context, orderID, approverID uuid.UUID, notes, nextStatus string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE orders SET
			approval_status = 'approved',
			approved_by_user_id = $2,
			approved_at = NOW(),
			approval_notes = NULLIF($3,''),
			status = $4,
			updated_at = NOW()
		WHERE id = $1 AND approval_status = 'pending'`,
		orderID, approverID, notes, nextStatus)
	return err
}

// RejectOrgOrder cancels an awaiting_approval order with a reason. Phase 5.3.
func (s *Store) RejectOrgOrder(ctx context.Context, orderID, approverID uuid.UUID, reason string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE orders SET
			approval_status = 'rejected',
			approved_by_user_id = $2,
			approved_at = NOW(),
			approval_notes = NULLIF($3,''),
			status = 'cancelled',
			cancellation_reason = NULLIF($3,''),
			cancelled_by = 'approver',
			updated_at = NOW()
		WHERE id = $1 AND approval_status = 'pending'`,
		orderID, approverID, reason)
	return err
}

// ListOrgOrdersByApprovalStatus filters the org's orders by approval state.
// Used by the approver dashboard (status=pending). Phase 5.3.
func (s *Store) ListOrgOrdersByApprovalStatus(ctx context.Context, orgID uuid.UUID, approvalStatus string, limit, offset int) ([]*Order, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, customer_user_id, order_number, subtotal, discount_amount, shipping_charges,
		       tax_amount, coupon_code, coupon_discount, final_amount, currency_code,
		       payment_method, payment_status, payment_id, payment_gateway, delivery_address_id,
		       delivery_address_snapshot, gift_message, status, cancellation_reason, cancelled_by,
		       idempotency_key, created_at, updated_at,
		       organization_id, po_number, cost_center, billing_address_snapshot, invoice_email,
		       approval_status, approved_by_user_id, approved_at, approval_notes,
		       credit_terms_days, payment_due_date
		FROM orders
		WHERE organization_id = $1 AND approval_status = $2
		ORDER BY created_at DESC
		LIMIT $3 OFFSET $4`, orgID, approvalStatus, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOrderRows(rows)
}

// ListOrgOrders returns all orders for an org, optionally filtered by status.
// Phase 5.5 procurement dashboard.
func (s *Store) ListOrgOrders(ctx context.Context, orgID uuid.UUID, status string, limit, offset int) ([]*Order, error) {
	var rows pgx.Rows
	var err error
	if status == "" {
		rows, err = s.db.Query(ctx, `
			SELECT id, customer_user_id, order_number, subtotal, discount_amount, shipping_charges,
			       tax_amount, coupon_code, coupon_discount, final_amount, currency_code,
			       payment_method, payment_status, payment_id, payment_gateway, delivery_address_id,
			       delivery_address_snapshot, gift_message, status, cancellation_reason, cancelled_by,
			       idempotency_key, created_at, updated_at,
			       organization_id, po_number, cost_center, billing_address_snapshot, invoice_email,
			       approval_status, approved_by_user_id, approved_at, approval_notes,
			       credit_terms_days, payment_due_date
			FROM orders WHERE organization_id = $1
			ORDER BY created_at DESC LIMIT $2 OFFSET $3`, orgID, limit, offset)
	} else {
		rows, err = s.db.Query(ctx, `
			SELECT id, customer_user_id, order_number, subtotal, discount_amount, shipping_charges,
			       tax_amount, coupon_code, coupon_discount, final_amount, currency_code,
			       payment_method, payment_status, payment_id, payment_gateway, delivery_address_id,
			       delivery_address_snapshot, gift_message, status, cancellation_reason, cancelled_by,
			       idempotency_key, created_at, updated_at,
			       organization_id, po_number, cost_center, billing_address_snapshot, invoice_email,
			       approval_status, approved_by_user_id, approved_at, approval_notes,
			       credit_terms_days, payment_due_date
			FROM orders WHERE organization_id = $1 AND status = $2
			ORDER BY created_at DESC LIMIT $3 OFFSET $4`, orgID, status, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOrderRows(rows)
}

// scanOrderRows materialises the full Order DTO (including B2B columns)
// from a pgx.Rows. Shared by ListOrgOrders + ListOrgOrdersByApprovalStatus.
func scanOrderRows(rows pgx.Rows) ([]*Order, error) {
	var out []*Order
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.CustomerUserID, &o.OrderNumber, &o.Subtotal, &o.DiscountAmount,
			&o.ShippingCharges, &o.TaxAmount, &o.CouponCode, &o.CouponDiscount, &o.FinalAmount,
			&o.CurrencyCode, &o.PaymentMethod, &o.PaymentStatus, &o.PaymentID, &o.PaymentGateway,
			&o.DeliveryAddressID, &o.DeliveryAddressSnapshot, &o.GiftMessage, &o.Status,
			&o.CancellationReason, &o.CancelledBy, &o.IdempotencyKey, &o.CreatedAt, &o.UpdatedAt,
			&o.OrganizationID, &o.PONumber, &o.CostCenter, &o.BillingAddressSnapshot, &o.InvoiceEmail,
			&o.ApprovalStatus, &o.ApprovedByUserID, &o.ApprovedAt, &o.ApprovalNotes,
			&o.CreditTermsDays, &o.PaymentDueDate); err != nil {
			return nil, err
		}
		out = append(out, &o)
	}
	return out, rows.Err()
}

// AcceptInvite marks the invite as accepted and upserts the membership row
// in one tx so a stale token can't be replayed against a different user.
func (s *Store) AcceptInvite(ctx context.Context, token string, userID uuid.UUID) (*OrganizationInvite, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	inv := &OrganizationInvite{}
	if err := tx.QueryRow(ctx, `
		UPDATE organization_invites SET accepted_at = NOW()
		WHERE token = $1 AND accepted_at IS NULL AND expires_at > NOW()
		RETURNING id, organization_id, email, role, token, invited_by, expires_at, accepted_at, created_at`, token,
	).Scan(&inv.ID, &inv.OrganizationID, &inv.Email, &inv.Role, &inv.Token,
		&inv.InvitedBy, &inv.ExpiresAt, &inv.AcceptedAt, &inv.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errors.New("invite expired or already accepted")
		}
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_members
			(organization_id, user_id, role, status, invited_email, joined_at)
		VALUES ($1, $2, $3, 'active', $4, NOW())
		ON CONFLICT (organization_id, user_id) DO UPDATE
			SET role = EXCLUDED.role,
			    status = 'active',
			    joined_at = COALESCE(organization_members.joined_at, NOW())`,
		inv.OrganizationID, userID, inv.Role, inv.Email,
	); err != nil {
		return nil, err
	}
	return inv, tx.Commit(ctx)
}
