// Package postgres — batch readers used by the service layer to fix
// HP2 (N+1 query patterns) from the commerce audit.
//
// Each method takes a slice of IDs and returns a map keyed on the
// entity ID so callers can fan out into in-memory zips without
// per-element round trips.
package postgres

import (
	"context"

	"github.com/google/uuid"
)

// GetProductsByIDs returns the supplied products as a map keyed by id.
// Missing ids are silently absent — callers must tolerate misses
// (matches the previous GetProductByID semantics, which returned
// pgx.ErrNoRows on a stale variant_id that no longer resolves).
//
// Used by Service.GetCart to replace N round trips per cart with one.
func (s *Store) GetProductsByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*Product, error) {
	if len(ids) == 0 {
		return map[uuid.UUID]*Product{}, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT id,seller_id,category_id,brand_id,tax_class_id,title,short_title,slug,description,
		  short_description,brand_name,manufacturer_name,product_type,condition,sku_root,status,visibility,approval_status,
		  rejection_reason,primary_image_media_id,video_media_id,weight_grams,length_cm,width_cm,height_cm,
		  country_of_origin,warranty_info,return_policy_type,return_policy_days,hsn_code,search_keywords,
		  meta_title,meta_description,
		  avg_rating,review_count,order_count,view_count,wishlist_count,is_featured,created_at,updated_at,published_at
		FROM products WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[uuid.UUID]*Product, len(ids))
	for rows.Next() {
		var p Product
		if err := rows.Scan(
			&p.ID, &p.SellerID, &p.CategoryID, &p.BrandID, &p.TaxClassID,
			&p.Title, &p.ShortTitle, &p.Slug, &p.Description, &p.ShortDescription,
			&p.BrandName, &p.ManufacturerName,
			&p.ProductType, &p.Condition, &p.SKURoot, &p.Status, &p.Visibility,
			&p.ApprovalStatus, &p.RejectionReason, &p.PrimaryImageMediaID, &p.VideoMediaID,
			&p.WeightGrams, &p.LengthCm, &p.WidthCm, &p.HeightCm,
			&p.CountryOfOrigin, &p.WarrantyInfo,
			&p.ReturnPolicyType, &p.ReturnPolicyDays, &p.HSNCode, &p.SearchKeywords,
			&p.MetaTitle, &p.MetaDescription, &p.AvgRating, &p.ReviewCount,
			&p.OrderCount, &p.ViewCount, &p.WishlistCount, &p.IsFeatured,
			&p.CreatedAt, &p.UpdatedAt, &p.PublishedAt,
		); err != nil {
			return nil, err
		}
		pCopy := p
		out[pCopy.ID] = &pCopy
	}
	return out, rows.Err()
}

// GetVariantsByIDs returns the supplied variants as a map keyed by id.
// Used by Service.GetCart to batch the per-cart-item variant lookup.
func (s *Store) GetVariantsByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*ProductVariant, error) {
	if len(ids) == 0 {
		return map[uuid.UUID]*ProductVariant{}, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT id,product_id,sku,barcode,option_1_name,option_1_value,
		       option_2_name,option_2_value,option_3_name,option_3_value,
		       mrp,selling_price,cost_price,currency_code,status,image_media_id,
		       weight_grams,created_at,updated_at
		FROM product_variants WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[uuid.UUID]*ProductVariant, len(ids))
	for rows.Next() {
		var v ProductVariant
		if err := rows.Scan(&v.ID, &v.ProductID, &v.SKU, &v.Barcode,
			&v.Option1Name, &v.Option1Value, &v.Option2Name, &v.Option2Value,
			&v.Option3Name, &v.Option3Value,
			&v.MRP, &v.SellingPrice, &v.CostPrice, &v.CurrencyCode, &v.Status,
			&v.ImageMediaID, &v.WeightGrams, &v.CreatedAt, &v.UpdatedAt,
		); err != nil {
			return nil, err
		}
		vCopy := v
		out[vCopy.ID] = &vCopy
	}
	return out, rows.Err()
}

// GetOrderItemsByOrderIDs is the batched fan-in for the seller
// fulfillment dashboard. Returns a map[order_id] -> []*OrderItem so the
// caller groups in O(n) instead of issuing one GetOrderItems per order.
//
// Items are returned in the same order they were inserted (created_at
// ASC) so multi-seller orders display predictably.
func (s *Store) GetOrderItemsByOrderIDs(ctx context.Context, orderIDs []uuid.UUID) (map[uuid.UUID][]*OrderItem, error) {
	if len(orderIDs) == 0 {
		return map[uuid.UUID][]*OrderItem{}, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT id,order_id,product_id,variant_id,seller_id,product_title,
		       variant_details,sku,quantity,unit_mrp,unit_price,discount_amount,
		       tax_amount,final_price,status,shipment_id,tracking_number,
		       return_eligible_until,delivered_at,created_at
		FROM order_items
		WHERE order_id = ANY($1)
		ORDER BY order_id, created_at ASC`, orderIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[uuid.UUID][]*OrderItem, len(orderIDs))
	for rows.Next() {
		var it OrderItem
		if err := rows.Scan(&it.ID, &it.OrderID, &it.ProductID, &it.VariantID, &it.SellerID,
			&it.ProductTitle, &it.VariantDetails, &it.SKU, &it.Quantity,
			&it.UnitMRP, &it.UnitPrice, &it.DiscountAmount, &it.TaxAmount, &it.FinalPrice,
			&it.Status, &it.ShipmentID, &it.TrackingNumber, &it.ReturnEligibleUntil,
			&it.DeliveredAt, &it.CreatedAt); err != nil {
			return nil, err
		}
		itCopy := it
		out[itCopy.OrderID] = append(out[itCopy.OrderID], &itCopy)
	}
	return out, rows.Err()
}

// GetOrderItemsByIDs batches the per-id lookup used by the seller
// returns inbox: one return row points at one order item, but rendering
// the inbox needs the item details for every row. Returns the map keyed
// by item id (since a return row stores order_item_id directly).
func (s *Store) GetOrderItemsByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*OrderItem, error) {
	if len(ids) == 0 {
		return map[uuid.UUID]*OrderItem{}, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT id,order_id,product_id,variant_id,seller_id,product_title,
		       variant_details,sku,quantity,unit_mrp,unit_price,discount_amount,
		       tax_amount,final_price,status,shipment_id,tracking_number,
		       return_eligible_until,delivered_at,created_at
		FROM order_items
		WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[uuid.UUID]*OrderItem, len(ids))
	for rows.Next() {
		var it OrderItem
		if err := rows.Scan(&it.ID, &it.OrderID, &it.ProductID, &it.VariantID, &it.SellerID,
			&it.ProductTitle, &it.VariantDetails, &it.SKU, &it.Quantity,
			&it.UnitMRP, &it.UnitPrice, &it.DiscountAmount, &it.TaxAmount, &it.FinalPrice,
			&it.Status, &it.ShipmentID, &it.TrackingNumber, &it.ReturnEligibleUntil,
			&it.DeliveredAt, &it.CreatedAt); err != nil {
			return nil, err
		}
		itCopy := it
		out[itCopy.ID] = &itCopy
	}
	return out, rows.Err()
}

// GetOrdersByIDs batches the per-return order header fetch in the
// seller returns inbox. Returns a map keyed by order id.
func (s *Store) GetOrdersByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*Order, error) {
	if len(ids) == 0 {
		return map[uuid.UUID]*Order{}, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT id,customer_user_id,order_number,subtotal,discount_amount,
		       shipping_charges,tax_amount,coupon_code,coupon_discount,final_amount,
		       currency_code,payment_method,payment_status,payment_id,payment_gateway,
		       delivery_address_id,delivery_address_snapshot,gift_message,status,
		       cancellation_reason,cancelled_by,idempotency_key,created_at,updated_at,
		       organization_id,po_number,cost_center,billing_address_snapshot,invoice_email,
		       approval_status,approved_by_user_id,approved_at,approval_notes,
		       credit_terms_days,payment_due_date
		FROM orders WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[uuid.UUID]*Order, len(ids))
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
		oCopy := o
		out[oCopy.ID] = &oCopy
	}
	return out, rows.Err()
}

// GetInventoryByVariantIDs batches the per-variant inventory lookup used
// inside priceCart (checkout + quote). Returns map keyed by variant_id.
// Variants without an inventory row are absent — caller treats missing
// as "0 available".
func (s *Store) GetInventoryByVariantIDs(ctx context.Context, variantIDs []uuid.UUID) (map[uuid.UUID]*InventoryItem, error) {
	if len(variantIDs) == 0 {
		return map[uuid.UUID]*InventoryItem{}, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, variant_id, seller_id, total_qty, reserved_qty, damaged_qty,
		       returned_qty, safety_stock, low_stock_alert, updated_at
		FROM inventory_items WHERE variant_id = ANY($1)`, variantIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[uuid.UUID]*InventoryItem, len(variantIDs))
	for rows.Next() {
		var i InventoryItem
		if err := rows.Scan(&i.ID, &i.VariantID, &i.SellerID, &i.TotalQty,
			&i.ReservedQty, &i.DamagedQty, &i.ReturnedQty, &i.SafetyStock,
			&i.LowStockAlert, &i.UpdatedAt); err != nil {
			return nil, err
		}
		iCopy := i
		out[iCopy.VariantID] = &iCopy
	}
	return out, rows.Err()
}

// GetShipmentsByOrderAndSeller is the seller-fulfillment batch fetch.
// One row per (order_id, seller_id) pair; missing pairs are omitted.
// Output map key is order_id because each seller only sees their own
// items on a given order — so within one seller's dashboard page each
// order has at most one shipment.
//
// Composite index idx_shipments_order_seller (HP5 migration 005)
// keeps this lookup index-only.
func (s *Store) GetShipmentsByOrderAndSeller(ctx context.Context, orderIDs []uuid.UUID, sellerID uuid.UUID) (map[uuid.UUID]*Shipment, error) {
	if len(orderIDs) == 0 {
		return map[uuid.UUID]*Shipment{}, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, order_id, seller_id, courier, tracking_number, courier_order_id,
		       label_url, tracking_url, status, eta, shipped_at, delivered_at,
		       last_event_at, created_at, updated_at
		FROM shipments
		WHERE order_id = ANY($1) AND seller_id = $2`, orderIDs, sellerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[uuid.UUID]*Shipment, len(orderIDs))
	for rows.Next() {
		sh := &Shipment{}
		if err := rows.Scan(&sh.ID, &sh.OrderID, &sh.SellerID, &sh.Courier, &sh.TrackingNumber,
			&sh.CourierOrderID, &sh.LabelURL, &sh.TrackingURL, &sh.Status, &sh.ETA,
			&sh.ShippedAt, &sh.DeliveredAt, &sh.LastEventAt, &sh.CreatedAt, &sh.UpdatedAt); err != nil {
			return nil, err
		}
		out[sh.OrderID] = sh
	}
	return out, rows.Err()
}
