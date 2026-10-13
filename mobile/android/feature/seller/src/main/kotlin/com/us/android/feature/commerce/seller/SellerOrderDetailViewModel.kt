package com.us.android.feature.commerce.seller

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.SellerAction
import com.us.android.core.commerce.model.SellerOrder
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
 * The ship sheet's form.
 *
 * Validation is local and shallow: it exists to stop a blank or obviously
 * mistyped tracking number leaving the device, not to know every courier's
 * format. The buyer follows this number on the courier's site, so a
 * character that cannot appear in one is the thing worth refusing here.
 */
data class ShipForm(
    val courier: String = "",
    val trackingNumber: String = "",
) {
    val courierProblem: String? get() = "Name the courier".takeIf { courier.isBlank() }
    val trackingProblem: String? get() = trackingNumberProblem(trackingNumber)
    val isValid: Boolean get() = courierProblem == null && trackingProblem == null
}

/**
 * Why a tracking number cannot be sent, or null when it can.
 *
 * Pure, so the rule is a table test. Letters, digits and hyphens only,
 * between [MIN_TRACKING_CHARS] and [MAX_TRACKING_CHARS] after trimming: the
 * shortest real consignment numbers in India are eight digits and the
 * longest common ones are under thirty, and a space inside a number is a
 * paste that picked up the label's text around it.
 */
fun trackingNumberProblem(raw: String): String? {
    val value = raw.trim()
    return when {
        value.isEmpty() -> "Enter the tracking number"
        value.length < MIN_TRACKING_CHARS -> "That looks too short for a tracking number"
        value.length > MAX_TRACKING_CHARS -> "That looks too long for a tracking number"
        !value.all { it.isLetterOrDigit() || it == '-' } -> "Letters, digits and hyphens only"
        else -> null
    }
}

const val MIN_TRACKING_CHARS = 6
const val MAX_TRACKING_CHARS = 40

/** Which sheet is up, if any. */
enum class SellerOrderSheet { SHIP, CANCEL }

sealed interface SellerOrderDetailUiState {
    data object Loading : SellerOrderDetailUiState

    data class Content(
        val order: SellerOrder,
        /**
         * The order's life: the server's recorded history when it sends one,
         * derived from the order's own stamps when it does not.
         */
        val timeline: List<TimelineEntry> = order.timeline(),
        /**
         * The action in flight, or null.
         *
         * One latch for all three buttons: a pack and a cancel racing each
         * other is two transitions against one row, and whichever the
         * server refuses would surface as an error the seller did not cause.
         */
        val busy: SellerAction? = null,
        val sheet: SellerOrderSheet? = null,
        val shipForm: ShipForm = ShipForm(),
        val cancelReason: String = "",
        /** A refused action, shown inline under the buttons. */
        val error: String? = null,
    ) : SellerOrderDetailUiState {
        val actions: List<SellerAction> get() = order.actions
        val canSubmitShip: Boolean get() = shipForm.isValid && busy == null
        val canSubmitCancel: Boolean get() = cancelReason.isNotBlank() && busy == null
    }

    data class Failed(val message: String, val retryable: Boolean) : SellerOrderDetailUiState
}

/**
 * One order from the seller's side, and what they do to it.
 *
 * Every action re-reads the order afterwards rather than assuming the new
 * status. The server decides where a transition lands (a cancel on a paid
 * order goes to refund_pending, not cancelled), and it is also the only
 * party that can refuse one; the buttons here are shown from the mirrored
 * table, but a stale screen can still offer an action the row has moved past.
 */
@HiltViewModel
class SellerOrderDetailViewModel @Inject constructor(
    private val repo: CommerceRepository,
    savedState: SavedStateHandle,
) : ViewModel() {

    private val orderId: String = requireNotNull(savedState["orderId"]) {
        "SellerOrderDetailViewModel requires an orderId argument"
    }

    private val _state = MutableStateFlow<SellerOrderDetailUiState>(SellerOrderDetailUiState.Loading)
    val state: StateFlow<SellerOrderDetailUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        _state.value = SellerOrderDetailUiState.Loading
        viewModelScope.launch { load() }
    }

    private suspend fun load(): Boolean = when (val r = repo.sellerOrder(orderId)) {
        is CommerceResult.Failure -> {
            _state.value = SellerOrderDetailUiState.Failed(r.error.describe(), r.error.isRetryable())
            false
        }

        is CommerceResult.Success -> {
            val order = r.value
            val timeline = when (val h = repo.sellerOrderHistory(orderId)) {
                is CommerceResult.Success -> order.timeline(h.value)
                // A server without the route (a bare 404), or one that could
                // not answer just now. The timeline is one section of the
                // screen, so it is derived rather than the order going unshown.
                is CommerceResult.Failure -> order.timeline()
            }
            _state.value = SellerOrderDetailUiState.Content(order, timeline = timeline)
            true
        }
    }

    fun pack() = perform(SellerAction.PACK) { repo.packOrder(orderId) }

    fun openShip() = showSheet(SellerOrderSheet.SHIP)

    fun openCancel() = showSheet(SellerOrderSheet.CANCEL)

    fun dismissSheet() {
        val current = content() ?: return
        if (current.busy != null) return
        _state.value = current.copy(sheet = null)
    }

    fun updateShipForm(transform: (ShipForm) -> ShipForm) {
        val current = content() ?: return
        _state.value = current.copy(shipForm = transform(current.shipForm), error = null)
    }

    fun updateCancelReason(reason: String) {
        val current = content() ?: return
        _state.value = current.copy(cancelReason = reason, error = null)
    }

    /** Sends the courier and tracking number. Refused locally while the form has a problem. */
    fun ship() {
        val current = content() ?: return
        if (!current.canSubmitShip) return
        val form = current.shipForm
        perform(SellerAction.SHIP) { repo.shipOrder(orderId, form.courier, form.trackingNumber) }
    }

    fun cancel() {
        val current = content() ?: return
        if (!current.canSubmitCancel) return
        val reason = current.cancelReason
        perform(SellerAction.CANCEL) { repo.sellerCancelOrder(orderId, reason) }
    }

    fun dismissError() {
        val current = content() ?: return
        _state.value = current.copy(error = null)
    }

    private fun showSheet(sheet: SellerOrderSheet) {
        val current = content() ?: return
        if (current.busy != null) return
        _state.value = current.copy(sheet = sheet, error = null)
    }

    /**
     * Runs one action under the latch.
     *
     * On success the order is re-read and the sheet, if any, goes with the
     * old state. On refusal the sheet stays up with the seller's typing
     * intact and the reason underneath, so a mistyped tracking number is a
     * correction rather than a retype.
     */
    private fun perform(action: SellerAction, call: suspend () -> CommerceResult<Unit>) {
        val current = content() ?: return
        if (current.busy != null) return
        if (action !in current.actions) return

        _state.value = current.copy(busy = action, error = null)
        viewModelScope.launch {
            when (val r = call()) {
                is CommerceResult.Failure -> _state.value = current.copy(
                    busy = null,
                    error = r.error.describe(),
                )

                is CommerceResult.Success -> load()
            }
        }
    }

    private fun content() = _state.value as? SellerOrderDetailUiState.Content
}
