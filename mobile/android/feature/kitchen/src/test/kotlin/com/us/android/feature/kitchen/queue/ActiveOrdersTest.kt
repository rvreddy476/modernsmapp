package com.us.android.feature.kitchen.queue

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.network.PartnerOrderDto
import com.us.android.core.food.repository.PartnerOrders
import org.junit.Test

/** The kitchen's active lists come from a status-filtered, fully paged read (food audit M5). */
class ActiveOrdersTest {

    private fun order(id: String, status: String) = PartnerOrderDto(id = id, status = status)

    @Test
    fun `only the statuses the two lists show are asked for`() {
        assertThat(ActiveOrders.STATUSES)
            .containsExactly("PREPARING", "READY_FOR_PICKUP", "DELIVERY_ASSIGNING", "DELIVERY_ASSIGNED")
    }

    @Test
    fun `orders split into cooking and waiting for a rider, and anything else is dropped`() {
        val read = PartnerOrders(
            listOf(
                order("o-1", "PREPARING"),
                order("o-2", "DELIVERY_ASSIGNED"),
                // An older server ignores the filter: these must not reach either list.
                order("o-3", "DELIVERED"),
                order("o-4", "CONFIRMED"),
                order("o-5", "READY_FOR_PICKUP"),
            ),
            complete = true,
        )

        val active = ActiveOrders.of(read)

        assertThat(active.preparing.map { it.id }).containsExactly("o-1")
        assertThat(active.awaitingPickup.map { it.id }).containsExactly("o-2", "o-5").inOrder()
        assertThat(active.incomplete).isFalse()
    }

    @Test
    fun `a read that stopped early says so`() {
        assertThat(ActiveOrders.of(PartnerOrders(emptyList(), complete = false)).incomplete).isTrue()
    }

    @Test
    fun `a full read of more than a hundred active orders is not flagged`() {
        val many = (1..250).map { order("o-$it", "PREPARING") }

        val active = ActiveOrders.of(PartnerOrders(many, complete = true))

        assertThat(active.preparing).hasSize(250)
        assertThat(active.incomplete).isFalse()
    }
}
