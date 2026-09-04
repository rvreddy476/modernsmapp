package com.us.android.screentime

import com.us.android.core.common.di.ApplicationScope
import com.us.android.core.datastore.UsageAccumulator
import com.us.android.core.profile.data.WellbeingGuardSnapshot
import com.us.android.core.profile.data.WellbeingRepository
import kotlinx.coroutines.CoroutineScope
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
 * nudge, never a block.
 *
 * The limit and sleep window come from [WellbeingRepository.guardSnapshot],
 * fetched at most once per foreground session ([onAppForeground]) rather than
 * on every tick — `GET /v1/users/me/wellbeing` on a per-minute poll was one
 * request a minute per user for a value that changes only when the person
 * edits the Screen time screen, which [WellbeingRepository.save] already
 * pushes into that same shared snapshot. The tick itself ([check]) is a pure
 * local computation against the cached snapshot and [UsageAccumulator]'s
 * ledger — no I/O — so a once-a-minute cadence costs nothing to keep.
 *
 * Lives in `:app` for the same reason [ScreenTimeSyncCoordinator] does: it
 * joins `:core:datastore`'s ledger with `:core:profile`'s settings endpoint.
 */
@Singleton
class ScreenTimeGuardCoordinator @Inject constructor(
    private val accumulator: UsageAccumulator,
    private val repository: WellbeingRepository,
    @ApplicationScope private val scope: CoroutineScope,
) {
    private var started = false

    /** Sticks for the process lifetime once dismissed — "once per session". */
    private var dismissed = false

    /** Guards the once-per-foreground-session fetch; cleared in [onAppBackground]. */
    private var fetchedThisSession = false

    private val _message = MutableStateFlow<ScreenTimeGuardMessage?>(null)
    val message: StateFlow<ScreenTimeGuardMessage?> = _message.asStateFlow()

    /** Starts the local tick. Idempotent: called once from process start. */
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

    /**
     * One fetch per foreground session, so the guard's view of the limit and
     * sleep window is current without polling. A failed fetch leaves whatever
     * [WellbeingRepository.guardSnapshot] already holds in place — never
     * blocks, never crashes the tick.
     */
    fun onAppForeground() {
        if (fetchedThisSession) return
        fetchedThisSession = true
        scope.launch { repository.settings() }
    }

    /** A new foreground later is a new session, and earns its own fetch. */
    fun onAppBackground() {
        fetchedThisSession = false
    }

    fun dismiss() {
        dismissed = true
        _message.value = null
    }

    /**
     * Ticks left before the nudge may show again. Counted in ticks rather
     * than wall-clock so the quarter hour is exact under the test scheduler
     * and needs no clock to inject.
     */
    private var snoozeTicksRemaining = 0

    /** "Remind me in 15 minutes": hides the nudge and holds it for [SNOOZE_TICKS] ticks. */
    fun snooze() {
        snoozeTicksRemaining = SNOOZE_TICKS
        _message.value = null
    }

    /** Purely local: no network, just the cached snapshot and the local usage ledger. */
    private fun check() {
        if (dismissed) return
        if (snoozeTicksRemaining > 0) {
            snoozeTicksRemaining--
            return
        }
        val snapshot = repository.guardSnapshot.value
        _message.value = snapshot?.let { resolve(it, accumulator.todaySeconds.value / SECONDS_PER_MINUTE) }
    }

    private fun resolve(snapshot: WellbeingGuardSnapshot, todayMinutes: Long): ScreenTimeGuardMessage? {
        val limit = snapshot.dailyLimitMins
        return when {
            limit != null && todayMinutes >= limit -> ScreenTimeGuardMessage.DAILY_LIMIT
            snapshot.sleepHoursEnabled && isWithinSleepWindow(snapshot) -> ScreenTimeGuardMessage.SLEEP_TIME
            else -> null
        }
    }

    /** Handles a window that wraps midnight (e.g. 23:00 -> 07:00), which is the common case. */
    private fun isWithinSleepWindow(snapshot: WellbeingGuardSnapshot): Boolean {
        val start = snapshot.bedtimeStart?.toLocalTimeOrNull() ?: return false
        val end = snapshot.bedtimeEnd?.toLocalTimeOrNull() ?: return false
        val now = LocalTime.now()
        return if (start <= end) now in start..end else now >= start || now <= end
    }

    private fun String.toLocalTimeOrNull(): LocalTime? = runCatching { LocalTime.parse(this, TIME_FORMAT) }.getOrNull()

    private companion object {
        const val CHECK_INTERVAL_MILLIS = 60_000L
        const val SECONDS_PER_MINUTE = 60L

        /** Fifteen one-minute ticks. */
        const val SNOOZE_TICKS = 15
        val TIME_FORMAT: DateTimeFormatter = DateTimeFormatter.ofPattern("HH:mm")
    }
}
