package com.us.android.feature.rider.home

import android.content.Context
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.model.Paise
import com.us.android.core.food.network.DeliveryAssignmentDto
import com.us.android.core.food.network.DeliveryOfferDto
import com.us.android.core.food.realtime.FoodRealtimeScope
import com.us.android.core.food.realtime.FoodRealtimeTokens
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.RiderRepository
import com.us.android.core.realtime.SseClient
import com.us.android.feature.rider.job.JobActions
import com.us.android.feature.rider.location.DutyStatus
import com.us.android.feature.rider.location.OfflineReason
import com.us.android.feature.rider.location.RiderDuty
import com.us.android.feature.rider.navigation.requireArg
import com.us.android.feature.rider.offers.LiveTransport
import com.us.android.feature.rider.offers.RiderLiveFeed
import com.us.android.feature.rider.offers.RiderRealtimeTopics
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
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
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
 * The feed runs for as long as Home is on the back stack, so an offer that
 * arrives while the rider is on another screen is already listed on return.
 */
@HiltViewModel
class HomeViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val rider: RiderRepository,
    private val duty: RiderDuty,
    private val disclosure: LocationDisclosureStore,
    sseClient: SseClient,
    tokens: FoodRealtimeTokens,
) : ViewModel() {

    private val userId = savedStateHandle.requireArg("userId")
    private val flow = GoOnlineFlow()

    private val feed = RiderLiveFeed(
        refresh = { refreshOffersAndJob() },
        subscribe = { source -> sseClient.connect(RiderRealtimeTopics.forRider(userId), source) },
        tokenSource = tokens.forScope(FoodRealtimeScope.Delivery),
    )

    private val _state = MutableStateFlow(HomeUiState())
    val state: StateFlow<HomeUiState> = _state.asStateFlow()

    private val _events = MutableSharedFlow<HomeEvent>(extraBufferCapacity = 4, onBufferOverflow = BufferOverflow.DROP_OLDEST)
    val events: SharedFlow<HomeEvent> = _events.asSharedFlow()

    init {
        viewModelScope.launch { loadProfile() }
        viewModelScope.launch { feed.run() }
        viewModelScope.launch { feed.transport.collect { t -> _state.update { it.copy(transport = t) } } }
        viewModelScope.launch { duty.status.collect(::onDutyStatus) }
        viewModelScope.launch { loadEarnings() }
    }

    fun refresh() {
        feed.refreshNow()
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

    fun onGoOfflineTapped() = perform(flow.onGoOfflineTapped())

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
                // The server still thinks the rider is online, but nothing on this
                // device is sharing location (the app was killed): put the server right.
                if (profile?.isOnline == true && !duty.isOnline) {
                    rider.setAvailability(online = false)
                    _state.update { it.copy(message = info("You were set offline while the app was closed. Go online when you're ready.")) }
                }
            }
            is FoodResult.Failure -> _state.update { it.copy(loading = false, message = result.error.asMessage()) }
        }
    }

    private suspend fun refreshOffersAndJob() {
        val offers = rider.offers()
        val job = rider.currentAssignment()
        _state.update { current ->
            current.copy(
                offers = (offers as? FoodResult.Success)?.value ?: current.offers,
                job = if (job is FoodResult.Success) job.value else current.job,
            )
        }
        if (job is FoodResult.Success) duty.setOnJob(job.value?.let { JobActions.of(it).isActive } == true)
    }

    private suspend fun loadEarnings() {
        val result = rider.earnings()
        if (result is FoodResult.Success) {
            _state.update { it.copy(deliveriesToday = result.value.deliveriesToday, earningsToday = result.value.earningsToday) }
        }
    }
}
