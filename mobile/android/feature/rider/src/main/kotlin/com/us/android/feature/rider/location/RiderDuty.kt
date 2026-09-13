package com.us.android.feature.rider.location

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
 * The one shared fact "is the rider on duty", between [RiderLocationService]
 * (which alone reports it) and the screens (which ask it to stop, and say
 * whether a job is active).
 *
 * In-process only: if the process dies the service dies with it, and Home
 * reconciles the server's `is_online` on the next start.
 */
@Singleton
class RiderDuty @Inject constructor() {

    private val _status = MutableStateFlow<DutyStatus>(DutyStatus.Offline(null))
    val status: StateFlow<DutyStatus> = _status.asStateFlow()

    private val _onJob = MutableStateFlow(false)
    val onJob: StateFlow<Boolean> = _onJob.asStateFlow()

    private val _stopRequests = MutableSharedFlow<OfflineReason>(extraBufferCapacity = STOP_BUFFER)
    val stopRequests: SharedFlow<OfflineReason> = _stopRequests.asSharedFlow()

    val isOnline: Boolean get() = _status.value is DutyStatus.Online

    /** Switches the cadence: 8 s / 15 m while a job is active. */
    fun setOnJob(active: Boolean) {
        _onJob.value = active
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
