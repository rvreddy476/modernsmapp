package com.us.android.core.food.network

import com.us.android.core.food.model.Paise
import com.us.android.core.food.model.RupeesAsPaiseSerializer
import com.us.android.core.food.model.WireDecimalSerializer
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import java.math.BigDecimal

/*
 * Wire shapes for the restaurant partner's menu, order and earnings routes
 * (Feast A3, 2026-09-13), transcribed from food-service handler.go,
 * handler_kitchen.go, handler_delivery_proof.go and store/postgres/models.go.
 *
 * No golden contract fixtures exist for these routes yet (the onboarding ones
 * do), so every field defaults and the app's lenient Json ignores additions.
 * When food-service adds goldens, copy them beside the onboarding ones and pin
 * these DTOs the same way.
 *
 * MONEY: fields ending `_paise` are integer paise and decode straight into
 * [Paise]. Every other money field here is a float RUPEE value on the wire and
 * goes through [RupeesAsPaiseSerializer] — decimal text to paise, no Double.
 */

// Shared envelopes

/** The `{"items": [...]}` shape the partner list routes return. */
@Serializable
data class ItemsDto<T>(
    val items: List<T> = emptyList(),
)

/** `{"status": "deleted" | "updated"}`. */
@Serializable
data class StatusDto(
    val status: String = "",
)

// Restaurants

/** `GET /v1/food/partner/restaurants[/:id]` (postgres.PartnerRestaurant). */
@Serializable
data class PartnerRestaurantDto(
    val id: String,
    @SerialName("partner_id") val partnerId: String = "",
    @SerialName("owner_user_id") val ownerUserId: String = "",
    val name: String = "",
    val slug: String = "",
    val description: String? = null,
    /** DRAFT, PENDING_REVIEW, ACTIVE, … */
    val status: String = "",
    @SerialName("is_open") val isOpen: Boolean = false,
    @SerialName("is_accepting_orders") val isAcceptingOrders: Boolean = false,
    val city: String = "",
    val state: String? = null,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("min_order_amount") val minOrderAmount: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("packaging_fee") val packagingFee: Paise = Paise.ZERO,
    @SerialName("created_at") val createdAt: String? = null,
)

// Menu

@Serializable
data class MenuCategoryDto(
    val id: String,
    val name: String = "",
    val description: String? = null,
    @SerialName("sort_order") val sortOrder: Int = 0,
    val items: List<MenuItemDto> = emptyList(),
)

@Serializable
data class MenuItemDto(
    val id: String,
    @SerialName("restaurant_id") val restaurantId: String = "",
    @SerialName("category_id") val categoryId: String? = null,
    val name: String = "",
    val description: String? = null,
    /** VEG, NON_VEG, EGG. */
    @SerialName("food_type") val foodType: String = "",
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("base_price") val basePrice: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("discount_price") val discountPrice: Paise? = null,
    @SerialName("image_url") val imageUrl: String? = null,
    @SerialName("preparation_minutes") val preparationMinutes: Int = 0,
    @SerialName("is_available") val isAvailable: Boolean = false,
    @SerialName("is_recommended") val isRecommended: Boolean = false,
    @Serializable(with = WireDecimalSerializer::class)
    @SerialName("tax_percentage") val taxPercentage: BigDecimal = BigDecimal.ZERO,
)

@Serializable
data class MenuCategoryRequest(
    val name: String,
    val description: String = "",
    @SerialName("sort_order") val sortOrder: Int,
)

/**
 * Create and update share one body. UPDATE REPLACES EVERY FIELD server-side
 * (an omitted string becomes empty), so callers always send the whole item.
 * [categoryId] is read on create only.
 */
@Serializable
data class MenuItemRequest(
    @SerialName("category_id") val categoryId: String? = null,
    val name: String,
    val description: String = "",
    @SerialName("food_type") val foodType: String,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("base_price") val basePrice: Paise,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("discount_price") val discountPrice: Paise? = null,
    @SerialName("image_url") val imageUrl: String = "",
    @SerialName("preparation_minutes") val preparationMinutes: Int,
    @SerialName("is_recommended") val isRecommended: Boolean,
    @Serializable(with = WireDecimalSerializer::class)
    @SerialName("tax_percentage") val taxPercentage: BigDecimal,
)

/** No default on purpose: `false` must reach the wire. */
@Serializable
data class AvailabilityRequest(
    @SerialName("is_available") val isAvailable: Boolean,
)

// Orders

/** `GET …/restaurants/:id/kitchen-queue` → `{"orders": [...]}`. */
@Serializable
data class KitchenQueueDto(
    val orders: List<KitchenOrderDto> = emptyList(),
)

/** A CONFIRMED order awaiting accept (postgres.KitchenOrder). */
@Serializable
data class KitchenOrderDto(
    val id: String,
    @SerialName("order_number") val orderNumber: String = "",
    val status: String = "",
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("final_amount") val finalAmount: Paise = Paise.ZERO,
    @SerialName("item_count") val itemCount: Int = 0,
    @SerialName("customer_instruction") val customerInstruction: String? = null,
    @SerialName("placed_at") val placedAt: String? = null,
    /** Postgres text, not guaranteed RFC 3339; prefer [secondsToBreach]. */
    @SerialName("accept_deadline_at") val acceptDeadlineAt: String? = null,
    /** Seconds left to accept, as the server computed it at response time. */
    @SerialName("seconds_to_breach") val secondsToBreach: Int? = null,
)

/** postgres.Order as the partner routes return it. */
@Serializable
data class PartnerOrderDto(
    val id: String,
    @SerialName("order_number") val orderNumber: String = "",
    @SerialName("restaurant_id") val restaurantId: String = "",
    val status: String = "",
    @SerialName("payment_status") val paymentStatus: String = "",
    @SerialName("payment_method") val paymentMethod: String = "",
    val totals: PriceBreakdownDto = PriceBreakdownDto(),
    @SerialName("estimated_preparation_minutes") val estimatedPreparationMinutes: Int = 0,
    @SerialName("placed_at") val placedAt: String? = null,
    @SerialName("delivered_at") val deliveredAt: String? = null,
    val items: List<OrderItemDto> = emptyList(),
    val history: List<OrderStatusHistoryDto> = emptyList(),
)

/** Float rupees on the wire, every one. */
@Serializable
data class PriceBreakdownDto(
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("item_subtotal") val itemSubtotal: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("addon_total") val addonTotal: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("packaging_fee") val packagingFee: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("tax_total") val taxTotal: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("restaurant_discount") val restaurantDiscount: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("final_amount") val finalAmount: Paise = Paise.ZERO,
)

@Serializable
data class OrderItemDto(
    val id: String = "",
    val name: String = "",
    @SerialName("food_type") val foodType: String = "",
    val quantity: Int = 0,
    val instruction: String? = null,
    /** Integer paise (Wave 1 B3). */
    @SerialName("unit_price_paise") val unitPricePaise: Paise = Paise.ZERO,
    @SerialName("line_total_paise") val lineTotalPaise: Paise = Paise.ZERO,
)

@Serializable
data class OrderStatusHistoryDto(
    @SerialName("from_status") val fromStatus: String? = null,
    @SerialName("to_status") val toStatus: String = "",
    val reason: String? = null,
    @SerialName("created_at") val createdAt: String? = null,
)

/** Accept, reject, mark-preparing, mark-ready. [reason] is read on reject. */
@Serializable
data class OrderStatusRequest(
    val reason: String? = null,
)

@Serializable
data class VerifyPickupRequest(
    val code: String,
)

@Serializable
data class VerifyPickupDto(
    @SerialName("order_id") val orderId: String = "",
    val status: String = "",
)

// Earnings

/** `GET …/reports/summary` (PartnerRestaurantSummary, a map server-side). Money in float rupees. */
@Serializable
data class RestaurantSummaryDto(
    @SerialName("restaurant_id") val restaurantId: String = "",
    val orders: Long = 0,
    val delivered: Long = 0,
    val refunded: Long = 0,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("gross_amount") val grossAmount: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    val commission: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    val refunds: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("payout_amount") val payoutAmount: Paise = Paise.ZERO,
)

/** One row of `GET …/settlements`. Money in float rupees. */
@Serializable
data class SettlementDto(
    val id: String = "",
    @SerialName("period_start") val periodStart: String? = null,
    @SerialName("period_end") val periodEnd: String? = null,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("gross_amount") val grossAmount: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    val commission: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("refund_adjustment") val refundAdjustment: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("penalty_amount") val penaltyAmount: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("payout_amount") val payoutAmount: Paise = Paise.ZERO,
    val status: String = "",
    @SerialName("paid_reference") val paidReference: String? = null,
    @SerialName("paid_at") val paidAt: String? = null,
)
