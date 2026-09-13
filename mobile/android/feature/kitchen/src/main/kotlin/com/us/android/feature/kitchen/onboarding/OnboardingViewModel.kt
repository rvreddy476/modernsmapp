package com.us.android.feature.kitchen.onboarding

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.network.PartnerRestaurantDto
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodRepository
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.KitchenRepository
import com.us.android.feature.kitchen.navigation.restaurantId
import com.us.android.feature.kitchen.ui.asMessage
import com.us.android.feature.kitchen.ui.info
import com.us.android.feature.kitchen.ui.success
import com.us.android.feature.kitchen.ui.userMessage
import com.us.android.feature.kitchen.ui.warning
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class OnboardingUiState(
    val restaurant: PartnerRestaurantDto? = null,
    val checklist: KitchenChecklist = KitchenChecklist.unchecked(),
    val loading: Boolean = true,
    val loadError: String? = null,
    val submitting: Boolean = false,
    val message: UsMessage? = null,
    /** Bumped when the restaurant's status changed here, so the shell re-reads it. */
    val restaurantChanges: Int = 0,
)

/**
 * The setup checklist and the submit.
 *
 * `missing[]` is only ever learnt from `POST …/submit` (there is no read of it —
 * see [KitchenChecklist]), so the last answer is kept in the SavedStateHandle
 * and survives rotation and process death.
 */
@HiltViewModel
class OnboardingViewModel @Inject constructor(
    private val savedStateHandle: SavedStateHandle,
    private val kitchen: KitchenRepository,
    private val food: FoodRepository,
) : ViewModel() {

    private val restaurantId = savedStateHandle.restaurantId()

    private val _state = MutableStateFlow(
        OnboardingUiState(
            checklist = savedStateHandle.get<ArrayList<String>>(KEY_MISSING)
                ?.let { KitchenChecklist.fromMissing(it) }
                ?: KitchenChecklist.unchecked(),
        ),
    )
    val state: StateFlow<OnboardingUiState> = _state.asStateFlow()

    fun refreshRestaurant() {
        viewModelScope.launch {
            when (val result = kitchen.restaurant(restaurantId)) {
                is FoodResult.Success -> _state.update {
                    it.copy(restaurant = result.value, loading = false, loadError = null)
                }
                is FoodResult.Failure -> _state.update {
                    it.copy(loading = false, loadError = result.error.userMessage())
                }
            }
        }
    }

    fun submit() {
        if (_state.value.submitting) return
        _state.update { it.copy(submitting = true, message = null) }
        viewModelScope.launch {
            when (val result = food.submit(restaurantId)) {
                is FoodResult.Success -> {
                    rememberMissing(result.value.missing)
                    _state.update { current ->
                        current.copy(
                            submitting = false,
                            checklist = KitchenChecklist.fromMissing(result.value.missing),
                            restaurant = current.restaurant?.let { r -> r.copy(status = result.value.status.ifBlank { r.status }) },
                            message = success("Submitted for review. We'll let you know when your kitchen is approved."),
                            restaurantChanges = current.restaurantChanges + 1,
                        )
                    }
                }
                is FoodResult.Failure -> onSubmitFailed(result.error)
            }
        }
    }

    fun dismissMessage() {
        _state.update { it.copy(message = null) }
    }

    private fun onSubmitFailed(error: FoodError) {
        when (error) {
            is FoodError.NotReady -> {
                rememberMissing(error.missing)
                val checklist = KitchenChecklist.fromMissing(error.missing)
                val remaining = checklist.remaining
                _state.update {
                    it.copy(
                        submitting = false,
                        checklist = checklist,
                        message = warning(if (remaining == 1) "One step to go." else "$remaining steps to go."),
                    )
                }
            }
            FoodError.NotDraft -> {
                _state.update {
                    it.copy(
                        submitting = false,
                        message = info("This kitchen has already been submitted."),
                        restaurantChanges = it.restaurantChanges + 1,
                    )
                }
                refreshRestaurant()
            }
            else -> _state.update { it.copy(submitting = false, message = error.asMessage()) }
        }
    }

    private fun rememberMissing(missing: List<String>) {
        savedStateHandle[KEY_MISSING] = ArrayList(missing)
    }

    private companion object {
        const val KEY_MISSING = "onboarding_missing"
    }
}
