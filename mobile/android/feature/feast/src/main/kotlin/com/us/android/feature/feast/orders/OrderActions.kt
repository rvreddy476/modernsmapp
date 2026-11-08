package com.us.android.feature.feast.orders

import com.us.android.core.food.network.FeastOrderDto

/**
 * What a customer may do with one order, read from the server's own fields.
 *
 * Cancel follows `can_cancel` (food-service `orderstate.CustomerCanCancel`,
 * founder decision 31 Oct): true only from PLACED or CONFIRMED. Once the
 * kitchen has started, a paid order offers a refund request instead. An unpaid
 * online order still inside its payment hold offers Pay now, which pays the
 * SAME order with a new attempt (food audit M1).
 */
object OrderActions {

    fun canCancel(order: FeastOrderDto): Boolean = order.canCancel ?: (order.status in CANCELLABLE_FALLBACK)

    /** ONLINE, awaiting payment, and not captured: `payments.awaitingPayment` on the server. */
    fun canPayNow(order: FeastOrderDto): Boolean =
        order.paymentMethod.equals(ONLINE, ignoreCase = true) &&
            order.status in AWAITING_PAYMENT &&
            !order.paymentStatus.equals(CAPTURED, ignoreCase = true)

    /** The kitchen has started (or delivered) a paid order: support decides a refund. */
    fun canRequestRefund(order: FeastOrderDto): Boolean =
        !canCancel(order) &&
            order.paymentStatus.equals(CAPTURED, ignoreCase = true) &&
            order.status in KITCHEN_STARTED_OR_DELIVERED

    /** For a server that predates `can_cancel`: the same two statuses `CustomerCanCancel` allows. */
    private val CANCELLABLE_FALLBACK = setOf("PLACED", "CONFIRMED")

    private val AWAITING_PAYMENT = setOf("PLACED", "PAYMENT_PENDING", "PAYMENT_FAILED")

    /** `orderstate.kitchenStarted` plus DELIVERED. */
    private val KITCHEN_STARTED_OR_DELIVERED = setOf(
        "PREPARING",
        "READY_FOR_PICKUP",
        "DELIVERY_ASSIGNING",
        "DELIVERY_ASSIGNED",
        "PICKED_UP",
        "OUT_FOR_DELIVERY",
        "DELIVERED",
    )

    private const val ONLINE = "ONLINE"
    private const val CAPTURED = "CAPTURED"
}
