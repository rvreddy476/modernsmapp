package com.us.android.feature.commerce.cart

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.Cart
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

sealed interface CartUiState {
    data object Loading : CartUiState

    data object Empty : CartUiState

    data class Content(
        val cart: Cart,
        /** Variant ids with a request in flight, so only those rows disable. */
        val busyVariantIds: Set<String> = emptySet(),
        val message: String? = null,
    ) : CartUiState {

        /**
         * Whether checkout may be attempted.
         *
         * A line whose quantity exceeds what the server says is available
         * would fail inside the checkout transaction. Blocking here turns a
         * failed order attempt into an obvious, fixable cart state.
         */
        val canCheckout: Boolean
            get() = cart.items.isNotEmpty() &&
                busyVariantIds.isEmpty() &&
                cart.items.none { line ->
                    line.availableQty != null && line.quantity > line.availableQty!!
                }

        /** Lines the server has told us cannot be fulfilled as-is. */
        val unavailableLines: List<String>
            get() = cart.items
                .filter { it.availableQty != null && it.quantity > it.availableQty!! }
                .map { it.variantId }

        /** Lines whose catalogue price moved since they were added. */
        val repricedLines: List<String>
            get() = cart.items.filter { it.priceChangedFrom != null }.map { it.variantId }
    }

    data class Failed(val message: String, val retryable: Boolean) : CartUiState
}

@HiltViewModel
class CartViewModel @Inject constructor(
    private val repo: CommerceRepository,
) : ViewModel() {

    private val _state = MutableStateFlow<CartUiState>(CartUiState.Loading)
    val state: StateFlow<CartUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    /**
     * Reloads the cart.
     *
     * Called on every return to this screen, not only on first composition.
     * Stock and prices move while the customer is elsewhere in the app, and a
     * cart rendered from a stale snapshot is how someone reaches checkout
     * holding a line that can no longer be bought.
     */
    fun refresh() {
        _state.value = CartUiState.Loading
        viewModelScope.launch { load() }
    }

    private suspend fun load() {
        when (val r = repo.cart()) {
            is CommerceResult.Failure ->
                _state.value =
                    CartUiState.Failed(r.error.describe(), r.error.isRetryable())

            is CommerceResult.Success -> _state.value = r.value.toState()
        }
    }

    fun setQuantity(variantId: String, quantity: Int) {
        if (quantity < 1) {
            remove(variantId)
            return
        }
        mutate(variantId) { repo.setQuantity(variantId, quantity) }
    }

    fun remove(variantId: String) {
        mutate(variantId) { repo.removeFromCart(variantId) }
    }

    /**
     * Runs a cart mutation with per-row busy state.
     *
     * Only the affected row is disabled. A whole-screen spinner for a
     * quantity change loses the customer's scroll position and makes a fast
     * operation feel slow.
     */
    private fun mutate(variantId: String, block: suspend () -> CommerceResult<Cart>) {
        val current = _state.value as? CartUiState.Content ?: return
        if (variantId in current.busyVariantIds) return

        _state.value = current.copy(
            busyVariantIds = current.busyVariantIds + variantId,
            message = null,
        )
        viewModelScope.launch {
            when (val r = block()) {
                is CommerceResult.Failure -> _state.value = current.copy(
                    busyVariantIds = current.busyVariantIds - variantId,
                    message = r.error.describe(),
                )

                is CommerceResult.Success -> _state.value = r.value.toState()
            }
        }
    }

    fun dismissMessage() {
        val current = _state.value as? CartUiState.Content ?: return
        _state.value = current.copy(message = null)
    }

    private fun Cart.toState(): CartUiState =
        if (items.isEmpty()) CartUiState.Empty else CartUiState.Content(this)
}
