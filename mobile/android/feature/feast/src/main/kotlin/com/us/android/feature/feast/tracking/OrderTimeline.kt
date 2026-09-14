package com.us.android.feature.feast.tracking

import com.us.android.core.food.network.FeastOrderDto

/** One step of the customer-facing timeline. */
enum class TimelineStep(val title: String, val detail: String) {
    PLACED("Order placed", "Waiting for payment to be confirmed"),
    CONFIRMED("Confirmed", "The restaurant has your order"),
    PREPARING("Preparing", "Your food is being made"),
    READY("Ready", "Waiting for your delivery partner"),
    ON_THE_WAY("On the way", "Your delivery partner has your food"),
    DELIVERED("Delivered", "Enjoy your meal"),
}

data class TimelineView(
    val steps: List<TimelineStep>,
    /** Index into [steps] of the step in progress; steps before it are done. */
    val currentIndex: Int,
    val cancelled: Boolean,
    val cancelledText: String?,
)

/** food-service order statuses (orderstate.go) onto the six customer steps. */
object OrderTimeline {

    fun of(order: FeastOrderDto): TimelineView {
        val steps = TimelineStep.entries
        val status = order.status
        if (status.startsWith("CANCELLED") || status == "REJECTED" || status == "FAILED" || status == "REFUNDED") {
            val lastReached = order.history.map { stepFor(it.toStatus) }.filterNotNull().maxOrNull() ?: TimelineStep.PLACED
            return TimelineView(steps, lastReached.ordinal, cancelled = true, cancelledText = cancelledText(status))
        }
        val step = stepFor(status) ?: TimelineStep.PLACED
        return TimelineView(steps, step.ordinal, cancelled = false, cancelledText = null)
    }

    fun isLive(status: String): Boolean = status !in TERMINAL && !status.startsWith("CANCELLED")

    fun stepFor(status: String): TimelineStep? = when (status) {
        "PLACED", "PAYMENT_PENDING" -> TimelineStep.PLACED
        "CONFIRMED", "ACCEPTED" -> TimelineStep.CONFIRMED
        "PREPARING" -> TimelineStep.PREPARING
        "READY_FOR_PICKUP", "DELIVERY_ASSIGNING", "DELIVERY_ASSIGNED" -> TimelineStep.READY
        "PICKED_UP", "OUT_FOR_DELIVERY" -> TimelineStep.ON_THE_WAY
        "DELIVERED" -> TimelineStep.DELIVERED
        else -> null
    }

    fun label(status: String): String = when {
        status.startsWith("CANCELLED") -> "Cancelled"
        status == "REJECTED" -> "Declined"
        status == "REFUNDED" -> "Refunded"
        else -> stepFor(status)?.title ?: status.lowercase().replace('_', ' ').replaceFirstChar { it.uppercase() }
    }

    private fun cancelledText(status: String): String = when (status) {
        "CANCELLED_BY_CUSTOMER" -> "You cancelled this order"
        "CANCELLED_BY_RESTAURANT", "REJECTED" -> "The restaurant couldn't take this order"
        "CANCELLED_BY_ADMIN" -> "This order was cancelled by Feast support"
        "REFUNDED" -> "This order was refunded"
        else -> "This order was cancelled"
    }

    private val TERMINAL = setOf("DELIVERED", "REJECTED", "FAILED", "REFUNDED")
}
