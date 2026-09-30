package com.us.android.feature.commerce

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.Order
import com.us.android.core.commerce.network.OrderDto
import com.us.android.core.commerce.network.OrderListDto
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.commerce.orders.OrderScope
import com.us.android.feature.commerce.orders.OrdersUiState
import com.us.android.feature.commerce.orders.OrdersViewModel
import com.us.android.feature.commerce.orders.appendOrders
import com.us.android.feature.commerce.payments.PaymentsUiState
import com.us.android.feature.commerce.payments.PaymentsViewModel
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Response

/**
 * The buyer's orders page (2026-09-30).
 *
 * `GET /orders` answers twenty rows and a `meta.next_cursor`; the list used
 * to drop the cursor, so a buyer's twenty-first order did not exist on the
 * phone. Now the first page is shown, the next is asked for with the cursor
 * as the end scrolls near, and it is APPENDED — never duplicated, never
 * replacing what is already on screen. Purchase history and Payments page
 * the same way. Through the REAL repository over the fake wire.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class OrdersPagingTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() = Dispatchers.setMain(dispatcher)

    @After
    fun tearDown() = Dispatchers.resetMain()

    private fun order(id: String, status: String = "confirmed") =
        OrderDto(id = id, orderNumber = "MS-$id", status = status, paymentStatus = "paid")

    /** Pages keyed by the cursor that asks for them; null is the first page. */
    private class PagedApi(private val pages: Map<String?, OrderListDto>) : FakeCommerceApi() {
        val cursorsAsked = mutableListOf<String?>()
        var failCursor: String? = null

        override suspend fun listOrders(cursor: String?, limit: Int): Response<ApiEnvelope<OrderListDto>> {
            cursorsAsked += cursor
            if (cursor != null && cursor == failCursor) return unused()
            return envelope(pages.getValue(cursor))
        }
    }

    private fun twoPages(second: String = "page-2") = PagedApi(
        mapOf(
            null to OrderListDto(items = listOf(order("o-1"), order("o-2")), nextCursor = second),
            second to OrderListDto(items = listOf(order("o-2"), order("o-3")), nextCursor = null),
        ),
    )

    private fun ordersVm(api: PagedApi, scope: OrderScope = OrderScope.ALL) =
        OrdersViewModel(CommerceRepository(api), SavedStateHandle(mapOf("scope" to scope.wire)))

    private fun content(vm: OrdersViewModel) = vm.state.value as OrdersUiState.Content

    @Test
    fun `the first page keeps the cursor, the second is appended with no duplicates`() = runTest(dispatcher) {
        val api = twoPages()
        val vm = ordersVm(api)
        dispatcher.scheduler.advanceUntilIdle()

        assertThat(content(vm).orders.map { it.id }).containsExactly("o-1", "o-2").inOrder()
        assertThat(content(vm).nextCursor).isEqualTo("page-2")
        assertThat(content(vm).hasMore).isTrue()

        vm.loadMore()
        dispatcher.scheduler.advanceUntilIdle()

        // o-2 came back on both pages (its status moved between reads); it is drawn once.
        assertThat(content(vm).orders.map { it.id }).containsExactly("o-1", "o-2", "o-3").inOrder()
        assertThat(content(vm).hasMore).isFalse()
        assertThat(api.cursorsAsked).containsExactly(null, "page-2").inOrder()
    }

    @Test
    fun `a load-more in flight is not asked for twice, and none is asked at the end`() = runTest(dispatcher) {
        val api = twoPages()
        val vm = ordersVm(api)
        dispatcher.scheduler.advanceUntilIdle()

        vm.loadMore()
        vm.loadMore()
        dispatcher.scheduler.advanceUntilIdle()
        vm.loadMore()
        dispatcher.scheduler.advanceUntilIdle()

        assertThat(api.cursorsAsked).containsExactly(null, "page-2").inOrder()
    }

    @Test
    fun `a failed page keeps what is on screen and says so at the end`() = runTest(dispatcher) {
        val api = twoPages().apply { failCursor = "page-2" }
        val vm = ordersVm(api)
        dispatcher.scheduler.advanceUntilIdle()

        vm.loadMore()
        dispatcher.scheduler.advanceUntilIdle()

        assertThat(content(vm).orders.map { it.id }).containsExactly("o-1", "o-2").inOrder()
        assertThat(content(vm).loadMoreError).isNotNull()
        assertThat(content(vm).loadingMore).isFalse()
        // The cursor is kept, so "Load more" can try again.
        assertThat(content(vm).nextCursor).isEqualTo("page-2")
    }

    @Test
    fun `purchase history reads past the first page when it holds nothing terminal`() = runTest(dispatcher) {
        val api = PagedApi(
            mapOf(
                null to OrderListDto(items = listOf(order("o-1", "shipped")), nextCursor = "p2"),
                "p2" to OrderListDto(items = listOf(order("o-2", "delivered")), nextCursor = "p3"),
                "p3" to OrderListDto(items = listOf(order("o-3", "cancelled")), nextCursor = null),
            ),
        )
        val vm = ordersVm(api, OrderScope.PAST)
        dispatcher.scheduler.advanceUntilIdle()

        // The first page was empty in scope, so the second was read; the
        // third waits for the buyer to scroll.
        assertThat(content(vm).orders.map { it.id }).containsExactly("o-2")
        assertThat(content(vm).nextCursor).isEqualTo("p3")

        vm.loadMore()
        dispatcher.scheduler.advanceUntilIdle()

        assertThat(content(vm).orders.map { it.id }).containsExactly("o-2", "o-3").inOrder()
    }

    @Test
    fun `payments page the same way`() = runTest(dispatcher) {
        val api = twoPages()
        val vm = PaymentsViewModel(CommerceRepository(api))
        dispatcher.scheduler.advanceUntilIdle()

        vm.loadMore()
        dispatcher.scheduler.advanceUntilIdle()

        val content = vm.state.value as PaymentsUiState.Content
        assertThat(content.orders.map { it.id }).containsExactly("o-1", "o-2", "o-3").inOrder()
        assertThat(content.hasMore).isFalse()
    }

    // ─── The pure rule ───────────────────────────────────────────────

    @Test
    fun `appending keeps the existing rows first and drops an order already shown`() = runTest(dispatcher) {
        // Domain orders are built through the repository's own mapping.
        val existing = domainOrders(order("o-1"), order("o-2"))
        val incoming = domainOrders(order("o-2"), order("o-3"))

        val merged = appendOrders(existing, incoming)

        assertThat(merged.map { it.id }).containsExactly("o-1", "o-2", "o-3").inOrder()
        assertThat(appendOrders(existing, emptyList())).isEqualTo(existing)
        assertThat(appendOrders(emptyList(), incoming)).isEqualTo(incoming)
    }

    private suspend fun domainOrders(vararg rows: OrderDto): List<Order> {
        val api = object : FakeCommerceApi() {
            override suspend fun listOrders(cursor: String?, limit: Int): Response<ApiEnvelope<OrderListDto>> =
                envelope(OrderListDto(items = rows.toList()))
        }
        return (CommerceRepository(api).orders() as CommerceResult.Success).value.items
    }
}
