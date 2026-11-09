package com.us.android.feature.rider.home

import android.content.Context
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.model.Paise
import com.us.android.core.food.network.DeliveryAssignmentDto
import com.us.android.core.food.network.DeliveryOfferDto
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.RiderRepository
import com.us.android.feature.rider.location.DutyNotice
import com.us.android.feature.rider.location.DutyStatus
import com.us.android.feature.rider.location.OfflineReason
import com.us.android.feature.rider.location.RiderDuty
import com.us.android.feature.rider.money.RiderMoney
import com.us.android.feature.rider.offers.LiveTransport
import com.us.android.feature.rider.offers.OfferSnapshot
import com.us.android.feature.rider.offers.RiderOfferWatch
import com.us.android.feature.rider.ui.asMessage
import com.us.android.feature.rider.ui.explanation
import com.us.android.feature.rider.ui.info
import com.us.android.feature.rider.ui.warning
import dagger.hilt.android.lifecycle.HiltViewModel
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject
import javax.inject.Singleton

data class HomeUiState(
    val loading: Boolean = true,
    val riderName: String = "",
    val partnerStatus: String = "",
    val duty: DutyState = DutyState.Offline(),
    val lastPingOk: Boolean? = null,
    val offers: List<DeliveryOfferDto> = emptyList(),
    val job: DeliveryAssignmentDto? = null,
    val transport: LiveTransport = LiveTransport.CONNECTING,
    /** The offer feed's clocks: offers count down from these, never from the bare device time. */
    val offerClock: OfferSnapshot = OfferSnapshot(),
    /** A delivery is active: Go offline and sign out are held until it ends (food audit M2). */
    val onJob: Boolean = false,
    val deliveriesToday: Int? = null,
    val earningsToday: Paise? = null,
    val message: UsMessage? = null,
)

/** One-shot instructions only the screen can carry out. */
sealed interface HomeEvent {
    data object RequestLocationPermission : HomeEvent

    data object StartLocationService : HomeEvent
}

/** Whether this install has shown and the rider accepted the location disclosure. */
interface LocationDisclosureStore {
    fun accepted(): Boolean
    fun markAccepted()
}

@Singleton
class SharedPrefsLocationDisclosureStore @Inject constructor(
    @ApplicationContext context: Context,
) : LocationDisclosureStore {
    private val prefs = context.getSharedPreferences("rider_location_disclosure", Context.MODE_PRIVATE)

    override fun accepted(): Boolean = prefs.getBoolean(KEY, false)

    override fun markAccepted() {
        prefs.edit().putBoolean(KEY, true).apply()
    }

    private companion object {
        const val KEY = "accepted_v1"
    }
}

/**
 * Home: the Go-online toggle ([GoOnlineFlow]), the live offers, the current
 * job and today's earnings.
 *
 * Offers and the job come from [RiderOfferWatch], held only while Home is
 * collected (food audit L5); the location service holds the same feed while
 * the rider is on duty, so nothing is missed off screen and nothing polls off
 * duty in the background.
 */
@HiltViewModel
class HomeViewModel @Inject constructor(
    private val rider: RiderRepository,
    private val duty: RiderDuty,
    private val disclosure: LocationDisclosureStore,
    private val watch: RiderOfferWatch,
) : ViewModel() {

    private val flow = GoOnlineFlow()

    private val _state = MutableStateFlow(HomeUiState())
    val state: StateFlow<HomeUiState> = combine(_state, watch.observe(), duty.onJob) { home, live, onJob ->
        home.copy(offers = live.offers, job = live.job, transport = live.transport, offerClock = live, onJob = onJob)
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MILLIS), HomeUiState())

    private val _events = MutableSharedFlow<HomeEvent>(extraBufferCapacity = 4, onBufferOverflow = BufferOverflow.DROP_OLDEST)
    val events: SharedFlow<HomeEvent> = _events.asSharedFlow()

    init {
        viewModelScope.launch { loadProfile() }
        viewModelScope.launch { duty.status.collect(::onDutyStatus) }
        viewModelScope.launch { duty.notices.collect(::onDutyNotice) }
        viewModelScope.launch { loadEarnings() }
    }

    fun refresh() {
        watch.refreshNow()
        viewModelScope.launch { loadEarnings() }
    }

    fun dismissMessage() {
        _state.update { it.copy(message = null) }
    }

    fun onGoOnlineTapped(permissionGranted: Boolean) =
        perform(flow.onGoOnlineTapped(permissionGranted, disclosure.accepted()))

    fun onDisclosureAccepted(permissionGranted: Boolean) {
        disclosure.markAccepted()
        perform(flow.onDisclosureAccepted(permissionGranted))
    }

    fun onDisclosureDeclined() {
        flow.onDisclosureDeclined()
        publishDuty()
    }

    fun onPermissionResult(granted: Boolean, canAskAgain: Boolean) =
        perform(flow.onPermissionResult(granted, canAskAgain))

    fun onGoOfflineTapped() = perform(flow.onGoOfflineTapped(onJob = duty.onJob.value))

    /** Whether the rider may sign out now. Never mid-delivery (food audit M2); the screen says why. */
    fun onSignOutTapped(): Boolean {
        if (!duty.onJob.value) return true
        _state.update { it.copy(message = warning(FINISH_FIRST)) }
        return false
    }

    private fun perform(effect: DutyEffect) {
        publishDuty()
        when (effect) {
            DutyEffect.None -> Unit
            DutyEffect.RequestPermission -> _events.tryEmit(HomeEvent.RequestLocationPermission)
            DutyEffect.SetServerOnline -> viewModelScope.launch {
                val result = rider.setAvailability(online = true)
                if (result is FoodResult.Failure) _state.update { it.copy(message = result.error.asMessage()) }
                perform(flow.onServerAvailability(accepted = result is FoodResult.Success))
            }
            DutyEffect.StartService -> _events.tryEmit(HomeEvent.StartLocationService)
            DutyEffect.StopService -> duty.requestStop(OfflineReason.TOGGLED_OFF)
            DutyEffect.RefusedOnJob -> _state.update { it.copy(message = warning(FINISH_FIRST)) }
        }
    }

    private fun onDutyStatus(status: DutyStatus) {
        when (status) {
            is DutyStatus.Online -> {
                if (flow.state !is DutyState.Online) flow.onServiceRunning()
                _state.update { it.copy(lastPingOk = status.lastPingOk) }
            }
            is DutyStatus.Offline -> {
                val wasOnDuty = flow.state == DutyState.Online || flow.state == DutyState.GoingOffline
                val reason = status.reason
                if (wasOnDuty && reason != null) {
                    flow.onServiceStopped(reason)
                    if (reason != OfflineReason.TOGGLED_OFF) {
                        _state.update { it.copy(message = warning(reason.explanation())) }
                    }
                }
            }
        }
        publishDuty()
    }

    private fun onDutyNotice(notice: DutyNotice) {
        val text = when (notice) {
            DutyNotice.OFFLINE_REFUSED ->
                "You're still online: Feast couldn't take you offline. Check your connection and try again."
            DutyNotice.OFFLINE_REFUSED_ON_JOB -> OFFLINE_REFUSED_ON_JOB
            DutyNotice.FIX_LOST_ON_JOB ->
                "No GPS signal for two minutes. You're still on duty; move into the open so the customer can see you."
            DutyNotice.FIX_BACK -> "GPS is back. The customer can see you again."
        }
        _state.update { it.copy(message = if (notice == DutyNotice.FIX_BACK) info(text) else warning(text)) }
    }

    private fun publishDuty() {
        _state.update { it.copy(duty = flow.state) }
    }

    private suspend fun loadProfile() {
        when (val result = rider.profile()) {
            is FoodResult.Success -> {
                val profile = result.value
                _state.update {
                    it.copy(loading = false, riderName = profile?.fullName.orEmpty(), partnerStatus = profile?.status.orEmpty())
                }
                // Duty belongs to the server across devices. Opening another phone
                // must never take an active partner offline or abandon accepted work.
                if (profile?.isOnline == true && !duty.isOnline) {
                    _state.update { it.copy(message = info("Your account is on duty. Enable location on this phone to continue receiving nearby work here.")) }
                }
            }
            is FoodResult.Failure -> _state.update { it.copy(loading = false, message = result.error.asMessage()) }
        }
    }

    private suspend fun loadEarnings() {
        val result = rider.earnings()
        if (result is FoodResult.Success) {
            _state.update { it.copy(deliveriesToday = result.value.deliveriesToday, earningsToday = RiderMoney.earnedToday(result.value)) }
        }
    }

    private companion object {
        const val STOP_TIMEOUT_MILLIS = 5_000L
        const val FINISH_FIRST = "Finish your delivery first. You stay online until it's delivered."
        const val OFFLINE_REFUSED_ON_JOB =
            "You're still online: you're carrying a delivery. Finish it, then go offline."
    }
}
