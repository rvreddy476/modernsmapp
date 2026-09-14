package com.us.android.core.food.network

import com.us.android.core.food.model.Paise
import com.us.android.core.food.model.RupeesAsPaiseSerializer
import com.us.android.core.food.model.WireDecimalSerializer
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import java.math.BigDecimal

/*
 * Wire shapes for food-service's CUSTOMER routes (Feast A5, Momentum).
 *
 * Pinned by golden fixtures (strict decode in FoodContractFixtureTest): the
 * cart (FeastCartDto), order detail (FeastOrderDto), payment status
 * (OrderPaymentDto), payment intent (PaymentIntentDto) and invoice
 * (FeastInvoiceDto). Every other shape here — restaurants, menu, addresses,
 * tracking — has NO golden fixture on the server yet and is written from the
 * Go structs in store/postgres/models.go.
 *
 * Money: every `*_paise` field is integer paise. The legacy float-rupee fields
 * (`totals`, `unit_price`, `line_total`, …) are decoded through
 * RupeesAsPaiseSerializer — decimal text, never a Double — only so the strict
 * fixture decode passes; screens render the `*_paise` and `taxes_and_charges`
 * figures. The client NEVER adds these up: the server's total is the total.
 */

// ── Discovery ──────────────────────────────────────────────────────────

/**
 * `RestaurantSummary` / `RestaurantDetail`. There is no distance, no in-range
 * flag and no "why closed" message on this route (backend gap, A5): the only
 * serviceability the list can show is [isOpen], [isAcceptingOrders] and
 * [status]. Range is decided at `POST /orders` (422 FOOD_ADDRESS_OUT_OF_RANGE).
 */
@Serializable
data class FeastRestaurantDto(
    val id: String,
    val name: String = "",
    val slug: String = "",
    val description: String? = null,
    val city: String = "",
    val state: String? = null,
    val status: String = "",
    @SerialName("is_open") val isOpen: Boolean = false,
    @SerialName("is_accepting_orders") val isAcceptingOrders: Boolean = false,
    @SerialName("avg_rating") @Serializable(with = WireDecimalSerializer::class)
    val avgRating: BigDecimal = BigDecimal.ZERO,
    @SerialName("rating_count") val ratingCount: Int = 0,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("min_order_amount") val minOrderAmount: Paise = Paise.ZERO,
    @SerialName("avg_preparation_minutes") val avgPreparationMinutes: Int = 0,
    @SerialName("hero_image_url") val heroImageUrl: String? = null,
    val cuisines: List<String> = emptyList(),
    @SerialName("estimated_delivery") val estimatedDelivery: String = "",
    // Detail only.
    @SerialName("address_line") val addressLine: String? = null,
    @SerialName("postal_code") val postalCode: String? = null,
)

/** `GET /restaurants/:id/menu` → `{categories: [...]}`. */
@Serializable
data class FeastMenuDto(
    val categories: List<FeastMenuCategoryDto> = emptyList(),
)

@Serializable
data class FeastMenuCategoryDto(
    val id: String,
    val name: String = "",
    val description: String? = null,
    @SerialName("sort_order") val sortOrder: Int = 0,
    val items: List<FeastMenuItemDto> = emptyList(),
)

/**
 * A customer menu item.
 *
 * [variants] and [addonGroups] are what the partner item read
 * (`menu_item_get_200.json`) carries. The CUSTOMER menu query does not select
 * them at c1b45af4 (store.go GetMenu), so today they arrive empty — a backend
 * gap reported with A5. The sheet renders them the moment the route sends them.
 */
@Serializable
data class FeastMenuItemDto(
    val id: String,
    @SerialName("restaurant_id") val restaurantId: String = "",
    @SerialName("category_id") val categoryId: String? = null,
    val name: String = "",
    val description: String? = null,
    /** VEG, NON_VEG, EGG. */
    @SerialName("food_type") val foodType: String = "",
    @SerialName("base_price_paise") val basePricePaise: Paise = Paise.ZERO,
    @SerialName("discount_price_paise") val discountPricePaise: Paise? = null,
    @SerialName("image_url") val imageUrl: String? = null,
    @SerialName("preparation_minutes") val preparationMinutes: Int = 0,
    @SerialName("is_available") val isAvailable: Boolean = false,
    @SerialName("is_recommended") val isRecommended: Boolean = false,
    val variants: List<FeastVariantDto> = emptyList(),
    @SerialName("addon_groups") val addonGroups: List<FeastAddonGroupDto> = emptyList(),
)

@Serializable
data class FeastVariantDto(
    val id: String,
    val name: String = "",
    @SerialName("price_paise") val pricePaise: Paise = Paise.ZERO,
    @SerialName("is_available") val isAvailable: Boolean = true,
    @SerialName("sort_order") val sortOrder: Int = 0,
)

@Serializable
data class FeastAddonGroupDto(
    val id: String,
    val name: String = "",
    @SerialName("min_select") val minSelect: Int = 0,
    @SerialName("max_select") val maxSelect: Int = 0,
    @SerialName("is_required") val isRequired: Boolean = false,
    @SerialName("sort_order") val sortOrder: Int = 0,
    val addons: List<FeastAddonDto> = emptyList(),
)

@Serializable
data class FeastAddonDto(
    val id: String,
    val name: String = "",
    @SerialName("price_paise") val pricePaise: Paise = Paise.ZERO,
    @SerialName("is_available") val isAvailable: Boolean = true,
    @SerialName("sort_order") val sortOrder: Int = 0,
)

// ── Cart (golden: cart_get_200_*.json) ─────────────────────────────────

@Serializable
data class AddCartItemRequest(
    @SerialName("menu_item_id") val menuItemId: String,
    val quantity: Int,
    @SerialName("variant_id") val variantId: String? = null,
    @SerialName("item_instruction") val itemInstruction: String? = null,
    /** Replace a cart from another restaurant, after the customer agreed. */
    @SerialName("clear_existing") val clearExisting: Boolean = false,
    val addons: List<CartAddonRequest> = emptyList(),
)

@Serializable
data class CartAddonRequest(
    @SerialName("addon_id") val addonId: String,
    val quantity: Int,
)

@Serializable
data class UpdateCartItemRequest(
    val quantity: Int,
    @SerialName("item_instruction") val itemInstruction: String? = null,
)

@Serializable
data class FeastCartDto(
    val id: String,
    @SerialName("user_id") val userId: String = "",
    @SerialName("restaurant_id") val restaurantId: String? = null,
    val restaurant: String? = null,
    @SerialName("coupon_code") val couponCode: String? = null,
    val items: List<FeastCartItemDto> = emptyList(),
    /** Legacy float rupees. Decoded, never rendered. */
    val totals: FeastLegacyTotalsDto = FeastLegacyTotalsDto(),
    /** Absent when the cart cannot be priced (see [pricingError]). */
    @SerialName("totals_paise") val totalsPaise: FeastTotalsPaiseDto? = null,
    /** What the bill renders, figure for figure. Null when pricing is blocked. */
    @SerialName("taxes_and_charges") val taxesAndCharges: TaxesAndChargesDto? = null,
    /** Set when the restaurant cannot be priced; the cart must not proceed. */
    @SerialName("pricing_error") val pricingError: PricingErrorDto? = null,
)

@Serializable
data class FeastCartItemDto(
    val id: String,
    @SerialName("restaurant_id") val restaurantId: String = "",
    @SerialName("menu_item_id") val menuItemId: String = "",
    @SerialName("variant_id") val variantId: String? = null,
    val name: String = "",
    @SerialName("image_url") val imageUrl: String? = null,
    @SerialName("food_type") val foodType: String = "",
    val quantity: Int = 0,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("unit_price") val unitPrice: Paise = Paise.ZERO,
    @Serializable(with = WireDecimalSerializer::class)
    @SerialName("tax_percentage") val taxPercentage: BigDecimal = BigDecimal.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("tax_amount") val taxAmount: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("line_total") val lineTotal: Paise = Paise.ZERO,
    @SerialName("item_instruction") val itemInstruction: String? = null,
    val addons: List<FeastCartAddonDto> = emptyList(),
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("addon_total") val addonTotal: Paise = Paise.ZERO,
    @SerialName("unit_price_paise") val unitPricePaise: Paise = Paise.ZERO,
    @SerialName("line_total_paise") val lineTotalPaise: Paise = Paise.ZERO,
    @SerialName("addon_total_paise") val addonTotalPaise: Paise = Paise.ZERO,
    @SerialName("tax_amount_paise") val taxAmountPaise: Paise = Paise.ZERO,
)

@Serializable
data class FeastCartAddonDto(
    @SerialName("addon_id") val addonId: String,
    val name: String = "",
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("unit_price") val unitPrice: Paise = Paise.ZERO,
    val quantity: Int = 0,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("line_total") val lineTotal: Paise = Paise.ZERO,
    @SerialName("unit_price_paise") val unitPricePaise: Paise = Paise.ZERO,
    @SerialName("line_total_paise") val lineTotalPaise: Paise = Paise.ZERO,
)

/** `PriceBreakdown` in float rupees — the legacy half of every cart and order. */
@Serializable
data class FeastLegacyTotalsDto(
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("item_subtotal") val itemSubtotal: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("addon_total") val addonTotal: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("packaging_fee") val packagingFee: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("tax_total") val taxTotal: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("delivery_fee") val deliveryFee: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("platform_fee") val platformFee: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("restaurant_discount") val restaurantDiscount: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("coupon_discount") val couponDiscount: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("final_amount") val finalAmount: Paise = Paise.ZERO,
)

/** `pricing.Totals` — integer paise, the server's own sums. */
@Serializable
data class FeastTotalsPaiseDto(
    @SerialName("item_subtotal_paise") val itemSubtotalPaise: Paise = Paise.ZERO,
    @SerialName("addon_total_paise") val addonTotalPaise: Paise = Paise.ZERO,
    @SerialName("packaging_fee_paise") val packagingFeePaise: Paise = Paise.ZERO,
    @SerialName("delivery_fee_paise") val deliveryFeePaise: Paise = Paise.ZERO,
    @SerialName("platform_fee_paise") val platformFeePaise: Paise = Paise.ZERO,
    @SerialName("tax_total_paise") val taxTotalPaise: Paise = Paise.ZERO,
    @SerialName("discount_total_paise") val discountTotalPaise: Paise = Paise.ZERO,
    @SerialName("final_amount_paise") val finalAmountPaise: Paise = Paise.ZERO,
)

/** `pricing.TaxesAndCharges`: "Taxes & charges" with no tax arithmetic on the client. */
@Serializable
data class TaxesAndChargesDto(
    val charges: List<ChargeDto> = emptyList(),
    val taxes: List<TaxGroupDto> = emptyList(),
    @SerialName("total_charges_paise") val totalChargesPaise: Paise = Paise.ZERO,
    @SerialName("total_tax_paise") val totalTaxPaise: Paise = Paise.ZERO,
    @SerialName("total_taxes_and_charges_paise") val totalTaxesAndChargesPaise: Paise = Paise.ZERO,
    @SerialName("menu_prices_treated_as_exclusive") val menuPricesTreatedAsExclusive: Boolean = false,
    @SerialName("needs_adviser_confirmation") val needsAdviserConfirmation: Boolean = false,
    @SerialName("adviser_notice") val adviserNotice: String? = null,
)

@Serializable
data class ChargeDto(
    /** PACKAGING, PLATFORM_FEE, DELIVERY_FEE. */
    val kind: String = "",
    val label: String = "",
    @SerialName("amount_paise") val amountPaise: Paise = Paise.ZERO,
)

@Serializable
data class TaxGroupDto(
    @SerialName("liable_party") val liableParty: String = "",
    /** SUPPLIER or ECO_SECTION_9_5. */
    val liability: String = "",
    val label: String = "",
    @SerialName("taxable_paise") val taxablePaise: Paise = Paise.ZERO,
    @SerialName("tax_paise") val taxPaise: Paise = Paise.ZERO,
    val rates: List<TaxRateDto> = emptyList(),
)

@Serializable
data class TaxRateDto(
    @SerialName("rate_bp") val rateBp: Int = 0,
    /** "18.00" — text, so it is shown exactly as the server states it. */
    @SerialName("rate_percent") val ratePercent: String = "",
    @SerialName("taxable_paise") val taxablePaise: Paise = Paise.ZERO,
    @SerialName("cgst_paise") val cgstPaise: Paise = Paise.ZERO,
    @SerialName("sgst_paise") val sgstPaise: Paise = Paise.ZERO,
    @SerialName("igst_paise") val igstPaise: Paise = Paise.ZERO,
    @SerialName("tax_paise") val taxPaise: Paise = Paise.ZERO,
    @SerialName("needs_adviser_confirmation") val needsAdviserConfirmation: Boolean = false,
)

@Serializable
data class PricingErrorDto(
    val code: String = "",
    val message: String = "",
)

// ── Addresses ──────────────────────────────────────────────────────────

@Serializable
data class FeastAddressDto(
    val id: String,
    val label: String? = null,
    @SerialName("receiver_name") val receiverName: String? = null,
    val phone: String? = null,
    @SerialName("address_line1") val addressLine1: String = "",
    @SerialName("address_line2") val addressLine2: String? = null,
    val landmark: String? = null,
    val city: String = "",
    val state: String? = null,
    val country: String? = null,
    @SerialName("postal_code") val postalCode: String? = null,
    /** Omitted by the server when zero, i.e. an address with no pin. */
    val latitude: Double? = null,
    val longitude: Double? = null,
    @SerialName("is_default") val isDefault: Boolean = false,
)

/**
 * Create/update body. `latitude`/`longitude` matter: `POST /orders` refuses an
 * address with no pin (422 FOOD_ADDRESS_LOCATION_REQUIRED).
 */
@Serializable
data class FeastAddressRequest(
    val label: String,
    @SerialName("address_line1") val addressLine1: String,
    val city: String,
    val country: String,
    @SerialName("receiver_name") val receiverName: String? = null,
    val phone: String? = null,
    @SerialName("address_line2") val addressLine2: String? = null,
    val landmark: String? = null,
    val state: String? = null,
    @SerialName("postal_code") val postalCode: String? = null,
    val latitude: Double? = null,
    val longitude: Double? = null,
    @SerialName("is_default") val isDefault: Boolean = false,
)

// ── Orders (golden: order_get_200*.json) ───────────────────────────────

/** `payment_method` is the INSTRUMENT the customer chose: `upi` or `card` at launch. */
@Serializable
data class PlaceOrderRequest(
    @SerialName("address_id") val addressId: String,
    @SerialName("payment_method") val paymentMethod: String,
    @SerialName("customer_instruction") val customerInstruction: String? = null,
)

@Serializable
data class CancelOrderRequest(
    val reason: String,
)

@Serializable
data class FeastOrderDto(
    val id: String,
    @SerialName("order_number") val orderNumber: String = "",
    @SerialName("user_id") val userId: String = "",
    @SerialName("restaurant_id") val restaurantId: String = "",
    @SerialName("restaurant_name") val restaurantName: String = "",
    val status: String = "",
    @SerialName("payment_status") val paymentStatus: String = "",
    @SerialName("payment_method") val paymentMethod: String = "",
    /** Legacy float rupees; [money] carries the paise figures when present. */
    val totals: FeastLegacyTotalsDto = FeastLegacyTotalsDto(),
    @SerialName("estimated_preparation_minutes") val estimatedPreparationMinutes: Int = 0,
    @SerialName("estimated_delivery_minutes") val estimatedDeliveryMinutes: Int = 0,
    @SerialName("placed_at") val placedAt: String? = null,
    @SerialName("delivered_at") val deliveredAt: String? = null,
    val items: List<FeastOrderItemDto> = emptyList(),
    val history: List<OrderStatusHistoryDto> = emptyList(),
    val money: FeastOrderMoneyDto? = null,
    /** Present ONLY while PICKED_UP / OUT_FOR_DELIVERY (DeliveryCodeVisible). */
    @SerialName("delivery_code") val deliveryCode: String? = null,
    /** RFC 3339; null unless the order can still arrive (ETAVisible). */
    @SerialName("eta_at") val etaAt: String? = null,
    /** `google` or `haversine`. */
    @SerialName("eta_source") val etaSource: String? = null,
)

@Serializable
data class FeastOrderItemDto(
    val id: String = "",
    val name: String = "",
    @SerialName("food_type") val foodType: String = "",
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("unit_price") val unitPrice: Paise = Paise.ZERO,
    val quantity: Int = 0,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("tax_amount") val taxAmount: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("line_total") val lineTotal: Paise = Paise.ZERO,
    val instruction: String? = null,
    @SerialName("unit_price_paise") val unitPricePaise: Paise = Paise.ZERO,
    @SerialName("tax_amount_paise") val taxAmountPaise: Paise = Paise.ZERO,
    @SerialName("line_total_paise") val lineTotalPaise: Paise = Paise.ZERO,
)

@Serializable
data class FeastOrderMoneyDto(
    @SerialName("totals_paise") val totalsPaise: FeastTotalsPaiseDto? = null,
    @SerialName("taxes_and_charges") val taxesAndCharges: TaxesAndChargesDto? = null,
    @SerialName("needs_adviser_confirmation") val needsAdviserConfirmation: Boolean = false,
)

/** `GET /orders/:id/tracking` — a map on the server; no golden fixture. */
@Serializable
data class FeastTrackingDto(
    @SerialName("order_id") val orderId: String = "",
    @SerialName("order_number") val orderNumber: String = "",
    val status: String = "",
    @SerialName("estimated_delivery_minutes") val estimatedDeliveryMinutes: Int = 0,
    @SerialName("eta_at") val etaAt: String? = null,
    @SerialName("eta_source") val etaSource: String? = null,
    @SerialName("delivery_location") val deliveryLocation: FeastPointDto? = null,
    @SerialName("customer_location") val customerLocation: FeastPointDto? = null,
    @SerialName("restaurant_location") val restaurantLocation: FeastPointDto? = null,
)

@Serializable
data class FeastPointDto(
    val latitude: Double? = null,
    val longitude: Double? = null,
    /** Postgres timestamp text on delivery_location. */
    @SerialName("recorded_at") val recordedAt: String? = null,
)

// ── Payment (golden: order_payment_get_*.json, payment_intent_post_*.json) ──

@Serializable
data class OrderPaymentDto(
    @SerialName("order_id") val orderId: String,
    /** `confirming | paid | failed`. */
    val status: String = "",
    @SerialName("amount_minor") val amountMinor: Long = 0,
    val currency: String = "",
    /** null, or a refund state such as `pending`. */
    @SerialName("refund_status") val refundStatus: String? = null,
    @SerialName("updated_at") val updatedAt: String? = null,
)

/** `upi` or `card` — the only instruments enabled at launch (PAYMENT_METHOD_INVALID otherwise). */
@Serializable
data class PaymentIntentRequest(
    val method: String,
)

@Serializable
data class PaymentIntentDto(
    @Serializable(with = RupeesAsPaiseSerializer::class)
    val amount: Paise = Paise.ZERO,
    /** Exactly provider / order_id / key_id (+ merchant_display_name). Absent with a stub gateway. */
    @SerialName("client_session") val clientSession: Map<String, String>? = null,
    val currency: String = "",
    val id: String = "",
    val method: String = "",
    @SerialName("order_id") val orderId: String = "",
    @SerialName("payment_intent") val paymentIntent: PublicPaymentIntentDto? = null,
    val provider: String = "",
    @SerialName("provider_order_id") val providerOrderId: String? = null,
    @SerialName("provider_payment_id") val providerPaymentId: String? = null,
    val status: String = "",
)

@Serializable
data class PublicPaymentIntentDto(
    @SerialName("amount_minor") val amountMinor: Long = 0,
    val currency: String = "",
    val id: String = "",
    val method: String = "",
    @SerialName("provider_ref") val providerRef: String? = null,
    @SerialName("reference_id") val referenceId: String? = null,
    @SerialName("reference_type") val referenceType: String? = null,
    val status: String = "",
)

// ── Invoice (golden: invoice_get_200.json) ─────────────────────────────

@Serializable
data class FeastInvoiceDto(
    @SerialName("order_id") val orderId: String? = null,
    @SerialName("order_number") val orderNumber: String = "",
    @SerialName("invoice_date") val invoiceDate: String = "",
    @SerialName("place_of_supply_state") val placeOfSupplyState: String? = null,
    val buyer: InvoicePartyDto = InvoicePartyDto(),
    val sections: List<InvoiceSectionDto> = emptyList(),
    @SerialName("grand_total_paise") val grandTotalPaise: Paise = Paise.ZERO,
    val currency: String = "",
    @SerialName("menu_prices_treated_as_exclusive") val menuPricesTreatedAsExclusive: Boolean = false,
    @SerialName("needs_adviser_confirmation") val needsAdviserConfirmation: Boolean = false,
    @SerialName("adviser_marker") val adviserMarker: String? = null,
    val legacy: Boolean = false,
    val notes: List<String> = emptyList(),
)

@Serializable
data class InvoicePartyDto(
    val name: String = "",
    @SerialName("legal_name") val legalName: String? = null,
    val gstin: String? = null,
    @SerialName("address_line") val addressLine: String? = null,
    val city: String? = null,
    val state: String? = null,
)

@Serializable
data class InvoiceSectionDto(
    /** RESTAURANT or PLATFORM. */
    val issuer: String = "",
    val title: String = "",
    @SerialName("invoice_number") val invoiceNumber: String = "",
    @SerialName("issuer_name") val issuerName: String = "",
    @SerialName("issuer_gstin") val issuerGstin: String? = null,
    val lines: List<InvoiceLineDto> = emptyList(),
    @SerialName("taxable_paise") val taxablePaise: Paise = Paise.ZERO,
    @SerialName("cgst_paise") val cgstPaise: Paise = Paise.ZERO,
    @SerialName("sgst_paise") val sgstPaise: Paise = Paise.ZERO,
    @SerialName("igst_paise") val igstPaise: Paise = Paise.ZERO,
    @SerialName("tax_paise") val taxPaise: Paise = Paise.ZERO,
    @SerialName("total_paise") val totalPaise: Paise = Paise.ZERO,
    val notes: List<String> = emptyList(),
)

@Serializable
data class InvoiceLineDto(
    val ref: String? = null,
    val kind: String = "",
    val description: String = "",
    val sac: String? = null,
    val quantity: Long = 0,
    @SerialName("unit_price_paise") val unitPricePaise: Paise = Paise.ZERO,
    @SerialName("supplied_by") val suppliedBy: String = "",
    val liability: String = "",
    @SerialName("rate_bp") val rateBp: Int = 0,
    @SerialName("rate_percent") val ratePercent: String = "",
    @SerialName("discount_paise") val discountPaise: Paise = Paise.ZERO,
    @SerialName("taxable_paise") val taxablePaise: Paise = Paise.ZERO,
    @SerialName("cgst_paise") val cgstPaise: Paise = Paise.ZERO,
    @SerialName("sgst_paise") val sgstPaise: Paise = Paise.ZERO,
    @SerialName("igst_paise") val igstPaise: Paise = Paise.ZERO,
    @SerialName("tax_paise") val taxPaise: Paise = Paise.ZERO,
    @SerialName("total_paise") val totalPaise: Paise = Paise.ZERO,
)
