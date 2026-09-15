package com.us.android.feature.dating.premium

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.payments.InFlightPayment
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentConfirmation
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentHandoffEvent
import com.us.android.core.payments.PaymentPollPolicy
import com.us.android.core.payments.PaymentStateStore
import com.us.android.core.payments.PaymentStatusReading
import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.data.code
import com.us.android.feature.dating.data.valueOrNull
import com.us.android.feature.dating.network.PremiumMeDto
import com.us.android.feature.dating.network.PremiumProductDto
import com.us.android.feature.dating.network.PremiumPurchaseResultDto
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed interface PremiumState {
    data object Loading : PremiumState

    /** 503 PREMIUM_UNAVAILABLE: "Premium isn't available yet". */
    data object Unavailable : PremiumState

    data class Failed(val message: String) : PremiumState

    data class Ready(
        val products: List<PremiumProductDto>,
        val me: PremiumMeDto?,
        val buying: String? = null,
        val notice: String? = null,
    ) : PremiumState

    /** The sheet is being opened from the Activity. */
    data class OpeningPayment(val request: DatingPaymentRequest) : PremiumState

    /** The sheet ended; the SERVER has not said paid. */
    data class Confirming(val purchaseId: String, val productName: String, val elapsedSeconds: Int) : PremiumState

    /** The server confirmed the capture. The only way to get here. */
    data class Paid(val productName: String) : PremiumState

    /** The server said failed, or the sheet never opened. A retry is a NEW purchase. */
    data class PaymentFailed(val productId: String, val productName: String, val reason: String?) : PremiumState

    /** Money moved and is being returned. Never a bought pass. */
    data class Refunding(val productName: String) : PremiumState

    /** The poll gave up without an answer. NOT a failure: the payment may still land. */
    data class StillConfirming(val purchaseId: String, val productName: String) : PremiumState
}

/**
 * Premium passes and Boost, paid through `:core:payments` exactly as Feast pays:
 *
 *  1. ONE idempotency key per buyer decision, saved before the purchase request
 *     leaves and reused on a resend, so a lost response is never a second purchase.
 *  2. The client never sends a price: the server's catalogue prices the purchase.
 *  3. A sheet ending is evidence, never proof: every ending polls
 *     `GET /premium/purchases/:id/payment` through [DatingPaymentStatusSource]
 *     and only its `paid` renders [PremiumState.Paid]; a failed read keeps confirming.
 *  4. An ending is applied only if it is THIS screen's attempt, from Dating's own stream.
 *  5. A failed purchase cannot be paid again (the same key returns no session), so a
 *     retry mints a NEW key; a purchase still confirming is re-opened under its key.
 *  6. After process death the server is asked first; a sheet never reopens by itself.
 */
@HiltViewModel
class PremiumViewModel @Inject constructor(
    private val repository: DatingRepository,
    private val handoff: PaymentHandoff,
    private val payments: PaymentCoordinator,
    savedState: SavedStateHandle,
) : ViewModel() {

    private val saved = PremiumContinuation(savedState)
    private val statusSource = DatingPaymentStatusSource(repository)
    private val policy = PaymentPollPolicy()

    private val _state = MutableStateFlow<PremiumState>(PremiumState.Loading)
    val state: StateFlow<PremiumState> = _state.asStateFlow()

    private var products: List<PremiumProductDto> = emptyList()
    private var confirmJob: Job? = null

    init {
        observeHandoff()
        viewModelScope.launch { start() }
    }

    fun buy(productId: String) {
        val ready = _state.value as? PremiumState.Ready ?: return
        if (ready.buying != null) return
        val product = products.firstOrNull { it.id == productId } ?: return
        saved.productId = product.id
        saved.productName = product.name
        // Recorded BEFORE the request leaves: recovery must know a purchase may exist.
        saved.phase = PremiumContinuation.Phase.SUBMITTING
        _state.value = ready.copy(buying = product.id, notice = null)
        viewModelScope.launch { submit() }
    }

    /** After a failure: a new decision, so a new key and a new purchase. After a timeout: the same purchase. */
    fun retry() {
        when (val s = _state.value) {
            is PremiumState.PaymentFailed -> {
                saved.clearPurchase()
                backToCatalogue(then = s.productId)
            }
            is PremiumState.StillConfirming -> {
                confirmJob?.cancel()
                saved.phase = PremiumContinuation.Phase.SUBMITTING
                _state.value = PremiumState.Loading
                viewModelScope.launch { submit() }
            }
            is PremiumState.Failed -> viewModelScope.launch { start() }
            else -> Unit
        }
    }

    /** Leaves a settled or failed outcome and shows the catalogue again. */
    fun done() {
        saved.clearPurchase()
        saved.phase = PremiumContinuation.Phase.FRESH
        backToCatalogue(then = null)
    }

    private suspend fun start() {
        when (saved.phase) {
            PremiumContinuation.Phase.FRESH -> loadCatalogue()
            PremiumContinuation.Phase.SUBMITTING -> if (saved.purchaseId == null) resubmit() else resume()
            PremiumContinuation.Phase.SHEET_REQUESTED, PremiumContinuation.Phase.SETTLED -> resume()
        }
    }

    private suspend fun loadCatalogue(notice: String? = null) {
        when (val result = repository.premiumCatalogue()) {
            is DatingResult.Failure -> _state.value = failure(result.error)
            is DatingResult.Success -> {
                products = result.value.products
                _state.value = PremiumState.Ready(products, repository.premiumMe().valueOrNull(), notice = notice)
            }
        }
    }

    private fun backToCatalogue(then: String?) {
        viewModelScope.launch {
            loadCatalogue()
            if (then != null) buy(then)
        }
    }

    private suspend fun submit() {
        val key = saved.purchaseKey { repository.newIdempotencyKey() }
        when (val result = repository.purchase(saved.productId.orEmpty(), key)) {
            is DatingResult.Success -> adopt(result.value)
            is DatingResult.Failure -> {
                val error = result.error
                saved.phase = PremiumContinuation.Phase.FRESH
                // A definite refusal created no purchase; a network failure keeps
                // the key, because the purchase may exist and a resend returns it.
                if (error !is DatingError.Network) saved.clearPurchase()
                if (error.code in UNAVAILABLE_CODES) {
                    _state.value = PremiumState.Unavailable
                    return
                }
                val notice = if (error is DatingError.Network) DatingCopy.NETWORK else DatingCopy.forError(error)
                if (products.isEmpty()) loadCatalogue(notice) else _state.value = PremiumState.Ready(products, null, notice = notice)
            }
        }
    }

    private fun adopt(result: PremiumPurchaseResultDto) {
        val purchase = result.purchase
        saved.purchaseId = purchase.id
        val name = saved.productName
        when (purchase.status) {
            STATUS_PAID -> _state.value = settled(name)
            STATUS_FAILED -> {
                saved.phase = PremiumContinuation.Phase.FRESH
                _state.value = PremiumState.PaymentFailed(purchase.product, name, reason = null)
            }
            STATUS_REFUNDED, STATUS_PARTIALLY_REFUNDED -> _state.value = PremiumState.Refunding(name)
            else -> {
                val session = result.toPaymentSession(name)
                if (session == null) {
                    saved.phase = PremiumContinuation.Phase.FRESH
                    _state.value = PremiumState.PaymentFailed(purchase.product, name, "Online payment isn't available right now.")
                    return
                }
                val attempt = PaymentAttempt(
                    applicationId = DATING_PAYMENT_APPLICATION_ID,
                    referenceId = purchase.id,
                    id = repository.newIdempotencyKey(),
                )
                saved.inFlight.attempt = attempt
                saved.phase = PremiumContinuation.Phase.SHEET_REQUESTED
                _state.value = PremiumState.OpeningPayment(DatingPaymentRequest(attempt, session))
            }
        }
    }

    private fun observeHandoff() {
        viewModelScope.launch {
            handoff.events(DATING_PAYMENT_APPLICATION_ID).collect { event ->
                val mine = saved.inFlight.attempt
                // Not ours: another screen, or an earlier attempt at this purchase.
                if (mine == null || event.attempt != mine) return@collect
                // Already acted on: a replay after rotation must not restart anything.
                if (handoff.isConsumed(mine)) return@collect
                handoff.consume(mine)
                when (event) {
                    // However the sheet ended — including the SDK's "success" — ask the server.
                    is PaymentHandoffEvent.SheetClosed -> confirm(mine.referenceId)
                    is PaymentHandoffEvent.Unavailable -> {
                        saved.phase = PremiumContinuation.Phase.FRESH
                        _state.value = PremiumState.PaymentFailed(saved.productId.orEmpty(), saved.productName, event.reason)
                    }
                }
            }
        }
    }

    private fun confirm(purchaseId: String) {
        confirmJob?.cancel()
        val name = saved.productName
        _state.value = PremiumState.Confirming(purchaseId, name, elapsedSeconds = 0)
        confirmJob = viewModelScope.launch {
            payments.confirm(DATING_PAYMENT_APPLICATION_ID, purchaseId, statusSource, policy).collect { confirmation ->
                _state.value = when (confirmation) {
                    is PaymentConfirmation.Confirming -> PremiumState.Confirming(purchaseId, name, confirmation.elapsedSeconds)
                    PaymentConfirmation.Paid -> settled(name)
                    is PaymentConfirmation.Failed -> {
                        saved.phase = PremiumContinuation.Phase.FRESH
                        PremiumState.PaymentFailed(saved.productId.orEmpty(), name, reason = null)
                    }
                    PaymentConfirmation.RefundPending, PaymentConfirmation.Refunded -> PremiumState.Refunding(name)
                    is PaymentConfirmation.TimedOut -> PremiumState.StillConfirming(purchaseId, name)
                }
            }
        }
    }

    private fun settled(name: String): PremiumState.Paid {
        saved.phase = PremiumContinuation.Phase.SETTLED
        saved.inFlight.clear()
        return PremiumState.Paid(name)
    }

    /** SUBMITTING with no purchase id: the response was lost. Resend under the SAME key. */
    private suspend fun resubmit() {
        if (saved.productId == null) {
            saved.phase = PremiumContinuation.Phase.FRESH
            loadCatalogue()
            return
        }
        _state.value = PremiumState.Loading
        products = repository.premiumCatalogue().valueOrNull()?.products.orEmpty()
        submit()
    }

    /** A purchase exists. Ask the server what happened; never buy again, never reopen a sheet. */
    private suspend fun resume() {
        val purchaseId = saved.purchaseId ?: return loadCatalogue()
        val name = saved.productName
        products = repository.premiumCatalogue().valueOrNull()?.products.orEmpty()
        when (val result = repository.purchasePayment(purchaseId)) {
            is DatingResult.Failure -> confirm(purchaseId)
            is DatingResult.Success -> when (result.value.toReading()) {
                PaymentStatusReading.Paid -> _state.value = settled(name)
                is PaymentStatusReading.Failed -> _state.value = PremiumState.PaymentFailed(saved.productId.orEmpty(), name, null)
                PaymentStatusReading.RefundPending, PaymentStatusReading.Refunded -> _state.value = PremiumState.Refunding(name)
                PaymentStatusReading.Confirming, is PaymentStatusReading.Unreachable -> confirm(purchaseId)
            }
        }
    }

    private fun failure(error: DatingError): PremiumState =
        if (error.code in UNAVAILABLE_CODES) PremiumState.Unavailable else PremiumState.Failed(DatingCopy.forError(error))

    /** The screen handed the request to the Activity. */
    internal fun activeAttempt(): PaymentAttempt? = saved.inFlight.attempt

    private companion object {
        val UNAVAILABLE_CODES = setOf("PREMIUM_UNAVAILABLE", "PREMIUM_PAYMENTS_UNAVAILABLE")
        const val STATUS_PAID = "paid"
        const val STATUS_FAILED = "failed"
        const val STATUS_REFUNDED = "refunded"
        const val STATUS_PARTIALLY_REFUNDED = "partially_refunded"
    }
}

/**
 * The Premium state that survives process death. Nothing here is a provider
 * session — only identities and how far the buyer got.
 */
internal class PremiumContinuation(private val handle: SavedStateHandle) {

    enum class Phase { FRESH, SUBMITTING, SHEET_REQUESTED, SETTLED }

    var phase: Phase
        get() = handle.get<String>(KEY_PHASE)?.let { runCatching { Phase.valueOf(it) }.getOrNull() } ?: Phase.FRESH
        set(v) {
            handle[KEY_PHASE] = v.name
        }

    /** Read-through-and-mint, persisted immediately, so a resend uses the key the lost request used. */
    fun purchaseKey(mint: () -> String): String =
        handle.get<String>(KEY_PURCHASE_KEY) ?: mint().also { handle[KEY_PURCHASE_KEY] = it }

    /** Forgets the purchase decision: the next buy is a new key and a new purchase. */
    fun clearPurchase() {
        handle[KEY_PURCHASE_KEY] = null
        handle[KEY_PURCHASE_ID] = null
        inFlight.clear()
    }

    var purchaseId: String?
        get() = handle[KEY_PURCHASE_ID]
        set(v) {
            handle[KEY_PURCHASE_ID] = v
        }

    var productId: String?
        get() = handle[KEY_PRODUCT_ID]
        set(v) {
            handle[KEY_PRODUCT_ID] = v
        }

    var productName: String
        get() = handle[KEY_PRODUCT_NAME] ?: ""
        set(v) {
            handle[KEY_PRODUCT_NAME] = v
        }

    /** The attempt, in `:core:payments`' record keyed by Dating's application id. */
    val inFlight = InFlightPayment(
        store = object : PaymentStateStore {
            override fun get(key: String): String? = handle[key]
            override fun set(key: String, value: String?) {
                handle[key] = value
            }
        },
        applicationId = DATING_PAYMENT_APPLICATION_ID,
    )

    private companion object {
        const val KEY_PHASE = "dating.premium.phase"
        const val KEY_PURCHASE_KEY = "dating.premium.purchaseKey"
        const val KEY_PURCHASE_ID = "dating.premium.purchaseId"
        const val KEY_PRODUCT_ID = "dating.premium.productId"
        const val KEY_PRODUCT_NAME = "dating.premium.productName"
    }
}
