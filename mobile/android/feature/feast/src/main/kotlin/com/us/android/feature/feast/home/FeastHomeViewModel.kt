package com.us.android.feature.feast.home

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.food.network.FeastAddressDto
import com.us.android.core.food.network.FeastCartDto
import com.us.android.core.food.network.FeastOrderDto
import com.us.android.core.food.network.FeastRestaurantDto
import com.us.android.core.food.repository.FeastRepository
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.serverMessage
import com.us.android.feature.feast.FeastSession
import com.us.android.feature.feast.cart.CartBill
import com.us.android.feature.feast.restaurant.Serviceability
import com.us.android.feature.feast.restaurant.ServiceabilityRules
import com.us.android.feature.feast.tracking.OrderTimeline
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class RestaurantRow(
    val restaurant: FeastRestaurantDto,
    val serviceability: Serviceability,
)

data class FeastHomeUiState(
    val loading: Boolean = true,
    val query: String = "",
    val address: FeastAddressDto? = null,
    val restaurants: List<RestaurantRow> = emptyList(),
    val cart: FeastCartDto? = null,
    val cartBill: CartBill? = null,
    /** The newest order that can still arrive, for the "track your order" banner. */
    val liveOrder: FeastOrderDto? = null,
    val error: String? = null,
) {
    val cartItemCount: Int get() = cart?.items?.sumOf { it.quantity } ?: 0
}

/**
 * Feast's landing page: where the food goes, what can deliver it, the cart
 * and any order on its way.
 *
 * "Near you" is the server's list for the address's CITY — the only filter the
 * route takes (ROUTE GAP A5: no lat/lng, no in-range flag). Open restaurants
 * sort first; closed ones stay visible, marked, and cannot be ordered from.
 */
@HiltViewModel
class FeastHomeViewModel @Inject constructor(
    private val repository: FeastRepository,
    private val session: FeastSession,
) : ViewModel() {

    private val _state = MutableStateFlow(FeastHomeUiState())
    val state: StateFlow<FeastHomeUiState> = _state.asStateFlow()

    private var searchJob: Job? = null

    /** Re-read on every visit: the cart, the address and live orders change elsewhere. */
    fun refresh() {
        viewModelScope.launch {
            val addresses = async { repository.addresses() }
            val cart = async { repository.cart() }
            val orders = async { repository.orders() }
            (addresses.await() as? FoodResult.Success)?.value?.let(session::reconcileAddresses)
            val c = (cart.await() as? FoodResult.Success)?.value
            val live = (orders.await() as? FoodResult.Success)?.value
                ?.filter { OrderTimeline.isLive(it.status) && it.status != "PAYMENT_PENDING" }
                ?.maxByOrNull { it.placedAt.orEmpty() }
            _state.update {
                it.copy(
                    address = session.address.value,
                    cart = c?.takeIf { cart -> cart.items.isNotEmpty() },
                    cartBill = c?.takeIf { cart -> cart.items.isNotEmpty() }?.let(CartBill::from),
                    liveOrder = live,
                )
            }
            loadRestaurants()
        }
    }

    fun onQueryChange(query: String) {
        _state.update { it.copy(query = query) }
        searchJob?.cancel()
        searchJob = viewModelScope.launch {
            delay(SEARCH_DEBOUNCE_MILLIS)
            loadRestaurants()
        }
    }

    private suspend fun loadRestaurants() {
        _state.update { it.copy(loading = it.restaurants.isEmpty(), error = null) }
        val city = session.address.value?.city
        var result = repository.restaurants(_state.value.query, city)
        // A city with nothing listed yet should not look like an empty app.
        if (result is FoodResult.Success && result.value.isEmpty() && !city.isNullOrBlank() && _state.value.query.isBlank()) {
            result = repository.restaurants(null, null)
        }
        when (result) {
            is FoodResult.Success -> _state.update {
                it.copy(
                    loading = false,
                    restaurants = result.value
                        .map { r -> RestaurantRow(r, session.refusalFor(r.id) ?: ServiceabilityRules.of(r)) }
                        .sortedBy { row -> row.serviceability is Serviceability.Blocked },
                )
            }
            is FoodResult.Failure -> _state.update { it.copy(loading = false, error = describe(result.error)) }
        }
    }

    private fun describe(error: FoodError): String = when (error) {
        is FoodError.Network -> "Check your connection and try again."
        else -> error.serverMessage ?: "Restaurants couldn't be loaded."
    }

    private companion object {
        const val SEARCH_DEBOUNCE_MILLIS = 350L
    }
}
