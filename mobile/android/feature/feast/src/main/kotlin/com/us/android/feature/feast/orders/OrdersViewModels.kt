package com.us.android.feature.feast.orders

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.food.network.FeastInvoiceDto
import com.us.android.core.food.network.FeastOrderDto
import com.us.android.core.food.repository.FeastRepository
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.serverMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class OrdersUiState(
    val loading: Boolean = true,
    val orders: List<FeastOrderDto> = emptyList(),
    val error: String? = null,
)

/** The customer's Feast order history, newest first. */
@HiltViewModel
class OrdersViewModel @Inject constructor(
    private val repository: FeastRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(OrdersUiState())
    val state: StateFlow<OrdersUiState> = _state.asStateFlow()

    fun load() {
        viewModelScope.launch {
            when (val result = repository.orders()) {
                is FoodResult.Success -> _state.update {
                    it.copy(loading = false, error = null, orders = result.value.sortedByDescending { o -> o.placedAt.orEmpty() })
                }
                is FoodResult.Failure -> _state.update { it.copy(loading = false, error = describe(result.error)) }
            }
        }
    }
}

sealed interface InvoiceUiState {
    data object Loading : InvoiceUiState

    data class Loaded(val invoice: FeastInvoiceDto) : InvoiceUiState

    data class Failed(val message: String) : InvoiceUiState
}

/** `GET /orders/:id/invoice`: the restaurant's and the platform's tax invoices, as issued. */
@HiltViewModel
class InvoiceViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: FeastRepository,
) : ViewModel() {

    private val orderId: String = checkNotNull(savedStateHandle.get<String>("orderId")) {
        "navigation argument 'orderId' is missing"
    }

    private val _state = MutableStateFlow<InvoiceUiState>(InvoiceUiState.Loading)
    val state: StateFlow<InvoiceUiState> = _state.asStateFlow()

    init {
        load()
    }

    fun load() {
        _state.value = InvoiceUiState.Loading
        viewModelScope.launch {
            _state.value = when (val result = repository.invoice(orderId)) {
                is FoodResult.Success -> InvoiceUiState.Loaded(result.value)
                is FoodResult.Failure -> InvoiceUiState.Failed(
                    when (result.error) {
                        is FoodError.Network -> "Check your connection and try again."
                        FoodError.NotFound -> "The invoice is issued once the order is confirmed."
                        else -> result.error.serverMessage ?: "The invoice couldn't be loaded."
                    },
                )
            }
        }
    }
}

private fun describe(error: FoodError): String = when (error) {
    is FoodError.Network -> "Check your connection and try again."
    else -> error.serverMessage ?: "Your orders couldn't be loaded."
}
