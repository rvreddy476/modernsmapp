package com.us.android.feature.kitchen.restaurant

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.network.PartnerRestaurantDto
import com.us.android.core.food.repository.FoodRepository
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.KitchenRepository
import com.us.android.feature.kitchen.navigation.restaurantId
import com.us.android.feature.kitchen.ui.asMessage
import com.us.android.feature.kitchen.ui.success
import com.us.android.feature.kitchen.ui.userMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class RestaurantUiState(
    val loading: Boolean = true,
    val restaurant: PartnerRestaurantDto? = null,
    val loadError: String? = null,
    val acceptingBusy: Boolean = false,
    val message: UsMessage? = null,
    /** Bumped when the restaurant changed here, so the shell re-reads it. */
    val restaurantChanges: Int = 0,
)

/**
 * The Kitchen tab: the pause switch (`PATCH …/accepting`), setup links and the
 * read-only basics.
 *
 * ROUTE GAP: basics are read-only. `PATCH …/restaurants/:id` REPLACES every
 * field (an omitted phone or e-mail becomes empty), and the partner `GET` does
 * not return phone, e-mail, legal or display name — so the app cannot send an
 * edit without erasing what it cannot see.
 */
@HiltViewModel
class RestaurantViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val kitchen: KitchenRepository,
    private val food: FoodRepository,
) : ViewModel() {

    private val restaurantId = savedStateHandle.restaurantId()

    private val _state = MutableStateFlow(RestaurantUiState())
    val state: StateFlow<RestaurantUiState> = _state.asStateFlow()

    fun load() {
        viewModelScope.launch {
            when (val result = kitchen.restaurant(restaurantId)) {
                is FoodResult.Success -> _state.update { it.copy(loading = false, restaurant = result.value, loadError = null) }
                is FoodResult.Failure -> _state.update { it.copy(loading = false, loadError = result.error.userMessage()) }
            }
        }
    }

    fun setAccepting(accepting: Boolean) {
        if (_state.value.acceptingBusy) return
        _state.update { it.copy(acceptingBusy = true) }
        viewModelScope.launch {
            when (val result = food.setAccepting(restaurantId, accepting)) {
                is FoodResult.Success -> _state.update { current ->
                    val dto = result.value
                    current.copy(
                        acceptingBusy = false,
                        restaurant = current.restaurant?.let { r ->
                            r.copy(isAcceptingOrders = dto.isAcceptingOrders, status = dto.status.ifBlank { r.status })
                        },
                        message = success(if (dto.isAcceptingOrders) "You're taking orders" else "Orders paused"),
                        restaurantChanges = current.restaurantChanges + 1,
                    )
                }
                is FoodResult.Failure -> _state.update {
                    it.copy(acceptingBusy = false, message = result.error.asMessage())
                }
            }
        }
    }

    fun dismissMessage() {
        _state.update { it.copy(message = null) }
    }
}
