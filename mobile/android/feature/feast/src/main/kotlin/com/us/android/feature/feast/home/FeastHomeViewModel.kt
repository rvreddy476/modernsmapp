package com.us.android.feature.feast.home

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.food.network.FeastAddressDto
import com.us.android.core.food.network.FeastCartDto
import com.us.android.core.food.network.FeastOrderDto
import com.us.android.core.food.network.FeastRestaurantDto
import com.us.android.core.food.network.deliveryPoint
import com.us.android.core.food.repository.FeastRepository
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.serverMessage
import com.us.android.feature.feast.FeastSession
import com.us.android.feature.feast.cart.CartBill
import com.us.android.feature.feast.restaurant.Serviceability
import com.us.android.feature.feast.restaurant.ServiceabilityRules
import com.us.android.feature.feast.tracking.OrderTimeline
import com.us.android.feature.feast.ui.distanceKmText
import com.us.android.feature.feast.ui.opensAtText
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.Instant
import javax.inject.Inject

data class RestaurantRow(
    val restaurant: FeastRestaurantDto,
    val serviceability: Serviceability,
)

/**
 * What a restaurant card says. Pure, so the card's words are tested.
 *
 * An unavailable restaurant is greyed ([enabled] false) but still opens, so
 * the customer can browse its menu; adding is what serviceability blocks.
 */
data class RestaurantCardModel(
    val enabled: Boolean,
    val statusLabel: String,
    /** The server's `unserviceable_message` when it wrote one, else the app's wording. */
    val unavailableMessage: String?,
    /** "14.8 km" from the server's straight-line `distance_meters`; null without a delivery point. */
    val distance: String?,
    /** "Opens at 6:00 PM" from `next_opens_at`, which the server sends only while closed. */
    val opensAt: String?,
) {
    companion object {
        fun from(row: RestaurantRow, now: Instant): RestaurantCardModel {
            val r = row.restaurant
            val blocked = row.serviceability as? Serviceability.Blocked
            val closed = blocked?.code == ServiceabilityRules.OUTSIDE_HOURS ||
                (blocked != null && blocked.code == null && (!r.isOpen || r.isOpenNow == false))
            return RestaurantCardModel(
                enabled = blocked == null,
                statusLabel = when {
                    blocked == null -> "Open"
                    blocked.code in ServiceabilityRules.ADDRESS_CODES -> "Doesn't deliver here"
                    closed -> "Closed"
                    blocked.code == null || blocked.code == ServiceabilityRules.NOT_ACCEPTING -> "Not accepting orders"
                    else -> "Unavailable"
                },
                unavailableMessage = blocked?.message,
                distance = r.distanceMeters?.let(::distanceKmText),
                opensAt = opensAtText(r.nextOpensAt, now),
            )
        }
    }
}

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
 * "Near you" sends the picked address's pin as `lat`/`lng`: the server judges
 * every restaurant for that point and orders the list serviceable first, then
 * nearest. The app keeps that order exactly — it never re-sorts. With no
 * address, or one without a pin, it falls back to the city listing, where the
 * server's order is also kept and serviceability is read from the flags.
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
        val address = session.address.value
        val near = address?.deliveryPoint()
        // A pinned address is judged by distance, not by the city it names.
        val city = if (near == null) address?.city else null
        val query = _state.value.query
        var result = repository.restaurants(query, city, near)
        // A city with nothing listed yet should not look like an empty app.
        if (result is FoodResult.Success && result.value.isEmpty() && near == null && !city.isNullOrBlank() && query.isBlank()) {
            result = repository.restaurants(null, null)
        }
        when (result) {
            is FoodResult.Success -> _state.update {
                it.copy(
                    loading = false,
                    // The server's order, as served.
                    restaurants = result.value.map { r ->
                        RestaurantRow(r, ServiceabilityRules.resolve(r, session.refusalFor(r.id)))
                    },
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
