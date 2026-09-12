package com.us.android.core.commerce.repository

import com.us.android.core.commerce.model.Address
import com.us.android.core.commerce.model.OrderStatus
import com.us.android.core.commerce.model.PaymentStatus
import com.us.android.core.commerce.model.ReturnStatus
import com.us.android.core.commerce.model.SellerEarning
import com.us.android.core.commerce.model.SellerOrder
import com.us.android.core.commerce.model.SellerOrderLine
import com.us.android.core.commerce.model.SellerOrderSummary
import com.us.android.core.commerce.model.SellerOrderTransition
import com.us.android.core.commerce.model.SellerReturn
import com.us.android.core.commerce.model.SellerShipment
import com.us.android.core.commerce.network.OrderStatusHistoryDto
import com.us.android.core.commerce.network.SellerEarningDto
import com.us.android.core.commerce.network.SellerOrderCardDto
import com.us.android.core.commerce.network.SellerOrderDto
import com.us.android.core.commerce.network.SellerOrderItemDto
import com.us.android.core.commerce.network.SellerReturnCardDto
import com.us.android.core.commerce.network.ShipmentDto
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import java.util.Base64

/**
 * Wire to domain for the seller surfaces.
 *
 * Kept out of CommerceRepository because these mappers carry the knowledge
 * of the server's dashboard-shaped structs, and the repository is long
 * enough without a second education in Go marshalling. Internal, so the
 * repository is still the only door.
 */

internal fun SellerOrderDto.toSummary() = SellerOrderSummary(
    id = id,
    orderNumber = orderNumber,
    status = OrderStatus.from(status),
    paymentStatus = PaymentStatus.from(paymentStatus),
    paymentMethod = paymentMethod,
    total = totalMinor,
    itemCount = itemCount,
    sellerSubtotal = sellerSubtotalMinor,
    placedAt = createdAt,
)

internal fun OrderStatusHistoryDto.toTransition() = SellerOrderTransition(
    from = fromStatus?.takeIf { it.isNotBlank() }?.let(OrderStatus::from),
    to = OrderStatus.from(toStatus),
    rawTo = toStatus,
    actorType = actorType,
    notes = notes?.takeIf { it.isNotBlank() },
    at = createdAt,
)

internal fun SellerOrderCardDto.toSellerOrder() = SellerOrder(
    id = order.id,
    orderNumber = order.orderNumber,
    status = OrderStatus.from(order.status),
    paymentStatus = PaymentStatus.from(order.paymentStatus),
    paymentMethod = order.paymentMethod,
    placedAt = order.createdAt,
    updatedAt = order.updatedAt,
    cancellationReason = order.cancellationReason,
    cancelledBy = order.cancelledBy,
    lines = items.map(SellerOrderItemDto::toLine),
    shipment = shipment?.toShipment(),
    sellerSubtotal = sellerSubtotalMinor,
    orderTotal = order.totalMinor,
    deliveryAddress = decodeAddressSnapshot(deliveryAddress),
)

internal fun SellerOrderItemDto.toLine() = SellerOrderLine(
    itemId = id,
    productId = productId,
    variantId = variantId,
    title = productTitle,
    sku = sku,
    quantity = quantity,
    unitPrice = unitPriceMinor,
    lineTotal = finalPriceMinor,
    status = status,
    imageUrl = imageUrl ?: thumbnailUrl,
)

internal fun ShipmentDto.toShipment() = SellerShipment(
    id = id,
    courier = courier,
    trackingNumber = trackingNumber,
    trackingUrl = trackingUrl,
    status = status,
    shippedAt = shippedAt,
    deliveredAt = deliveredAt,
    eta = eta,
)

internal fun SellerReturnCardDto.toSellerReturn() = SellerReturn(
    id = request.id,
    orderId = request.orderId,
    orderNumber = order?.orderNumber,
    status = ReturnStatus.from(request.status),
    reasonCode = request.reasonCode,
    reasonDescription = request.reasonDescription,
    requestedAt = request.requestedAt,
    rejectionReason = request.rejectionReason,
    refundAmount = request.refundAmount,
    itemTitle = orderItem?.productTitle,
    itemSku = orderItem?.sku,
    quantity = orderItem?.quantity,
    lineTotal = orderItem?.finalPriceMinor,
    imageUrl = orderItem?.imageUrl ?: orderItem?.thumbnailUrl,
)

internal fun SellerEarningDto.toEarning() = SellerEarning(
    orderItemId = orderItemId,
    orderId = orderId,
    orderNumber = orderNumber,
    productTitle = productTitle,
    sku = sku,
    quantity = quantity,
    gross = grossAmount,
    commission = commissionAmount,
    platformFee = platformFee,
    tds = tdsAmount,
    net = netAmount,
    paymentMethod = paymentMethod,
    status = status,
    deliveredAt = deliveredAt,
)

/**
 * Opens the order's delivery snapshot.
 *
 * The server sends `orders.delivery_address_snapshot` raw: a JSON object,
 * base64-encoded because that is how Go marshals `[]byte`. The keys follow
 * the `customer_addresses` columns (`address_line_1`, `postal_code`, ...),
 * with the checkout-era short names accepted too, because two generations of
 * checkout wrote the column. Anything unreadable is null rather than a
 * failure: the address is one section of the screen, not the screen.
 */
internal fun decodeAddressSnapshot(encoded: String?): Address? {
    val raw = encoded?.takeIf { it.isNotBlank() } ?: return null
    val text = runCatching { String(Base64.getDecoder().decode(raw), Charsets.UTF_8) }
        .getOrNull() ?: return null
    val obj = runCatching { Json.parseToJsonElement(text).jsonObject }.getOrNull() ?: return null

    fun field(vararg keys: String): String? = keys
        .asSequence()
        .mapNotNull { key -> obj[key]?.jsonPrimitive?.content?.takeIf { it.isNotBlank() } }
        .firstOrNull()

    val city = field("city").orEmpty()
    val state = field("state").orEmpty()
    val postalCode = field("postal_code", "pincode", "pin").orEmpty()
    val line1 = field("address_line_1", "line1", "address_line1").orEmpty()
    // A snapshot with no routing fields at all is not an address.
    if (city.isEmpty() && postalCode.isEmpty() && line1.isEmpty()) return null

    return Address(
        id = field("id").orEmpty(),
        label = field("label").orEmpty(),
        contactName = field("contact_name", "name").orEmpty(),
        phone = field("phone").orEmpty(),
        line1 = line1,
        line2 = field("address_line_2", "line2", "address_line2"),
        landmark = field("landmark"),
        city = city,
        state = state,
        postalCode = postalCode,
        isDefault = false,
    )
}
