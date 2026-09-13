package com.us.android.feature.kitchen.earnings

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.food.network.RestaurantSummaryDto
import com.us.android.core.food.network.SettlementDto
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.KitchenRepository
import com.us.android.feature.kitchen.navigation.restaurantId
import com.us.android.feature.kitchen.ui.userMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class EarningsUiState(
    val loading: Boolean = true,
    val summary: RestaurantSummaryDto? = null,
    val summaryError: String? = null,
    val settlements: List<SettlementDto> = emptyList(),
    val settlementsError: String? = null,
)

/** `GET …/reports/summary` and `GET …/settlements`, read together. */
@HiltViewModel
class EarningsViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val kitchen: KitchenRepository,
) : ViewModel() {

    private val restaurantId = savedStateHandle.restaurantId()

    private val _state = MutableStateFlow(EarningsUiState())
    val state: StateFlow<EarningsUiState> = _state.asStateFlow()

    fun load() {
        viewModelScope.launch {
            val summaryCall = async { kitchen.summary(restaurantId) }
            val settlementsCall = async { kitchen.settlements(restaurantId) }
            val summary = summaryCall.await()
            val settlements = settlementsCall.await()
            _state.update { current ->
                current.copy(
                    loading = false,
                    summary = if (summary is FoodResult.Success) summary.value else current.summary,
                    summaryError = (summary as? FoodResult.Failure)?.error?.userMessage(),
                    settlements = if (settlements is FoodResult.Success) settlements.value else current.settlements,
                    settlementsError = (settlements as? FoodResult.Failure)?.error?.userMessage(),
                )
            }
        }
    }
}
