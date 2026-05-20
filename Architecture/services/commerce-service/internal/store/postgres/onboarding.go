package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ─── Seller onboarding store methods ────────────────────────────

// StartSellerOnboarding creates a draft seller record linked to a business page.
func (s *Store) StartSellerOnboarding(ctx context.Context, sel *Seller) error {
	sel.ID = uuid.New()
	sel.CreatedAt = time.Now()
	sel.UpdatedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO sellers (id, user_id, business_page_id, seller_type, business_type, store_name,
		  brand_name, owner_name, slug, description, tagline, email, phone,
		  state, city, postal_code, status, onboarding_step, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,'draft',1,$17,$18)`,
		sel.ID, sel.UserID, sel.BusinessPageID, sel.SellerType, sel.BusinessType, sel.StoreName,
		sel.BrandName, sel.OwnerName, sel.Slug, sel.Description, sel.Tagline, sel.Email, sel.Phone,
		sel.State, sel.City, sel.PostalCode, sel.CreatedAt, sel.UpdatedAt,
	)
	return err
}

// GetSellerOnboardingStatus returns full seller row including new onboarding fields.
func (s *Store) GetSellerOnboardingStatus(ctx context.Context, userID uuid.UUID) (*Seller, error) {
	var sel Seller
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, business_page_id, seller_type, business_type, store_name,
		  brand_name, owner_name, slug, description, tagline, email, phone, gst_number,
		  state, city, postal_code, status, onboarding_step,
		  submitted_at, approved_at, rejected_at, rejection_reason, changes_requested,
		  verification_status, store_status, created_at, updated_at
		FROM sellers WHERE user_id=$1`, userID).Scan(
		&sel.ID, &sel.UserID, &sel.BusinessPageID, &sel.SellerType, &sel.BusinessType, &sel.StoreName,
		&sel.BrandName, &sel.OwnerName, &sel.Slug, &sel.Description, &sel.Tagline, &sel.Email, &sel.Phone,
		&sel.GSTNumber, &sel.State, &sel.City, &sel.PostalCode, &sel.Status, &sel.OnboardingStep,
		&sel.SubmittedAt, &sel.ApprovedAt, &sel.RejectedAt, &sel.RejectionReason, &sel.ChangesRequested,
		&sel.VerificationStatus, &sel.StoreStatus, &sel.CreatedAt, &sel.UpdatedAt,
	)
	return &sel, err
}

// SaveOnboardingBasic saves step 3 — basic business info.
func (s *Store) SaveOnboardingBasic(ctx context.Context, userID uuid.UUID, in OnboardingBasicInput) error {
	_, err := s.db.Exec(ctx, `
		UPDATE sellers SET
		  store_name=$2, owner_name=$3, business_type=$4, seller_type=$5,
		  email=$6, phone=$7, state=$8, city=$9, postal_code=$10,
		  description=$11, onboarding_step=GREATEST(onboarding_step,3), updated_at=NOW()
		WHERE user_id=$1`,
		userID, in.StoreName, in.OwnerName, in.BusinessType, in.SellerType,
		in.Email, in.Phone, in.State, in.City, in.PostalCode, in.Description,
	)
	return err
}

// SaveOnboardingStorefront saves step 4 — storefront identity.
func (s *Store) SaveOnboardingStorefront(ctx context.Context, userID uuid.UUID, in OnboardingStorefrontInput) error {
	_, err := s.db.Exec(ctx, `
		UPDATE sellers SET
		  brand_name=$2, logo_media_id=$3, banner_media_id=$4, tagline=$5,
		  support_phone=$6, support_email=$7, social_links_json=$8,
		  onboarding_step=GREATEST(onboarding_step,4), updated_at=NOW()
		WHERE user_id=$1`,
		userID, in.BrandName, in.LogoMediaID, in.BannerMediaID, in.Tagline,
		in.SupportPhone, in.SupportEmail, in.SocialLinksJSON,
	)
	return err
}

// SaveOnboardingCompliance saves step 5 — KYC documents (upsert each doc).
func (s *Store) SaveOnboardingCompliance(ctx context.Context, sellerID uuid.UUID, docs []SellerDocument) error {
	for _, d := range docs {
		d.ID = uuid.New()
		d.SellerID = sellerID
		d.UploadedAt = time.Now()
		_, err := s.db.Exec(ctx, `
			INSERT INTO seller_documents (id, seller_id, document_type, document_number, media_id, verification_status, uploaded_at)
			VALUES ($1,$2,$3,$4,$5,'pending',$6)
			ON CONFLICT (seller_id, document_type) DO UPDATE SET
			  document_number=EXCLUDED.document_number, media_id=EXCLUDED.media_id,
			  verification_status='pending', uploaded_at=EXCLUDED.uploaded_at`,
			d.ID, d.SellerID, d.DocumentType, d.DocumentNumber, d.MediaID, d.UploadedAt,
		)
		if err != nil {
			return err
		}
	}
	_, err := s.db.Exec(ctx,
		`UPDATE sellers SET onboarding_step=GREATEST(onboarding_step,5), updated_at=NOW() WHERE id=$1`, sellerID)
	return err
}

// SaveOnboardingFulfillment saves step 6 — delivery / fulfillment settings.
func (s *Store) SaveOnboardingFulfillment(ctx context.Context, sellerID uuid.UUID, in OnboardingFulfillmentInput) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO seller_fulfillment_settings
		  (id, seller_id, delivery_modes, cod_enabled, dispatch_sla_hours, return_supported, return_window_days, updated_at)
		VALUES (gen_random_uuid(),$1,$2,$3,$4,$5,$6,NOW())
		ON CONFLICT (seller_id) DO UPDATE SET
		  delivery_modes=EXCLUDED.delivery_modes, cod_enabled=EXCLUDED.cod_enabled,
		  dispatch_sla_hours=EXCLUDED.dispatch_sla_hours, return_supported=EXCLUDED.return_supported,
		  return_window_days=EXCLUDED.return_window_days, updated_at=NOW()`,
		sellerID, in.DeliveryModes, in.CODEnabled, in.DispatchSLAHours,
		in.ReturnSupported, in.ReturnWindowDays,
	)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx,
		`UPDATE sellers SET onboarding_step=GREATEST(onboarding_step,6), updated_at=NOW() WHERE id=$1`, sellerID)
	return err
}

// SaveOnboardingPayout saves step 7 — bank/payout details.
func (s *Store) SaveOnboardingPayout(ctx context.Context, sellerID uuid.UUID, in OnboardingPayoutInput) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO seller_payout_accounts
		  (id, seller_id, account_holder_name, bank_name, account_number, ifsc_code, upi_id, is_primary, created_at, updated_at)
		VALUES (gen_random_uuid(),$1,$2,$3,$4,$5,$6,TRUE,NOW(),NOW())
		ON CONFLICT (seller_id) WHERE is_primary=TRUE DO UPDATE SET
		  account_holder_name=EXCLUDED.account_holder_name, bank_name=EXCLUDED.bank_name,
		  account_number=EXCLUDED.account_number, ifsc_code=EXCLUDED.ifsc_code,
		  upi_id=EXCLUDED.upi_id, updated_at=NOW()`,
		sellerID, in.AccountHolderName, in.BankName, in.AccountNumber, in.IFSCCode, in.UPIID,
	)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx,
		`UPDATE sellers SET onboarding_step=GREATEST(onboarding_step,7), updated_at=NOW() WHERE id=$1`, sellerID)
	return err
}

// SubmitSellerApplication sets status=submitted and records timestamp.
func (s *Store) SubmitSellerApplication(ctx context.Context, userID uuid.UUID) error {
	now := time.Now()
	tag, err := s.db.Exec(ctx, `
		UPDATE sellers SET status='submitted', submitted_at=$2, onboarding_step=9, updated_at=$2
		WHERE user_id=$1 AND status='draft'`, userID, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAlreadySubmitted
	}
	return nil
}

// ─── Internal admin methods ──────────────────────────────────────

// ListSellerQueue returns sellers pending admin review.
func (s *Store) ListSellerQueue(ctx context.Context, limit, offset int) ([]*Seller, int, error) {
	var total int
	_ = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM sellers WHERE status IN ('submitted','under_review','changes_required')`).Scan(&total)

	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, business_page_id, store_name, email, business_type, seller_type,
		  status, onboarding_step, submitted_at, created_at, updated_at
		FROM sellers WHERE status IN ('submitted','under_review','changes_required')
		ORDER BY submitted_at ASC NULLS LAST
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var sellers []*Seller
	for rows.Next() {
		var sel Seller
		if err := rows.Scan(&sel.ID, &sel.UserID, &sel.BusinessPageID, &sel.StoreName, &sel.Email,
			&sel.BusinessType, &sel.SellerType, &sel.Status, &sel.OnboardingStep,
			&sel.SubmittedAt, &sel.CreatedAt, &sel.UpdatedAt); err != nil {
			return nil, 0, err
		}
		sellers = append(sellers, &sel)
	}
	return sellers, total, rows.Err()
}

// ApproveSellerByAdmin sets status=approved and logs the action.
func (s *Store) ApproveSellerByAdmin(ctx context.Context, sellerID, actorID uuid.UUID, notes string) error {
	now := time.Now()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE sellers SET status='approved', approved_at=$2, store_status='active', updated_at=$2 WHERE id=$1`,
		sellerID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO seller_onboarding_reviews (id,seller_id,action,notes,actor_user_id,created_at)
		 VALUES (gen_random_uuid(),$1,'approve',$2,$3,$4)`,
		sellerID, notes, actorID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RejectSellerByAdmin sets status=rejected and logs the action.
func (s *Store) RejectSellerByAdmin(ctx context.Context, sellerID, actorID uuid.UUID, reason, notes string) error {
	now := time.Now()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE sellers SET status='rejected', rejected_at=$2, rejection_reason=$3, updated_at=$2 WHERE id=$1`,
		sellerID, now, reason); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO seller_onboarding_reviews (id,seller_id,action,notes,actor_user_id,created_at)
		 VALUES (gen_random_uuid(),$1,'reject',$2,$3,$4)`,
		sellerID, notes, actorID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RequestSellerChanges sets status=changes_required and logs the action.
func (s *Store) RequestSellerChanges(ctx context.Context, sellerID, actorID uuid.UUID, changes, notes string) error {
	now := time.Now()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE sellers SET status='changes_required', changes_requested=$2, updated_at=$3 WHERE id=$1`,
		sellerID, changes, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO seller_onboarding_reviews (id,seller_id,action,notes,actor_user_id,created_at)
		 VALUES (gen_random_uuid(),$1,'request_changes',$2,$3,$4)`,
		sellerID, notes, actorID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SuspendSellerByAdmin sets status=suspended.
func (s *Store) SuspendSellerByAdmin(ctx context.Context, sellerID, actorID uuid.UUID, reason, notes string) error {
	now := time.Now()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE sellers SET status='suspended', suspension_reason=$2, updated_at=$3 WHERE id=$1`,
		sellerID, reason, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO seller_onboarding_reviews (id,seller_id,action,notes,actor_user_id,created_at)
		 VALUES (gen_random_uuid(),$1,'suspend',$2,$3,$4)`,
		sellerID, notes, actorID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ListProductQueue returns products pending moderation.
func (s *Store) ListProductQueue(ctx context.Context, limit, offset int) ([]*Product, int, error) {
	var total int
	_ = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM products WHERE approval_status IN ('submitted','under_review')`).Scan(&total)

	rows, err := s.db.Query(ctx, `
		SELECT id, seller_id, title, slug, approval_status, created_at, updated_at
		FROM products WHERE approval_status IN ('submitted','under_review')
		ORDER BY created_at ASC
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var products []*Product
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.SellerID, &p.Title, &p.Slug, &p.ApprovalStatus, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, 0, err
		}
		products = append(products, &p)
	}
	return products, total, rows.Err()
}

// ApproveProductByAdmin sets approval_status=live and logs.
func (s *Store) ApproveProductByAdmin(ctx context.Context, productID, actorID uuid.UUID, notes string) error {
	now := time.Now()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE products SET approval_status='live', status='active', published_at=$2, updated_at=$2 WHERE id=$1`,
		productID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO product_moderation_log (id,product_id,action,reason,actor_user_id,created_at)
		 VALUES (gen_random_uuid(),$1,'approve',$2,$3,$4)`,
		productID, notes, actorID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RequestProductChangesByAdmin parks a product at approval_status=
// changes_requested with the moderator's feedback in rejection_reason
// (overloaded as "feedback") so the seller-facing dashboard can surface
// it. Logged in product_moderation_log as 'request_changes'.
func (s *Store) RequestProductChangesByAdmin(ctx context.Context, productID, actorID uuid.UUID, message string) error {
	now := time.Now()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE products SET approval_status='changes_requested', rejection_reason=$2, updated_at=$3 WHERE id=$1`,
		productID, message, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO product_moderation_log (id,product_id,action,reason,actor_user_id,created_at)
		 VALUES (gen_random_uuid(),$1,'request_changes',$2,$3,$4)`,
		productID, message, actorID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RejectProductByAdmin sets approval_status=rejected and logs.
func (s *Store) RejectProductByAdmin(ctx context.Context, productID, actorID uuid.UUID, reason string) error {
	now := time.Now()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE products SET approval_status='rejected', rejection_reason=$2, updated_at=$3 WHERE id=$1`,
		productID, reason, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO product_moderation_log (id,product_id,action,reason,actor_user_id,created_at)
		 VALUES (gen_random_uuid(),$1,'reject',$2,$3,$4)`,
		productID, reason, actorID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ─── Dashboard ───────────────────────────────────────────────────

type DashboardStats struct {
	TotalProducts   int     `json:"total_products"`
	LiveProducts    int     `json:"live_products"`
	DraftProducts   int     `json:"draft_products"`
	PendingProducts int     `json:"pending_products"`
	LowStockItems   int     `json:"low_stock_items"`
	OrdersToday     int     `json:"orders_today"`
	RevenueTotal    float64 `json:"revenue_total"`
	SellerStatus    string  `json:"seller_status"`
}

func (s *Store) GetDashboardStats(ctx context.Context, sellerID uuid.UUID) (*DashboardStats, error) {
	stats := &DashboardStats{}

	_ = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM products WHERE seller_id=$1`, sellerID).Scan(&stats.TotalProducts)
	_ = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM products WHERE seller_id=$1 AND approval_status='live'`, sellerID).Scan(&stats.LiveProducts)
	_ = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM products WHERE seller_id=$1 AND approval_status='draft'`, sellerID).Scan(&stats.DraftProducts)
	_ = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM products WHERE seller_id=$1 AND approval_status IN ('submitted','under_review')`, sellerID).Scan(&stats.PendingProducts)
	_ = s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM inventory_items i
		JOIN product_variants v ON v.id = i.variant_id
		JOIN products p ON p.id = v.product_id
		WHERE p.seller_id=$1 AND (i.total_qty - i.reserved_qty) <= i.low_stock_alert`, sellerID).Scan(&stats.LowStockItems)
	_ = s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM orders o
		JOIN order_items oi ON oi.order_id = o.id
		WHERE oi.seller_id=$1 AND o.created_at >= NOW()::date`, sellerID).Scan(&stats.OrdersToday)
	_ = s.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(oi.final_price),0) FROM order_items oi
		JOIN orders o ON o.id = oi.order_id
		WHERE oi.seller_id=$1 AND o.payment_status='paid'`, sellerID).Scan(&stats.RevenueTotal)
	_ = s.db.QueryRow(ctx, `SELECT status FROM sellers WHERE id=$1`, sellerID).Scan(&stats.SellerStatus)

	return stats, nil
}

// ─── Product submit ───────────────────────────────────────────────

// SubmitProductForReview sets approval_status=submitted (only if seller is approved).
func (s *Store) SubmitProductForReview(ctx context.Context, productID, sellerID uuid.UUID) error {
	// Check seller is approved
	var status string
	if err := s.db.QueryRow(ctx, `SELECT status FROM sellers WHERE id=$1`, sellerID).Scan(&status); err != nil {
		return err
	}
	if status != "approved" {
		return ErrSellerNotApproved
	}
	tag, err := s.db.Exec(ctx,
		`UPDATE products SET approval_status='submitted', updated_at=NOW() WHERE id=$1 AND seller_id=$2 AND approval_status='draft'`,
		productID, sellerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrProductNotDraft
	}
	return nil
}
