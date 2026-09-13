package com.us.android.feature.kitchen.root

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.AuthRepository
import com.us.android.core.food.network.PartnerRestaurantDto
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodRepository
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.KitchenRepository
import com.us.android.feature.kitchen.gate.RoleGate
import com.us.android.feature.kitchen.ui.userMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed interface KitchenRootState {
    data object Loading : KitchenRootState

    /** Signed in without a restaurant: the restaurant-partners-only screen. */
    data object NotPartner : KitchenRootState

    data object SessionExpired : KitchenRootState

    data class Unavailable(val message: String) : KitchenRootState

    data class Ready(
        val restaurants: List<PartnerRestaurantDto>,
        val selected: PartnerRestaurantDto,
    ) : KitchenRootState
}

/**
 * The gate in front of every Kitchen screen: capabilities first (fail closed,
 * see [RoleGate]), then the partner's restaurants.
 */
@HiltViewModel
class KitchenRootViewModel @Inject constructor(
    private val food: FoodRepository,
    private val kitchen: KitchenRepository,
    private val auth: AuthRepository,
) : ViewModel() {

    private val _state = MutableStateFlow<KitchenRootState>(KitchenRootState.Loading)
    val state: StateFlow<KitchenRootState> = _state.asStateFlow()

    init {
        load()
    }

    fun load() {
        _state.value = KitchenRootState.Loading
        viewModelScope.launch {
            when (val gate = RoleGate.from(food.capabilities())) {
                is RoleGate.RestaurantPartner -> loadRestaurants(selectedId = null)
                RoleGate.NotRestaurantPartner -> _state.value = KitchenRootState.NotPartner
                RoleGate.SessionExpired -> _state.value = KitchenRootState.SessionExpired
                is RoleGate.Unavailable -> _state.value = KitchenRootState.Unavailable(gate.error.userMessage())
                RoleGate.Checking -> Unit
            }
        }
    }

    fun select(restaurantId: String) {
        val ready = _state.value as? KitchenRootState.Ready ?: return
        val chosen = ready.restaurants.firstOrNull { it.id == restaurantId } ?: return
        _state.value = ready.copy(selected = chosen)
    }

    /** Re-reads the restaurants after a status change (submitted, accepting toggled). Keeps the selection. */
    fun refreshRestaurant() {
        val selectedId = (_state.value as? KitchenRootState.Ready)?.selected?.id
        viewModelScope.launch { loadRestaurants(selectedId) }
    }

    fun signOut() {
        viewModelScope.launch { auth.logout() }
    }

    private suspend fun loadRestaurants(selectedId: String?) {
        when (val result = kitchen.restaurants()) {
            is FoodResult.Success -> {
                val restaurants = result.value
                _state.value = if (restaurants.isEmpty()) {
                    KitchenRootState.NotPartner
                } else {
                    KitchenRootState.Ready(
                        restaurants = restaurants,
                        selected = restaurants.firstOrNull { it.id == selectedId } ?: restaurants.first(),
                    )
                }
            }
            is FoodResult.Failure -> {
                // A refresh failure keeps the kitchen open on what it had.
                if (_state.value is KitchenRootState.Ready && result.error !is FoodError.Unauthorized) return
                _state.value = when (result.error) {
                    FoodError.Unauthorized -> KitchenRootState.SessionExpired
                    else -> KitchenRootState.Unavailable(result.error.userMessage())
                }
            }
        }
    }
}
