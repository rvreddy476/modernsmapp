package postgres

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

type RestaurantFilter struct {
	Query       string
	City        string
	Limit       int
	Latitude    float64
	Longitude   float64
	HasLocation bool
}

func (s *Store) ListCuisines(ctx context.Context) ([]Cuisine, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, name, slug, COALESCE(image_url, ''), sort_order
		FROM food.cuisines
		WHERE is_active = TRUE
		ORDER BY sort_order, name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cuisines []Cuisine
	for rows.Next() {
		var c Cuisine
		if err := rows.Scan(&c.ID, &c.Name, &c.Slug, &c.ImageURL, &c.SortOrder); err != nil {
			return nil, err
		}
		cuisines = append(cuisines, c)
	}
	return cuisines, rows.Err()
}

// ListDishCategories returns the platform-curated master list of dish
// categories. Partners choose from these; they cannot add their own.
// Reuses the Cuisine shape (id/name/slug/sort_order) — same contract.
func (s *Store) ListDishCategories(ctx context.Context) ([]Cuisine, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, name, slug, '', sort_order
		FROM food.dish_categories
		WHERE is_active = TRUE
		ORDER BY sort_order, name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var categories []Cuisine
	for rows.Next() {
		var c Cuisine
		if err := rows.Scan(&c.ID, &c.Name, &c.Slug, &c.ImageURL, &c.SortOrder); err != nil {
			return nil, err
		}
		categories = append(categories, c)
	}
	return categories, rows.Err()
}

func (s *Store) ListRestaurants(ctx context.Context, filter RestaurantFilter) ([]RestaurantSummary, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	var args []any
	clauses := []string{"r.status = 'ACTIVE'", "r.is_accepting_orders = TRUE"}
	if strings.TrimSpace(filter.City) != "" {
		args = append(args, filter.City)
		clauses = append(clauses, fmt.Sprintf("LOWER(r.city) = LOWER($%d)", len(args)))
	}
	if strings.TrimSpace(filter.Query) != "" {
		args = append(args, "%"+strings.ToLower(strings.TrimSpace(filter.Query))+"%")
		clauses = append(clauses, fmt.Sprintf(`(
			LOWER(r.name) LIKE $%d
			OR LOWER(r.description) LIKE $%d
			OR EXISTS (
				SELECT 1
				FROM food.restaurant_cuisines rc
				JOIN food.cuisines c ON c.id = rc.cuisine_id
				WHERE rc.restaurant_id = r.id AND LOWER(c.name) LIKE $%d
			)
		)`, len(args), len(args), len(args)))
	}
	distanceExpression := "0::float8"
	if filter.HasLocation && filter.Latitude >= -90 && filter.Latitude <= 90 && filter.Longitude >= -180 && filter.Longitude <= 180 {
		args = append(args, filter.Latitude, filter.Longitude)
		latArg, lngArg := len(args)-1, len(args)
		distanceExpression = fmt.Sprintf(`CASE WHEN r.latitude IS NULL OR r.longitude IS NULL THEN 0 ELSE
			6371 * 2 * ASIN(SQRT(POWER(SIN(RADIANS(r.latitude - $%d) / 2), 2) +
			COS(RADIANS($%d)) * COS(RADIANS(r.latitude)) * POWER(SIN(RADIANS(r.longitude - $%d) / 2), 2))) END`, latArg, latArg, lngArg)
	}
	args = append(args, limit)

	query := fmt.Sprintf(`
		SELECT
			r.id,
			r.name,
			r.slug,
			COALESCE(r.description, ''),
			r.city,
			COALESCE(r.state, ''),
			r.status::text,
			r.is_open,
			r.is_accepting_orders,
			r.avg_rating::float8,
			r.rating_count,
			r.min_order_amount::float8,
			r.packaging_fee::float8,
			r.avg_preparation_minutes,
			COALESCE((
				SELECT image_url
				FROM food.restaurant_images ri
				WHERE ri.restaurant_id = r.id AND ri.is_active = TRUE
				ORDER BY CASE WHEN ri.image_type = 'hero' THEN 0 ELSE 1 END, ri.sort_order
				LIMIT 1
			), ''),
			COALESCE((
				SELECT string_agg(c.name, ', ' ORDER BY c.sort_order, c.name)
				FROM food.restaurant_cuisines rc
				JOIN food.cuisines c ON c.id = rc.cuisine_id
				WHERE rc.restaurant_id = r.id
			), ''),
			%s
		FROM food.restaurants r
		WHERE %s
		ORDER BY r.is_open DESC, r.avg_rating DESC, r.rating_count DESC, r.name
		LIMIT $%d
	`, distanceExpression, strings.Join(clauses, " AND "), len(args))

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var restaurants []RestaurantSummary
	for rows.Next() {
		item, err := scanRestaurantSummary(rows)
		if err != nil {
			return nil, err
		}
		restaurants = append(restaurants, item)
	}
	return restaurants, rows.Err()
}

func (s *Store) GetRestaurant(ctx context.Context, id uuid.UUID) (*RestaurantDetail, error) {
	row := s.db.QueryRow(ctx, `
		SELECT
			r.id,
			r.name,
			r.slug,
			COALESCE(r.description, ''),
			r.city,
			COALESCE(r.state, ''),
			r.status::text,
			r.is_open,
			r.is_accepting_orders,
			r.avg_rating::float8,
			r.rating_count,
			r.min_order_amount::float8,
			r.packaging_fee::float8,
			r.avg_preparation_minutes,
			COALESCE((
				SELECT image_url
				FROM food.restaurant_images ri
				WHERE ri.restaurant_id = r.id AND ri.is_active = TRUE
				ORDER BY CASE WHEN ri.image_type = 'hero' THEN 0 ELSE 1 END, ri.sort_order
				LIMIT 1
			), ''),
			COALESCE((
				SELECT string_agg(c.name, ', ' ORDER BY c.sort_order, c.name)
				FROM food.restaurant_cuisines rc
				JOIN food.cuisines c ON c.id = rc.cuisine_id
				WHERE rc.restaurant_id = r.id
			), ''),
			COALESCE(r.phone, ''),
			COALESCE(r.email, ''),
			r.address_line1,
			COALESCE(r.postal_code, ''),
			COALESCE(r.latitude, 0)::float8,
			COALESCE(r.longitude, 0)::float8
		FROM food.restaurants r
		WHERE r.id = $1 AND r.status IN ('ACTIVE', 'APPROVED')
	`, id)

	summary, detail, err := scanRestaurantDetail(row)
	if err != nil {
		return nil, err
	}
	detail.RestaurantSummary = summary
	return &detail, nil
}

func (s *Store) GetMenu(ctx context.Context, restaurantID uuid.UUID) ([]MenuCategory, error) {
	rows, err := s.db.Query(ctx, `
		SELECT
			c.id,
			c.name,
			COALESCE(c.description, ''),
			c.sort_order,
			i.id,
			i.restaurant_id,
			i.name,
			COALESCE(i.description, ''),
			i.food_type::text,
			i.base_price::float8,
			COALESCE(i.discount_price, 0)::float8,
			COALESCE(i.image_url, ''),
			i.preparation_minutes,
			i.is_available,
			i.is_recommended,
			i.tax_percentage::float8,
			i.metadata
		FROM food.menu_categories c
		JOIN food.menu_items i ON i.category_id = c.id
		WHERE c.restaurant_id = $1
			AND c.is_active = TRUE
			AND i.is_active = TRUE
		ORDER BY c.sort_order, c.name, i.is_recommended DESC, i.name
	`, restaurantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byID := map[uuid.UUID]int{}
	var categories []MenuCategory
	for rows.Next() {
		var cat MenuCategory
		var item MenuItem
		if err := rows.Scan(
			&cat.ID,
			&cat.Name,
			&cat.Description,
			&cat.SortOrder,
			&item.ID,
			&item.RestaurantID,
			&item.Name,
			&item.Description,
			&item.FoodType,
			&item.BasePrice,
			&item.DiscountPrice,
			&item.ImageURL,
			&item.PreparationMinutes,
			&item.IsAvailable,
			&item.IsRecommended,
			&item.TaxPercentage,
			&item.Metadata,
		); err != nil {
			return nil, err
		}
		item.CategoryID = cat.ID
		idx, ok := byID[cat.ID]
		if !ok {
			cat.Items = []MenuItem{}
			categories = append(categories, cat)
			idx = len(categories) - 1
			byID[cat.ID] = idx
		}
		categories[idx].Items = append(categories[idx].Items, item)
	}
	return categories, rows.Err()
}

type restaurantRow interface {
	Scan(dest ...any) error
}

func scanRestaurantSummary(rows pgx.Rows) (RestaurantSummary, error) {
	var r RestaurantSummary
	var cuisines string
	if err := rows.Scan(
		&r.ID,
		&r.Name,
		&r.Slug,
		&r.Description,
		&r.City,
		&r.State,
		&r.Status,
		&r.IsOpen,
		&r.IsAcceptingOrders,
		&r.AvgRating,
		&r.RatingCount,
		&r.MinOrderAmount,
		&r.PackagingFee,
		&r.AvgPreparationMins,
		&r.HeroImageURL,
		&cuisines,
		&r.DistanceKM,
	); err != nil {
		return RestaurantSummary{}, err
	}
	r.Cuisines = splitCSV(cuisines)
	travelMinutes := 10
	if r.DistanceKM > 0 {
		travelMinutes = int(math.Ceil(r.DistanceKM*3.2)) + 4
	}
	r.EstimatedDeliveryMins = r.AvgPreparationMins + travelMinutes
	r.EstimatedDelivery = fmt.Sprintf("%d-%d min", r.EstimatedDeliveryMins, r.EstimatedDeliveryMins+8)
	r.DeliveryFeeEstimate = math.Round(19 + math.Max(r.DistanceKM, 2)*5)
	return r, nil
}

func scanRestaurantDetail(row restaurantRow) (RestaurantSummary, RestaurantDetail, error) {
	var summary RestaurantSummary
	var detail RestaurantDetail
	var cuisines string
	if err := row.Scan(
		&summary.ID,
		&summary.Name,
		&summary.Slug,
		&summary.Description,
		&summary.City,
		&summary.State,
		&summary.Status,
		&summary.IsOpen,
		&summary.IsAcceptingOrders,
		&summary.AvgRating,
		&summary.RatingCount,
		&summary.MinOrderAmount,
		&summary.PackagingFee,
		&summary.AvgPreparationMins,
		&summary.HeroImageURL,
		&cuisines,
		&detail.Phone,
		&detail.Email,
		&detail.AddressLine,
		&detail.PostalCode,
		&detail.Latitude,
		&detail.Longitude,
	); err != nil {
		return RestaurantSummary{}, RestaurantDetail{}, err
	}
	summary.Cuisines = splitCSV(cuisines)
	summary.EstimatedDelivery = fmt.Sprintf("%d-%d min", summary.AvgPreparationMins+12, summary.AvgPreparationMins+22)
	summary.DeliveryFeeEstimate = 29
	return summary, detail, nil
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{}
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
