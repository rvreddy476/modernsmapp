package com.us.android.feature.rider.location

import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.code
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import javax.inject.Inject
import javax.inject.Singleton

/** Something the service tells the rider without changing whether they are on duty. */
enum class DutyNotice {
    /** The server could not be asked, or refused for another reason; still on duty. */
    OFFLINE_REFUSED,

    /** 409 WORK_BUSY: the server says the rider is carrying a delivery; still on duty. */
    OFFLINE_REFUSED_ON_JOB,

    /** No location fix for two minutes during a delivery; still on duty, the customer's map is stale. */
    FIX_LOST_ON_JOB,

    /** A fix arrived again after [FIX_LOST_ON_JOB]. */
    FIX_BACK,
}

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

    private val _notices = MutableSharedFlow<DutyNotice>(extraBufferCapacity = STOP_BUFFER)
    val notices: SharedFlow<DutyNotice> = _notices.asSharedFlow()

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

    internal fun notice(notice: DutyNotice) {
        _notices.tryEmit(notice)
    }

    private companion object {
        const val STOP_BUFFER = 4
    }
}

/**
 * Which notice a refused "Go offline" gives. 409 WORK_BUSY (food server lane,
 * 9 Nov 2026: refused while a job is held, with partnerwork on or off) says
 * finish the delivery; any other failure, including no answer, keeps the
 * general [DutyNotice.OFFLINE_REFUSED]. The rider stays on duty either way.
 */
fun offlineRefusal(error: FoodError): DutyNotice =
    if (error.code == WORK_BUSY) DutyNotice.OFFLINE_REFUSED_ON_JOB else DutyNotice.OFFLINE_REFUSED

private const val WORK_BUSY = "WORK_BUSY"
