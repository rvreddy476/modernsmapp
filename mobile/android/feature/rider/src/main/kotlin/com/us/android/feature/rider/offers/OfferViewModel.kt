package com.us.android.feature.rider.offers

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.network.DeliveryOfferDto
import com.us.android.core.food.repository.RiderRepository
import com.us.android.feature.rider.location.RiderDuty
import com.us.android.feature.rider.navigation.requireArg
import com.us.android.feature.rider.ui.error
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.receiveAsFlow
import kotlinx.coroutines.flow.stateIn
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
 * One job offer, opened from Home, the realtime stream, the offer heads-up or
 * the `/rider/offers/{offer_id}` push link.
 *
 * The offer comes from [RiderOfferWatch], held only while this screen is
 * collected (food audit L5), and counts down on the server's clock (L1). The
 * button greys at expiry; nothing refuses a tap locally, the server decides.
 * An accept whose answer is lost is recovered by [OfferAcceptance] (M3).
 */
@HiltViewModel
class OfferViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val rider: RiderRepository,
    private val duty: RiderDuty,
    private val watch: RiderOfferWatch,
    private val clock: RiderClock,
) : ViewModel() {

    private val offerId = savedStateHandle.requireArg("offerId")
    private val acceptance = OfferAcceptance(rider)

    private data class Local(val responding: Boolean = false, val message: UsMessage? = null, val gone: Boolean = false)

    private val local = MutableStateFlow(Local())

    /** The offer as last listed; kept so an accept in flight still knows its order. */
    private var lastSeen: DeliveryOfferDto? = null

    val state: StateFlow<OfferUiState> = combine(watch.observe(), local, ticks()) { snapshot, now, _ ->
        present(snapshot, now)
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MILLIS), OfferUiState())

    private val outcomes = Channel<OfferOutcome>(Channel.CONFLATED)
    val outcome: Flow<OfferOutcome> = outcomes.receiveAsFlow()

    fun accept() {
        val offer = lastSeen
        if (local.value.responding || offer == null) return
        local.update { it.copy(responding = true, message = null) }
        viewModelScope.launch {
            when (val result = acceptance.accept(offerId, offer.orderId)) {
                AcceptOutcome.Accepted -> {
                    duty.setOnJob(true)
                    watch.refreshNow()
                    outcomes.trySend(OfferOutcome.Accepted)
                }
                is AcceptOutcome.Refused -> {
                    val text = OfferAcceptance.refusalMessage(result.error)
                    val gone = text == OfferAcceptance.GONE
                    local.update { it.copy(responding = false, message = error(text), gone = gone) }
                    watch.refreshNow()
                }
            }
        }
    }

    /** A decline that fails leaves the offer to expire in seconds; the rider is never held on it. */
    fun reject() {
        if (local.value.responding) return
        local.update { it.copy(responding = true) }
        viewModelScope.launch {
            rider.rejectOffer(offerId)
            watch.refreshNow()
            outcomes.trySend(OfferOutcome.Closed)
        }
    }

    fun dismissMessage() = local.update { it.copy(message = null) }

    private fun present(snapshot: OfferSnapshot, now: Local): OfferUiState {
        val listed = snapshot.offers.firstOrNull { it.id == offerId }
        if (listed != null) lastSeen = listed
        val offer = listed ?: lastSeen
        val gone = now.gone || (snapshot.loaded && listed == null && !now.responding)
        val window = offer?.let { OfferCountdown.of(it.expiresAt).at(snapshot.clock.serverNow(clock.now())) }
        return OfferUiState(
            loading = !snapshot.loaded && offer == null,
            offer = offer,
            gone = gone,
            window = window ?: OfferWindow.Unknown,
            responding = now.responding,
            message = now.message,
        )
    }

    private fun ticks(): Flow<Unit> = flow {
        while (true) {
            emit(Unit)
            delay(TICK_MILLIS)
        }
    }

    private companion object {
        const val TICK_MILLIS = 1_000L
        const val STOP_TIMEOUT_MILLIS = 5_000L
    }
}
