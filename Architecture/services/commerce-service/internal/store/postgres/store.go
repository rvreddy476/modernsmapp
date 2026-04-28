package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store { return &Store{db: db} }

// ─── Seller ──────────────────────────────────────────────────

func (s *Store) CreateSeller(ctx context.Context, sel *Seller) error {
	sel.ID = uuid.New()
	sel.CreatedAt = time.Now()
	sel.UpdatedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO sellers (id, user_id, seller_type, store_name, brand_name, slug, description,
		  email, phone, gst_number, state, city, postal_code, verification_status, store_status, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		sel.ID, sel.UserID, sel.SellerType, sel.StoreName, sel.BrandName, sel.Slug, sel.Description,
		sel.Email, sel.Phone, sel.GSTNumber, sel.State, sel.City, sel.PostalCode,
		sel.VerificationStatus, sel.StoreStatus, sel.CreatedAt, sel.UpdatedAt,
	)
	return err
}

func (s *Store) GetSellerByUserID(ctx context.Context, userID uuid.UUID) (*Seller, error) {
	var sel Seller
	err := s.db.QueryRow(ctx, `SELECT id,user_id,seller_type,store_name,brand_name,slug,description,
		logo_media_id,banner_media_id,email,phone,gst_number,pan_number,state,city,postal_code,
		verification_status,store_status,quality_score,performance_tier,avg_rating,review_count,
		follower_count,total_products,total_orders,created_at,updated_at
		FROM sellers WHERE user_id=$1`, userID).Scan(
		&sel.ID, &sel.UserID, &sel.SellerType, &sel.StoreName, &sel.BrandName, &sel.Slug, &sel.Description,
		&sel.LogoMediaID, &sel.BannerMediaID, &sel.Email, &sel.Phone, &sel.GSTNumber, &sel.PANNumber,
		&sel.State, &sel.City, &sel.PostalCode, &sel.VerificationStatus, &sel.StoreStatus,
		&sel.QualityScore, &sel.PerformanceTier, &sel.AvgRating, &sel.ReviewCount,
		&sel.FollowerCount, &sel.TotalProducts, &sel.TotalOrders, &sel.CreatedAt, &sel.UpdatedAt,
	)
	return &sel, err
}

func (s *Store) GetSellerByID(ctx context.Context, id uuid.UUID) (*Seller, error) {
	var sel Seller
	err := s.db.QueryRow(ctx, `SELECT id,user_id,seller_type,store_name,brand_name,slug,description,
		logo_media_id,banner_media_id,email,phone,gst_number,pan_number,state,city,postal_code,
		verification_status,store_status,quality_score,performance_tier,avg_rating,review_count,
		follower_count,total_products,total_orders,created_at,updated_at
		FROM sellers WHERE id=$1`, id).Scan(
		&sel.ID, &sel.UserID, &sel.SellerType, &sel.StoreName, &sel.BrandName, &sel.Slug, &sel.Description,
		&sel.LogoMediaID, &sel.BannerMediaID, &sel.Email, &sel.Phone, &sel.GSTNumber, &sel.PANNumber,
		&sel.State, &sel.City, &sel.PostalCode, &sel.VerificationStatus, &sel.StoreStatus,
		&sel.QualityScore, &sel.PerformanceTier, &sel.AvgRating, &sel.ReviewCount,
		&sel.FollowerCount, &sel.TotalProducts, &sel.TotalOrders, &sel.CreatedAt, &sel.UpdatedAt,
	)
	return &sel, err
}

func (s *Store) UpdateSellerStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx, `UPDATE sellers SET store_status=$2, updated_at=NOW() WHERE id=$1`, id, status)
	return err
}

// ─── Categories ──────────────────────────────────────────────

func (s *Store) ListCategories(ctx context.Context) ([]*ProductCategory, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id,parent_id,name,slug,description,display_order,is_active,is_featured,created_at
		FROM product_categories WHERE is_active=TRUE ORDER BY display_order, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cats []*ProductCategory
	for rows.Next() {
		var c ProductCategory
		if err := rows.Scan(&c.ID, &c.ParentID, &c.Name, &c.Slug, &c.Description,
			&c.DisplayOrder, &c.IsActive, &c.IsFeatured, &c.CreatedAt); err != nil {
			return nil, err
		}
		cats = append(cats, &c)
	}
	return cats, nil
}

// ─── Products ────────────────────────────────────────────────

func (s *Store) CreateProduct(ctx context.Context, p *Product) error {
	p.ID = uuid.New()
	p.CreatedAt = time.Now()
	p.UpdatedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO products (id,seller_id,category_id,tax_class_id,title,short_title,slug,description,
		  short_description,product_type,condition,sku_root,status,visibility,approval_status,
		  primary_image_media_id,weight_grams,country_of_origin,return_policy_type,return_policy_days,
		  hsn_code,meta_title,meta_description,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25)`,
		p.ID, p.SellerID, p.CategoryID, p.TaxClassID, p.Title, p.ShortTitle, p.Slug, p.Description,
		p.ShortDescription, p.ProductType, p.Condition, p.SKURoot, p.Status, p.Visibility, p.ApprovalStatus,
		p.PrimaryImageMediaID, p.WeightGrams, p.CountryOfOrigin, p.ReturnPolicyType, p.ReturnPolicyDays,
		p.HSNCode, p.MetaTitle, p.MetaDescription, p.CreatedAt, p.UpdatedAt,
	)
	return err
}

func (s *Store) GetProductByID(ctx context.Context, id uuid.UUID) (*Product, error) {
	var p Product
	err := s.db.QueryRow(ctx, `
		SELECT id,seller_id,category_id,brand_id,tax_class_id,title,short_title,slug,description,
		  short_description,product_type,condition,sku_root,status,visibility,approval_status,
		  rejection_reason,primary_image_media_id,weight_grams,country_of_origin,warranty_info,
		  return_policy_type,return_policy_days,hsn_code,meta_title,meta_description,
		  avg_rating,review_count,order_count,view_count,wishlist_count,is_featured,created_at,updated_at,published_at
		FROM products WHERE id=$1`, id).Scan(
		&p.ID, &p.SellerID, &p.CategoryID, &p.BrandID, &p.TaxClassID,
		&p.Title, &p.ShortTitle, &p.Slug, &p.Description, &p.ShortDescription,
		&p.ProductType, &p.Condition, &p.SKURoot, &p.Status, &p.Visibility,
		&p.ApprovalStatus, &p.RejectionReason, &p.PrimaryImageMediaID,
		&p.WeightGrams, &p.CountryOfOrigin, &p.WarrantyInfo,
		&p.ReturnPolicyType, &p.ReturnPolicyDays, &p.HSNCode,
		&p.MetaTitle, &p.MetaDescription, &p.AvgRating, &p.ReviewCount,
		&p.OrderCount, &p.ViewCount, &p.WishlistCount, &p.IsFeatured,
		&p.CreatedAt, &p.UpdatedAt, &p.PublishedAt,
	)
	return &p, err
}

func (s *Store) ListSellerProducts(ctx context.Context, sellerID uuid.UUID, status string, limit, offset int) ([]*Product, int, error) {
	where := "WHERE seller_id=$1"
	args := []any{sellerID}
	if status != "" {
		where += " AND status=$2"
		args = append(args, status)
	}
	var total int
	_ = s.db.QueryRow(ctx, "SELECT COUNT(*) FROM products "+where, args...).Scan(&total)

	args = append(args, limit, offset)
	rows, err := s.db.Query(ctx, `SELECT id,seller_id,category_id,title,slug,status,approval_status,
		avg_rating,review_count,order_count,view_count,created_at,updated_at FROM products `+
		where+fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var products []*Product
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.SellerID, &p.CategoryID, &p.Title, &p.Slug,
			&p.Status, &p.ApprovalStatus, &p.AvgRating, &p.ReviewCount, &p.OrderCount,
			&p.ViewCount, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, 0, err
		}
		products = append(products, &p)
	}
	return products, total, nil
}

// ListProducts returns paginated products for the customer-facing browse
// surface: active + approved only, optionally filtered by category and a
// title search. Newest first. Returns total count for pagination.
//
// status values per the products_status_check constraint: draft, active,
// paused, archived. approval_status: draft, submitted, under_review,
// approved, rejected, live, hidden, archived. We surface active+approved.
func (s *Store) ListProducts(ctx context.Context, categoryID *uuid.UUID, query string, limit, offset int) ([]*Product, int, error) {
	conds := []string{"status = 'active'", "approval_status = 'approved'"}
	args := []any{}
	idx := 1
	if categoryID != nil {
		conds = append(conds, fmt.Sprintf("category_id = $%d", idx))
		args = append(args, *categoryID)
		idx++
	}
	if q := strings.TrimSpace(query); q != "" {
		conds = append(conds, fmt.Sprintf("title ILIKE $%d", idx))
		args = append(args, "%"+q+"%")
		idx++
	}
	where := "WHERE " + strings.Join(conds, " AND ")

	var total int
	if err := s.db.QueryRow(ctx, "SELECT COUNT(*) FROM products "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	rows, err := s.db.Query(ctx, `SELECT id,seller_id,category_id,title,slug,status,approval_status,
		avg_rating,review_count,order_count,view_count,created_at,updated_at FROM products `+
		where+fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", idx, idx+1), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var products []*Product
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.SellerID, &p.CategoryID, &p.Title, &p.Slug,
			&p.Status, &p.ApprovalStatus, &p.AvgRating, &p.ReviewCount, &p.OrderCount,
			&p.ViewCount, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, 0, err
		}
		products = append(products, &p)
	}
	return products, total, rows.Err()
}

func (s *Store) UpdateProduct(ctx context.Context, id uuid.UUID, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	sets := make([]string, 0, len(updates))
	args := make([]any, 0, len(updates)+1)
	i := 1
	for k, v := range updates {
		sets = append(sets, fmt.Sprintf("%s=$%d", k, i))
		args = append(args, v)
		i++
	}
	args = append(args, id)
	_, err := s.db.Exec(ctx,
		"UPDATE products SET "+strings.Join(sets, ",")+",updated_at=NOW() WHERE id=$"+fmt.Sprint(i),
		args...,
	)
	return err
}

func (s *Store) IncrProductViewCount(ctx context.Context, id uuid.UUID) {
	_, _ = s.db.Exec(ctx, "UPDATE products SET view_count=view_count+1 WHERE id=$1", id)
}

// ─── Product Variants ────────────────────────────────────────

func (s *Store) CreateVariant(ctx context.Context, v *ProductVariant) error {
	v.ID = uuid.New()
	v.CreatedAt = time.Now()
	v.UpdatedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO product_variants (id,product_id,sku,barcode,option_1_name,option_1_value,
		  option_2_name,option_2_value,option_3_name,option_3_value,mrp,selling_price,cost_price,
		  currency_code,status,image_media_id,weight_grams,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		v.ID, v.ProductID, v.SKU, v.Barcode, v.Option1Name, v.Option1Value,
		v.Option2Name, v.Option2Value, v.Option3Name, v.Option3Value,
		v.MRP, v.SellingPrice, v.CostPrice, v.CurrencyCode, v.Status,
		v.ImageMediaID, v.WeightGrams, v.CreatedAt, v.UpdatedAt,
	)
	return err
}

func (s *Store) GetVariantByID(ctx context.Context, id uuid.UUID) (*ProductVariant, error) {
	var v ProductVariant
	err := s.db.QueryRow(ctx, `SELECT id,product_id,sku,barcode,option_1_name,option_1_value,
		option_2_name,option_2_value,option_3_name,option_3_value,mrp,selling_price,cost_price,
		currency_code,status,image_media_id,weight_grams,created_at,updated_at
		FROM product_variants WHERE id=$1`, id).Scan(
		&v.ID, &v.ProductID, &v.SKU, &v.Barcode, &v.Option1Name, &v.Option1Value,
		&v.Option2Name, &v.Option2Value, &v.Option3Name, &v.Option3Value,
		&v.MRP, &v.SellingPrice, &v.CostPrice, &v.CurrencyCode, &v.Status,
		&v.ImageMediaID, &v.WeightGrams, &v.CreatedAt, &v.UpdatedAt,
	)
	return &v, err
}

func (s *Store) GetVariantsByProduct(ctx context.Context, productID uuid.UUID) ([]*ProductVariant, error) {
	rows, err := s.db.Query(ctx, `SELECT id,product_id,sku,option_1_name,option_1_value,
		option_2_name,option_2_value,mrp,selling_price,currency_code,status,image_media_id,created_at
		FROM product_variants WHERE product_id=$1 AND status='active' ORDER BY created_at`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var variants []*ProductVariant
	for rows.Next() {
		var v ProductVariant
		if err := rows.Scan(&v.ID, &v.ProductID, &v.SKU, &v.Option1Name, &v.Option1Value,
			&v.Option2Name, &v.Option2Value, &v.MRP, &v.SellingPrice, &v.CurrencyCode,
			&v.Status, &v.ImageMediaID, &v.CreatedAt); err != nil {
			return nil, err
		}
		variants = append(variants, &v)
	}
	return variants, nil
}

// ─── Inventory ───────────────────────────────────────────────

func (s *Store) UpsertInventory(ctx context.Context, variantID, sellerID uuid.UUID, totalQty int) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO inventory_items (id,variant_id,seller_id,total_qty,updated_at)
		VALUES (gen_random_uuid(),$1,$2,$3,NOW())
		ON CONFLICT (variant_id) DO UPDATE SET total_qty=$3, updated_at=NOW()`,
		variantID, sellerID, totalQty,
	)
	return err
}

func (s *Store) GetInventory(ctx context.Context, variantID uuid.UUID) (*InventoryItem, error) {
	var inv InventoryItem
	err := s.db.QueryRow(ctx, `SELECT id,variant_id,seller_id,total_qty,reserved_qty,damaged_qty,
		returned_qty,safety_stock,low_stock_alert,updated_at
		FROM inventory_items WHERE variant_id=$1`, variantID).Scan(
		&inv.ID, &inv.VariantID, &inv.SellerID, &inv.TotalQty, &inv.ReservedQty,
		&inv.DamagedQty, &inv.ReturnedQty, &inv.SafetyStock, &inv.LowStockAlert, &inv.UpdatedAt,
	)
	return &inv, err
}

// ReserveStock atomically reserves qty for a cart/order. Returns error if insufficient.
func (s *Store) ReserveStock(ctx context.Context, variantID, userID uuid.UUID, qty int, orderID *uuid.UUID, resType string, ttl time.Duration) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Lock row and check availability
	var avail int
	err = tx.QueryRow(ctx, `
		SELECT total_qty - reserved_qty FROM inventory_items WHERE variant_id=$1 FOR UPDATE`,
		variantID).Scan(&avail)
	if err != nil {
		return fmt.Errorf("inventory not found: %w", err)
	}
	if avail < qty {
		return fmt.Errorf("insufficient stock: available=%d requested=%d", avail, qty)
	}

	// Increment reserved_qty
	if _, err = tx.Exec(ctx, `UPDATE inventory_items SET reserved_qty=reserved_qty+$2,updated_at=NOW() WHERE variant_id=$1`, variantID, qty); err != nil {
		return err
	}

	// Create reservation record
	if _, err = tx.Exec(ctx, `
		INSERT INTO inventory_reservations (id,variant_id,order_id,user_id,quantity,type,expires_at)
		VALUES (gen_random_uuid(),$1,$2,$3,$4,$5,$6)`,
		variantID, orderID, userID, qty, resType, time.Now().Add(ttl)); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// ReleaseReservation releases a previously reserved qty.
func (s *Store) ReleaseReservation(ctx context.Context, variantID, userID uuid.UUID, qty int) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err = tx.Exec(ctx, `
		UPDATE inventory_items SET reserved_qty=GREATEST(0,reserved_qty-$2),updated_at=NOW()
		WHERE variant_id=$1`, variantID, qty); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		DELETE FROM inventory_reservations
		WHERE variant_id=$1 AND user_id=$2 AND order_id IS NULL LIMIT 1`,
		variantID, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeductStock commits stock after successful payment (releases reservation, deducts from total).
func (s *Store) DeductStock(ctx context.Context, variantID uuid.UUID, qty int, orderID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err = tx.Exec(ctx, `
		UPDATE inventory_items
		SET total_qty=GREATEST(0,total_qty-$2),
		    reserved_qty=GREATEST(0,reserved_qty-$2),
		    updated_at=NOW()
		WHERE variant_id=$1`, variantID, qty); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		DELETE FROM inventory_reservations WHERE variant_id=$1 AND order_id=$2`,
		variantID, orderID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ─── Cart ────────────────────────────────────────────────────

func (s *Store) GetOrCreateCart(ctx context.Context, userID uuid.UUID) (*Cart, error) {
	cart := &Cart{}
	err := s.db.QueryRow(ctx, `SELECT id,user_id,expires_at,updated_at FROM carts WHERE user_id=$1`, userID).
		Scan(&cart.ID, &cart.UserID, &cart.ExpiresAt, &cart.UpdatedAt)
	if err != nil {
		// Create new cart
		cart.ID = uuid.New()
		cart.UserID = userID
		cart.UpdatedAt = time.Now()
		_, err = s.db.Exec(ctx, `INSERT INTO carts (id,user_id,updated_at) VALUES ($1,$2,$3)`,
			cart.ID, cart.UserID, cart.UpdatedAt)
		return cart, err
	}
	return cart, nil
}

func (s *Store) UpsertCartItem(ctx context.Context, cartID, variantID, productID uuid.UUID, qty int, price float64) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO cart_items (id,cart_id,variant_id,product_id,quantity,price_snapshot,added_at)
		VALUES (gen_random_uuid(),$1,$2,$3,$4,$5,NOW())
		ON CONFLICT (cart_id,variant_id) DO UPDATE SET quantity=$4,price_snapshot=$5`,
		cartID, variantID, productID, qty, price,
	)
	return err
}

func (s *Store) RemoveCartItem(ctx context.Context, cartID, variantID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM cart_items WHERE cart_id=$1 AND variant_id=$2`, cartID, variantID)
	return err
}

func (s *Store) GetCartItems(ctx context.Context, cartID uuid.UUID) ([]*CartItem, error) {
	rows, err := s.db.Query(ctx, `SELECT id,cart_id,variant_id,product_id,quantity,price_snapshot,added_at
		FROM cart_items WHERE cart_id=$1 ORDER BY added_at`, cartID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []*CartItem
	for rows.Next() {
		var ci CartItem
		if err := rows.Scan(&ci.ID, &ci.CartID, &ci.VariantID, &ci.ProductID, &ci.Quantity, &ci.PriceSnapshot, &ci.AddedAt); err != nil {
			return nil, err
		}
		items = append(items, &ci)
	}
	return items, nil
}

func (s *Store) ClearCart(ctx context.Context, cartID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM cart_items WHERE cart_id=$1`, cartID)
	return err
}

// ─── Orders ──────────────────────────────────────────────────

func (s *Store) CreateOrder(ctx context.Context, o *Order, items []*OrderItem) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	o.ID = uuid.New()
	o.CreatedAt = time.Now()
	o.UpdatedAt = time.Now()

	// Generate human-readable order number
	var orderNum string
	if err = tx.QueryRow(ctx, `SELECT generate_order_number()`).Scan(&orderNum); err != nil {
		return fmt.Errorf("generate order number: %w", err)
	}
	o.OrderNumber = orderNum

	addrSnapshot, _ := json.Marshal(o.DeliveryAddressSnapshot)

	if _, err = tx.Exec(ctx, `
		INSERT INTO orders (id,customer_user_id,order_number,subtotal,discount_amount,shipping_charges,
		  tax_amount,coupon_code,coupon_discount,final_amount,currency_code,payment_method,payment_status,
		  delivery_address_id,delivery_address_snapshot,gift_message,status,idempotency_key,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`,
		o.ID, o.CustomerUserID, o.OrderNumber, o.Subtotal, o.DiscountAmount, o.ShippingCharges,
		o.TaxAmount, o.CouponCode, o.CouponDiscount, o.FinalAmount, o.CurrencyCode,
		o.PaymentMethod, o.PaymentStatus, o.DeliveryAddressID, addrSnapshot, o.GiftMessage,
		o.Status, o.IdempotencyKey, o.CreatedAt, o.UpdatedAt,
	); err != nil {
		return fmt.Errorf("insert order: %w", err)
	}

	for _, item := range items {
		item.ID = uuid.New()
		item.OrderID = o.ID
		item.CreatedAt = time.Now()
		varDetails, _ := json.Marshal(item.VariantDetails)
		if _, err = tx.Exec(ctx, `
			INSERT INTO order_items (id,order_id,product_id,variant_id,seller_id,product_title,
			  variant_details,sku,quantity,unit_mrp,unit_price,discount_amount,tax_amount,
			  final_price,status,return_eligible_until,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
			item.ID, item.OrderID, item.ProductID, item.VariantID, item.SellerID,
			item.ProductTitle, varDetails, item.SKU, item.Quantity,
			item.UnitMRP, item.UnitPrice, item.DiscountAmount, item.TaxAmount,
			item.FinalPrice, item.Status, item.ReturnEligibleUntil, item.CreatedAt,
		); err != nil {
			return fmt.Errorf("insert order item: %w", err)
		}
	}

	// Record initial status
	if _, err = tx.Exec(ctx, `
		INSERT INTO order_status_history (id,order_id,to_status,actor_type,created_at)
		VALUES (gen_random_uuid(),$1,$2,'system',NOW())`, o.ID, o.Status,
	); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *Store) GetOrderByID(ctx context.Context, id uuid.UUID) (*Order, error) {
	var o Order
	err := s.db.QueryRow(ctx, `SELECT id,customer_user_id,order_number,subtotal,discount_amount,
		shipping_charges,tax_amount,coupon_code,coupon_discount,final_amount,currency_code,
		payment_method,payment_status,payment_id,payment_gateway,delivery_address_id,
		delivery_address_snapshot,gift_message,status,cancellation_reason,cancelled_by,
		idempotency_key,created_at,updated_at FROM orders WHERE id=$1`, id).Scan(
		&o.ID, &o.CustomerUserID, &o.OrderNumber, &o.Subtotal, &o.DiscountAmount,
		&o.ShippingCharges, &o.TaxAmount, &o.CouponCode, &o.CouponDiscount, &o.FinalAmount,
		&o.CurrencyCode, &o.PaymentMethod, &o.PaymentStatus, &o.PaymentID, &o.PaymentGateway,
		&o.DeliveryAddressID, &o.DeliveryAddressSnapshot, &o.GiftMessage, &o.Status,
		&o.CancellationReason, &o.CancelledBy, &o.IdempotencyKey, &o.CreatedAt, &o.UpdatedAt,
	)
	return &o, err
}

func (s *Store) GetOrdersByCustomer(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*Order, int, error) {
	var total int
	_ = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM orders WHERE customer_user_id=$1`, userID).Scan(&total)

	rows, err := s.db.Query(ctx, `SELECT id,customer_user_id,order_number,final_amount,currency_code,
		payment_status,status,created_at,updated_at FROM orders
		WHERE customer_user_id=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var orders []*Order
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.CustomerUserID, &o.OrderNumber, &o.FinalAmount,
			&o.CurrencyCode, &o.PaymentStatus, &o.Status, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, 0, err
		}
		orders = append(orders, &o)
	}
	return orders, total, nil
}

// GetOrdersBySeller returns orders containing at least one item sold by the given seller.
func (s *Store) GetOrdersBySeller(ctx context.Context, sellerID uuid.UUID, limit, offset int) ([]*Order, int, error) {
	var total int
	_ = s.db.QueryRow(ctx, `
		SELECT COUNT(DISTINCT o.id) FROM orders o
		JOIN order_items oi ON oi.order_id = o.id
		WHERE oi.seller_id = $1
	`, sellerID).Scan(&total)

	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT o.id, o.customer_user_id, o.order_number, o.final_amount, o.currency_code,
			o.payment_status, o.status, o.created_at, o.updated_at
		FROM orders o
		JOIN order_items oi ON oi.order_id = o.id
		WHERE oi.seller_id = $1
		ORDER BY o.created_at DESC
		LIMIT $2 OFFSET $3
	`, sellerID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var orders []*Order
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.CustomerUserID, &o.OrderNumber, &o.FinalAmount,
			&o.CurrencyCode, &o.PaymentStatus, &o.Status, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, 0, err
		}
		orders = append(orders, &o)
	}
	return orders, total, nil
}

func (s *Store) UpdateOrderStatus(ctx context.Context, orderID uuid.UUID, toStatus string, actorID *uuid.UUID, actorType, notes string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var fromStatus string
	if err = tx.QueryRow(ctx, `UPDATE orders SET status=$2,updated_at=NOW() WHERE id=$1 RETURNING (SELECT status FROM orders WHERE id=$1)`, orderID, toStatus).Scan(&fromStatus); err != nil {
		// Fallback: just update
		if _, err2 := tx.Exec(ctx, `UPDATE orders SET status=$2,updated_at=NOW() WHERE id=$1`, orderID, toStatus); err2 != nil {
			return err2
		}
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO order_status_history (id,order_id,from_status,to_status,changed_by,actor_type,notes,created_at)
		VALUES (gen_random_uuid(),$1,$2,$3,$4,$5,$6,NOW())`,
		orderID, fromStatus, toStatus, actorID, actorType, notes,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) GetOrderItems(ctx context.Context, orderID uuid.UUID) ([]*OrderItem, error) {
	rows, err := s.db.Query(ctx, `SELECT id,order_id,product_id,variant_id,seller_id,product_title,
		variant_details,sku,quantity,unit_mrp,unit_price,discount_amount,tax_amount,final_price,
		status,shipment_id,tracking_number,return_eligible_until,delivered_at,created_at
		FROM order_items WHERE order_id=$1`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []*OrderItem
	for rows.Next() {
		var item OrderItem
		if err := rows.Scan(&item.ID, &item.OrderID, &item.ProductID, &item.VariantID, &item.SellerID,
			&item.ProductTitle, &item.VariantDetails, &item.SKU, &item.Quantity,
			&item.UnitMRP, &item.UnitPrice, &item.DiscountAmount, &item.TaxAmount, &item.FinalPrice,
			&item.Status, &item.ShipmentID, &item.TrackingNumber, &item.ReturnEligibleUntil,
			&item.DeliveredAt, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, &item)
	}
	return items, nil
}

func (s *Store) UpdatePaymentStatus(ctx context.Context, orderID uuid.UUID, paymentStatus, paymentID, gateway string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE orders SET payment_status=$2, payment_id=$3, payment_gateway=$4, updated_at=NOW()
		WHERE id=$1`, orderID, paymentStatus, paymentID, gateway)
	return err
}

// ─── Customer Addresses ──────────────────────────────────────

func (s *Store) CreateAddress(ctx context.Context, addr *CustomerAddress) error {
	addr.ID = uuid.New()
	addr.CreatedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO customer_addresses (id,user_id,label,contact_name,phone,address_line_1,
		  address_line_2,landmark,city,state,country,postal_code,address_type,is_default,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		addr.ID, addr.UserID, addr.Label, addr.ContactName, addr.Phone, addr.AddressLine1,
		addr.AddressLine2, addr.Landmark, addr.City, addr.State, addr.Country, addr.PostalCode,
		addr.AddressType, addr.IsDefault, addr.CreatedAt,
	)
	return err
}

// TaxClass holds GST percentages for a given class (e.g. "GST 18%").
type TaxClass struct {
	ID              uuid.UUID `db:"id"`
	Name            string    `db:"name"`
	CGSTPercentage  float64   `db:"cgst_percentage"`
	SGSTPercentage  float64   `db:"sgst_percentage"`
	IGSTPercentage  float64   `db:"igst_percentage"`
	CESSPercentage  float64   `db:"cess_percentage"`
}

func (s *Store) GetTaxClass(ctx context.Context, id uuid.UUID) (*TaxClass, error) {
	tc := &TaxClass{}
	err := s.db.QueryRow(ctx, `
		SELECT id, name, cgst_percentage, sgst_percentage, igst_percentage, cess_percentage
		FROM tax_classes WHERE id = $1
	`, id).Scan(&tc.ID, &tc.Name, &tc.CGSTPercentage, &tc.SGSTPercentage, &tc.IGSTPercentage, &tc.CESSPercentage)
	if err != nil {
		return nil, err
	}
	return tc, nil
}

func (s *Store) GetAddressByID(ctx context.Context, id uuid.UUID) (*CustomerAddress, error) {
	a := &CustomerAddress{}
	err := s.db.QueryRow(ctx, `SELECT id,user_id,label,contact_name,phone,address_line_1,
		address_line_2,landmark,city,state,country,postal_code,address_type,is_default,created_at
		FROM customer_addresses WHERE id=$1`, id).Scan(
		&a.ID, &a.UserID, &a.Label, &a.ContactName, &a.Phone, &a.AddressLine1,
		&a.AddressLine2, &a.Landmark, &a.City, &a.State, &a.Country, &a.PostalCode,
		&a.AddressType, &a.IsDefault, &a.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Store) UpdateAddress(ctx context.Context, id, userID uuid.UUID, addr *CustomerAddress) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE customer_addresses SET
			contact_name=$3, phone=$4, address_line_1=$5, address_line_2=$6,
			landmark=$7, city=$8, state=$9, country=$10, postal_code=$11,
			address_type=$12, is_default=$13
		WHERE id=$1 AND user_id=$2`,
		id, userID, addr.ContactName, addr.Phone, addr.AddressLine1, addr.AddressLine2,
		addr.Landmark, addr.City, addr.State, addr.Country, addr.PostalCode,
		addr.AddressType, addr.IsDefault,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("address not found")
	}
	return nil
}

func (s *Store) DeleteAddress(ctx context.Context, id, userID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM customer_addresses WHERE id=$1 AND user_id=$2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("address not found")
	}
	return nil
}

// SetDefaultAddress atomically clears any existing default and sets the given address as default.
func (s *Store) SetDefaultAddress(ctx context.Context, id, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `UPDATE customer_addresses SET is_default=false WHERE user_id=$1`, userID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE customer_addresses SET is_default=true WHERE id=$1 AND user_id=$2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("address not found")
	}
	return tx.Commit(ctx)
}

func (s *Store) GetAddressesByUser(ctx context.Context, userID uuid.UUID) ([]*CustomerAddress, error) {
	rows, err := s.db.Query(ctx, `SELECT id,user_id,label,contact_name,phone,address_line_1,
		address_line_2,landmark,city,state,country,postal_code,address_type,is_default,created_at
		FROM customer_addresses WHERE user_id=$1 ORDER BY is_default DESC, created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var addrs []*CustomerAddress
	for rows.Next() {
		var a CustomerAddress
		if err := rows.Scan(&a.ID, &a.UserID, &a.Label, &a.ContactName, &a.Phone, &a.AddressLine1,
			&a.AddressLine2, &a.Landmark, &a.City, &a.State, &a.Country, &a.PostalCode,
			&a.AddressType, &a.IsDefault, &a.CreatedAt); err != nil {
			return nil, err
		}
		addrs = append(addrs, &a)
	}
	return addrs, nil
}

// ─── Reviews ─────────────────────────────────────────────────

func (s *Store) CreateReview(ctx context.Context, r *Review) error {
	r.ID = uuid.New()
	r.CreatedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO reviews (id,product_id,seller_id,order_item_id,reviewer_id,
		  rating,title,body,is_verified_purchase,is_published,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		r.ID, r.ProductID, r.SellerID, r.OrderItemID, r.ReviewerID,
		r.Rating, r.Title, r.Body, r.IsVerifiedPurchase, r.IsPublished, r.CreatedAt,
	)
	return err
}

func (s *Store) GetProductReviews(ctx context.Context, productID uuid.UUID, limit, offset int) ([]*Review, int, error) {
	var total int
	_ = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM reviews WHERE product_id=$1 AND is_published=TRUE`, productID).Scan(&total)

	rows, err := s.db.Query(ctx, `SELECT id,product_id,seller_id,order_item_id,reviewer_id,
		rating,title,body,is_verified_purchase,helpful_count,created_at
		FROM reviews WHERE product_id=$1 AND is_published=TRUE
		ORDER BY helpful_count DESC, created_at DESC LIMIT $2 OFFSET $3`, productID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var reviews []*Review
	for rows.Next() {
		var r Review
		if err := rows.Scan(&r.ID, &r.ProductID, &r.SellerID, &r.OrderItemID, &r.ReviewerID,
			&r.Rating, &r.Title, &r.Body, &r.IsVerifiedPurchase, &r.HelpfulCount, &r.CreatedAt); err != nil {
			return nil, 0, err
		}
		reviews = append(reviews, &r)
	}
	return reviews, total, nil
}

// ─── Coupons ─────────────────────────────────────────────────

func (s *Store) GetCouponByCode(ctx context.Context, code string) (*Coupon, error) {
	var c Coupon
	err := s.db.QueryRow(ctx, `SELECT id,seller_id,code,description,discount_type,discount_value,
		max_discount_amount,min_order_amount,max_uses,uses_count,max_uses_per_user,
		applicable_to,is_active,starts_at,expires_at
		FROM coupons WHERE code=$1 AND is_active=TRUE AND starts_at<=NOW()
		AND (expires_at IS NULL OR expires_at>NOW())`, code).Scan(
		&c.ID, &c.SellerID, &c.Code, &c.Description, &c.DiscountType, &c.DiscountValue,
		&c.MaxDiscountAmount, &c.MinOrderAmount, &c.MaxUses, &c.UsesCount, &c.MaxUsesPerUser,
		&c.ApplicableTo, &c.IsActive, &c.StartsAt, &c.ExpiresAt,
	)
	return &c, err
}

func (s *Store) IncrCouponUsage(ctx context.Context, couponID, userID, orderID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err = tx.Exec(ctx, `UPDATE coupons SET uses_count=uses_count+1 WHERE id=$1`, couponID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO coupon_usages (coupon_id,user_id,order_id) VALUES ($1,$2,$3)`,
		couponID, userID, orderID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ─── Return Requests ─────────────────────────────────────────

func (s *Store) CreateReturnRequest(ctx context.Context, r *ReturnRequest) error {
	r.ID = uuid.New()
	r.RequestedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO return_requests (id,order_id,order_item_id,customer_user_id,seller_id,
		  reason_code,reason_description,status,requested_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		r.ID, r.OrderID, r.OrderItemID, r.CustomerUserID, r.SellerID,
		r.ReasonCode, r.ReasonDescription, r.Status, r.RequestedAt,
	)
	return err
}

// ListReturnsByCustomer returns a customer's return requests across all orders.
func (s *Store) ListReturnsByCustomer(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*ReturnRequest, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, order_id, order_item_id, customer_user_id, seller_id, reason_code,
			reason_description, status, approved_at, rejected_at, rejection_reason,
			requested_at
		FROM return_requests
		WHERE customer_user_id = $1
		ORDER BY requested_at DESC
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ReturnRequest
	for rows.Next() {
		var r ReturnRequest
		if err := rows.Scan(&r.ID, &r.OrderID, &r.OrderItemID, &r.CustomerUserID, &r.SellerID,
			&r.ReasonCode, &r.ReasonDescription, &r.Status, &r.ApprovedAt, &r.RejectedAt,
			&r.RejectionReason, &r.RequestedAt); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, nil
}

// UpdateReturnStatus advances a return through requested → approved/rejected.
// rejReason is only persisted when status is 'rejected'; pass nil otherwise.
//
// (Earlier signature took an actorID for audit, but no approved_by /
// rejected_by columns exist on return_requests so it was always discarded.
// Dropped to avoid a pgx parameter-type-inference error when callers passed
// untyped nils.)
func (s *Store) UpdateReturnStatus(ctx context.Context, id uuid.UUID, status string, rejReason *string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE return_requests SET status=$2,
		  approved_at=CASE WHEN $2='approved' THEN NOW() ELSE approved_at END,
		  rejected_at=CASE WHEN $2='rejected' THEN NOW() ELSE rejected_at END,
		  rejection_reason=COALESCE($3,rejection_reason)
		WHERE id=$1`, id, status, rejReason)
	return err
}

// GetReturnRequestByID returns a single return request for inspection.
func (s *Store) GetReturnRequestByID(ctx context.Context, id uuid.UUID) (*ReturnRequest, error) {
	r := &ReturnRequest{}
	err := s.db.QueryRow(ctx, `
		SELECT id, order_id, order_item_id, customer_user_id, seller_id,
		       reason_code, reason_description, status,
		       approved_at, rejected_at, rejection_reason, refund_amount,
		       requested_at
		FROM return_requests WHERE id=$1`, id).Scan(
		&r.ID, &r.OrderID, &r.OrderItemID, &r.CustomerUserID, &r.SellerID,
		&r.ReasonCode, &r.ReasonDescription, &r.Status,
		&r.ApprovedAt, &r.RejectedAt, &r.RejectionReason, &r.RefundAmount,
		&r.RequestedAt)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// SetReturnPickupLabel records the courier-issued return shipping details
// (pickup at the customer's address, drop at the seller). Called after
// ApproveReturn books a pickup with the courier.
func (s *Store) SetReturnPickupLabel(ctx context.Context, returnID uuid.UUID, courierName, awb, labelURL string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE return_requests
		SET pickup_courier=$2, pickup_tracking_number=$3, pickup_label_url=$4
		WHERE id=$1`, returnID, courierName, awb, labelURL)
	return err
}

// CreateCODRemittance inserts a COD remittance row. Idempotent on
// shipment_id (the table has a UNIQUE constraint) — a second delivery
// webhook for the same shipment is dropped silently. Returns nil on
// successful insert OR on conflict, both of which are "fine".
func (s *Store) CreateCODRemittance(ctx context.Context, r *CODRemittance) error {
	r.ID = uuid.New()
	r.CreatedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO cod_remittances (
			id, shipment_id, order_id, seller_id,
			gross_amount, commission_amount, platform_fee, tds_amount, net_amount,
			currency_code, status, delivered_at, created_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (shipment_id) DO NOTHING`,
		r.ID, r.ShipmentID, r.OrderID, r.SellerID,
		r.GrossAmount, r.CommissionAmount, r.PlatformFee, r.TDSAmount, r.NetAmount,
		r.CurrencyCode, r.Status, r.DeliveredAt, r.CreatedAt,
	)
	return err
}

// ListCODRemittancesBySeller returns the seller's COD remittances newest
// first, optionally filtered by status. Used by the seller payouts UI.
func (s *Store) ListCODRemittancesBySeller(ctx context.Context, sellerID uuid.UUID, status string, limit, offset int) ([]*CODRemittance, int, error) {
	conds := []string{"seller_id = $1"}
	args := []any{sellerID}
	idx := 2
	if status != "" {
		conds = append(conds, fmt.Sprintf("status = $%d", idx))
		args = append(args, status)
		idx++
	}
	where := "WHERE " + strings.Join(conds, " AND ")

	var total int
	if err := s.db.QueryRow(ctx, "SELECT COUNT(*) FROM cod_remittances "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	rows, err := s.db.Query(ctx, `
		SELECT id, shipment_id, order_id, seller_id,
		       gross_amount, commission_amount, platform_fee, tds_amount, net_amount,
		       currency_code, status, delivered_at, settled_at, payout_batch_id, created_at
		FROM cod_remittances `+where+
		fmt.Sprintf(" ORDER BY delivered_at DESC LIMIT $%d OFFSET $%d", idx, idx+1), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*CODRemittance
	for rows.Next() {
		r := &CODRemittance{}
		if err := rows.Scan(&r.ID, &r.ShipmentID, &r.OrderID, &r.SellerID,
			&r.GrossAmount, &r.CommissionAmount, &r.PlatformFee, &r.TDSAmount, &r.NetAmount,
			&r.CurrencyCode, &r.Status, &r.DeliveredAt, &r.SettledAt, &r.PayoutBatchID, &r.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// MarkCODRemittanceSettled flips a pending remittance to settled and stamps
// the payout batch. Used by the Ops-side payout job when cash actually
// transfers to the seller's bank/UPI.
func (s *Store) MarkCODRemittanceSettled(ctx context.Context, remittanceID, payoutBatchID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE cod_remittances
		SET status = 'settled',
		    settled_at = NOW(),
		    payout_batch_id = $2
		WHERE id = $1 AND status = 'pending'`, remittanceID, payoutBatchID)
	return err
}

// SetReturnRefund stamps the refund intent + status onto the return. Used
// once payments-service accepts the refund — even if the gateway is async,
// we record the intent ID immediately so a follow-up webhook can find it.
func (s *Store) SetReturnRefund(ctx context.Context, returnID uuid.UUID, refundIntentID, status string, amount float64) error {
	_, err := s.db.Exec(ctx, `
		UPDATE return_requests
		SET refund_intent_id=$2, refund_status=$3, refund_amount=$4
		WHERE id=$1`, returnID, refundIntentID, status, amount)
	return err
}

// ─── Payout Batches ──────────────────────────────────────────

func (s *Store) CreatePayoutBatch(ctx context.Context, batch *PayoutBatch, txns []*PayoutTransaction) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	batch.ID = uuid.New()
	batch.CreatedAt = time.Now()
	if _, err = tx.Exec(ctx, `
		INSERT INTO payout_batches (id,batch_date,payout_cycle_start,payout_cycle_end,
		  total_sellers,total_amount,status,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		batch.ID, batch.BatchDate, batch.CycleStart, batch.CycleEnd,
		batch.TotalSellers, batch.TotalAmount, batch.Status, batch.CreatedAt,
	); err != nil {
		return err
	}

	for _, t := range txns {
		t.ID = uuid.New()
		t.BatchID = batch.ID
		t.InitiatedAt = time.Now()
		if _, err = tx.Exec(ctx, `
			INSERT INTO payout_transactions (id,batch_id,seller_id,gross_amount,commission_amount,
			  platform_fee,tax_deducted,adjustment_amount,net_amount,bank_account_id,status,initiated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			t.ID, t.BatchID, t.SellerID, t.GrossAmount, t.CommissionAmount,
			t.PlatformFee, t.TaxDeducted, t.AdjustmentAmount, t.NetAmount,
			t.BankAccountID, t.Status, t.InitiatedAt,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
