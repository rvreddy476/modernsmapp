package com.us.android.feature.commerce.seller

import com.us.android.core.commerce.model.OrderStatus
import com.us.android.core.commerce.model.PaymentStatus
import com.us.android.core.commerce.model.SellerOrder
import com.us.android.core.commerce.model.SellerOrderTransition

/** One step of an order's life, and whether it has happened. */
data class TimelineEntry(
    val label: String,
    /** RFC 3339 when the server said when; null for a step known only to have happened. */
    val at: String?,
    val done: Boolean,
)

/**
 * The order's life from the server's own record.
 *
 * [history] is `order_status_history`, oldest first, from
 * `GET /seller/orders/{id}/history`: each row is one step, done, at the
 * moment the server wrote it, so every time shown here is a time something
 * actually happened. Two things are added around the record. A "Placed"
 * step is put first when the history does not begin with the order's
 * creation, timed from the order's own stamp. And for an order still on its
 * way, the fulfilment steps it has not reached follow as not done, so the
 * seller sees what is left as well as what happened.
 *
 * An empty record falls back to the derived [timeline]: it is what the app
 * showed before the route existed, and it is still honest.
 */
fun SellerOrder.timeline(history: List<SellerOrderTransition>): List<TimelineEntry> {
    if (history.isEmpty()) return timeline()

    val steps = mutableListOf<TimelineEntry>()
    if (history.first().to != OrderStatus.PAYMENT_PENDING) {
        steps += TimelineEntry("Placed", placedAt, done = true)
    }
    history.mapTo(steps) { TimelineEntry(it.label(), it.at, done = true) }

    if (!status.isClosed()) {
        // The record can lag the row by one move (the trigger writes the
        // history after the update), so the further of the two is the truth.
        val reached = maxOf(status.fulfilmentRank(), history.maxOf { it.to.fulfilmentRank() })
        if (reached < RANK_CONFIRMED) steps += TimelineEntry("Confirmed", null, done = false)
        if (reached < RANK_PACKED) steps += TimelineEntry("Packed", null, done = false)
        if (reached < RANK_SHIPPED) steps += TimelineEntry("Shipped", null, done = false)
        if (reached < RANK_DELIVERED) steps += TimelineEntry("Delivered", null, done = false)
    }
    return steps
}

/** What one recorded move reads as. The closing moves say who made them. */
private fun SellerOrderTransition.label(): String = when (to) {
    OrderStatus.PAYMENT_PENDING -> "Placed"
    OrderStatus.PAYMENT_FAILED -> "Payment failed"
    OrderStatus.CONFIRMED -> "Confirmed"
    OrderStatus.PACKED -> "Packed"
    OrderStatus.SHIPPED -> "Shipped"
    OrderStatus.OUT_FOR_DELIVERY -> "Out for delivery"
    OrderStatus.DELIVERED -> "Delivered"
    OrderStatus.CANCELLED, OrderStatus.REFUND_PENDING, OrderStatus.REFUNDED, OrderStatus.EXPIRED ->
        closingLabel(to, actorType)
    // A status this build does not know: shown as the server said it, not hidden.
    OrderStatus.UNKNOWN -> rawTo.replace('_', ' ').replaceFirstChar { it.uppercase() }.ifBlank { "Updated" }
}

private fun OrderStatus.isClosed(): Boolean = when (this) {
    OrderStatus.CANCELLED, OrderStatus.REFUND_PENDING, OrderStatus.REFUNDED, OrderStatus.EXPIRED -> true
    else -> false
}

/**
 * The order's life as a list of steps, derived.
 *
 * The fallback for a server that does not send `order_status_history` on
 * the seller route (a bare 404 from the history endpoint). What the detail
 * does send is enough for an honest version: the order's own timestamps,
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

/**
 * The closing step's words. [who] is the order's `cancelled_by` or a history
 * row's `actor_type`; the server uses one vocabulary for both.
 */
private fun closingLabel(status: OrderStatus, who: String?): String = when (status) {
    OrderStatus.EXPIRED -> "Expired unpaid"
    OrderStatus.REFUND_PENDING -> "Cancelled, refund on the way"
    OrderStatus.REFUNDED -> "Refunded"
    else -> when (who?.lowercase()) {
        "customer" -> "Cancelled by the buyer"
        "seller" -> "Cancelled by you"
        "admin" -> "Cancelled by Momentum"
        "system" -> "Cancelled automatically"
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
