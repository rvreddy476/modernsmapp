package com.us.android.feature.feast.cart

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.network.FeastCartDto
import com.us.android.core.food.repository.FeastRepository
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.serverMessage
import com.us.android.feature.feast.ui.errorMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class CartUiState(
    val loading: Boolean = true,
    val cart: FeastCartDto? = null,
    val bill: CartBill? = null,
    val updatingLineId: String? = null,
    val loadError: String? = null,
    val message: UsMessage? = null,
) {
    val isEmpty: Boolean get() = cart != null && cart.items.isEmpty()
}

/**
 * The cart. Every change goes to the server and the server's cart — with its
 * re-computed `totals_paise` and `taxes_and_charges` — replaces what is shown.
 * Nothing is adjusted locally, not even optimistically, so the bill on screen
 * is always one the server stated.
 */
@HiltViewModel
class CartViewModel @Inject constructor(
    private val repository: FeastRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(CartUiState())
    val state: StateFlow<CartUiState> = _state.asStateFlow()

    fun load() {
        _state.update { it.copy(loading = it.cart == null, loadError = null) }
        viewModelScope.launch {
            when (val result = repository.cart()) {
                is FoodResult.Success -> show(result.value)
                is FoodResult.Failure -> _state.update { it.copy(loading = false, loadError = describe(result.error)) }
            }
        }
    }

    fun changeQuantity(cartItemId: String, quantity: Int) {
        if (_state.value.updatingLineId != null) return
        _state.update { it.copy(updatingLineId = cartItemId) }
        viewModelScope.launch {
            val result = if (quantity <= 0) {
                repository.removeCartItem(cartItemId)
            } else {
                repository.updateCartItem(cartItemId, quantity)
            }
            when (result) {
                is FoodResult.Success -> show(result.value)
                is FoodResult.Failure -> _state.update {
                    it.copy(updatingLineId = null, message = errorMessage(describe(result.error)))
                }
            }
        }
    }

    fun dismissMessage() {
        _state.update { it.copy(message = null) }
    }

    private fun show(cart: FeastCartDto) {
        _state.update { it.copy(loading = false, cart = cart, bill = CartBill.from(cart), updatingLineId = null) }
    }

    private fun describe(error: FoodError): String = when (error) {
        is FoodError.Network -> "Check your connection and try again."
        else -> error.serverMessage ?: "Something went wrong. Please try again."
    }
}
