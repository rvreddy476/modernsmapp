package com.us.android.feature.kitchen.queue

import com.us.android.core.food.network.PartnerOrderDto
import com.us.android.core.food.repository.PartnerOrders
import com.us.android.feature.kitchen.ui.OrderStatusText

/**
 * The kitchen's two active lists: orders being cooked, and orders waiting for
 * a rider (food audit M5).
 *
 * They are read with `status=` naming only these statuses, every page of it,
 * so an old order still in progress is never pushed out by newer delivered or
 * cancelled ones. Each list is still filtered by status here: a server that
 * predates the filter answers the latest 100 orders of any status.
 */
data class ActiveOrders(
    val preparing: List<PartnerOrderDto>,
    val awaitingPickup: List<PartnerOrderDto>,
    /** The read stopped before the last page (a looping server); the lists may be short. */
    val incomplete: Boolean,
) {
    companion object {
        /** Exactly the statuses the two lists show: nothing else is asked for. */
        val STATUSES: List<String> = listOf(OrderStatusText.PREPARING) + OrderStatusText.AWAITING_PICKUP

        fun of(read: PartnerOrders): ActiveOrders = ActiveOrders(
            preparing = read.items.filter { it.status == OrderStatusText.PREPARING },
            awaitingPickup = read.items.filter { it.status in OrderStatusText.AWAITING_PICKUP },
            incomplete = !read.complete,
        )
    }
}
