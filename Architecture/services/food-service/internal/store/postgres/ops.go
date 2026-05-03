package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Store) CreatePartnerRestaurant(ctx context.Context, ownerID uuid.UUID, in PartnerRestaurantInput) (*PartnerRestaurant, error) {
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.AddressLine1) == "" || strings.TrimSpace(in.City) == "" {
		return nil, fmt.Errorf("name, address_line1, and city are required")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	legalName := strings.TrimSpace(in.LegalName)
	if legalName == "" {
		legalName = in.Name
	}
	var partnerID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO food.restaurant_partners (
			owner_user_id, legal_name, display_name, phone, email, status
		)
		VALUES ($1, $2, $3, $4, $5, 'PENDING_REVIEW')
		RETURNING id
	`, ownerID, legalName, in.DisplayName, in.Phone, in.Email).Scan(&partnerID); err != nil {
		return nil, err
	}

	slug := strings.TrimSpace(in.Slug)
	if slug == "" {
		slug = strings.ToLower(strings.ReplaceAll(in.Name, " ", "-"))
	}
	var restaurantID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO food.restaurants (
			partner_id, owner_user_id, name, slug, description, phone, email,
			status, address_line1, address_line2, city, state, postal_code,
			latitude, longitude, min_order_amount, packaging_fee
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'PENDING_REVIEW',$8,$9,$10,$11,$12,$13,$14,$15,$16)
		RETURNING id
	`, partnerID, ownerID, in.Name, slug, in.Description, in.Phone, in.Email,
		in.AddressLine1, in.AddressLine2, in.City, in.State, in.PostalCode,
		in.Latitude, in.Longitude, in.MinOrderAmount, in.PackagingFee).Scan(&restaurantID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetPartnerRestaurant(ctx, ownerID, restaurantID)
}

func (s *Store) ListPartnerRestaurants(ctx context.Context, ownerID uuid.UUID) ([]PartnerRestaurant, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, partner_id, owner_user_id, name, slug, COALESCE(description, ''),
			status::text, is_open, is_accepting_orders, city, COALESCE(state, ''),
			min_order_amount::float8, packaging_fee::float8, created_at::text
		FROM food.restaurants
		WHERE owner_user_id = $1
		ORDER BY created_at DESC
	`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var restaurants []PartnerRestaurant
	for rows.Next() {
		restaurant, err := scanPartnerRestaurant(rows)
		if err != nil {
			return nil, err
		}
		restaurants = append(restaurants, restaurant)
	}
	return restaurants, rows.Err()
}

func (s *Store) GetPartnerRestaurant(ctx context.Context, ownerID, restaurantID uuid.UUID) (*PartnerRestaurant, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, partner_id, owner_user_id, name, slug, COALESCE(description, ''),
			status::text, is_open, is_accepting_orders, city, COALESCE(state, ''),
			min_order_amount::float8, packaging_fee::float8, created_at::text
		FROM food.restaurants
		WHERE owner_user_id = $1 AND id = $2
	`, ownerID, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, pgx.ErrNoRows
	}
	restaurant, err := scanPartnerRestaurant(rows)
	if err != nil {
		return nil, err
	}
	return &restaurant, rows.Err()
}

func (s *Store) UpdatePartnerRestaurant(ctx context.Context, ownerID, restaurantID uuid.UUID, in PartnerRestaurantInput) (*PartnerRestaurant, error) {
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.AddressLine1) == "" || strings.TrimSpace(in.City) == "" {
		return nil, fmt.Errorf("name, address_line1, and city are required")
	}
	slug := strings.TrimSpace(in.Slug)
	if slug == "" {
		slug = strings.ToLower(strings.ReplaceAll(in.Name, " ", "-"))
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE food.restaurants
		SET name = $3,
			slug = $4,
			description = $5,
			phone = $6,
			email = $7,
			address_line1 = $8,
			address_line2 = $9,
			city = $10,
			state = $11,
			postal_code = $12,
			latitude = $13,
			longitude = $14,
			min_order_amount = $15,
			packaging_fee = $16,
			status = CASE WHEN status = 'ACTIVE' THEN 'PENDING_REVIEW' ELSE status END,
			is_accepting_orders = CASE WHEN status = 'ACTIVE' THEN FALSE ELSE is_accepting_orders END
		WHERE owner_user_id = $1 AND id = $2
	`, ownerID, restaurantID, in.Name, slug, in.Description, in.Phone, in.Email,
		in.AddressLine1, in.AddressLine2, in.City, in.State, in.PostalCode,
		in.Latitude, in.Longitude, in.MinOrderAmount, in.PackagingFee)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, pgx.ErrNoRows
	}
	return s.GetPartnerRestaurant(ctx, ownerID, restaurantID)
}

func (s *Store) AddRestaurantDocument(ctx context.Context, ownerID, restaurantID uuid.UUID, input map[string]any) (map[string]any, error) {
	if err := s.requireRestaurantOwner(ctx, ownerID, restaurantID); err != nil {
		return nil, err
	}
	documentType := stringValue(input, "document_type", "")
	if documentType == "" {
		return nil, fmt.Errorf("document_type is required")
	}
	var mediaID any
	if raw := stringValue(input, "media_id", ""); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid media_id")
		}
		mediaID = id
	}
	var id uuid.UUID
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.restaurant_documents (
			restaurant_id, document_type, document_number, media_id, file_url
		)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, restaurantID, documentType, emptyToNil(stringValue(input, "document_number", "")),
		mediaID, emptyToNil(stringValue(input, "file_url", ""))).Scan(&id); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "restaurant_id": restaurantID, "document_type": documentType, "status": "PENDING"}, nil
}

func (s *Store) AddRestaurantImage(ctx context.Context, ownerID, restaurantID uuid.UUID, input map[string]any) (map[string]any, error) {
	if err := s.requireRestaurantOwner(ctx, ownerID, restaurantID); err != nil {
		return nil, err
	}
	imageURL := stringValue(input, "image_url", "")
	if imageURL == "" {
		return nil, fmt.Errorf("image_url is required")
	}
	var mediaID any
	if raw := stringValue(input, "media_id", ""); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid media_id")
		}
		mediaID = id
	}
	imageType := stringValue(input, "image_type", "gallery")
	sortOrder := intValue(input, "sort_order", 0)
	var id uuid.UUID
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.restaurant_images (restaurant_id, media_id, image_url, image_type, sort_order)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, restaurantID, mediaID, imageURL, imageType, sortOrder).Scan(&id); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "restaurant_id": restaurantID, "image_url": imageURL, "image_type": imageType}, nil
}

func (s *Store) CreateMenuCategory(ctx context.Context, ownerID, restaurantID uuid.UUID, in MenuCategoryInput) (*MenuCategory, error) {
	if err := s.requireRestaurantOwner(ctx, ownerID, restaurantID); err != nil {
		return nil, err
	}
	var category MenuCategory
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.menu_categories (restaurant_id, name, description, sort_order)
		VALUES ($1, $2, $3, $4)
		RETURNING id, name, COALESCE(description, ''), sort_order
	`, restaurantID, in.Name, in.Description, in.SortOrder).Scan(&category.ID, &category.Name, &category.Description, &category.SortOrder); err != nil {
		return nil, err
	}
	category.Items = []MenuItem{}
	return &category, nil
}

func (s *Store) ListMenuCategories(ctx context.Context, ownerID, restaurantID uuid.UUID) ([]MenuCategory, error) {
	if err := s.requireRestaurantOwner(ctx, ownerID, restaurantID); err != nil {
		return nil, err
	}
	return s.GetMenu(ctx, restaurantID)
}

func (s *Store) UpdateMenuCategory(ctx context.Context, ownerID, categoryID uuid.UUID, in MenuCategoryInput) (*MenuCategory, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE food.menu_categories c
		SET name = $3, description = $4, sort_order = $5
		FROM food.restaurants r
		WHERE c.restaurant_id = r.id AND r.owner_user_id = $1 AND c.id = $2
	`, ownerID, categoryID, in.Name, in.Description, in.SortOrder)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, pgx.ErrNoRows
	}
	var category MenuCategory
	if err := s.db.QueryRow(ctx, `
		SELECT id, name, COALESCE(description, ''), sort_order
		FROM food.menu_categories
		WHERE id = $1
	`, categoryID).Scan(&category.ID, &category.Name, &category.Description, &category.SortOrder); err != nil {
		return nil, err
	}
	category.Items = []MenuItem{}
	return &category, nil
}

func (s *Store) DeleteMenuCategory(ctx context.Context, ownerID, categoryID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE food.menu_categories c
		SET is_active = FALSE
		FROM food.restaurants r
		WHERE c.restaurant_id = r.id AND r.owner_user_id = $1 AND c.id = $2
	`, ownerID, categoryID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) CreateMenuItem(ctx context.Context, ownerID, restaurantID, categoryID uuid.UUID, in MenuItemInput) (*MenuItem, error) {
	if err := s.requireRestaurantOwner(ctx, ownerID, restaurantID); err != nil {
		return nil, err
	}
	foodType := in.FoodType
	if foodType == "" {
		foodType = "VEG"
	}
	if in.PreparationMinutes <= 0 {
		in.PreparationMinutes = 20
	}
	var item MenuItem
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.menu_items (
			restaurant_id, category_id, name, description, food_type, base_price,
			discount_price, image_url, preparation_minutes, is_recommended, tax_percentage
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING id, restaurant_id, category_id, name, COALESCE(description, ''),
			food_type::text, base_price::float8, COALESCE(discount_price, 0)::float8,
			COALESCE(image_url, ''), preparation_minutes, is_available,
			is_recommended, tax_percentage::float8
	`, restaurantID, categoryID, in.Name, in.Description, foodType, in.BasePrice,
		in.DiscountPrice, in.ImageURL, in.PreparationMinutes, in.IsRecommended,
		in.TaxPercentage).Scan(&item.ID, &item.RestaurantID, &item.CategoryID, &item.Name,
		&item.Description, &item.FoodType, &item.BasePrice, &item.DiscountPrice,
		&item.ImageURL, &item.PreparationMinutes, &item.IsAvailable, &item.IsRecommended,
		&item.TaxPercentage); err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Store) UpdateMenuItem(ctx context.Context, ownerID, itemID uuid.UUID, in MenuItemInput) (*MenuItem, error) {
	if in.FoodType == "" {
		in.FoodType = "VEG"
	}
	if in.PreparationMinutes <= 0 {
		in.PreparationMinutes = 20
	}
	var item MenuItem
	if err := s.db.QueryRow(ctx, `
		UPDATE food.menu_items i
		SET name = $3,
			description = $4,
			food_type = $5,
			base_price = $6,
			discount_price = $7,
			image_url = $8,
			preparation_minutes = $9,
			is_recommended = $10,
			tax_percentage = $11
		FROM food.restaurants r
		WHERE i.restaurant_id = r.id AND r.owner_user_id = $1 AND i.id = $2
		RETURNING i.id, i.restaurant_id, i.category_id, i.name, COALESCE(i.description, ''),
			i.food_type::text, i.base_price::float8, COALESCE(i.discount_price, 0)::float8,
			COALESCE(i.image_url, ''), i.preparation_minutes, i.is_available,
			i.is_recommended, i.tax_percentage::float8
	`, ownerID, itemID, in.Name, in.Description, in.FoodType, in.BasePrice,
		in.DiscountPrice, in.ImageURL, in.PreparationMinutes, in.IsRecommended,
		in.TaxPercentage).Scan(&item.ID, &item.RestaurantID, &item.CategoryID, &item.Name,
		&item.Description, &item.FoodType, &item.BasePrice, &item.DiscountPrice,
		&item.ImageURL, &item.PreparationMinutes, &item.IsAvailable, &item.IsRecommended,
		&item.TaxPercentage); err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Store) DeleteMenuItem(ctx context.Context, ownerID, itemID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE food.menu_items i
		SET is_active = FALSE, is_available = FALSE
		FROM food.restaurants r
		WHERE i.restaurant_id = r.id AND r.owner_user_id = $1 AND i.id = $2
	`, ownerID, itemID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) SetMenuItemAvailability(ctx context.Context, ownerID, itemID uuid.UUID, available bool) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE food.menu_items i
		SET is_available = $3
		FROM food.restaurants r
		WHERE i.restaurant_id = r.id AND r.owner_user_id = $1 AND i.id = $2
	`, ownerID, itemID, available)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) ListPartnerOrders(ctx context.Context, ownerID, restaurantID uuid.UUID) ([]Order, error) {
	if err := s.requireRestaurantOwner(ctx, ownerID, restaurantID); err != nil {
		return nil, err
	}
	return s.listOrdersByWhere(ctx, "restaurant_id = $1", restaurantID)
}

func (s *Store) PartnerUpdateOrderStatus(ctx context.Context, ownerID, orderID uuid.UUID, toStatus, reason, idempotencyKey string) (*Order, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if idempotencyKey != "" {
		_, handled, err := s.lockGenericIdempotency(ctx, tx, ownerID, idempotencyKey)
		if err != nil {
			return nil, err
		}
		if handled {
			var userID uuid.UUID
			if err := tx.QueryRow(ctx, `
				SELECT user_id
				FROM food.orders
				WHERE id = $1 AND restaurant_id IN (
					SELECT id FROM food.restaurants WHERE owner_user_id = $2
				)
			`, orderID, ownerID).Scan(&userID); err != nil {
				return nil, err
			}
			return s.getOrderTx(ctx, tx, userID, orderID, true)
		}
	}
	var order Order
	if err := tx.QueryRow(ctx, `
		SELECT id, order_number, user_id, restaurant_id, restaurant_name_snapshot,
			status::text, payment_status::text, payment_method::text,
			item_subtotal::float8, addon_total::float8, packaging_fee::float8,
			tax_total::float8, delivery_fee::float8, platform_fee::float8,
			restaurant_discount::float8, coupon_discount::float8, final_amount::float8,
			COALESCE(estimated_preparation_minutes, 0),
			COALESCE(estimated_delivery_minutes, 0),
			placed_at::text, COALESCE(delivered_at::text, '')
		FROM food.orders
		WHERE id = $1 AND restaurant_id IN (
			SELECT id FROM food.restaurants WHERE owner_user_id = $2
		)
		FOR UPDATE
	`, orderID, ownerID).Scan(&order.ID, &order.OrderNumber, &order.UserID, &order.RestaurantID,
		&order.RestaurantName, &order.Status, &order.PaymentStatus, &order.PaymentMethod,
		&order.Totals.ItemSubtotal, &order.Totals.AddonTotal, &order.Totals.PackagingFee,
		&order.Totals.TaxTotal, &order.Totals.DeliveryFee, &order.Totals.PlatformFee,
		&order.Totals.RestaurantDiscount, &order.Totals.CouponDiscount,
		&order.Totals.FinalAmount, &order.EstimatedPrepMins, &order.EstimatedDeliveryMins,
		&order.PlacedAt, &order.DeliveredAt); err != nil {
		return nil, err
	}
	if !partnerTransitionAllowed(order.Status, toStatus) {
		return nil, fmt.Errorf("invalid partner transition: %s -> %s", order.Status, toStatus)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.orders SET status = $2 WHERE id = $1
	`, orderID, toStatus); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.order_status_history (order_id, from_status, to_status, changed_by, reason)
		VALUES ($1, $2, $3, $4, $5)
	`, orderID, order.Status, toStatus, ownerID, reason); err != nil {
		return nil, err
	}
	if toStatus == "READY_FOR_PICKUP" {
		if _, err := tx.Exec(ctx, `
			UPDATE food.orders SET status = 'DELIVERY_ASSIGNING' WHERE id = $1
		`, orderID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO food.order_status_history (order_id, from_status, to_status, changed_by, reason)
			VALUES ($1, 'READY_FOR_PICKUP', 'DELIVERY_ASSIGNING', $2, 'delivery assignment started')
		`, orderID, ownerID); err != nil {
			return nil, err
		}
	}
	updated, err := s.getOrderTx(ctx, tx, order.UserID, orderID, true)
	if err != nil {
		return nil, err
	}
	if err := s.completeGenericIdempotency(ctx, tx, ownerID, idempotencyKey, 200, map[string]any{
		"order_id": orderID.String(),
		"status":   updated.Status,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *Store) UpsertDeliveryPartner(ctx context.Context, userID uuid.UUID, in DeliveryPartnerInput) (*DeliveryPartner, error) {
	if strings.TrimSpace(in.FullName) == "" || strings.TrimSpace(in.Phone) == "" {
		return nil, fmt.Errorf("full_name and phone are required")
	}
	var partner DeliveryPartner
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.delivery_partners (
			user_id, full_name, phone, email, vehicle_type, vehicle_number, city, status
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'PENDING_REVIEW')
		ON CONFLICT (user_id) DO UPDATE SET
			full_name = EXCLUDED.full_name,
			phone = EXCLUDED.phone,
			email = EXCLUDED.email,
			vehicle_type = EXCLUDED.vehicle_type,
			vehicle_number = EXCLUDED.vehicle_number,
			city = EXCLUDED.city,
			updated_at = NOW()
		RETURNING id, user_id, full_name, phone, COALESCE(email, ''), status::text,
			COALESCE(vehicle_type, ''), COALESCE(vehicle_number, ''), COALESCE(city, ''),
			is_online, created_at::text
	`, userID, in.FullName, in.Phone, in.Email, in.VehicleType, in.VehicleNumber, in.City).Scan(
		&partner.ID, &partner.UserID, &partner.FullName, &partner.Phone, &partner.Email,
		&partner.Status, &partner.VehicleType, &partner.VehicleNumber, &partner.City,
		&partner.IsOnline, &partner.CreatedAt); err != nil {
		return nil, err
	}
	return &partner, nil
}

func (s *Store) GetDeliveryPartner(ctx context.Context, userID uuid.UUID) (*DeliveryPartner, error) {
	var partner DeliveryPartner
	if err := s.db.QueryRow(ctx, `
		SELECT id, user_id, full_name, phone, COALESCE(email, ''), status::text,
			COALESCE(vehicle_type, ''), COALESCE(vehicle_number, ''), COALESCE(city, ''),
			is_online, created_at::text
		FROM food.delivery_partners
		WHERE user_id = $1
	`, userID).Scan(&partner.ID, &partner.UserID, &partner.FullName, &partner.Phone,
		&partner.Email, &partner.Status, &partner.VehicleType, &partner.VehicleNumber,
		&partner.City, &partner.IsOnline, &partner.CreatedAt); err != nil {
		return nil, err
	}
	return &partner, nil
}

func (s *Store) AddDeliveryDocument(ctx context.Context, userID uuid.UUID, input map[string]any) (map[string]any, error) {
	partner, err := s.GetDeliveryPartner(ctx, userID)
	if err != nil {
		return nil, err
	}
	documentType := stringValue(input, "document_type", "")
	if documentType == "" {
		return nil, fmt.Errorf("document_type is required")
	}
	var mediaID any
	if raw := stringValue(input, "media_id", ""); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid media_id")
		}
		mediaID = id
	}
	var id uuid.UUID
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.delivery_partner_documents (
			delivery_partner_id, document_type, document_number, media_id, file_url
		)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, partner.ID, documentType, emptyToNil(stringValue(input, "document_number", "")),
		mediaID, emptyToNil(stringValue(input, "file_url", ""))).Scan(&id); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "delivery_partner_id": partner.ID, "document_type": documentType, "status": "PENDING"}, nil
}

func (s *Store) SetDeliveryAvailability(ctx context.Context, userID uuid.UUID, online bool) (*DeliveryPartner, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var partnerID uuid.UUID
	if err := tx.QueryRow(ctx, `
		UPDATE food.delivery_partners
		SET is_online = $2, status = (CASE WHEN $2 THEN 'ACTIVE' ELSE 'OFFLINE' END)::food.delivery_partner_status
		WHERE user_id = $1 AND status IN ('APPROVED', 'ACTIVE', 'OFFLINE')
		RETURNING id
	`, userID, online).Scan(&partnerID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.delivery_partner_availability (delivery_partner_id, is_online, changed_by)
		VALUES ($1, $2, $3)
	`, partnerID, online, userID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetDeliveryPartner(ctx, userID)
}

func (s *Store) ListDeliveryAssignments(ctx context.Context, userID uuid.UUID) ([]DeliveryAssignment, error) {
	partner, err := s.GetDeliveryPartner(ctx, userID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT da.id, da.order_id, o.order_number, o.restaurant_name_snapshot,
			o.restaurant_id, da.delivery_partner_id, da.status::text, o.status::text,
			da.delivery_fee::float8, da.delivery_partner_payout::float8, da.created_at::text
		FROM food.delivery_assignments da
		JOIN food.orders o ON o.id = da.order_id
		WHERE da.delivery_partner_id = $1
			OR (da.delivery_partner_id IS NULL AND da.status = 'CREATED' AND $2 = TRUE)
		ORDER BY da.created_at DESC
		LIMIT 50
	`, partner.ID, partner.IsOnline)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var assignments []DeliveryAssignment
	for rows.Next() {
		assignment, err := scanDeliveryAssignment(rows)
		if err != nil {
			return nil, err
		}
		assignments = append(assignments, assignment)
	}
	return assignments, rows.Err()
}

func (s *Store) DeliveryUpdateAssignment(ctx context.Context, userID, assignmentID uuid.UUID, toStatus, idempotencyKey string) (*DeliveryAssignment, error) {
	partner, err := s.GetDeliveryPartner(ctx, userID)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if idempotencyKey != "" {
		_, handled, err := s.lockGenericIdempotency(ctx, tx, userID, idempotencyKey)
		if err != nil {
			return nil, err
		}
		if handled {
			return s.getAssignmentTx(ctx, tx, assignmentID)
		}
	}
	var fromStatus string
	var orderID uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT status::text, order_id
		FROM food.delivery_assignments
		WHERE id = $1 AND (delivery_partner_id = $2 OR delivery_partner_id IS NULL)
		FOR UPDATE
	`, assignmentID, partner.ID).Scan(&fromStatus, &orderID); err != nil {
		return nil, err
	}
	if !deliveryTransitionAllowed(fromStatus, toStatus) {
		return nil, fmt.Errorf("invalid delivery transition: %s -> %s", fromStatus, toStatus)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.delivery_assignments
		SET delivery_partner_id = $2, status = $3::food.assignment_status,
			accepted_at = CASE WHEN $3 = 'ACCEPTED' THEN NOW() ELSE accepted_at END,
			picked_up_at = CASE WHEN $3 = 'PICKED_UP' THEN NOW() ELSE picked_up_at END,
			delivered_at = CASE WHEN $3 = 'DELIVERED' THEN NOW() ELSE delivered_at END
		WHERE id = $1
	`, assignmentID, partner.ID, toStatus); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.delivery_tracking_events (
			assignment_id, delivery_partner_id, status, latitude, longitude, note
		)
		SELECT $1, $2, $3::food.assignment_status, current_latitude, current_longitude, 'status update'
		FROM food.delivery_partners
		WHERE id = $2
	`, assignmentID, partner.ID, toStatus); err != nil {
		return nil, err
	}
	orderStatus := map[string]string{
		"ACCEPTED":              "DELIVERY_ASSIGNED",
		"ARRIVED_AT_RESTAURANT": "DELIVERY_ASSIGNED",
		"PICKED_UP":             "PICKED_UP",
		"ARRIVED_AT_CUSTOMER":   "OUT_FOR_DELIVERY",
		"DELIVERED":             "DELIVERED",
	}[toStatus]
	if orderStatus != "" {
		if _, err := tx.Exec(ctx, `UPDATE food.orders SET status = $2::food.order_status WHERE id = $1`, orderID, orderStatus); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO food.order_status_history (order_id, from_status, to_status, changed_by, reason)
			VALUES ($1, NULL, $2, $3, 'delivery update')
		`, orderID, orderStatus, userID); err != nil {
			return nil, err
		}
	}
	if toStatus == "DELIVERED" {
		if _, err := tx.Exec(ctx, `UPDATE food.orders SET delivered_at = NOW() WHERE id = $1`, orderID); err != nil {
			return nil, err
		}
	}
	assignment, err := s.getAssignmentTx(ctx, tx, assignmentID)
	if err != nil {
		return nil, err
	}
	if err := s.completeGenericIdempotency(ctx, tx, userID, idempotencyKey, 200, map[string]any{
		"assignment_id": assignmentID.String(),
		"status":        assignment.Status,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return assignment, nil
}

func (s *Store) GetCurrentDeliveryAssignment(ctx context.Context, userID uuid.UUID) (*DeliveryAssignment, error) {
	partner, err := s.GetDeliveryPartner(ctx, userID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT da.id, da.order_id, o.order_number, o.restaurant_name_snapshot,
			o.restaurant_id, da.delivery_partner_id, da.status::text, o.status::text,
			da.delivery_fee::float8, da.delivery_partner_payout::float8, da.created_at::text
		FROM food.delivery_assignments da
		JOIN food.orders o ON o.id = da.order_id
		WHERE da.delivery_partner_id = $1
			AND da.status NOT IN ('DELIVERED', 'FAILED', 'CANCELLED', 'REJECTED')
		ORDER BY da.created_at DESC
		LIMIT 1
	`, partner.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, pgx.ErrNoRows
	}
	assignment, err := scanDeliveryAssignment(rows)
	if err != nil {
		return nil, err
	}
	return &assignment, rows.Err()
}

func (s *Store) DeliveryEarnings(ctx context.Context, userID uuid.UUID) (map[string]any, error) {
	partner, err := s.GetDeliveryPartner(ctx, userID)
	if err != nil {
		return nil, err
	}
	var todayCount, totalCount int
	var todayAmount, totalAmount float64
	if err := s.db.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE delivered_at::date = CURRENT_DATE),
			COALESCE(SUM(delivery_partner_payout) FILTER (WHERE delivered_at::date = CURRENT_DATE), 0)::float8,
			COUNT(*) FILTER (WHERE status = 'DELIVERED'),
			COALESCE(SUM(delivery_partner_payout) FILTER (WHERE status = 'DELIVERED'), 0)::float8
		FROM food.delivery_assignments
		WHERE delivery_partner_id = $1
	`, partner.ID).Scan(&todayCount, &todayAmount, &totalCount, &totalAmount); err != nil {
		return nil, err
	}
	return map[string]any{
		"deliveries_today": todayCount,
		"earnings_today":   todayAmount,
		"total_deliveries": totalCount,
		"total_earnings":   totalAmount,
	}, nil
}

func (s *Store) DeliveryHistory(ctx context.Context, userID uuid.UUID) ([]DeliveryAssignment, error) {
	partner, err := s.GetDeliveryPartner(ctx, userID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT da.id, da.order_id, o.order_number, o.restaurant_name_snapshot,
			o.restaurant_id, da.delivery_partner_id, da.status::text, o.status::text,
			da.delivery_fee::float8, da.delivery_partner_payout::float8, da.created_at::text
		FROM food.delivery_assignments da
		JOIN food.orders o ON o.id = da.order_id
		WHERE da.delivery_partner_id = $1
			AND da.status IN ('DELIVERED', 'FAILED', 'CANCELLED', 'REJECTED')
		ORDER BY da.created_at DESC
		LIMIT 100
	`, partner.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var assignments []DeliveryAssignment
	for rows.Next() {
		assignment, err := scanDeliveryAssignment(rows)
		if err != nil {
			return nil, err
		}
		assignments = append(assignments, assignment)
	}
	return assignments, rows.Err()
}

func (s *Store) AdminDashboard(ctx context.Context) (*AdminDashboard, error) {
	var d AdminDashboard
	if err := s.db.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM food.orders WHERE placed_at::date = CURRENT_DATE),
			(SELECT COALESCE(SUM(final_amount), 0)::float8 FROM food.orders WHERE placed_at::date = CURRENT_DATE),
			(SELECT COUNT(*) FROM food.orders WHERE placed_at::date = CURRENT_DATE AND status::text LIKE 'CANCELLED%'),
			(SELECT COUNT(*) FROM food.restaurants WHERE status = 'ACTIVE'),
			(SELECT COUNT(*) FROM food.restaurants WHERE status = 'PENDING_REVIEW'),
			(SELECT COUNT(*) FROM food.delivery_partners WHERE status = 'PENDING_REVIEW'),
			(SELECT COUNT(*) FROM food.delivery_partners WHERE is_online = TRUE)
	`).Scan(&d.TotalOrdersToday, &d.GMVToday, &d.CancelledOrdersToday,
		&d.ActiveRestaurants, &d.PendingRestaurants, &d.PendingDeliveryPartners,
		&d.OnlineDeliveryPartners); err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Store) AdminPendingRestaurants(ctx context.Context) ([]PartnerRestaurant, error) {
	return s.adminRestaurantsByStatus(ctx, "PENDING_REVIEW")
}

func (s *Store) AdminApproveRestaurant(ctx context.Context, adminID, restaurantID uuid.UUID, approve bool, reason string) error {
	status := "ACTIVE"
	partnerStatus := "APPROVED"
	if !approve {
		status = "REJECTED"
		partnerStatus = "REJECTED"
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var partnerID uuid.UUID
	if err := tx.QueryRow(ctx, `
		UPDATE food.restaurants
		SET status = $2::food.restaurant_status, is_open = $3, is_accepting_orders = $3,
			approved_by = CASE WHEN $3 THEN $4::uuid ELSE approved_by END,
			approved_at = CASE WHEN $3 THEN NOW() ELSE approved_at END,
			rejection_reason = CASE WHEN $3 THEN NULL ELSE $5 END
		WHERE id = $1
		RETURNING partner_id
	`, restaurantID, status, approve, adminID, reason).Scan(&partnerID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.restaurant_partners
		SET status = $2::food.partner_status, approved_by = CASE WHEN $2 = 'APPROVED' THEN $3::uuid ELSE approved_by END,
			approved_at = CASE WHEN $2 = 'APPROVED' THEN NOW() ELSE approved_at END,
			rejection_reason = CASE WHEN $2 = 'APPROVED' THEN NULL ELSE $4 END
		WHERE id = $1
	`, partnerID, partnerStatus, adminID, reason); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.admin_audit_logs (actor_user_id, action, entity_type, entity_id, new_value)
		VALUES ($1, $2::text, 'restaurant', $3, jsonb_build_object('status', $4::text, 'reason', $5::text))
	`, adminID, "restaurant.review", restaurantID, status, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) AdminSetRestaurantStatus(ctx context.Context, adminID, restaurantID uuid.UUID, status, reason string) error {
	allowed := map[string]bool{
		"DRAFT": true, "PENDING_REVIEW": true, "APPROVED": true, "REJECTED": true,
		"ACTIVE": true, "INACTIVE": true, "TEMP_CLOSED": true, "SUSPENDED": true, "CLOSED": true,
	}
	if !allowed[status] {
		return fmt.Errorf("invalid restaurant status")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE food.restaurants
		SET status = $2::food.restaurant_status,
			is_open = CASE WHEN $2 = 'ACTIVE' THEN TRUE ELSE FALSE END,
			is_accepting_orders = CASE WHEN $2 = 'ACTIVE' THEN TRUE ELSE FALSE END,
			rejection_reason = CASE WHEN $2 = 'REJECTED' THEN $3 ELSE rejection_reason END
		WHERE id = $1
	`, restaurantID, status, reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.admin_audit_logs (actor_user_id, action, entity_type, entity_id, new_value)
		VALUES ($1, 'restaurant.status_changed', 'restaurant', $2, jsonb_build_object('status', $3::text, 'reason', $4::text))
	`, adminID, restaurantID, status, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) AdminPendingDeliveryPartners(ctx context.Context) ([]DeliveryPartner, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, full_name, phone, COALESCE(email, ''), status::text,
			COALESCE(vehicle_type, ''), COALESCE(vehicle_number, ''), COALESCE(city, ''),
			is_online, created_at::text
		FROM food.delivery_partners
		WHERE status = 'PENDING_REVIEW'
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var partners []DeliveryPartner
	for rows.Next() {
		partner, err := scanDeliveryPartner(rows)
		if err != nil {
			return nil, err
		}
		partners = append(partners, partner)
	}
	return partners, rows.Err()
}

func (s *Store) AdminApproveDeliveryPartner(ctx context.Context, adminID, partnerID uuid.UUID, approve bool, reason string) error {
	status := "APPROVED"
	if !approve {
		status = "REJECTED"
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE food.delivery_partners
		SET status = $2::food.delivery_partner_status,
			approved_by = CASE WHEN $2 = 'APPROVED' THEN $3::uuid ELSE approved_by END,
			approved_at = CASE WHEN $2 = 'APPROVED' THEN NOW() ELSE approved_at END,
			rejection_reason = CASE WHEN $2 = 'APPROVED' THEN NULL ELSE $4 END
		WHERE id = $1
	`, partnerID, status, adminID, reason); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.admin_audit_logs (actor_user_id, action, entity_type, entity_id, new_value)
		VALUES ($1, 'delivery_partner.review', 'delivery_partner', $2, jsonb_build_object('status', $3::text, 'reason', $4::text))
	`, adminID, partnerID, status, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) AdminSetDeliveryPartnerStatus(ctx context.Context, adminID, partnerID uuid.UUID, status, reason string) error {
	allowed := map[string]bool{
		"DRAFT": true, "PENDING_REVIEW": true, "APPROVED": true, "REJECTED": true,
		"ACTIVE": true, "OFFLINE": true, "SUSPENDED": true, "CLOSED": true,
	}
	if !allowed[status] {
		return fmt.Errorf("invalid delivery partner status")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE food.delivery_partners
		SET status = $2::food.delivery_partner_status,
			is_online = CASE WHEN $2 = 'ACTIVE' THEN is_online ELSE FALSE END,
			rejection_reason = CASE WHEN $2 = 'REJECTED' THEN $3 ELSE rejection_reason END
		WHERE id = $1
	`, partnerID, status, reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.admin_audit_logs (actor_user_id, action, entity_type, entity_id, new_value)
		VALUES ($1, 'delivery_partner.status_changed', 'delivery_partner', $2, jsonb_build_object('status', $3::text, 'reason', $4::text))
	`, adminID, partnerID, status, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) AdminListOrders(ctx context.Context, page Pagination) ([]Order, error) {
	return s.listOrdersByWherePage(ctx, "TRUE", nil, page)
}

func (s *Store) AdminGetOrder(ctx context.Context, orderID uuid.UUID) (*Order, error) {
	var userID uuid.UUID
	if err := s.db.QueryRow(ctx, `SELECT user_id FROM food.orders WHERE id = $1`, orderID).Scan(&userID); err != nil {
		return nil, err
	}
	return s.getOrder(ctx, s.db, userID, orderID, true)
}

func (s *Store) AdminCancelOrder(ctx context.Context, adminID, orderID uuid.UUID, reason string) (*Order, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var userID uuid.UUID
	var fromStatus string
	if err := tx.QueryRow(ctx, `
		SELECT user_id, status::text
		FROM food.orders
		WHERE id = $1
		FOR UPDATE
	`, orderID).Scan(&userID, &fromStatus); err != nil {
		return nil, err
	}
	if fromStatus == "DELIVERED" || strings.HasPrefix(fromStatus, "CANCELLED") || fromStatus == "REFUNDED" {
		return nil, fmt.Errorf("order cannot be cancelled from %s", fromStatus)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.orders
		SET status = 'CANCELLED_BY_ADMIN',
			cancellation_reason = $3,
			cancelled_by = $2,
			cancelled_at = NOW()
		WHERE id = $1
	`, orderID, adminID, reason); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.order_status_history (order_id, from_status, to_status, changed_by, reason)
		VALUES ($1, $2, 'CANCELLED_BY_ADMIN', $3, $4)
	`, orderID, fromStatus, adminID, reason); err != nil {
		return nil, err
	}
	order, err := s.getOrderTx(ctx, tx, userID, orderID, true)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return order, nil
}

func (s *Store) AdminRefundOrder(ctx context.Context, adminID, orderID uuid.UUID, reason string, amount float64, idempotencyKey string) (map[string]any, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if idempotencyKey != "" {
		body, handled, err := s.lockGenericIdempotency(ctx, tx, adminID, idempotencyKey)
		if err != nil {
			return nil, err
		}
		if handled {
			return body, nil
		}
	}
	var paymentID uuid.UUID
	var finalAmount float64
	var fromStatus string
	if err := tx.QueryRow(ctx, `
		SELECT p.id, o.final_amount::float8, o.status::text
		FROM food.orders o
		LEFT JOIN food.payments p ON p.order_id = o.id
		WHERE o.id = $1
		ORDER BY p.created_at DESC
		LIMIT 1
	`, orderID).Scan(&paymentID, &finalAmount, &fromStatus); err != nil {
		return nil, err
	}
	if amount <= 0 || amount > finalAmount {
		amount = finalAmount
	}
	if fromStatus == "REFUNDED" {
		existing, err := s.latestRefundTx(ctx, tx, orderID)
		if err != nil {
			return nil, err
		}
		if err := s.completeGenericIdempotency(ctx, tx, adminID, idempotencyKey, 200, existing); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return existing, nil
	}
	var refundID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO food.refunds (order_id, payment_id, amount, reason, requested_by, processed_by, processed_at, status)
		VALUES ($1, $2, $3, $4, $5, $5, NOW(), 'PROCESSED')
		RETURNING id
	`, orderID, paymentID, amount, reason, adminID).Scan(&refundID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.orders SET status = 'REFUNDED', payment_status = 'REFUNDED' WHERE id = $1
	`, orderID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.payments SET status = 'REFUNDED' WHERE id = $1
	`, paymentID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.order_status_history (order_id, from_status, to_status, changed_by, reason)
		VALUES ($1, $2, 'REFUNDED', $3, $4)
	`, orderID, fromStatus, adminID, reason); err != nil {
		return nil, err
	}
	refund := map[string]any{"id": refundID, "order_id": orderID, "amount": amount, "status": "PROCESSED"}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.admin_audit_logs (actor_user_id, action, entity_type, entity_id, new_value)
		VALUES ($1, 'order.refund', 'order', $2, jsonb_build_object('amount', $3::numeric, 'reason', $4::text))
	`, adminID, orderID, amount, reason); err != nil {
		return nil, err
	}
	if err := s.completeGenericIdempotency(ctx, tx, adminID, idempotencyKey, 200, refund); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return refund, nil
}

func (s *Store) AdminListCoupons(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id::text, code, title, coupon_type::text, discount_value::float8,
			COALESCE(max_discount_amount, 0)::float8, min_order_amount::float8,
			is_active, starts_at::text, ends_at::text, used_count
		FROM food.coupons
		ORDER BY created_at DESC
		LIMIT 50
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var coupons []map[string]any
	for rows.Next() {
		var id, code, title, couponType, startsAt, endsAt string
		var discount, maxDiscount, minOrder float64
		var active bool
		var used int
		if err := rows.Scan(&id, &code, &title, &couponType, &discount, &maxDiscount, &minOrder, &active, &startsAt, &endsAt, &used); err != nil {
			return nil, err
		}
		coupons = append(coupons, map[string]any{
			"id": id, "code": code, "title": title, "coupon_type": couponType,
			"discount_value": discount, "max_discount_amount": maxDiscount,
			"min_order_amount": minOrder, "is_active": active,
			"starts_at": startsAt, "ends_at": endsAt, "used_count": used,
		})
	}
	return coupons, rows.Err()
}

func (s *Store) AdminCreateCoupon(ctx context.Context, adminID uuid.UUID, input map[string]any) (map[string]any, error) {
	code, _ := input["code"].(string)
	title, _ := input["title"].(string)
	couponType, _ := input["coupon_type"].(string)
	if couponType == "" {
		couponType = "FLAT"
	}
	discount := number(input["discount_value"], 50)
	minOrder := number(input["min_order_amount"], 199)
	maxDiscount := number(input["max_discount_amount"], discount)
	var id uuid.UUID
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.coupons (
			code, title, coupon_type, discount_value, max_discount_amount,
			min_order_amount, starts_at, ends_at, created_by
		)
		VALUES ($1,$2,$3,$4,$5,$6,NOW(),NOW() + INTERVAL '90 days',$7)
		RETURNING id
	`, strings.ToUpper(code), title, couponType, discount, maxDiscount, minOrder, adminID).Scan(&id); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "code": strings.ToUpper(code), "title": title}, nil
}

func (s *Store) AdminUpdateCoupon(ctx context.Context, adminID, couponID uuid.UUID, input map[string]any) (map[string]any, error) {
	code := strings.ToUpper(stringValue(input, "code", ""))
	title := stringValue(input, "title", "")
	couponType := stringValue(input, "coupon_type", "FLAT")
	discount := number(input["discount_value"], 50)
	minOrder := number(input["min_order_amount"], 199)
	maxDiscount := number(input["max_discount_amount"], discount)
	active := boolValue(input, "is_active", true)
	tag, err := s.db.Exec(ctx, `
		UPDATE food.coupons
		SET code = COALESCE(NULLIF($3, ''), code),
			title = COALESCE(NULLIF($4, ''), title),
			coupon_type = $5,
			discount_value = $6,
			max_discount_amount = $7,
			min_order_amount = $8,
			is_active = $9,
			created_by = COALESCE(created_by, $2)
		WHERE id = $1
	`, couponID, adminID, code, title, couponType, discount, maxDiscount, minOrder, active)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, pgx.ErrNoRows
	}
	return map[string]any{"id": couponID, "code": code, "title": title, "is_active": active}, nil
}

func (s *Store) AdminListServiceAreas(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id::text, name, city, COALESCE(state, ''), country, COALESCE(postal_code, ''),
			COALESCE(center_latitude, 0)::float8, COALESCE(center_longitude, 0)::float8,
			radius_km::float8, is_active, created_at::text
		FROM food.service_areas
		ORDER BY city, name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var areas []map[string]any
	for rows.Next() {
		var id, name, city, state, country, postalCode, createdAt string
		var lat, lng, radius float64
		var active bool
		if err := rows.Scan(&id, &name, &city, &state, &country, &postalCode, &lat, &lng, &radius, &active, &createdAt); err != nil {
			return nil, err
		}
		areas = append(areas, map[string]any{
			"id": id, "name": name, "city": city, "state": state, "country": country,
			"postal_code": postalCode, "center_latitude": lat, "center_longitude": lng,
			"radius_km": radius, "is_active": active, "created_at": createdAt,
		})
	}
	return areas, rows.Err()
}

func (s *Store) AdminCreateServiceArea(ctx context.Context, adminID uuid.UUID, input map[string]any) (map[string]any, error) {
	name := stringValue(input, "name", "")
	city := stringValue(input, "city", "")
	if name == "" || city == "" {
		return nil, fmt.Errorf("name and city are required")
	}
	var id uuid.UUID
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.service_areas (
			name, city, state, country, postal_code, center_latitude,
			center_longitude, radius_km, is_active, created_by
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id
	`, name, city, stringValue(input, "state", ""), stringValue(input, "country", "India"),
		stringValue(input, "postal_code", ""), numberPtr(input, "center_latitude"),
		numberPtr(input, "center_longitude"), number(input["radius_km"], 8),
		boolValue(input, "is_active", true), adminID).Scan(&id); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "name": name, "city": city}, nil
}

func (s *Store) AdminUpdateServiceArea(ctx context.Context, adminID, areaID uuid.UUID, input map[string]any) (map[string]any, error) {
	name := stringValue(input, "name", "")
	city := stringValue(input, "city", "")
	tag, err := s.db.Exec(ctx, `
		UPDATE food.service_areas
		SET name = COALESCE(NULLIF($3, ''), name),
			city = COALESCE(NULLIF($4, ''), city),
			state = $5,
			country = COALESCE(NULLIF($6, ''), country),
			postal_code = $7,
			center_latitude = $8,
			center_longitude = $9,
			radius_km = $10,
			is_active = $11,
			created_by = COALESCE(created_by, $2)
		WHERE id = $1
	`, areaID, adminID, name, city, stringValue(input, "state", ""),
		stringValue(input, "country", "India"), stringValue(input, "postal_code", ""),
		numberPtr(input, "center_latitude"), numberPtr(input, "center_longitude"),
		number(input["radius_km"], 8), boolValue(input, "is_active", true))
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, pgx.ErrNoRows
	}
	return map[string]any{"id": areaID, "name": name, "city": city}, nil
}

func (s *Store) AdminListRestaurantSettlements(ctx context.Context, page Pagination) ([]map[string]any, error) {
	page = normalizePagination(page)
	rows, err := s.db.Query(ctx, `
		SELECT rs.id::text, rs.restaurant_id::text, r.name, rs.period_start::text,
			rs.period_end::text, rs.gross_order_amount::float8, rs.commission_amount::float8,
			rs.refund_adjustment::float8, rs.penalty_amount::float8, rs.payout_amount::float8,
			rs.status::text, COALESCE(rs.paid_reference, ''), COALESCE(rs.paid_at::text, ''),
			rs.created_at::text
		FROM food.restaurant_settlements rs
		JOIN food.restaurants r ON r.id = rs.restaurant_id
		ORDER BY rs.created_at DESC
		LIMIT $1 OFFSET $2
	`, page.Limit, page.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var settlements []map[string]any
	for rows.Next() {
		var id, restaurantID, restaurantName, start, end, status, ref, paidAt, createdAt string
		var gross, commission, refund, penalty, payout float64
		if err := rows.Scan(&id, &restaurantID, &restaurantName, &start, &end, &gross, &commission, &refund, &penalty, &payout, &status, &ref, &paidAt, &createdAt); err != nil {
			return nil, err
		}
		settlements = append(settlements, map[string]any{
			"id": id, "restaurant_id": restaurantID, "restaurant_name": restaurantName,
			"period_start": start, "period_end": end, "gross_order_amount": gross,
			"commission_amount": commission, "refund_adjustment": refund,
			"penalty_amount": penalty, "payout_amount": payout, "status": status,
			"paid_reference": ref, "paid_at": paidAt, "created_at": createdAt,
		})
	}
	return settlements, rows.Err()
}

func (s *Store) AdminMarkRestaurantSettlementPaid(ctx context.Context, adminID, settlementID uuid.UUID, reference string) (map[string]any, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE food.restaurant_settlements
		SET status = 'PAID', paid_reference = $3::text, paid_at = NOW(), created_by = COALESCE(created_by, $2)
		WHERE id = $1
	`, settlementID, adminID, reference)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, pgx.ErrNoRows
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.admin_audit_logs (actor_user_id, action, entity_type, entity_id, new_value)
		VALUES ($1, 'restaurant_settlement.mark_paid', 'restaurant_settlement', $2, jsonb_build_object('status', 'PAID', 'reference', $3::text))
	`, adminID, settlementID, reference); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return map[string]any{"id": settlementID, "status": "PAID", "paid_reference": reference}, nil
}

func (s *Store) AdminGenerateSettlements(ctx context.Context, adminID uuid.UUID, in SettlementGenerateInput) (map[string]any, error) {
	start, err := time.Parse("2006-01-02", strings.TrimSpace(in.PeriodStart))
	if err != nil {
		return nil, fmt.Errorf("period_start must be YYYY-MM-DD")
	}
	end, err := time.Parse("2006-01-02", strings.TrimSpace(in.PeriodEnd))
	if err != nil {
		return nil, fmt.Errorf("period_end must be YYYY-MM-DD")
	}
	if end.Before(start) {
		return nil, fmt.Errorf("period_end must be on or after period_start")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if in.IdempotencyKey != "" {
		body, handled, err := s.lockGenericIdempotency(ctx, tx, adminID, in.IdempotencyKey)
		if err != nil {
			return nil, err
		}
		if handled {
			return body, nil
		}
	}

	restaurantRows, err := tx.Query(ctx, `
		WITH refunds_by_order AS (
			SELECT order_id, COALESCE(SUM(amount), 0)::float8 AS refund_amount
			FROM food.refunds
			WHERE status = 'PROCESSED'
			GROUP BY order_id
		)
		SELECT o.restaurant_id,
			COALESCE(SUM(o.final_amount), 0)::float8,
			COALESCE(SUM(o.commission_amount), 0)::float8,
			COALESCE(SUM(rbo.refund_amount), 0)::float8
		FROM food.orders o
		LEFT JOIN refunds_by_order rbo ON rbo.order_id = o.id
		WHERE o.status IN ('DELIVERED', 'REFUNDED')
			AND o.placed_at::date BETWEEN $1::date AND $2::date
			AND ($3::uuid IS NULL OR o.restaurant_id = $3)
		GROUP BY o.restaurant_id
	`, start.Format("2006-01-02"), end.Format("2006-01-02"), in.RestaurantID)
	if err != nil {
		return nil, err
	}
	type restaurantSettlementAggregate struct {
		restaurantID uuid.UUID
		gross        float64
		commission   float64
		refund       float64
	}
	restaurantAggregates := []restaurantSettlementAggregate{}
	for restaurantRows.Next() {
		var aggregate restaurantSettlementAggregate
		if err := restaurantRows.Scan(&aggregate.restaurantID, &aggregate.gross, &aggregate.commission, &aggregate.refund); err != nil {
			restaurantRows.Close()
			return nil, err
		}
		restaurantAggregates = append(restaurantAggregates, aggregate)
	}
	restaurantRows.Close()
	if err := restaurantRows.Err(); err != nil {
		return nil, err
	}
	restaurantItems := []map[string]any{}
	for _, aggregate := range restaurantAggregates {
		payout := roundMoney(aggregate.gross - aggregate.commission - aggregate.refund)
		item, err := s.upsertRestaurantSettlementTx(ctx, tx, adminID, aggregate.restaurantID, start, end, aggregate.gross, aggregate.commission, aggregate.refund, payout)
		if err != nil {
			return nil, err
		}
		restaurantItems = append(restaurantItems, item)
	}

	deliveryRows, err := tx.Query(ctx, `
		SELECT da.delivery_partner_id,
			COUNT(*)::int,
			COALESCE(SUM(da.delivery_partner_payout), 0)::float8
		FROM food.delivery_assignments da
		WHERE da.status = 'DELIVERED'
			AND da.delivery_partner_id IS NOT NULL
			AND da.delivered_at::date BETWEEN $1::date AND $2::date
			AND ($3::uuid IS NULL OR da.delivery_partner_id = $3)
		GROUP BY da.delivery_partner_id
	`, start.Format("2006-01-02"), end.Format("2006-01-02"), in.DeliveryPartnerID)
	if err != nil {
		return nil, err
	}
	type deliverySettlementAggregate struct {
		partnerID uuid.UUID
		count     int
		gross     float64
	}
	deliveryAggregates := []deliverySettlementAggregate{}
	for deliveryRows.Next() {
		var aggregate deliverySettlementAggregate
		if err := deliveryRows.Scan(&aggregate.partnerID, &aggregate.count, &aggregate.gross); err != nil {
			deliveryRows.Close()
			return nil, err
		}
		deliveryAggregates = append(deliveryAggregates, aggregate)
	}
	deliveryRows.Close()
	if err := deliveryRows.Err(); err != nil {
		return nil, err
	}
	deliveryItems := []map[string]any{}
	for _, aggregate := range deliveryAggregates {
		item, err := s.upsertDeliverySettlementTx(ctx, tx, adminID, aggregate.partnerID, start, end, aggregate.count, aggregate.gross, aggregate.gross)
		if err != nil {
			return nil, err
		}
		deliveryItems = append(deliveryItems, item)
	}

	result := map[string]any{
		"period_start":           start.Format("2006-01-02"),
		"period_end":             end.Format("2006-01-02"),
		"restaurant_settlements": restaurantItems,
		"delivery_settlements":   deliveryItems,
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO food.admin_audit_logs (actor_user_id, action, entity_type, new_value)
		VALUES ($1, 'settlements.generate', 'settlement_batch',
			jsonb_build_object('period_start', $2::date, 'period_end', $3::date, 'restaurant_count', $4::integer, 'delivery_count', $5::integer))
	`, adminID, start.Format("2006-01-02"), end.Format("2006-01-02"), len(restaurantItems), len(deliveryItems)); err != nil {
		return nil, err
	}
	if err := s.completeGenericIdempotency(ctx, tx, adminID, in.IdempotencyKey, 200, result); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) AdminOrderReport(ctx context.Context) (map[string]any, error) {
	rows, err := s.db.Query(ctx, `
		SELECT status::text, COUNT(*)::int, COALESCE(SUM(final_amount), 0)::float8
		FROM food.orders
		GROUP BY status
		ORDER BY status
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var status string
		var count int
		var amount float64
		if err := rows.Scan(&status, &count, &amount); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"status": status, "count": count, "amount": amount})
	}
	return map[string]any{"items": items}, rows.Err()
}

func (s *Store) AdminRevenueReport(ctx context.Context) (map[string]any, error) {
	var gmv, commission, refunds float64
	var orders int
	if err := s.db.QueryRow(ctx, `
		SELECT COUNT(*)::int,
			COALESCE(SUM(final_amount), 0)::float8,
			COALESCE(SUM(commission_amount), 0)::float8,
			COALESCE((SELECT SUM(amount) FROM food.refunds), 0)::float8
		FROM food.orders
	`).Scan(&orders, &gmv, &commission, &refunds); err != nil {
		return nil, err
	}
	return map[string]any{
		"orders":      orders,
		"gmv":         gmv,
		"commission":  commission,
		"refunds":     refunds,
		"net_revenue": roundMoney(commission - refunds),
	}, nil
}

func (s *Store) requireRestaurantOwner(ctx context.Context, ownerID, restaurantID uuid.UUID) error {
	var exists bool
	if err := s.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM food.restaurants WHERE owner_user_id = $1 AND id = $2)
	`, ownerID, restaurantID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return pgx.ErrNoRows
	}
	return nil
}

func scanPartnerRestaurant(rows pgx.Rows) (PartnerRestaurant, error) {
	var restaurant PartnerRestaurant
	err := rows.Scan(&restaurant.ID, &restaurant.PartnerID, &restaurant.OwnerUserID,
		&restaurant.Name, &restaurant.Slug, &restaurant.Description, &restaurant.Status,
		&restaurant.IsOpen, &restaurant.IsAcceptingOrders, &restaurant.City,
		&restaurant.State, &restaurant.MinOrderAmount, &restaurant.PackagingFee,
		&restaurant.CreatedAt)
	return restaurant, err
}

func scanDeliveryPartner(rows pgx.Rows) (DeliveryPartner, error) {
	var partner DeliveryPartner
	err := rows.Scan(&partner.ID, &partner.UserID, &partner.FullName, &partner.Phone,
		&partner.Email, &partner.Status, &partner.VehicleType, &partner.VehicleNumber,
		&partner.City, &partner.IsOnline, &partner.CreatedAt)
	return partner, err
}

func scanDeliveryAssignment(rows pgx.Rows) (DeliveryAssignment, error) {
	var assignment DeliveryAssignment
	err := rows.Scan(&assignment.ID, &assignment.OrderID, &assignment.OrderNumber,
		&assignment.RestaurantName, &assignment.RestaurantID, &assignment.DeliveryPartnerID,
		&assignment.Status, &assignment.OrderStatus, &assignment.DeliveryFee,
		&assignment.DeliveryPartnerPayout, &assignment.CreatedAt)
	return assignment, err
}

func (s *Store) getAssignmentTx(ctx context.Context, tx pgx.Tx, assignmentID uuid.UUID) (*DeliveryAssignment, error) {
	rows, err := tx.Query(ctx, `
		SELECT da.id, da.order_id, o.order_number, o.restaurant_name_snapshot,
			o.restaurant_id, da.delivery_partner_id, da.status::text, o.status::text,
			da.delivery_fee::float8, da.delivery_partner_payout::float8, da.created_at::text
		FROM food.delivery_assignments da
		JOIN food.orders o ON o.id = da.order_id
		WHERE da.id = $1
	`, assignmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, pgx.ErrNoRows
	}
	assignment, err := scanDeliveryAssignment(rows)
	if err != nil {
		return nil, err
	}
	return &assignment, rows.Err()
}

func (s *Store) adminRestaurantsByStatus(ctx context.Context, status string) ([]PartnerRestaurant, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, partner_id, owner_user_id, name, slug, COALESCE(description, ''),
			status::text, is_open, is_accepting_orders, city, COALESCE(state, ''),
			min_order_amount::float8, packaging_fee::float8, created_at::text
		FROM food.restaurants
		WHERE status = $1
		ORDER BY created_at DESC
	`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var restaurants []PartnerRestaurant
	for rows.Next() {
		restaurant, err := scanPartnerRestaurant(rows)
		if err != nil {
			return nil, err
		}
		restaurants = append(restaurants, restaurant)
	}
	return restaurants, rows.Err()
}

func (s *Store) listOrdersByWhere(ctx context.Context, clause string, arg any) ([]Order, error) {
	return s.listOrdersByWherePage(ctx, clause, arg, Pagination{Limit: 100})
}

func (s *Store) listOrdersByWherePage(ctx context.Context, clause string, arg any, page Pagination) ([]Order, error) {
	page = normalizePagination(page)
	args := []any{}
	if arg != nil {
		args = append(args, arg)
	}
	args = append(args, page.Limit, page.Offset)
	limitArg := len(args) - 1
	offsetArg := len(args)
	rows, err := s.db.Query(ctx, fmt.Sprintf(`
		SELECT id, order_number, user_id, restaurant_id, restaurant_name_snapshot,
			status::text, payment_status::text, payment_method::text,
			item_subtotal::float8, addon_total::float8, packaging_fee::float8,
			tax_total::float8, delivery_fee::float8, platform_fee::float8,
			restaurant_discount::float8, coupon_discount::float8, final_amount::float8,
			COALESCE(estimated_preparation_minutes, 0),
			COALESCE(estimated_delivery_minutes, 0),
			placed_at::text, COALESCE(delivered_at::text, '')
		FROM food.orders
		WHERE %s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d
	`, clause, limitArg, offsetArg), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orders []Order
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	return orders, rows.Err()
}

func (s *Store) latestRefundTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) (map[string]any, error) {
	var id string
	var amount float64
	var status string
	if err := tx.QueryRow(ctx, `
		SELECT id::text, amount::float8, status
		FROM food.refunds
		WHERE order_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, orderID).Scan(&id, &amount, &status); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "order_id": orderID.String(), "amount": amount, "status": status}, nil
}

func (s *Store) upsertRestaurantSettlementTx(ctx context.Context, tx pgx.Tx, adminID, restaurantID uuid.UUID, start, end time.Time, gross, commission, refund, payout float64) (map[string]any, error) {
	startDate := start.Format("2006-01-02")
	endDate := end.Format("2006-01-02")
	var id, status string
	err := tx.QueryRow(ctx, `
		UPDATE food.restaurant_settlements
		SET gross_order_amount = $4::numeric,
			commission_amount = $5::numeric,
			refund_adjustment = $6::numeric,
			payout_amount = $7::numeric,
			created_by = COALESCE(created_by, $8)
		WHERE restaurant_id = $1
			AND period_start = $2::date
			AND period_end = $3::date
			AND status <> 'PAID'
		RETURNING id::text, status::text
	`, restaurantID, startDate, endDate, gross, commission, refund, payout, adminID).Scan(&id, &status)
	if err == pgx.ErrNoRows {
		err = tx.QueryRow(ctx, `
			INSERT INTO food.restaurant_settlements (
				restaurant_id, period_start, period_end, gross_order_amount,
				commission_amount, refund_adjustment, payout_amount, created_by
			)
			SELECT $1, $2::date, $3::date, $4::numeric, $5::numeric, $6::numeric, $7::numeric, $8
			WHERE NOT EXISTS (
				SELECT 1 FROM food.restaurant_settlements
				WHERE restaurant_id = $1 AND period_start = $2::date AND period_end = $3::date
			)
			RETURNING id::text, status::text
		`, restaurantID, startDate, endDate, gross, commission, refund, payout, adminID).Scan(&id, &status)
	}
	if err == pgx.ErrNoRows {
		err = tx.QueryRow(ctx, `
			SELECT id::text, status::text
			FROM food.restaurant_settlements
			WHERE restaurant_id = $1 AND period_start = $2::date AND period_end = $3::date
			ORDER BY created_at DESC
			LIMIT 1
		`, restaurantID, startDate, endDate).Scan(&id, &status)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id": id, "restaurant_id": restaurantID.String(), "period_start": startDate,
		"period_end": endDate, "gross_amount": gross, "commission": commission,
		"refund_adjustment": refund, "payout_amount": payout, "status": status,
	}, nil
}

func (s *Store) upsertDeliverySettlementTx(ctx context.Context, tx pgx.Tx, adminID, partnerID uuid.UUID, start, end time.Time, count int, gross, payout float64) (map[string]any, error) {
	startDate := start.Format("2006-01-02")
	endDate := end.Format("2006-01-02")
	var id, status string
	err := tx.QueryRow(ctx, `
		UPDATE food.delivery_partner_settlements
		SET delivery_count = $4::integer,
			gross_earning_amount = $5::numeric,
			payout_amount = $6::numeric,
			created_by = COALESCE(created_by, $7)
		WHERE delivery_partner_id = $1
			AND period_start = $2::date
			AND period_end = $3::date
			AND status <> 'PAID'
		RETURNING id::text, status::text
	`, partnerID, startDate, endDate, count, gross, payout, adminID).Scan(&id, &status)
	if err == pgx.ErrNoRows {
		err = tx.QueryRow(ctx, `
			INSERT INTO food.delivery_partner_settlements (
				delivery_partner_id, period_start, period_end, delivery_count,
				gross_earning_amount, payout_amount, created_by
			)
			SELECT $1, $2::date, $3::date, $4::integer, $5::numeric, $6::numeric, $7
			WHERE NOT EXISTS (
				SELECT 1 FROM food.delivery_partner_settlements
				WHERE delivery_partner_id = $1 AND period_start = $2::date AND period_end = $3::date
			)
			RETURNING id::text, status::text
		`, partnerID, startDate, endDate, count, gross, payout, adminID).Scan(&id, &status)
	}
	if err == pgx.ErrNoRows {
		err = tx.QueryRow(ctx, `
			SELECT id::text, status::text
			FROM food.delivery_partner_settlements
			WHERE delivery_partner_id = $1 AND period_start = $2::date AND period_end = $3::date
			ORDER BY created_at DESC
			LIMIT 1
		`, partnerID, startDate, endDate).Scan(&id, &status)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id": id, "delivery_partner_id": partnerID.String(), "period_start": startDate,
		"period_end": endDate, "delivery_count": count, "gross_amount": gross,
		"incentive_amount": 0, "penalty_amount": 0, "payout_amount": payout, "status": status,
	}, nil
}

func normalizePagination(page Pagination) Pagination {
	if page.Limit <= 0 {
		page.Limit = 50
	}
	if page.Limit > 100 {
		page.Limit = 100
	}
	if page.Offset < 0 {
		page.Offset = 0
	}
	return page
}

func partnerTransitionAllowed(from, to string) bool {
	allowed := map[string]map[string]struct{}{
		"CONFIRMED": {"PREPARING": {}, "RESTAURANT_REJECTED": {}},
		"PREPARING": {"READY_FOR_PICKUP": {}},
	}
	_, ok := allowed[from][to]
	return ok
}

func deliveryTransitionAllowed(from, to string) bool {
	allowed := map[string]map[string]struct{}{
		"CREATED":               {"ACCEPTED": {}, "REJECTED": {}},
		"ASSIGNED":              {"ACCEPTED": {}, "REJECTED": {}},
		"ACCEPTED":              {"ARRIVED_AT_RESTAURANT": {}},
		"ARRIVED_AT_RESTAURANT": {"PICKED_UP": {}},
		"PICKED_UP":             {"ARRIVED_AT_CUSTOMER": {}},
		"ARRIVED_AT_CUSTOMER":   {"DELIVERED": {}},
	}
	_, ok := allowed[from][to]
	return ok
}

func number(value any, fallback float64) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	default:
		return fallback
	}
}

func stringValue(input map[string]any, key, fallback string) string {
	value, ok := input[key]
	if !ok || value == nil {
		return fallback
	}
	if s, ok := value.(string); ok {
		if strings.TrimSpace(s) == "" {
			return fallback
		}
		return strings.TrimSpace(s)
	}
	return fallback
}

func intValue(input map[string]any, key string, fallback int) int {
	value, ok := input[key]
	if !ok || value == nil {
		return fallback
	}
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return fallback
	}
}

func boolValue(input map[string]any, key string, fallback bool) bool {
	value, ok := input[key]
	if !ok || value == nil {
		return fallback
	}
	if b, ok := value.(bool); ok {
		return b
	}
	return fallback
}

func numberPtr(input map[string]any, key string) any {
	value, ok := input[key]
	if !ok || value == nil {
		return nil
	}
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	default:
		return nil
	}
}
