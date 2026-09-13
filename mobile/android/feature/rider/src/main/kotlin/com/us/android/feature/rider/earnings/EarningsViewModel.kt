package com.us.android.feature.rider.earnings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.network.DeliveryAssignmentDto
import com.us.android.core.food.network.DeliveryEarningsDto
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.RiderRepository
import com.us.android.feature.rider.ui.asMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class EarningsUiState(
    val loading: Boolean = true,
    val earnings: DeliveryEarningsDto? = null,
    val history: List<DeliveryAssignmentDto> = emptyList(),
    val message: UsMessage? = null,
)

/** `GET …/earnings` and `GET …/history`. Money is paise from the start; see RiderDtos for the wire. */
@HiltViewModel
class EarningsViewModel @Inject constructor(
    private val rider: RiderRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(EarningsUiState())
    val state: StateFlow<EarningsUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            val earnings = rider.earnings()
            val history = rider.history()
            _state.update {
                it.copy(
                    loading = false,
                    earnings = (earnings as? FoodResult.Success)?.value ?: it.earnings,
                    history = (history as? FoodResult.Success)?.value ?: it.history,
                    message = (earnings as? FoodResult.Failure)?.error?.asMessage(),
                )
            }
        }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }
}
