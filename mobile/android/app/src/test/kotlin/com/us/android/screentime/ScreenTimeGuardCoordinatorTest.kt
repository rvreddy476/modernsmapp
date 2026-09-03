package com.us.android.screentime

import com.google.common.truth.Truth.assertThat
import com.us.android.core.datastore.UsageAccumulator
import com.us.android.core.datastore.UsageRecord
import com.us.android.core.datastore.UsageStore
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.profile.data.WellbeingApi
import com.us.android.core.profile.data.WellbeingGuardCache
import com.us.android.core.profile.data.WellbeingGuardSnapshot
import com.us.android.core.profile.data.WellbeingRepository
import com.us.android.core.profile.data.WellbeingSettings
import com.us.android.core.profile.data.dto.ScreenTimeDayDto
import com.us.android.core.profile.data.dto.ScreenTimeReportRequest
import com.us.android.core.profile.data.dto.ScreenTimeWeekDto
import com.us.android.core.profile.data.dto.UpdateWellbeingRequest
import com.us.android.core.profile.data.dto.WellbeingDto
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Test
import java.time.LocalDate
import java.time.ZoneOffset

/**
 * Covers the fix for the wellbeing settings being fetched once a minute
 * (`GET /v1/users/me/wellbeing` on every tick): the guard now fetches at
 * most once per foreground session, adopts a save immediately through the
 * shared [WellbeingRepository.guardSnapshot], and the per-minute tick itself
 * never touches the network.
 *
 * Every scheduled coroutine here is driven with [runCurrent], not
 * `advanceUntilIdle` — the latter does not reliably drain work launched on
 * [TestScope.backgroundScope] in the coroutines-test version this project
 * pins, confirmed by direct experiment against this suite.
 */
class ScreenTimeGuardCoordinatorTest {

    private val json = Json { ignoreUnknownKeys = true }
    private val zone = ZoneOffset.UTC
    private val today = "2026-09-03"
    private val todayClockMillis =
        LocalDate.parse(today).atStartOfDay(zone).toInstant().toEpochMilli() + ONE_HOUR_MILLIS

    private class FakeApi(var wellbeing: WellbeingDto = WellbeingDto()) : WellbeingApi {
        var wellbeingCalls = 0

        override suspend fun wellbeing(): ApiEnvelope<WellbeingDto> {
            wellbeingCalls++
            return ApiEnvelope(wellbeing)
        }

        override suspend fun updateWellbeing(body: UpdateWellbeingRequest): ApiEnvelope<WellbeingDto> {
            wellbeing = wellbeing.copy(dailyLimitMins = body.dailyLimitMins.takeIf { it > 0 })
            return ApiEnvelope(wellbeing)
        }

        override suspend fun reportScreenTime(body: ScreenTimeReportRequest): ApiEnvelope<ScreenTimeDayDto> =
            ApiEnvelope(ScreenTimeDayDto(body.date, 0, body.sessions))

        override suspend fun screenTime(range: String): ApiEnvelope<ScreenTimeWeekDto> =
            ApiEnvelope(ScreenTimeWeekDto(range, emptyList(), todayMinutes = 0, dailyLimitMins = null))
    }

    private class FakeGuardCache : WellbeingGuardCache {
        var stored: WellbeingGuardSnapshot? = null
        override suspend fun read(): WellbeingGuardSnapshot? = stored
        override suspend fun write(snapshot: WellbeingGuardSnapshot) {
            stored = snapshot
        }
    }

    private class FakeUsageStore(var records: List<UsageRecord> = emptyList()) : UsageStore {
        override suspend fun read(): List<UsageRecord> = records
        override suspend fun write(records: List<UsageRecord>) {
            this.records = records
        }
    }

    private fun TestScope.buildRepository(api: FakeApi) =
        WellbeingRepository(api, ErrorMapper(json), FakeGuardCache(), backgroundScope)

    /** A ledger with exactly [todayMinutes] already on today's date, no active session. */
    private suspend fun buildAccumulator(todayMinutes: Long): UsageAccumulator {
        val store = FakeUsageStore(listOf(UsageRecord(today, todayMinutes * MILLIS_PER_MINUTE, sessions = 1)))
        val accumulator = UsageAccumulator(store, zone) { todayClockMillis }
        accumulator.snapshot() // loads the ledger and populates todaySeconds, matching a cold start
        return accumulator
    }

    private fun TestScope.buildCoordinator(api: FakeApi, accumulator: UsageAccumulator) =
        ScreenTimeGuardCoordinator(accumulator, buildRepository(api), backgroundScope)

    @Test
    fun `onAppForeground fetches wellbeing at most once per session`() = runTest {
        val api = FakeApi()
        val coordinator = buildCoordinator(api, buildAccumulator(todayMinutes = 0))

        coordinator.onAppForeground()
        coordinator.onAppForeground() // still the same session — must not refetch
        runCurrent()

        assertThat(api.wellbeingCalls).isEqualTo(1)
    }

    @Test
    fun `backgrounding then foregrounding again starts a new session and refetches`() = runTest {
        val api = FakeApi()
        val coordinator = buildCoordinator(api, buildAccumulator(todayMinutes = 0))

        coordinator.onAppForeground()
        runCurrent()
        coordinator.onAppBackground()
        coordinator.onAppForeground()
        runCurrent()

        assertThat(api.wellbeingCalls).isEqualTo(2)
    }

    @Test
    fun `the per-minute tick is purely local and makes no network calls`() = runTest {
        val api = FakeApi().apply { wellbeing = wellbeing.copy(dailyLimitMins = 60) }
        val coordinator = buildCoordinator(api, buildAccumulator(todayMinutes = 5))

        coordinator.onAppForeground()
        runCurrent()
        assertThat(api.wellbeingCalls).isEqualTo(1)

        coordinator.start()
        advanceTimeBy(TEN_TICKS_MILLIS)
        runCurrent()

        // Ten more minutes of ticking resolved the dialog state repeatedly,
        // but never called wellbeing() again.
        assertThat(api.wellbeingCalls).isEqualTo(1)
    }

    @Test
    fun `a save is visible on the very next tick, with no fetch involved`() = runTest {
        val api = FakeApi()
        val repository = buildRepository(api)
        val coordinator = ScreenTimeGuardCoordinator(buildAccumulator(todayMinutes = 65), repository, backgroundScope)

        repository.save(
            WellbeingSettings(
                dailyLimitMins = 60,
                bedtimeStart = null,
                bedtimeEnd = null,
                focusModeEnabled = false,
                nudgeIntervalMins = 0,
                hideLikeCounts = false,
            ),
        )

        coordinator.start()
        runCurrent()

        assertThat(coordinator.message.value).isEqualTo(ScreenTimeGuardMessage.DAILY_LIMIT)
        // The guard read the save's result off the shared snapshot; it never
        // had to call GET wellbeing() itself.
        assertThat(api.wellbeingCalls).isEqualTo(0)
    }

    @Test
    fun `dismiss sticks for the rest of the process, even once the limit is exceeded`() = runTest {
        val api = FakeApi().apply { wellbeing = wellbeing.copy(dailyLimitMins = 60) }
        val coordinator = buildCoordinator(api, buildAccumulator(todayMinutes = 65))

        coordinator.onAppForeground()
        runCurrent() // let the one-shot fetch land before starting the (infinite) tick loop
        coordinator.start()
        runCurrent()
        assertThat(coordinator.message.value).isEqualTo(ScreenTimeGuardMessage.DAILY_LIMIT)

        coordinator.dismiss()
        advanceTimeBy(TEN_TICKS_MILLIS)
        runCurrent()

        assertThat(coordinator.message.value).isNull()
    }

    private companion object {
        const val MILLIS_PER_MINUTE = 60_000L
        const val ONE_HOUR_MILLIS = 60 * MILLIS_PER_MINUTE
        const val CHECK_INTERVAL_MILLIS = 60_000L
        const val TEN_TICKS_MILLIS = 10 * CHECK_INTERVAL_MILLIS
    }
}
