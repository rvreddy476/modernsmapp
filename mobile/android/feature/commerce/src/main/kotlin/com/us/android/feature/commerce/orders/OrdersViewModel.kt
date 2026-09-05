package com.us.android.feature.commerce.orders

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.Order
import com.us.android.core.commerce.model.OrderStatus
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

// ─── List ────────────────────────────────────────────────────────────

sealed interface OrdersUiState {
    data object Loading : OrdersUiState
    data object Empty : OrdersUiState
    data class Content(val orders: List<Order>) : OrdersUiState
    data class Failed(val message: String, val retryable: Boolean) : OrdersUiState
}

/**
 * Which orders a list shows.
 *
 * MStore's profile menu has both "My orders" and "Purchase history", and they
 * are not the same question: the first is everything, including the parcel on
 * its way; the second is what has already happened, which is what someone
 * looking for a past purchase actually wants to scroll.
 */
enum class OrderScope(val wire: String, val title: String) {
    ALL("all", "Your orders"),
    PAST("past", "Purchase history"),
    ;

    companion object {
        fun from(raw: String?): OrderScope =
            entries.firstOrNull { it.wire == raw } ?: ALL
    }
}

/**
 * Whether an order belongs in [OrderScope.PAST].
 *
 * Terminal states only. An order still moving is not history, and a payment
 * that has not settled is emphatically not — putting either in "purchase
 * history" tells the buyer something finished when it has not.
 *
 * Pure, so the boundary is a table test rather than an eyeball.
 */
fun isPast(order: Order): Boolean = when (order.status) {
    OrderStatus.DELIVERED,
    OrderStatus.CANCELLED,
    OrderStatus.REFUNDED,
    -> true

    else -> false
}

@HiltViewModel
class OrdersViewModel @Inject constructor(
    private val repo: CommerceRepository,
    savedState: SavedStateHandle,
) : ViewModel() {

    /** ALL unless the route asked for history. */
    val scope: OrderScope = OrderScope.from(savedState["scope"])

    private val _state = MutableStateFlow<OrdersUiState>(OrdersUiState.Loading)
    val state: StateFlow<OrdersUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    /**
     * Reloads the list.
     *
     * Called on every return to the screen. An order's status changes on the
     * server — a webhook confirms a payment, a courier scans a parcel — with
     * nothing to tell the app, so a cached list goes stale silently.
     */
    fun refresh() {
        _state.value = OrdersUiState.Loading
        viewModelScope.launch {
            when (val r = repo.orders()) {
                is CommerceResult.Failure ->
                    _state.value =
                        OrdersUiState.Failed(r.error.describe(), r.error.isRetryable())

                is CommerceResult.Success -> {
                    // The scope is applied here rather than asked of the
                    // server: `GET /orders` has no status filter, and inventing
                    // a query parameter the server ignores would silently
                    // return everything under a title that promised less.
                    val orders = when (scope) {
                        OrderScope.ALL -> r.value
                        OrderScope.PAST -> r.value.filter(::isPast)
                    }
                    _state.value =
                        if (orders.isEmpty()) OrdersUiState.Empty else OrdersUiState.Content(orders)
                }
            }
        }
    }
}

// ─── Detail ──────────────────────────────────────────────────────────

sealed interface OrderDetailUiState {
    data object Loading : OrderDetailUiState

    data class Content(
        val order: Order,
        val cancelling: Boolean = false,
        /** Set while the confirm-cancel dialog is open. */
        val confirmingCancel: Boolean = false,
        val message: String? = null,
    ) : OrderDetailUiState

    data class Failed(val message: String, val retryable: Boolean) : OrderDetailUiState
}

@HiltViewModel
class OrderDetailViewModel @Inject constructor(
    private val repo: CommerceRepository,
    savedState: SavedStateHandle,
) : ViewModel() {

    private val orderId: String = requireNotNull(savedState["orderId"]) {
        "OrderDetailViewModel requires an orderId argument"
    }

    private val _state = MutableStateFlow<OrderDetailUiState>(OrderDetailUiState.Loading)
    val state: StateFlow<OrderDetailUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        _state.value = OrderDetailUiState.Loading
        viewModelScope.launch { load() }
    }

    private suspend fun load() {
        when (val r = repo.order(orderId)) {
            is CommerceResult.Failure ->
                _state.value =
                    OrderDetailUiState.Failed(r.error.describe(), r.error.isRetryable())

            is CommerceResult.Success -> _state.value = OrderDetailUiState.Content(r.value)
        }
    }

    fun askToCancel() {
        val current = _state.value as? OrderDetailUiState.Content ?: return
        _state.value = current.copy(confirmingCancel = true)
    }

    fun dismissCancel() {
        val current = _state.value as? OrderDetailUiState.Content ?: return
        _state.value = current.copy(confirmingCancel = false)
    }

    /**
     * Cancels the order.
     *
     * `canCancel` comes from the server and is re-checked there: the D6
     * matrix is enforced by a database trigger, so a stale button cannot
     * cancel something that has already shipped. The client hides the action
     * as a courtesy, not as the control.
     */
    fun cancel(reason: String) {
        val current = _state.value as? OrderDetailUiState.Content ?: return
        if (current.cancelling) return

        _state.value = current.copy(cancelling = true, confirmingCancel = false, message = null)
        viewModelScope.launch {
            when (val r = repo.cancelOrder(orderId, reason.ifBlank { "Changed my mind" })) {
                is CommerceResult.Failure -> _state.value = current.copy(
                    cancelling = false,
                    confirmingCancel = false,
                    message = r.error.describe(),
                )

                is CommerceResult.Success -> {
                    // Re-read rather than assuming a status. Cancelling a PAID
                    // order moves it to refund_pending, not cancelled, and the
                    // difference matters to the customer waiting for money back.
                    load()
                }
            }
        }
    }

    fun dismissMessage() {
        val current = _state.value as? OrderDetailUiState.Content ?: return
        _state.value = current.copy(message = null)
    }
}
