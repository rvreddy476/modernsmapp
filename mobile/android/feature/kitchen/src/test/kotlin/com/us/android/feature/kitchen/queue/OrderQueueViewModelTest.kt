package com.us.android.feature.kitchen.queue

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModelStore
import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.network.PartnerOrderDto
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.KitchenRepository
import com.us.android.core.food.repository.PartnerOrders
import com.us.android.core.notifications.NotificationUpdates
import com.us.android.core.realtime.SseClient
import com.us.android.core.testing.MainDispatcherRule
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.every
import io.mockk.mockk
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.flow.flow
import org.junit.Rule
import org.junit.Test
import java.time.Instant

/** The live kitchen reads its active lists by status, every page, and says when a read stopped short (M5). */
class OrderQueueViewModelTest {

    @get:Rule
    val main = MainDispatcherRule()

    private val kitchen = mockk<KitchenRepository>()

    private fun viewModel(store: ViewModelStore): OrderQueueViewModel {
        coEvery { kitchen.kitchenQueue(RESTAURANT) } returns FoodResult.Failure(FoodError.Network(null))
        val sse = mockk<SseClient>()
        every { sse.connect(any(), any()) } returns flow { awaitCancellation() }
        return OrderQueueViewModel(
            SavedStateHandle(mapOf("restaurantId" to RESTAURANT)),
            kitchen,
            sse,
            mockk(relaxed = true),
            { Instant.parse("2026-11-10T06:30:00Z") },
            mockk(relaxed = true),
            NotificationUpdates(),
        ).also { store.put("queue", it) }
    }

    @Test
    fun `the active lists ask for only their statuses`() {
        coEvery { kitchen.ordersIn(RESTAURANT, any(), any()) } returns FoodResult.Success(
            PartnerOrders(listOf(PartnerOrderDto(id = "o-1", status = "PREPARING")), complete = true),
        )
        val store = ViewModelStore()
        try {
            val vm = viewModel(store)
            vm.refresh()

            coVerify { kitchen.ordersIn(RESTAURANT, ActiveOrders.STATUSES, any()) }
            assertThat(vm.state.value.preparing.map { it.id }).containsExactly("o-1")
            assertThat(vm.state.value.activeListIncomplete).isFalse()
        } finally { store.clear() }
    }

    @Test
    fun `a read that stopped before its last page shows the may-be-missing note`() {
        coEvery { kitchen.ordersIn(RESTAURANT, any(), any()) } returns FoodResult.Success(
            PartnerOrders(listOf(PartnerOrderDto(id = "o-2", status = "READY_FOR_PICKUP")), complete = false),
        )
        val store = ViewModelStore()
        try {
            val vm = viewModel(store)
            vm.refresh()

            assertThat(vm.state.value.awaitingPickup.map { it.id }).containsExactly("o-2")
            assertThat(vm.state.value.activeListIncomplete).isTrue()
        } finally { store.clear() }
    }

    private companion object {
        const val RESTAURANT = "rst_1"
    }
}
