package com.us.android.core.commerce.network

import com.us.android.core.commerce.model.Paise
import kotlinx.serialization.KSerializer
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.descriptors.PrimitiveKind
import kotlinx.serialization.descriptors.PrimitiveSerialDescriptor
import kotlinx.serialization.descriptors.SerialDescriptor
import kotlinx.serialization.encoding.Decoder
import kotlinx.serialization.encoding.Encoder
import kotlinx.serialization.json.JsonDecoder
import kotlinx.serialization.json.jsonPrimitive
import java.math.BigDecimal
import java.math.RoundingMode

/**
 * The wire shapes of the seller's order, return and earnings surfaces.
 *
 * These mirror commerce-service structs that were written for its web
 * dashboard rather than for this contract, so they are less tidy than the P0
 * DTOs and the comments say where each oddity comes from. Every field that
 * the server can omit is defaulted: the list row is a raw `postgres.Order`
 * with `omitempty` on everything, and a row with no shipment simply has no
 * `shipment` key.
 */

// Orders

/**
 * One row of `GET /seller/orders`: the raw order header, as the server's
 * `postgres.Order` marshals it.
 *
 * Money is read from the `_minor` keys only. The rupee mirrors (`subtotal`,
 * `final_amount`) are on the wire too, and are 0.00 on every order the P0
 * checkout wrote (migration 007 stopped maintaining them), so declaring them
 * here would only offer a wrong number to read.
 */
@Serializable
data class SellerOrderDto(
    val id: String,
    @SerialName("order_number") val orderNumber: String = "",
    val status: String = "",
    @SerialName("payment_status") val paymentStatus: String = "",
    @SerialName("payment_method") val paymentMethod: String? = null,
    @SerialName("subtotal_minor") val subtotalMinor: Paise = Paise.ZERO,
    @SerialName("discount_minor") val discountMinor: Paise = Paise.ZERO,
    @SerialName("shipping_minor") val shippingMinor: Paise = Paise.ZERO,
    @SerialName("tax_minor") val taxMinor: Paise = Paise.ZERO,
    @SerialName("total_minor") val totalMinor: Paise = Paise.ZERO,
    @SerialName("currency_code") val currencyCode: String = "INR",
    @SerialName("cancellation_reason") val cancellationReason: String? = null,
    @SerialName("cancelled_by") val cancelledBy: String? = null,
    /** RFC 3339, as Go's `time.Time` marshals. */
    @SerialName("created_at") val createdAt: String? = null,
    @SerialName("updated_at") val updatedAt: String? = null,
)

/**
 * `GET /seller/orders/{id}`: the server's `SellerOrderCard`.
 *
 * `items` holds only THIS seller's lines even on a multi-seller order, and
 * `seller_subtotal_minor` is their sum. `delivery_address` is the order's
 * routing snapshot as the server stores it: a JSON object, base64-encoded
 * because Go marshals a byte slice that way. After the PII cutover it carries
 * routing fields only, so a decoded address can lack a name and a street.
 */
@Serializable
data class SellerOrderCardDto(
    val order: SellerOrderDto,
    val items: List<SellerOrderItemDto> = emptyList(),
    val shipment: ShipmentDto? = null,
    @SerialName("seller_subtotal_minor") val sellerSubtotalMinor: Paise = Paise.ZERO,
    @SerialName("delivery_address") val deliveryAddress: String? = null,
)

/** One of the seller's lines, as `postgres.OrderItem` marshals it. */
@Serializable
data class SellerOrderItemDto(
    val id: String,
    @SerialName("product_id") val productId: String = "",
    @SerialName("variant_id") val variantId: String = "",
    @SerialName("product_title") val productTitle: String = "",
    val sku: String = "",
    val quantity: Int = 1,
    @SerialName("unit_price_minor") val unitPriceMinor: Paise = Paise.ZERO,
    @SerialName("final_price_minor") val finalPriceMinor: Paise = Paise.ZERO,
    val status: String = "",
    @SerialName("tracking_number") val trackingNumber: String? = null,
    @SerialName("image_url") val imageUrl: String? = null,
    @SerialName("thumbnail_url") val thumbnailUrl: String? = null,
    @SerialName("delivered_at") val deliveredAt: String? = null,
)

/**
 * The seller's shipment on an order.
 *
 * Keyed by Go FIELD names, not snake_case: `postgres.Shipment` has `db` tags
 * and no `json` tags, so `encoding/json` falls back to the exported names.
 * A rename on the server to snake_case would silently empty every field
 * here, which is why the decode test pins these keys.
 */
@Serializable
data class ShipmentDto(
    @SerialName("ID") val id: String = "",
    @SerialName("Courier") val courier: String = "",
    @SerialName("TrackingNumber") val trackingNumber: String? = null,
    @SerialName("TrackingURL") val trackingUrl: String? = null,
    @SerialName("LabelURL") val labelUrl: String? = null,
    @SerialName("Status") val status: String = "",
    @SerialName("ETA") val eta: String? = null,
    @SerialName("ShippedAt") val shippedAt: String? = null,
    @SerialName("DeliveredAt") val deliveredAt: String? = null,
)

/** `POST /seller/orders/{id}/ship`: the courier and its tracking number. */
@Serializable
data class ShipOrderRequest(
    val courier: String,
    @SerialName("tracking_number") val trackingNumber: String,
)

/** `POST /seller/orders/{id}/cancel`: why, in words the buyer will read. */
@Serializable
data class SellerCancelOrderRequest(val reason: String)

// Returns

/** `GET /seller/returns`: `{"returns": [...], "status": "..."}`. */
@Serializable
data class SellerReturnsDto(
    val returns: List<SellerReturnCardDto> = emptyList(),
)

/**
 * A return request with the line and order it concerns joined in.
 *
 * `order_item` and `order` are `omitempty` on the server and absent when the
 * batch lookup missed, so a card can arrive as the bare request.
 */
@Serializable
data class SellerReturnCardDto(
    @SerialName("return") val request: ReturnRequestDto,
    @SerialName("order_item") val orderItem: SellerOrderItemDto? = null,
    val order: SellerOrderDto? = null,
)

/**
 * `postgres.ReturnRequest`.
 *
 * `refund_amount` is a rupee float on the server with no minor twin; it is
 * decoded through [RupeesAsPaiseSerializer] so no Double reaches the app.
 */
@Serializable
data class ReturnRequestDto(
    val id: String,
    @SerialName("order_id") val orderId: String = "",
    @SerialName("order_item_id") val orderItemId: String = "",
    @SerialName("reason_code") val reasonCode: String = "",
    @SerialName("reason_description") val reasonDescription: String? = null,
    val status: String = "",
    @SerialName("requested_at") val requestedAt: String? = null,
    @SerialName("approved_at") val approvedAt: String? = null,
    @SerialName("rejected_at") val rejectedAt: String? = null,
    @SerialName("rejection_reason") val rejectionReason: String? = null,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("refund_amount") val refundAmount: Paise? = null,
)

@Serializable
data class RejectReturnRequest(val reason: String)

// Earnings

/** `GET /seller/earnings`: `{"earnings": [...]}`. */
@Serializable
data class SellerEarningsDto(
    val earnings: List<SellerEarningDto> = emptyList(),
)

/**
 * One delivered prepaid line with its payout arithmetic.
 *
 * Every amount here is a RUPEE FLOAT on the wire (`gross_amount`,
 * `commission_amount`, `platform_fee`, `tds_amount`, `net_amount`), the only
 * commerce surface still emitting one, and there are no `_minor` twins to
 * prefer. They are decoded through [RupeesAsPaiseSerializer], which reads the
 * number's decimal text and never a Double, so the money gate holds on this
 * side even though it does not on the other.
 */
@Serializable
data class SellerEarningDto(
    @SerialName("order_item_id") val orderItemId: String,
    @SerialName("order_id") val orderId: String = "",
    @SerialName("order_number") val orderNumber: String = "",
    @SerialName("product_title") val productTitle: String = "",
    val sku: String = "",
    val quantity: Int = 1,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("gross_amount") val grossAmount: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("commission_amount") val commissionAmount: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("platform_fee") val platformFee: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("tds_amount") val tdsAmount: Paise = Paise.ZERO,
    @Serializable(with = RupeesAsPaiseSerializer::class)
    @SerialName("net_amount") val netAmount: Paise = Paise.ZERO,
    @SerialName("payment_method") val paymentMethod: String? = null,
    val status: String = "",
    @SerialName("delivered_at") val deliveredAt: String? = null,
)

/**
 * Reads a rupee amount the server wrote as a JSON number and yields paise.
 *
 * This exists for the few seller routes that still emit `float64` rupees
 * with no minor-unit twin. The number is taken as its DECIMAL TEXT from the
 * JSON tree and scaled with [BigDecimal], so "1299.5" becomes exactly 129950
 * and no IEEE double is ever constructed on the way, which is the round trip
 * LB-19 exists to keep out of the app. A third decimal place is rounded half
 * up, matching the server's own `round2`.
 *
 * Decode only. Nothing in the app sends rupees, and encoding a float would
 * be reintroducing the defect at the source.
 */
object RupeesAsPaiseSerializer : KSerializer<Paise> {
    override val descriptor: SerialDescriptor =
        PrimitiveSerialDescriptor("RupeesAsPaise", PrimitiveKind.STRING)

    override fun serialize(encoder: Encoder, value: Paise) {
        error("rupee floats are read, never written; send paise instead")
    }

    override fun deserialize(decoder: Decoder): Paise {
        val text = if (decoder is JsonDecoder) {
            decoder.decodeJsonElement().jsonPrimitive.content
        } else {
            decoder.decodeString()
        }
        return rupeeTextToPaise(text)
    }
}

/** "1299.5" to 129950, by decimal arithmetic. Blank or unparseable text is zero. */
fun rupeeTextToPaise(text: String): Paise {
    val decimal = text.trim().toBigDecimalOrNull() ?: return Paise.ZERO
    return Paise(
        decimal
            .movePointRight(RUPEE_SCALE)
            .setScale(0, RoundingMode.HALF_UP)
            .longValueExact(),
    )
}

private const val RUPEE_SCALE = 2
