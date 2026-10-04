package com.us.android.feature.doorsteppro.location

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
 * The one shared fact "is the professional on duty", between
 * [ProLocationService] (which alone reports it) and the screens (which ask it
 * to stop, and say whether the professional is travelling to a job).
 * Feast Rider's RiderDuty, copied.
 *
 * In-process only: if the process dies the service dies with it, and Home
 * puts the server right (duty off) on the next start.
 */
@Singleton
class ProDuty @Inject constructor() {

    private val _status = MutableStateFlow<DutyStatus>(DutyStatus.Offline(null))
    val status: StateFlow<DutyStatus> = _status.asStateFlow()

    private val _travelling = MutableStateFlow(false)

    /** True while a job is en route: the cadence runs at 8 s / 15 m, high accuracy. */
    val travelling: StateFlow<Boolean> = _travelling.asStateFlow()

    private val _stopRequests = MutableSharedFlow<OfflineReason>(extraBufferCapacity = STOP_BUFFER)
    val stopRequests: SharedFlow<OfflineReason> = _stopRequests.asSharedFlow()

    val isOnline: Boolean get() = _status.value is DutyStatus.Online

    fun setTravelling(active: Boolean) {
        _travelling.value = active
    }

    /** Asks the running service to go off duty. A no-op when none runs. */
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
