package com.us.android.feature.feast.checkout

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.food.network.FeastAddressDto
import com.us.android.core.food.network.FeastOrderDto
import com.us.android.core.food.network.PlaceOrderRequest
import com.us.android.core.food.repository.FeastRepository
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.serverMessage
import com.us.android.core.payments.InFlightPayment
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentConfirmation
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentHandoffEvent
import com.us.android.core.payments.PaymentPollPolicy
import com.us.android.core.payments.PaymentStateStore
import com.us.android.core.payments.PaymentStatusReading
import com.us.android.feature.feast.FeastSession
import com.us.android.feature.feast.cart.CartBill
import com.us.android.feature.feast.restaurant.ServiceabilityRules
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed interface FeastCheckoutState {
    data object Loading : FeastCheckoutState

    data object EmptyCart : FeastCheckoutState

    data class Ready(
        val restaurantName: String,
        val restaurantId: String?,
        val itemCount: Int,
        val bill: CartBill,
        val address: FeastAddressDto?,
        val method: FeastPaymentMethod,
        val placing: Boolean = false,
        /** The server's words when it refused the order (range, hours, pricing). */
        val refusal: String? = null,
    ) : FeastCheckoutState

    /** The sheet is being opened from the Activity. */
    data class OpeningPayment(val request: FeastPaymentRequest) : FeastCheckoutState

    /** The sheet ended; the SERVER has not said paid. */
    data class Confirming(val orderId: String, val orderNumber: String, val elapsedSeconds: Int) : FeastCheckoutState

    /** The server confirmed the capture. The only way to get here. */
    data class Paid(val orderId: String, val orderNumber: String) : FeastCheckoutState

    /** The server said failed, or the sheet never opened. The order stays and can be paid again. */
    data class PaymentFailed(
        val orderId: String,
        val orderNumber: String,
        val method: FeastPaymentMethod,
        val reason: String?,
    ) : FeastCheckoutState

    /** Money moved after the order lapsed and is being returned. Never a successful order. */
    data class Refunding(val orderId: String, val orderNumber: String) : FeastCheckoutState

    /** The poll gave up without an answer. NOT a failure: the payment may still land. */
    data class StillConfirming(val orderId: String, val orderNumber: String) : FeastCheckoutState

    data class Failed(val message: String, val retryable: Boolean) : FeastCheckoutState
}

/**
 * Feast checkout: place the order, pay, and believe only the server.
 *
 * The rules, each of which the commerce checkout learned the hard way:
 *
 *  1. ONE Idempotency-Key per customer decision, persisted before the request
 *     leaves and reused on a resend, so a lost response can never become a
 *     second order.
 *  2. The total is the server's `final_amount_paise` ([CartBill]); the client
 *     sends no amount anywhere.
 *  3. A sheet ending is evidence, never proof (A1/R-3): every ending moves to
 *     [FeastCheckoutState.Confirming] and polls `GET /orders/:id/payment`
 *     through [PaymentCoordinator.confirm]; only the server's `paid` renders
 *     [FeastCheckoutState.Paid], and a read that fails keeps confirming.
 *  4. An ending is applied only if it is for THIS checkout's active attempt,
 *     read from Feast's own stream of the shared bus.
 *  5. A failed payment is retried on the SAME order with a NEW attempt — the
 *     server keeps the order pending for 15 minutes — and never re-places it.
 *  6. After process death the saved phase decides, and the server is asked
 *     before anything is offered; a sheet is never reopened by itself.
 */
@HiltViewModel
class FeastCheckoutViewModel @Inject constructor(
    private val repository: FeastRepository,
    private val session: FeastSession,
    private val handoff: PaymentHandoff,
    private val payments: PaymentCoordinator,
    savedState: SavedStateHandle,
) : ViewModel() {

    private val saved = FeastCheckoutContinuation(savedState)
    private val statusSource = FeastPaymentStatusSource(repository)
    private val policy = PaymentPollPolicy()

    private val _state = MutableStateFlow<FeastCheckoutState>(FeastCheckoutState.Loading)
    val state: StateFlow<FeastCheckoutState> = _state.asStateFlow()

    private var confirmJob: Job? = null

    init {
        observeHandoff()
        viewModelScope.launch { start() }
    }

    fun selectMethod(method: FeastPaymentMethod) {
        _state.update { if (it is FeastCheckoutState.Ready && !it.placing) it.copy(method = method) else it }
    }

    /** The address picked on the address screen, reflected when the customer comes back. */
    fun refreshAddress() {
        val ready = _state.value as? FeastCheckoutState.Ready ?: return
        viewModelScope.launch {
            val addresses = (repository.addresses() as? FoodResult.Success)?.value
            if (addresses != null) session.reconcileAddresses(addresses)
            _state.update {
                if (it is FeastCheckoutState.Ready) it.copy(address = session.address.value, refusal = null) else it
            }
        }
        if (ready.address?.id != session.address.value?.id) {
            _state.value = ready.copy(address = session.address.value, refusal = null)
        }
    }

    fun placeOrder() {
        val ready = _state.value as? FeastCheckoutState.Ready ?: return
        if (ready.placing || !ready.bill.payable) return
        val address = ready.address
        if (address == null) {
            _state.value = ready.copy(refusal = "Add a delivery address to continue.")
            return
        }
        saved.addressId = address.id
        saved.restaurantId = ready.restaurantId
        saved.method = ready.method
        // Recorded BEFORE the request leaves: recovery must know a request may
        // have created an order.
        saved.phase = FeastCheckoutContinuation.Phase.SUBMITTING
        _state.value = ready.copy(placing = true, refusal = null)
        viewModelScope.launch { submit(address.id, ready.method, ready) }
    }

    /** Pay again for the order that already exists: a NEW attempt, the SAME order. */
    fun retryPayment(method: FeastPaymentMethod? = null) {
        val (orderId, orderNumber) = when (val s = _state.value) {
            is FeastCheckoutState.PaymentFailed -> s.orderId to s.orderNumber
            is FeastCheckoutState.StillConfirming -> s.orderId to s.orderNumber
            else -> return
        }
        confirmJob?.cancel()
        method?.let { saved.method = it }
        openPayment(orderId, orderNumber)
    }

    /** Called by the screen when it has handed the request to the Activity. */
    internal fun activeAttempt(): PaymentAttempt? = saved.inFlight.attempt

    private suspend fun start() {
        when (saved.phase) {
            FeastCheckoutContinuation.Phase.FRESH -> loadCart()
            FeastCheckoutContinuation.Phase.SUBMITTING -> if (saved.orderId == null) resubmit() else resumeOrder()
            FeastCheckoutContinuation.Phase.SHEET_REQUESTED,
            FeastCheckoutContinuation.Phase.SETTLED,
            -> resumeOrder()
        }
    }

    private suspend fun loadCart() {
        _state.value = FeastCheckoutState.Loading
        val cart = when (val result = repository.cart()) {
            is FoodResult.Failure -> {
                _state.value = failure(result.error)
                return
            }
            is FoodResult.Success -> result.value
        }
        if (cart.items.isEmpty()) {
            _state.value = FeastCheckoutState.EmptyCart
            return
        }
        (repository.addresses() as? FoodResult.Success)?.value?.let(session::reconcileAddresses)
        _state.value = FeastCheckoutState.Ready(
            restaurantName = cart.restaurant.orEmpty(),
            restaurantId = cart.restaurantId,
            itemCount = cart.items.sumOf { it.quantity },
            bill = CartBill.from(cart),
            address = session.address.value,
            method = saved.method,
        )
    }

    private suspend fun submit(addressId: String, method: FeastPaymentMethod, ready: FeastCheckoutState.Ready?) {
        val result = repository.placeOrder(saved.orderKey { repository.newIdempotencyKey() }, PlaceOrderRequest(addressId, method.wire))
        when (result) {
            is FoodResult.Success -> adopt(result.value)
            is FoodResult.Failure -> {
                val error = result.error
                saved.phase = FeastCheckoutContinuation.Phase.FRESH
                // A definite refusal created no order, and the next attempt is
                // a new decision: a new key. A network failure keeps the key —
                // the order may exist, and a resend under the same key returns it.
                if (error !is FoodError.Network) saved.clearOrderKey()
                val refusal = ServiceabilityRules.fromRefusal(error)
                if (refusal != null) {
                    saved.restaurantId?.let { session.recordRefusal(it, addressId, refusal) }
                }
                val message = refusal?.message ?: when (error) {
                    is FoodError.Network -> "Check your connection and try again."
                    else -> error.serverMessage ?: "We couldn't place your order. Please try again."
                }
                _state.value = ready?.copy(placing = false, refusal = message) ?: FeastCheckoutState.Failed(message, retryable = true)
            }
        }
    }

    private fun adopt(order: FeastOrderDto) {
        saved.orderId = order.id
        saved.orderNumber = order.orderNumber
        openPayment(order.id, order.orderNumber)
    }

    private fun openPayment(orderId: String, orderNumber: String) {
        val attempt = PaymentAttempt(
            applicationId = FEAST_PAYMENT_APPLICATION_ID,
            referenceId = orderId,
            id = repository.newIdempotencyKey(),
        )
        saved.inFlight.attempt = attempt
        saved.phase = FeastCheckoutContinuation.Phase.SHEET_REQUESTED
        _state.value = FeastCheckoutState.OpeningPayment(FeastPaymentRequest(attempt, orderNumber, saved.method))
    }

    private fun observeHandoff() {
        viewModelScope.launch {
            handoff.events(FEAST_PAYMENT_APPLICATION_ID).collect { event ->
                val mine = saved.inFlight.attempt
                // Not ours: another checkout, or an earlier attempt at this order.
                if (mine == null || event.attempt != mine) return@collect
                // Already acted on: a replay after rotation must not restart anything.
                if (handoff.isConsumed(mine)) return@collect
                handoff.consume(mine)
                when (event) {
                    // However the sheet ended — including the SDK's "success" — ask the server.
                    is PaymentHandoffEvent.SheetClosed -> confirm(mine.referenceId, saved.orderNumber)
                    is PaymentHandoffEvent.Unavailable -> _state.value = FeastCheckoutState.PaymentFailed(
                        orderId = mine.referenceId,
                        orderNumber = saved.orderNumber,
                        method = saved.method,
                        reason = event.reason,
                    )
                }
            }
        }
    }

    private fun confirm(orderId: String, orderNumber: String) {
        confirmJob?.cancel()
        _state.value = FeastCheckoutState.Confirming(orderId, orderNumber, elapsedSeconds = 0)
        confirmJob = viewModelScope.launch {
            payments.confirm(FEAST_PAYMENT_APPLICATION_ID, orderId, statusSource, policy).collect { confirmation ->
                _state.value = when (confirmation) {
                    is PaymentConfirmation.Confirming ->
                        FeastCheckoutState.Confirming(orderId, orderNumber, confirmation.elapsedSeconds)
                    PaymentConfirmation.Paid -> settled(orderId, orderNumber)
                    is PaymentConfirmation.Failed ->
                        FeastCheckoutState.PaymentFailed(orderId, orderNumber, saved.method, reason = null)
                    PaymentConfirmation.RefundPending,
                    PaymentConfirmation.Refunded,
                    -> FeastCheckoutState.Refunding(orderId, orderNumber)
                    is PaymentConfirmation.TimedOut -> FeastCheckoutState.StillConfirming(orderId, orderNumber)
                }
            }
        }
    }

    private fun settled(orderId: String, orderNumber: String): FeastCheckoutState.Paid {
        saved.phase = FeastCheckoutContinuation.Phase.SETTLED
        saved.inFlight.clear()
        return FeastCheckoutState.Paid(orderId, orderNumber)
    }

    /** SUBMITTING with no order id: the response was lost. Resend under the SAME key. */
    private suspend fun resubmit() {
        val addressId = saved.addressId
        if (addressId == null) {
            saved.phase = FeastCheckoutContinuation.Phase.FRESH
            loadCart()
            return
        }
        _state.value = FeastCheckoutState.Loading
        submit(addressId, saved.method, ready = null)
    }

    /** An order exists. Ask the server what happened; never place again, never reopen a sheet. */
    private suspend fun resumeOrder() {
        val orderId = saved.orderId ?: return loadCart()
        val orderNumber = saved.orderNumber
        _state.value = FeastCheckoutState.Loading
        when (val result = repository.orderPayment(orderId)) {
            // An order we cannot read right now: keep confirming; do not offer to place another.
            is FoodResult.Failure -> confirm(orderId, orderNumber)
            is FoodResult.Success -> _state.value = when (result.value.toReading()) {
                PaymentStatusReading.Paid -> settled(orderId, orderNumber)
                is PaymentStatusReading.Failed -> FeastCheckoutState.PaymentFailed(orderId, orderNumber, saved.method, null)
                PaymentStatusReading.RefundPending,
                PaymentStatusReading.Refunded,
                -> FeastCheckoutState.Refunding(orderId, orderNumber)
                PaymentStatusReading.Confirming,
                is PaymentStatusReading.Unreachable,
                -> {
                    confirm(orderId, orderNumber)
                    return
                }
            }
        }
    }

    private fun failure(error: FoodError): FeastCheckoutState.Failed = when (error) {
        is FoodError.Network -> FeastCheckoutState.Failed("Check your connection and try again.", retryable = true)
        else -> FeastCheckoutState.Failed(error.serverMessage ?: "Something went wrong.", retryable = true)
    }

    fun reload() {
        if (_state.value is FeastCheckoutState.Failed) viewModelScope.launch { start() }
    }
}

/**
 * The checkout state that survives process death. Nothing here is a provider
 * session — only identities and how far the customer got.
 */
internal class FeastCheckoutContinuation(private val handle: SavedStateHandle) {

    enum class Phase { FRESH, SUBMITTING, SHEET_REQUESTED, SETTLED }

    var phase: Phase
        get() = handle.get<String>(KEY_PHASE)?.let { runCatching { Phase.valueOf(it) }.getOrNull() } ?: Phase.FRESH
        set(v) {
            handle[KEY_PHASE] = v.name
        }

    /** Read-through-and-mint, persisted immediately, so a resend uses the key the lost request used. */
    fun orderKey(mint: () -> String): String =
        handle.get<String>(KEY_ORDER_KEY) ?: mint().also { handle[KEY_ORDER_KEY] = it }

    fun clearOrderKey() {
        handle[KEY_ORDER_KEY] = null
    }

    var orderId: String?
        get() = handle[KEY_ORDER_ID]
        set(v) {
            handle[KEY_ORDER_ID] = v
        }

    var orderNumber: String
        get() = handle[KEY_ORDER_NUMBER] ?: ""
        set(v) {
            handle[KEY_ORDER_NUMBER] = v
        }

    var addressId: String?
        get() = handle[KEY_ADDRESS_ID]
        set(v) {
            handle[KEY_ADDRESS_ID] = v
        }

    var restaurantId: String?
        get() = handle[KEY_RESTAURANT_ID]
        set(v) {
            handle[KEY_RESTAURANT_ID] = v
        }

    var method: FeastPaymentMethod
        get() = FeastPaymentMethod.fromWire(handle[KEY_METHOD])
        set(v) {
            handle[KEY_METHOD] = v.wire
        }

    /** The attempt, in `:core:payments`' record keyed by Feast's application id. */
    val inFlight = InFlightPayment(
        store = object : PaymentStateStore {
            override fun get(key: String): String? = handle[key]
            override fun set(key: String, value: String?) {
                handle[key] = value
            }
        },
        applicationId = FEAST_PAYMENT_APPLICATION_ID,
    )

    private companion object {
        const val KEY_PHASE = "feast.checkout.phase"
        const val KEY_ORDER_KEY = "feast.checkout.orderKey"
        const val KEY_ORDER_ID = "feast.checkout.orderId"
        const val KEY_ORDER_NUMBER = "feast.checkout.orderNumber"
        const val KEY_ADDRESS_ID = "feast.checkout.addressId"
        const val KEY_RESTAURANT_ID = "feast.checkout.restaurantId"
        const val KEY_METHOD = "feast.checkout.method"
    }
}
