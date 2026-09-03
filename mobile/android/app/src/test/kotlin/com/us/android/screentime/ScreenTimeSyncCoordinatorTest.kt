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
import java.time.LocalDateTime
import java.time.ZoneOffset

/**
 * Cadence (on background, and at most every five minutes while foreground)
 * and the zero-delta skip: [ScreenTimeSyncCoordinator.markFlushed] never
 * drops *today* from the ledger, so it reappears in every snapshot even when
 * nothing new has accumulated since the last confirmed report — these tests
 * pin down that a redundant flush is skipped rather than re-posted.
 */
class ScreenTimeSyncCoordinatorTest {

    private val json = Json { ignoreUnknownKeys = true }
    private val zone = ZoneOffset.UTC
    private var clock = 0L

    private fun at(text: String): Long = LocalDateTime.parse(text).atZone(zone).toInstant().toEpochMilli()

    private class FakeApi : WellbeingApi {
        var reportCalls = 0
        var lastReport: ScreenTimeReportRequest? = null

        override suspend fun wellbeing() = ApiEnvelope(WellbeingDto())
        override suspend fun updateWellbeing(body: UpdateWellbeingRequest) = ApiEnvelope(WellbeingDto())

        override suspend fun reportScreenTime(body: ScreenTimeReportRequest): ApiEnvelope<ScreenTimeDayDto> {
            reportCalls++
            lastReport = body
            return ApiEnvelope(ScreenTimeDayDto(body.date, 0, body.sessions))
        }

        override suspend fun screenTime(range: String) =
            ApiEnvelope(ScreenTimeWeekDto(range, emptyList(), todayMinutes = 0, dailyLimitMins = null))
    }

    private class FakeGuardCache : WellbeingGuardCache {
        override suspend fun read(): WellbeingGuardSnapshot? = null
        override suspend fun write(snapshot: WellbeingGuardSnapshot) = Unit
    }

    private class FakeUsageStore(var records: List<UsageRecord> = emptyList()) : UsageStore {
        override suspend fun read(): List<UsageRecord> = records
        override suspend fun write(records: List<UsageRecord>) {
            this.records = records
        }
    }

    private fun TestScope.build(api: FakeApi): ScreenTimeSyncCoordinator {
        val accumulator = UsageAccumulator(FakeUsageStore(), zone) { clock }
        val repository = WellbeingRepository(api, ErrorMapper(json), FakeGuardCache(), backgroundScope)
        return ScreenTimeSyncCoordinator(accumulator, repository, backgroundScope)
    }

    @Test
    fun `the foreground loop does not flush before five minutes have elapsed`() = runTest {
        clock = at("2026-09-03T10:00:00")
        val api = FakeApi()
        val coordinator = build(api)

        coordinator.onForeground()
        runCurrent() // opens the session; the loop is now parked in delay(FLUSH_INTERVAL_MILLIS)

        clock = at("2026-09-03T10:04:59")
        advanceTimeBy(FLUSH_INTERVAL_MILLIS - 1_000)
        runCurrent()

        assertThat(api.reportCalls).isEqualTo(0)
    }

    @Test
    fun `the foreground loop flushes at the five minute mark`() = runTest {
        clock = at("2026-09-03T10:00:00")
        val api = FakeApi()
        val coordinator = build(api)

        coordinator.onForeground()
        runCurrent()

        clock = at("2026-09-03T10:04:59")
        advanceTimeBy(FLUSH_INTERVAL_MILLIS - 1_000)
        runCurrent()
        clock = at("2026-09-03T10:05:00")
        advanceTimeBy(1_000)
        runCurrent()

        assertThat(api.reportCalls).isEqualTo(1)
        assertThat(api.lastReport?.foregroundSecs).isEqualTo(SECONDS_PER_FLUSH_INTERVAL)
    }

    @Test
    fun `backgrounding always flushes immediately, without waiting for the loop`() = runTest {
        clock = at("2026-09-03T10:00:00")
        val api = FakeApi()
        val coordinator = build(api)

        coordinator.onForeground()
        runCurrent()
        clock = at("2026-09-03T10:00:30")

        coordinator.onBackground()
        runCurrent()

        assertThat(api.reportCalls).isEqualTo(1)
        assertThat(api.lastReport?.foregroundSecs).isEqualTo(30L)
    }

    @Test
    fun `a redundant background flush with nothing new since the last confirmed report is skipped`() = runTest {
        clock = at("2026-09-03T10:00:00")
        val api = FakeApi()
        val coordinator = build(api)

        coordinator.onForeground()
        runCurrent()
        clock = at("2026-09-03T10:00:30")
        coordinator.onBackground()
        runCurrent()
        assertThat(api.reportCalls).isEqualTo(1)

        // A second background trigger with the clock unchanged — the same
        // total that was just confirmed. Must not be re-posted.
        coordinator.onBackground()
        runCurrent()

        assertThat(api.reportCalls).isEqualTo(1)
    }

    @Test
    fun `a later background flush with genuinely new time is not skipped`() = runTest {
        clock = at("2026-09-03T10:00:00")
        val api = FakeApi()
        val coordinator = build(api)

        coordinator.onForeground()
        runCurrent()
        clock = at("2026-09-03T10:00:30")
        coordinator.onBackground()
        runCurrent()
        assertThat(api.reportCalls).isEqualTo(1)

        clock = at("2026-09-03T10:05:00")
        coordinator.onForeground()
        runCurrent()
        clock = at("2026-09-03T10:06:00")
        coordinator.onBackground()
        runCurrent()

        assertThat(api.reportCalls).isEqualTo(2)
        assertThat(api.lastReport?.foregroundSecs).isEqualTo(90L)
    }

    private companion object {
        const val FLUSH_INTERVAL_MILLIS = 5 * 60 * 1000L
        const val SECONDS_PER_FLUSH_INTERVAL = 300L
    }
}
