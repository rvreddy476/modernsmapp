package com.us.android.feature.doorsteppro.home

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.feature.doorsteppro.data.DoorstepProRepository
import com.us.android.feature.doorsteppro.data.LocationRequest
import com.us.android.feature.doorsteppro.data.OfferDto
import com.us.android.feature.doorsteppro.data.ProJobDto
import com.us.android.feature.doorsteppro.data.ProResult
import com.us.android.feature.doorsteppro.data.ProfessionalDto
import com.us.android.feature.doorsteppro.di.ProEventStream
import com.us.android.feature.doorsteppro.domain.JobStatus
import com.us.android.feature.doorsteppro.domain.OnboardingChecklist
import com.us.android.feature.doorsteppro.domain.ProStatus
import com.us.android.feature.doorsteppro.location.CurrentLocationSource
import com.us.android.feature.doorsteppro.location.DutyEffect
import com.us.android.feature.doorsteppro.location.DutyState
import com.us.android.feature.doorsteppro.location.DutyStatus
import com.us.android.feature.doorsteppro.location.GoOnDutyFlow
import com.us.android.feature.doorsteppro.location.OfflineReason
import com.us.android.feature.doorsteppro.location.ProDuty
import com.us.android.feature.doorsteppro.realtime.LiveTransport
import com.us.android.feature.doorsteppro.realtime.ProLiveFeed
import com.us.android.feature.doorsteppro.realtime.ProRealtimeTokenSource
import com.us.android.feature.doorsteppro.root.ProSession
import com.us.android.feature.doorsteppro.store.LocationDisclosureStore
import com.us.android.feature.doorsteppro.ui.asMessage
import com.us.android.feature.doorsteppro.ui.errorMessage
import com.us.android.feature.doorsteppro.ui.infoMessage
import com.us.android.feature.doorsteppro.ui.warningMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.emptyFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class HomeUiState(
    val professional: ProfessionalDto? = null,
    val status: ProStatus = ProStatus.UNKNOWN,
    val canGoOnDuty: Boolean = false,
    val duty: DutyState = DutyState.Offline(),
    val lastPingOk: Boolean? = null,
    val offers: List<OfferDto> = emptyList(),
    val activeJobs: List<ProJobDto> = emptyList(),
    val transport: LiveTransport = LiveTransport.CONNECTING,
    val message: UsMessage? = null,
)

/** One-shot instructions only the screen can carry out. */
sealed interface HomeEvent {
    data object RequestLocationPermission : HomeEvent

    data object StartLocationService : HomeEvent
}

/**
 * Home: the duty toggle ([GoOnDutyFlow]: disclosure → permission → `duty/on`
 * with a fresh fix → the location service), the open offers and the active
 * jobs, kept fresh by [ProLiveFeed] — doorstep.pro.<user_id> over SSE when the
 * token is issued, polling every 15 s when not.
 */
@HiltViewModel
class HomeViewModel @Inject constructor(
    private val repository: DoorstepProRepository,
    private val duty: ProDuty,
    private val disclosure: LocationDisclosureStore,
    private val location: CurrentLocationSource,
    session: ProSession,
    stream: ProEventStream,
) : ViewModel() {

    private val flow = GoOnDutyFlow()

    private val userId: String? = session.professional.value?.userId

    private val feed = ProLiveFeed(
        refresh = { refreshOffersAndJobs() },
        subscribe = { source -> userId?.let { stream.events(it, source) } ?: emptyFlow() },
        tokenSource = ProRealtimeTokenSource(repository),
    )

    private val _state = MutableStateFlow(HomeUiState(professional = session.professional.value))
    val state: StateFlow<HomeUiState> = _state.asStateFlow()

    private val _events = MutableSharedFlow<HomeEvent>(extraBufferCapacity = 4, onBufferOverflow = BufferOverflow.DROP_OLDEST)
    val events: SharedFlow<HomeEvent> = _events.asSharedFlow()

    init {
        viewModelScope.launch { loadAccount() }
        viewModelScope.launch { feed.run() }
        viewModelScope.launch { feed.transport.collect { t -> _state.update { it.copy(transport = t) } } }
        viewModelScope.launch { duty.status.collect(::onDutyStatus) }
        viewModelScope.launch {
            // Nothing on this device is sharing location (the process died, or a
            // fresh start): make sure the server does not hold us on duty either.
            if (!duty.isOnline) repository.dutyOff()
        }
    }

    fun refresh() {
        feed.refreshNow()
        viewModelScope.launch { loadAccount() }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun onGoOnDutyTapped(permissionGranted: Boolean) {
        if (!_state.value.canGoOnDuty) {
            _state.update { it.copy(message = errorMessage(notReadyText(it.status))) }
            return
        }
        perform(flow.onGoOnlineTapped(permissionGranted, disclosure.accepted()))
    }

    fun onDisclosureAccepted(permissionGranted: Boolean) {
        disclosure.markAccepted()
        perform(flow.onDisclosureAccepted(permissionGranted))
    }

    fun onDisclosureDeclined() {
        flow.onDisclosureDeclined()
        publishDuty()
    }

    fun onPermissionResult(granted: Boolean, canAskAgain: Boolean) = perform(flow.onPermissionResult(granted, canAskAgain))

    fun onGoOffDutyTapped() = perform(flow.onGoOfflineTapped())

    private fun perform(effect: DutyEffect) {
        publishDuty()
        when (effect) {
            DutyEffect.None -> Unit
            DutyEffect.RequestPermission -> _events.tryEmit(HomeEvent.RequestLocationPermission)
            DutyEffect.SetServerOnline -> viewModelScope.launch { goOnDuty() }
            DutyEffect.StartService -> _events.tryEmit(HomeEvent.StartLocationService)
            DutyEffect.StopService -> duty.requestStop(OfflineReason.TOGGLED_OFF)
        }
    }

    private suspend fun goOnDuty() {
        val fix = location.current()
        if (fix == null) {
            _state.update { it.copy(message = errorMessage("Couldn't get your location. Step outside or turn on GPS, then try again.")) }
            perform(flow.onServerAvailability(accepted = false))
            return
        }
        val result = repository.dutyOn(LocationRequest(lat = fix.latitude, lng = fix.longitude))
        if (result is ProResult.Failure) _state.update { it.copy(message = result.error.asMessage()) }
        perform(flow.onServerAvailability(accepted = result is ProResult.Success && result.value.onDuty))
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
                    if (reason != OfflineReason.TOGGLED_OFF) _state.update { it.copy(message = warningMessage(explanation(reason))) }
                }
            }
        }
        publishDuty()
    }

    private fun publishDuty() = _state.update { it.copy(duty = flow.state) }

    private suspend fun loadAccount() {
        val me = repository.me()
        val readiness = repository.readiness()
        _state.update { current ->
            val pro = (me as? ProResult.Success)?.value ?: current.professional
            val checklist = (readiness as? ProResult.Success)?.value?.let { OnboardingChecklist.of(it) }
            current.copy(
                professional = pro,
                status = ProStatus.of(pro?.status),
                canGoOnDuty = checklist?.canGoOnDuty ?: current.canGoOnDuty,
                message = current.message ?: (me as? ProResult.Failure)?.error?.asMessage(),
            )
        }
    }

    private suspend fun refreshOffersAndJobs() {
        val offers = repository.offers()
        val jobs = repository.jobs(status = ACTIVE, cursor = null)
        _state.update { current ->
            current.copy(
                offers = (offers as? ProResult.Success)?.value ?: current.offers,
                activeJobs = (jobs as? ProResult.Success)?.value?.items?.filter { JobStatus.of(it.status).isActive } ?: current.activeJobs,
            )
        }
        if (jobs is ProResult.Success) {
            duty.setTravelling(jobs.value.items.any { JobStatus.of(it.status) == JobStatus.EN_ROUTE })
        }
    }

    private fun notReadyText(status: ProStatus): String = when (status) {
        ProStatus.SUSPENDED -> "Your account is paused. You can't go on duty until Doorstep reinstates it."
        ProStatus.BLOCKED -> "This account can no longer take Doorstep jobs."
        ProStatus.APPROVED -> "A step needs attention first — open your checklist (your police certificate may have expired)."
        else -> "Finish your checklist and Doorstep's review first."
    }

    private fun explanation(reason: OfflineReason): String = when (reason) {
        OfflineReason.NO_FIX -> "You went off duty: no location for 2 minutes."
        OfflineReason.PERMISSION_REVOKED -> "You went off duty: location permission was turned off."
        OfflineReason.SERVER_REFUSED -> "Doorstep took you off duty. Check your account, then go on duty again."
        OfflineReason.SIGNED_OUT -> "You were signed out."
        OfflineReason.TOGGLED_OFF -> "You're off duty."
    }

    /** Called by the screen when the app comes back with no service running but the flow thinks otherwise. */
    fun onResumedWithoutService() {
        if (!duty.isOnline && flow.state == DutyState.Online) {
            flow.onServiceStopped(OfflineReason.NO_FIX)
            publishDuty()
            _state.update { it.copy(message = infoMessage("You're off duty.")) }
        }
    }

    private companion object {
        const val ACTIVE = "active"
    }
}
