package com.us.android.feature.mopedu.captain.location

import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import javax.inject.Inject
import javax.inject.Singleton

sealed interface DutyStatus {
    data class Offline(val reason: OfflineReason?) : DutyStatus

    /** [lastPingOk] is null until the first ping has been answered. */
    data class Online(val lastPingOk: Boolean?) : DutyStatus
}

/**
 * The one shared fact "is the captain on duty", between [CaptainLocationService]
 * (which alone reports it) and the screens (which ask it to stop, and say
 * whether a ride is active). In-process only: if the process dies the service
 * dies with it, and Home reconciles the server's `is_online` on the next start.
 */
@Singleton
class CaptainDuty @Inject constructor() {

    private val _status = MutableStateFlow<DutyStatus>(DutyStatus.Offline(null))
    val status: StateFlow<DutyStatus> = _status.asStateFlow()

    private val _onRide = MutableStateFlow(false)
    val onRide: StateFlow<Boolean> = _onRide.asStateFlow()

    private val _stopRequests = MutableSharedFlow<OfflineReason>(extraBufferCapacity = STOP_BUFFER)
    val stopRequests: SharedFlow<OfflineReason> = _stopRequests.asSharedFlow()

    val isOnline: Boolean get() = _status.value is DutyStatus.Online

    /** Switches the cadence: 5 s / 10 m while a ride is active. */
    fun setOnRide(active: Boolean) {
        _onRide.value = active
    }

    /** Asks the running service to go offline. A no-op when none runs. */
    fun requestStop(reason: OfflineReason) {
        _stopRequests.tryEmit(reason)
    }

    internal fun report(status: DutyStatus) {
        _status.value = status
    }

    private companion object {
        const val STOP_BUFFER = 4
    }
}
