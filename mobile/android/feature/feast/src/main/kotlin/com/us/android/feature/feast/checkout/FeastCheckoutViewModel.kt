package com.us.android.feature.feast.checkout

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.food.model.Paise
import com.us.android.core.food.network.FeastAddressDto
import com.us.android.core.food.network.FeastOrderDto
import com.us.android.core.food.network.PlaceOrderRequest
import com.us.android.core.food.repository.FeastRepository
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.outcomeUnknown
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
        /**
         * The last Place order got no definite answer: the order may exist. The
         * key, the address and the method are held until a resend under the
         * same key settles it (food audit M1).
         */
        val awaitingOutcome: Boolean = false,
    ) : FeastCheckoutState {
        /** While the outcome is unknown the resend is always allowed: it is the same order. */
        val canSubmit: Boolean get() = !placing && (awaitingOutcome || orderable)

        private val orderable: Boolean get() = bill.payable && address != null
    }

    /**
     * An order exists and nothing has been paid on it yet: "Pay now" from
     * tracking or orders, a checkout restored after process death before the
     * sheet opened, or a total the server re-priced. [amount] is the server's
     * figure; [previousTotal] is the bill the customer saw, when it differs
     * (food audit L2).
     */
    data class Unpaid(
        val orderId: String,
        val orderNumber: String,
        val method: FeastPaymentMethod,
        val amount: Paise?,
        val previousTotal: Paise? = null,
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

    /** "Pay now" for an order that already exists (`FeastCheckoutRoute.payOrderId`). */
    private val payOrderId: String? = savedState.get<String>(ARG_PAY_ORDER_ID)?.takeIf { it.isNotBlank() }
    private val payOrderNumber: String = savedState.get<String>(ARG_PAY_ORDER_NUMBER).orEmpty()

    private val _state = MutableStateFlow<FeastCheckoutState>(FeastCheckoutState.Loading)
    val state: StateFlow<FeastCheckoutState> = _state.asStateFlow()

    private var confirmJob: Job? = null

    init {
        observeHandoff()
        viewModelScope.launch { start() }
    }

    /** Held while an unanswered Place order is outstanding: a different method is a different order body. */
    fun selectMethod(method: FeastPaymentMethod) {
        _state.update {
            if (it is FeastCheckoutState.Ready && !it.placing && !it.awaitingOutcome) it.copy(method = method) else it
        }
    }

    /**
     * The address picked on the address screen, reflected when the customer
     * comes back, with the cart re-read so the bill is priced for it (food
     * audit L2). Held, like the method, while an unanswered order is outstanding.
     */
    fun refreshAddress() {
        val ready = _state.value as? FeastCheckoutState.Ready ?: return
        if (ready.addressHeld) return
        viewModelScope.launch {
            val addresses = (repository.addresses() as? FoodResult.Success)?.value
            if (addresses != null) session.reconcileAddresses(addresses)
            val picked = session.address.value
            val changed = picked?.id != ready.address?.id
            val repriced = if (changed) {
                (repository.cart() as? FoodResult.Success)?.value?.takeIf { it.items.isNotEmpty() }
            } else {
                null
            }
            _state.update {
                if (it !is FeastCheckoutState.Ready || it.addressHeld) return@update it
                it.copy(
                    address = picked,
                    refusal = if (changed) null else it.refusal,
                    bill = repriced?.let(CartBill::from) ?: it.bill,
                    itemCount = repriced?.items?.sumOf { line -> line.quantity } ?: it.itemCount,
                )
            }
        }
    }

    fun placeOrder() {
        val ready = _state.value as? FeastCheckoutState.Ready ?: return
        if (ready.placing) return
        if (ready.awaitingOutcome) {
            // The SAME body under the SAME key: the server answers with the
            // order it already made, or makes it now.
            val addressId = saved.addressId ?: return
            _state.value = ready.copy(placing = true)
            viewModelScope.launch { submit(addressId, saved.method, ready) }
            return
        }
        if (!ready.bill.payable) return
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

    /** Pay for the order that already exists: a NEW attempt, the SAME order. */
    fun retryPayment(method: FeastPaymentMethod? = null) {
        val (orderId, orderNumber) = when (val s = _state.value) {
            is FeastCheckoutState.PaymentFailed -> s.orderId to s.orderNumber
            is FeastCheckoutState.StillConfirming -> s.orderId to s.orderNumber
            is FeastCheckoutState.Unpaid -> s.orderId to s.orderNumber
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
            FeastCheckoutContinuation.Phase.FRESH -> if (payOrderId != null) payExisting(payOrderId) else loadCart()
            FeastCheckoutContinuation.Phase.SUBMITTING -> if (saved.orderId == null) resubmit() else resumeOrder()
            FeastCheckoutContinuation.Phase.SHEET_REQUESTED,
            FeastCheckoutContinuation.Phase.SETTLED,
            -> resumeOrder()
        }
    }

    /** Pay now: adopt the existing order and ask the server where its payment stands. */
    private suspend fun payExisting(orderId: String) {
        saved.orderId = orderId
        saved.orderNumber = payOrderNumber
        saved.phase = FeastCheckoutContinuation.Phase.SUBMITTING
        resumeOrder()
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
        val key = saved.orderKey { repository.newIdempotencyKey() }
        when (val result = repository.placeOrder(key, PlaceOrderRequest(addressId, method.wire))) {
            is FoodResult.Success -> adopt(result.value, shownTotal = ready?.bill?.total)
            is FoodResult.Failure ->
                if (result.error.outcomeUnknown) awaitOutcome(ready) else refused(result.error, addressId, ready)
        }
    }

    /**
     * No definite answer (a lost response, a gateway 5xx, the same key still
     * in flight): the order may exist and the cart may already be gone. Keep
     * the phase, the key and the body; the next Place order resends them.
     */
    private fun awaitOutcome(ready: FeastCheckoutState.Ready?) {
        _state.value = ready?.copy(placing = false, awaitingOutcome = true, refusal = OUTCOME_UNKNOWN)
            ?: FeastCheckoutState.Failed(OUTCOME_UNKNOWN, retryable = true)
    }

    /** A definite 4xx refusal created no order: the next attempt is a new decision, with a new key. */
    private fun refused(error: FoodError, addressId: String, ready: FeastCheckoutState.Ready?) {
        saved.phase = FeastCheckoutContinuation.Phase.FRESH
        saved.clearOrderKey()
        val refusal = ServiceabilityRules.fromRefusal(error)
        if (refusal != null) {
            saved.restaurantId?.let { session.recordRefusal(it, addressId, refusal) }
        }
        val message = refusal?.message ?: when {
            (error as? FoodError.Unexpected)?.code == PAYLOAD_MISMATCH -> PAYLOAD_MISMATCH_MESSAGE
            else -> error.serverMessage ?: "We couldn't place your order. Please try again."
        }
        _state.value = ready?.copy(placing = false, awaitingOutcome = false, refusal = message)
            ?: FeastCheckoutState.Failed(message, retryable = true)
    }

    /**
     * The order exists. When the server priced it differently from the bill
     * on screen, say so before any sheet opens (food audit L2); otherwise open
     * payment straight away.
     */
    private fun adopt(order: FeastOrderDto, shownTotal: Paise?) {
        saved.orderId = order.id
        saved.orderNumber = order.orderNumber
        val charged = order.money?.totalsPaise?.finalAmountPaise
        if (charged != null && shownTotal != null && charged != shownTotal) {
            _state.value = FeastCheckoutState.Unpaid(order.id, order.orderNumber, saved.method, charged, shownTotal)
            return
        }
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

    /**
     * An order exists. Ask the server what happened; never place again, never
     * reopen a sheet. With no attempt ever opened from this checkout there is
     * nothing to confirm: the order is simply unpaid, and paying is offered.
     */
    private suspend fun resumeOrder() {
        val orderId = saved.orderId ?: return loadCart()
        val orderNumber = saved.orderNumber
        val attempted = saved.inFlight.attempt != null
        _state.value = FeastCheckoutState.Loading
        when (val result = repository.orderPayment(orderId)) {
            // An order we cannot read right now: keep confirming an attempt;
            // never offer to place another.
            is FoodResult.Failure ->
                if (attempted) confirm(orderId, orderNumber) else _state.value = failure(result.error)
            is FoodResult.Success -> _state.value = when (result.value.toReading()) {
                PaymentStatusReading.Paid -> settled(orderId, orderNumber)
                is PaymentStatusReading.Failed -> FeastCheckoutState.PaymentFailed(orderId, orderNumber, saved.method, null)
                PaymentStatusReading.RefundPending,
                PaymentStatusReading.Refunded,
                -> FeastCheckoutState.Refunding(orderId, orderNumber)
                PaymentStatusReading.Confirming,
                is PaymentStatusReading.Unreachable,
                -> {
                    if (!attempted) {
                        val amount = Paise(result.value.amountMinor).takeIf { it > Paise.ZERO }
                        _state.value = FeastCheckoutState.Unpaid(orderId, orderNumber, saved.method, amount)
                        return
                    }
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

    /** The address may not change while an order is being sent, or while one may exist unanswered. */
    private val FeastCheckoutState.Ready.addressHeld: Boolean get() = placing || awaitingOutcome

    companion object {
        /** Navigation argument names: `FeastCheckoutRoute`'s properties. */
        const val ARG_PAY_ORDER_ID = "payOrderId"
        const val ARG_PAY_ORDER_NUMBER = "payOrderNumber"

        const val OUTCOME_UNKNOWN =
            "We couldn't tell whether your order went through. Try again: it's the same order, " +
                "so nothing is ordered or charged twice."
        const val PAYLOAD_MISMATCH_MESSAGE =
            "An earlier attempt at this order may already have gone through. Check Your orders before ordering again."

        private const val PAYLOAD_MISMATCH = "FOOD_IDEMPOTENCY_PAYLOAD_MISMATCH"
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
