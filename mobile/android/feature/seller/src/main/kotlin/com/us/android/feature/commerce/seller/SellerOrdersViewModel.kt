package com.us.android.feature.commerce.seller

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.OrderStatus
import com.us.android.core.commerce.model.SellerOrderSummary
import com.us.android.core.commerce.network.SELLER_PAGE_SIZE
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.feature.commerce.ui.describe
import com.us.android.feature.commerce.ui.isRetryable
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The chips over the seller's order list.
 *
 * Named for what the seller has to DO, not for the status word: a seller
 * opening the app wants "what do I pack today", and "confirmed" does not
 * say that. Applied on the client, because `GET /seller/orders` takes no
 * status filter and inventing a query parameter the server ignores would
 * return everything under a chip that promised less.
 */
enum class SellerOrderFilter(val label: String, private val statuses: Set<OrderStatus>?) {
    ALL("All", null),
    TO_PACK("To pack", setOf(OrderStatus.CONFIRMED)),
    TO_SHIP("To ship", setOf(OrderStatus.PACKED)),
    IN_TRANSIT("On the way", setOf(OrderStatus.SHIPPED, OrderStatus.OUT_FOR_DELIVERY)),
    DELIVERED("Delivered", setOf(OrderStatus.DELIVERED)),
    CLOSED("Cancelled", setOf(OrderStatus.CANCELLED, OrderStatus.REFUND_PENDING, OrderStatus.REFUNDED)),
    ;

    fun matches(status: OrderStatus): Boolean = statuses?.contains(status) ?: true
}

sealed interface SellerOrdersUiState {
    data object Loading : SellerOrdersUiState

    data class Content(
        /** Every row loaded so far, unfiltered, in server order. */
        val orders: List<SellerOrderSummary>,
        val filter: SellerOrderFilter = SellerOrderFilter.ALL,
        val loadingMore: Boolean = false,
        /** The server answered a short page, so there is nothing past [orders]. */
        val exhausted: Boolean = false,
        /** A failed load-more; the rows already shown stay. */
        val message: String? = null,
    ) : SellerOrdersUiState {
        val visible: List<SellerOrderSummary> get() = orders.filter { filter.matches(it.status) }
        val canLoadMore: Boolean get() = !exhausted && !loadingMore
    }

    data class Failed(val message: String, val retryable: Boolean) : SellerOrdersUiState
}

/**
 * The seller's order list.
 *
 * Offset-paged because the route is: the next request asks from the count
 * already held. That is good enough for a list a seller scrolls, and it is
 * what exists; a cursor would need a server change first.
 */
@HiltViewModel
class SellerOrdersViewModel @Inject constructor(
    private val repo: CommerceRepository,
) : ViewModel() {

    private val _state = MutableStateFlow<SellerOrdersUiState>(SellerOrdersUiState.Loading)
    val state: StateFlow<SellerOrdersUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    /** Reloads from the top. Called on every return: a courier scan changes a status with nothing to tell the app. */
    fun refresh() {
        val filter = (_state.value as? SellerOrdersUiState.Content)?.filter ?: SellerOrderFilter.ALL
        _state.value = SellerOrdersUiState.Loading
        viewModelScope.launch {
            when (val r = repo.sellerOrders(offset = 0)) {
                is CommerceResult.Failure ->
                    _state.value = SellerOrdersUiState.Failed(r.error.describe(), r.error.isRetryable())

                is CommerceResult.Success -> _state.value = SellerOrdersUiState.Content(
                    orders = r.value,
                    filter = filter,
                    exhausted = r.value.size < SELLER_PAGE_SIZE,
                )
            }
        }
    }

    fun select(filter: SellerOrderFilter) {
        val current = _state.value as? SellerOrdersUiState.Content ?: return
        _state.value = current.copy(filter = filter)
    }

    fun loadMore() {
        val current = _state.value as? SellerOrdersUiState.Content ?: return
        if (!current.canLoadMore) return

        _state.value = current.copy(loadingMore = true, message = null)
        viewModelScope.launch {
            when (val r = repo.sellerOrders(offset = current.orders.size)) {
                is CommerceResult.Failure -> _state.value = current.copy(
                    loadingMore = false,
                    message = r.error.describe(),
                )

                is CommerceResult.Success -> _state.value = current.copy(
                    // De-duplicated by id: an order confirmed between two
                    // pages shifts the offset and the server repeats a row.
                    orders = (current.orders + r.value).distinctBy { it.id },
                    loadingMore = false,
                    exhausted = r.value.size < SELLER_PAGE_SIZE,
                )
            }
        }
    }
}
