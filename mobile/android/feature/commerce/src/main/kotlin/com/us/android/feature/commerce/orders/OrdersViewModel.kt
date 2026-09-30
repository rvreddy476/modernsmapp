package com.us.android.feature.commerce.orders

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.Order
import com.us.android.core.commerce.model.OrderStatus
import com.us.android.core.commerce.payment.PaymentAttempt
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.core.payments.PaymentConfirmation
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentHandoffEvent
import com.us.android.feature.commerce.checkout.CommercePaymentStatusSource
import com.us.android.feature.commerce.checkout.MSTORE_PAYMENT_APPLICATION_ID
import com.us.android.feature.commerce.checkout.toSheetAttempt
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

    /**
     * The orders loaded so far.
     *
     * [nextCursor] is the server's continuation for the page after these;
     * null means there is no more. [loadingMore] is set while the next page
     * is in flight, so the list shows one footer spinner and asks once.
     */
    data class Content(
        val orders: List<Order>,
        val nextCursor: String? = null,
        val loadingMore: Boolean = false,
        /** The last page's failure, shown as one line at the end of the list. */
        val loadMoreError: String? = null,
    ) : OrdersUiState {
        val hasMore: Boolean get() = nextCursor != null
    }

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

/**
 * The list after another page arrived: [incoming] after [existing], with any
 * order already on screen dropped rather than drawn twice.
 *
 * The server pages on a keyset, so a duplicate should not happen — but an
 * order whose status moved between two page reads can, and a list with the
 * same order twice is a list that crashes on its LazyColumn key. Pure, so
 * the rule is a table test (2026-09-30).
 */
fun appendOrders(existing: List<Order>, incoming: List<Order>): List<Order> {
    val seen = existing.mapTo(mutableSetOf()) { it.id }
    return existing + incoming.filter { seen.add(it.id) }
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
     * Reloads the list from the first page.
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
                    var orders = inScope(r.value.items)
                    var cursor = r.value.nextCursor
                    // The scope is applied here rather than asked of the
                    // server: `GET /orders` has no status filter, and inventing
                    // a query parameter the server ignores would silently
                    // return everything under a title that promised less.
                    // So a history page can be EMPTY while later pages are
                    // not; keep reading until something is in scope or the
                    // pages run out, within a bound so a buyer with a
                    // thousand open orders is not made to wait on all of them.
                    var pagesRead = 1
                    while (orders.isEmpty() && cursor != null && pagesRead < MAX_PAGES_TO_FILL) {
                        val next = repo.orders(cursor)
                        if (next !is CommerceResult.Success) break
                        orders = appendOrders(orders, inScope(next.value.items))
                        cursor = next.value.nextCursor
                        pagesRead++
                    }
                    _state.value = if (orders.isEmpty() && cursor == null) {
                        OrdersUiState.Empty
                    } else {
                        OrdersUiState.Content(orders, nextCursor = cursor)
                    }
                }
            }
        }
    }

    /**
     * The next page, appended. A no-op while one is already in flight or
     * when the server said there is none: the list calls this on every
     * scroll to the end, and asking twice would append the same page twice.
     */
    fun loadMore() {
        val current = _state.value as? OrdersUiState.Content ?: return
        val cursor = current.nextCursor ?: return
        if (current.loadingMore) return

        _state.value = current.copy(loadingMore = true, loadMoreError = null)
        viewModelScope.launch {
            val latest = _state.value as? OrdersUiState.Content ?: return@launch
            _state.value = when (val r = repo.orders(cursor)) {
                is CommerceResult.Failure -> latest.copy(
                    loadingMore = false,
                    loadMoreError = r.error.describe(),
                )

                is CommerceResult.Success -> latest.copy(
                    orders = appendOrders(latest.orders, inScope(r.value.items)),
                    nextCursor = r.value.nextCursor,
                    loadingMore = false,
                )
            }
        }
    }

    private fun inScope(orders: List<Order>): List<Order> = when (scope) {
        OrderScope.ALL -> orders
        OrderScope.PAST -> orders.filter(::isPast)
    }

    private companion object {
        const val MAX_PAGES_TO_FILL = 5
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
        /**
         * Set from "Pay now" until the payment is settled or refused: the
         * sheet is opening, open, or the server is being polled for the
         * capture. The button is held while it is true.
         */
        val paying: Boolean = false,
        val message: String? = null,
    ) : OrderDetailUiState

    data class Failed(val message: String, val retryable: Boolean) : OrderDetailUiState
}

/**
 * One order, and the two things a buyer does to it: cancel, and pay again.
 *
 * "Pay now" (2026-09-30, contract §4.7) is a NEW payment attempt against the
 * existing order: `:app` opens the intent through [CheckoutPaymentOpener],
 * the server re-reserves the stock and re-opens the order (or refuses with
 * OUT_OF_STOCK, which arrives here as one line), the sheet's ending arrives
 * on [PaymentHandoff], and the server is then polled for the truth exactly as
 * checkout polls it. Before this the screen opened the sheet and listened for
 * nothing, so a retried payment settled on the server with the screen still
 * saying "Payment failed".
 */
@HiltViewModel
class OrderDetailViewModel @Inject constructor(
    private val repo: CommerceRepository,
    private val handoff: PaymentHandoff,
    private val payments: PaymentCoordinator,
    savedState: SavedStateHandle,
) : ViewModel() {

    private val orderId: String = requireNotNull(savedState["orderId"]) {
        "OrderDetailViewModel requires an orderId argument"
    }

    private val _state = MutableStateFlow<OrderDetailUiState>(OrderDetailUiState.Loading)
    val state: StateFlow<OrderDetailUiState> = _state.asStateFlow()

    /** MStore's payment status endpoint, as the coordinator reads it. */
    private val paymentStatus = CommercePaymentStatusSource(repo)

    /** The attempt "Pay now" minted, so a late ending for an earlier one is ignored. */
    private var activeAttempt: PaymentAttempt? = null

    private var observingHandoff = false

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

    /**
     * Starts a payment attempt for this order, or null when one is already
     * under way. The screen hands the attempt to `:app`, which opens the
     * intent and the sheet; the ending comes back through [observePaymentHandoff].
     *
     * C3-LB-4: a NEW attempt id every time. The previous attempt may still
     * have a callback in flight, and it must not be able to settle this one.
     */
    fun payNow(): PaymentAttempt? {
        val current = _state.value as? OrderDetailUiState.Content ?: return null
        if (current.paying) return null
        val attempt = PaymentAttempt(orderId = orderId, id = repo.newCheckoutKey())
        activeAttempt = attempt
        _state.value = current.copy(paying = true, message = null)
        observePaymentHandoff()
        return attempt
    }

    /**
     * Subscribes to MStore's handoff stream for the attempt this screen
     * minted. The same rules as checkout: every event is checked against
     * the active attempt first, a consumed event is never acted on twice,
     * a closed sheet means "poll the server", and an unavailable sheet means
     * the intent was refused — its reason is the one line the buyer reads.
     */
    private fun observePaymentHandoff() {
        if (observingHandoff) return
        observingHandoff = true
        viewModelScope.launch {
            handoff.events(MSTORE_PAYMENT_APPLICATION_ID).collect { event ->
                val mine = activeAttempt?.toSheetAttempt()
                if (mine == null || event.attempt != mine || handoff.isConsumed(mine)) return@collect
                handoff.consume(mine)
                when (event) {
                    is PaymentHandoffEvent.SheetClosed -> pollPaymentStatus()
                    is PaymentHandoffEvent.Unavailable -> {
                        val current = _state.value as? OrderDetailUiState.Content ?: return@collect
                        _state.value = current.copy(paying = false, message = event.reason)
                    }
                }
            }
        }
    }

    /**
     * Polls the server through the coordinator until it says paid, failed
     * or refunded (or 180 s pass), then re-reads the order so the screen
     * shows the server's status rather than one inferred from the poll.
     */
    private fun pollPaymentStatus() {
        viewModelScope.launch {
            var outcome: String? = null
            payments.confirm(MSTORE_PAYMENT_APPLICATION_ID, orderId, paymentStatus).collect { confirmation ->
                outcome = when (confirmation) {
                    is PaymentConfirmation.Confirming -> null
                    PaymentConfirmation.Paid -> null
                    is PaymentConfirmation.Failed -> "The payment didn't go through. You can try again."
                    PaymentConfirmation.RefundPending,
                    PaymentConfirmation.Refunded,
                    -> "The payment arrived after the order lapsed, so it is being refunded."

                    is PaymentConfirmation.TimedOut ->
                        "We're still confirming your payment. Check back in a moment."
                }
            }
            load()
            val current = _state.value as? OrderDetailUiState.Content ?: return@launch
            _state.value = current.copy(paying = false, message = outcome)
        }
    }

    fun dismissMessage() {
        val current = _state.value as? OrderDetailUiState.Content ?: return
        _state.value = current.copy(message = null)
    }
}
