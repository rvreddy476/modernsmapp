package com.us.android.screentime

import com.us.android.core.common.result.AppResult
import com.us.android.core.datastore.UsageAccumulator
import com.us.android.core.profile.data.WellbeingRepository
import com.us.android.core.profile.data.WellbeingSettings
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import java.time.LocalTime
import java.time.format.DateTimeFormatter
import javax.inject.Inject
import javax.inject.Singleton

enum class ScreenTimeGuardMessage { DAILY_LIMIT, SLEEP_TIME }

/**
 * "You've reached your daily limit" / "It's your sleep time" — a dismissible
 * nudge, never a block. Checked on a low-frequency timer rather than pushed
 * by the server: neither wellbeing setting nor the local usage ledger has a
 * change notification, and a once-a-minute poll is cheap enough that a
 * bespoke event source is not worth the complexity.
 *
 * Lives in `:app` for the same reason [ScreenTimeSyncCoordinator] does: it
 * joins `:core:datastore`'s ledger with `:core:profile`'s settings endpoint.
 */
@Singleton
class ScreenTimeGuardCoordinator @Inject constructor(
    private val accumulator: UsageAccumulator,
    private val repository: WellbeingRepository,
) {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private var started = false

    /** Sticks for the process lifetime once dismissed — "once per session". */
    private var dismissed = false

    private val _message = MutableStateFlow<ScreenTimeGuardMessage?>(null)
    val message: StateFlow<ScreenTimeGuardMessage?> = _message.asStateFlow()

    fun start() {
        if (started) return
        started = true
        scope.launch {
            while (isActive) {
                check()
                delay(CHECK_INTERVAL_MILLIS)
            }
        }
    }

    fun dismiss() {
        dismissed = true
        _message.value = null
    }

    private suspend fun check() {
        if (dismissed) return
        val settings = (repository.settings() as? AppResult.Success)?.data
        _message.value = settings?.let { resolve(it, accumulator.todaySeconds.value / SECONDS_PER_MINUTE) }
    }

    private fun resolve(settings: WellbeingSettings, todayMinutes: Long): ScreenTimeGuardMessage? {
        val limit = settings.dailyLimitMins
        return when {
            limit != null && todayMinutes >= limit -> ScreenTimeGuardMessage.DAILY_LIMIT
            settings.sleepHoursEnabled && isWithinSleepWindow(settings) -> ScreenTimeGuardMessage.SLEEP_TIME
            else -> null
        }
    }

    /** Handles a window that wraps midnight (e.g. 23:00 -> 07:00), which is the common case. */
    private fun isWithinSleepWindow(settings: WellbeingSettings): Boolean {
        val start = settings.bedtimeStart?.toLocalTimeOrNull() ?: return false
        val end = settings.bedtimeEnd?.toLocalTimeOrNull() ?: return false
        val now = LocalTime.now()
        return if (start <= end) now in start..end else now >= start || now <= end
    }

    private fun String.toLocalTimeOrNull(): LocalTime? = runCatching { LocalTime.parse(this, TIME_FORMAT) }.getOrNull()

    private companion object {
        const val CHECK_INTERVAL_MILLIS = 60_000L
        const val SECONDS_PER_MINUTE = 60L
        val TIME_FORMAT: DateTimeFormatter = DateTimeFormatter.ofPattern("HH:mm")
    }
}
