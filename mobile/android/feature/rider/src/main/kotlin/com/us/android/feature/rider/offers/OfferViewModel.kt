package com.us.android.feature.rider.offers

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.network.DeliveryOfferDto
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.RiderRepository
import com.us.android.feature.rider.location.RiderDuty
import com.us.android.feature.rider.navigation.requireArg
import com.us.android.feature.rider.ui.asMessage
import com.us.android.feature.rider.ui.warning
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.receiveAsFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class OfferUiState(
    val loading: Boolean = true,
    val offer: DeliveryOfferDto? = null,
    /** The offer is no longer pending for this rider (taken, expired or never theirs). */
    val gone: Boolean = false,
    val window: OfferWindow = OfferWindow.Unknown,
    val responding: Boolean = false,
    val message: UsMessage? = null,
)

sealed interface OfferOutcome {
    data object Accepted : OfferOutcome

    data object Closed : OfferOutcome
}

/**
 * One job offer, opened from Home, the realtime stream or the
 * `/rider/offers/{offer_id}` push link. Counts down to `expires_at`; accept is
 * refused locally once the countdown expires, and the server stays the judge.
 */
@HiltViewModel
class OfferViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val rider: RiderRepository,
    private val duty: RiderDuty,
    private val clock: RiderClock,
) : ViewModel() {

    private val offerId = savedStateHandle.requireArg("offerId")
    private var countdown: OfferCountdown? = null

    private val _state = MutableStateFlow(OfferUiState())
    val state: StateFlow<OfferUiState> = _state.asStateFlow()

    private val outcomes = Channel<OfferOutcome>(Channel.CONFLATED)
    val outcome: Flow<OfferOutcome> = outcomes.receiveAsFlow()

    init {
        viewModelScope.launch { load() }
        viewModelScope.launch {
            while (true) {
                tick()
                delay(TICK_MILLIS)
            }
        }
    }

    fun accept() {
        val current = countdown
        if (_state.value.responding || _state.value.gone) return
        if (current != null && !current.canRespond(clock.now())) {
            _state.update { it.copy(message = warning("Too late — this offer has expired.")) }
            return
        }
        _state.update { it.copy(responding = true) }
        viewModelScope.launch {
            when (val result = rider.acceptOffer(offerId)) {
                is FoodResult.Success -> {
                    duty.setOnJob(true)
                    outcomes.trySend(OfferOutcome.Accepted)
                }
                is FoodResult.Failure -> {
                    _state.update { it.copy(responding = false, message = result.error.asMessage()) }
                    load()
                }
            }
        }
    }

    fun reject() {
        if (_state.value.responding) return
        _state.update { it.copy(responding = true) }
        viewModelScope.launch {
            rider.rejectOffer(offerId)
            outcomes.trySend(OfferOutcome.Closed)
        }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    private suspend fun load() {
        when (val result = rider.offers()) {
            is FoodResult.Success -> {
                val offer = result.value.firstOrNull { it.id == offerId }
                countdown = offer?.let { OfferCountdown.of(it.expiresAt) }
                _state.update { it.copy(loading = false, offer = offer, gone = offer == null) }
                tick()
            }
            is FoodResult.Failure -> _state.update { it.copy(loading = false, message = result.error.asMessage()) }
        }
    }

    private fun tick() {
        val current = countdown ?: return
        _state.update { it.copy(window = current.at(clock.now())) }
    }

    private companion object {
        const val TICK_MILLIS = 1_000L
    }
}
