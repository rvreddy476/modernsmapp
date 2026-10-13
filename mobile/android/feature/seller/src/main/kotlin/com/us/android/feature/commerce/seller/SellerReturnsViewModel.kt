package com.us.android.feature.commerce.seller

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.ReturnStatus
import com.us.android.core.commerce.model.SellerReturn
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
 * The chips over the returns inbox. Sent to the server as its `status`
 * filter, so the wire value is carried here.
 */
enum class ReturnFilter(val label: String, val wire: String?) {
    OPEN("Waiting on you", "requested"),
    APPROVED("Approved", "approved"),
    REJECTED("Rejected", "rejected"),
    ALL("All", null),
}

sealed interface SellerReturnsUiState {
    data object Loading : SellerReturnsUiState

    data class Content(
        val returns: List<SellerReturn>,
        val filter: ReturnFilter = ReturnFilter.OPEN,
        /** The return a decision is in flight for. One at a time. */
        val busyId: String? = null,
        /** The return whose reject sheet is up. */
        val rejecting: SellerReturn? = null,
        val rejectReason: String = "",
        val error: String? = null,
    ) : SellerReturnsUiState {
        val canSubmitReject: Boolean get() = rejectReason.isNotBlank() && busyId == null
    }

    data class Failed(val message: String, val retryable: Boolean) : SellerReturnsUiState
}

/**
 * The returns inbox, and the two decisions a seller makes in it.
 *
 * Approving books the reverse pickup and queues the refund on the server;
 * rejecting closes the request with a reason the buyer reads. Both re-read
 * the inbox afterwards, because approval also moves the ORDER's status and
 * the row's refund amount is decided server-side.
 */
@HiltViewModel
class SellerReturnsViewModel @Inject constructor(
    private val repo: CommerceRepository,
) : ViewModel() {

    private val _state = MutableStateFlow<SellerReturnsUiState>(SellerReturnsUiState.Loading)
    val state: StateFlow<SellerReturnsUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        val filter = (_state.value as? SellerReturnsUiState.Content)?.filter ?: ReturnFilter.OPEN
        _state.value = SellerReturnsUiState.Loading
        viewModelScope.launch { load(filter) }
    }

    fun select(filter: ReturnFilter) {
        val current = _state.value as? SellerReturnsUiState.Content ?: return
        if (current.busyId != null || current.filter == filter) return
        _state.value = SellerReturnsUiState.Loading
        viewModelScope.launch { load(filter) }
    }

    fun approve(returnId: String) {
        decide(returnId) { repo.approveReturn(returnId) }
    }

    fun openReject(item: SellerReturn) {
        val current = _state.value as? SellerReturnsUiState.Content ?: return
        if (current.busyId != null) return
        _state.value = current.copy(rejecting = item, rejectReason = "", error = null)
    }

    fun updateRejectReason(reason: String) {
        val current = _state.value as? SellerReturnsUiState.Content ?: return
        _state.value = current.copy(rejectReason = reason, error = null)
    }

    fun dismissReject() {
        val current = _state.value as? SellerReturnsUiState.Content ?: return
        if (current.busyId != null) return
        _state.value = current.copy(rejecting = null)
    }

    fun reject() {
        val current = _state.value as? SellerReturnsUiState.Content ?: return
        val target = current.rejecting ?: return
        if (!current.canSubmitReject) return
        val reason = current.rejectReason
        decide(target.id) { repo.rejectReturn(target.id, reason) }
    }

    private fun decide(returnId: String, call: suspend () -> CommerceResult<Unit>) {
        val current = _state.value as? SellerReturnsUiState.Content ?: return
        if (current.busyId != null) return
        val target = current.returns.firstOrNull { it.id == returnId } ?: return
        // Only a request still waiting can be decided; the server refuses
        // anything else, and offering the button was already a mistake.
        if (target.status != ReturnStatus.REQUESTED) return

        _state.value = current.copy(busyId = returnId, error = null)
        viewModelScope.launch {
            when (val r = call()) {
                is CommerceResult.Failure -> _state.value = current.copy(
                    busyId = null,
                    error = r.error.describe(),
                )

                is CommerceResult.Success -> load(current.filter)
            }
        }
    }

    private suspend fun load(filter: ReturnFilter) {
        when (val r = repo.sellerReturns(status = filter.wire)) {
            is CommerceResult.Failure ->
                _state.value = SellerReturnsUiState.Failed(r.error.describe(), r.error.isRetryable())

            is CommerceResult.Success ->
                _state.value = SellerReturnsUiState.Content(returns = r.value, filter = filter)
        }
    }
}
