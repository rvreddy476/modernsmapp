package com.us.android.feature.commerce.seller

import com.us.android.core.commerce.model.OrderStatus
import com.us.android.core.commerce.model.PaymentStatus
import com.us.android.core.commerce.model.SellerOrder

/** One step of an order's life, and whether it has happened. */
data class TimelineEntry(
    val label: String,
    /** RFC 3339 when the server said when; null for a step known only to have happened. */
    val at: String?,
    val done: Boolean,
)

/**
 * The order's life as a list of steps.
 *
 * Derived, because the seller detail carries no status history: the server
 * writes `order_status_history` but does not send it on this route. What it
 * does send is enough for the honest version: the order's own timestamps,
 * the payment status, the current status, and the shipment's shipped and
 * delivered stamps. A step is marked done when the status has passed it,
 * with a time only where the server gave one; a done step with no time is
 * shown as done, never with a guessed time.
 *
 * A cancelled or refunded order ends in one closing step instead of the
 * delivery steps it will never reach.
 */
fun SellerOrder.timeline(): List<TimelineEntry> {
    val rank = status.fulfilmentRank()
    val paid = paymentStatus == PaymentStatus.PAID ||
        paymentStatus == PaymentStatus.REFUND_PENDING ||
        paymentStatus == PaymentStatus.REFUNDED ||
        rank >= RANK_CONFIRMED

    val steps = mutableListOf(
        TimelineEntry("Placed", placedAt, done = true),
        TimelineEntry("Paid", null, done = paid),
    )

    when (status) {
        OrderStatus.CANCELLED, OrderStatus.REFUND_PENDING, OrderStatus.REFUNDED, OrderStatus.EXPIRED -> {
            steps += TimelineEntry(closingLabel(status, cancelledBy), updatedAt, done = true)
        }

        else -> {
            steps += TimelineEntry("Packed", null, done = rank >= RANK_PACKED)
            steps += TimelineEntry(
                "Shipped",
                shipment?.shippedAt,
                done = rank >= RANK_SHIPPED || shipment?.shippedAt != null,
            )
            steps += TimelineEntry(
                "Delivered",
                shipment?.deliveredAt,
                done = rank >= RANK_DELIVERED || shipment?.deliveredAt != null,
            )
        }
    }
    return steps
}

private fun closingLabel(status: OrderStatus, cancelledBy: String?): String = when (status) {
    OrderStatus.EXPIRED -> "Expired unpaid"
    OrderStatus.REFUND_PENDING -> "Cancelled, refund on the way"
    OrderStatus.REFUNDED -> "Refunded"
    else -> when (cancelledBy?.lowercase()) {
        "customer" -> "Cancelled by the buyer"
        "seller" -> "Cancelled by you"
        "admin" -> "Cancelled by Momentum"
        else -> "Cancelled"
    }
}

/**
 * How far along the fulfilment path a status is. Statuses off the path
 * (payment states, cancellation) rank below the first step.
 */
private fun OrderStatus.fulfilmentRank(): Int = when (this) {
    OrderStatus.CONFIRMED -> RANK_CONFIRMED
    OrderStatus.PACKED -> RANK_PACKED
    OrderStatus.SHIPPED -> RANK_SHIPPED
    OrderStatus.OUT_FOR_DELIVERY -> RANK_OUT_FOR_DELIVERY
    OrderStatus.DELIVERED -> RANK_DELIVERED
    else -> 0
}

private const val RANK_CONFIRMED = 1
private const val RANK_PACKED = 2
private const val RANK_SHIPPED = 3
private const val RANK_OUT_FOR_DELIVERY = 4
private const val RANK_DELIVERED = 5
