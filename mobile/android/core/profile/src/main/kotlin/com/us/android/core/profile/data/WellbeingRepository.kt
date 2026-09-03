package com.us.android.core.profile.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.profile.data.dto.ScreenTimeDayDto
import com.us.android.core.profile.data.dto.ScreenTimeReportRequest
import com.us.android.core.profile.data.dto.ScreenTimeWeekDto
import com.us.android.core.profile.data.dto.UpdateWellbeingRequest
import com.us.android.core.profile.data.dto.WellbeingDto
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonPrimitive
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Screen-time controls and the usage ledger, both owned by user-service.
 *
 * The server is authoritative for the limit and sleep window; the client only
 * reports foreground seconds per local date ([reportScreenTime]) and reads
 * back the week it has assembled.
 */
@Singleton
class WellbeingRepository @Inject constructor(
    private val api: WellbeingApi,
    private val errorMapper: ErrorMapper,
) {
    suspend fun settings(): AppResult<WellbeingSettings> =
        apiCall(errorMapper) { api.wellbeing() }.map { it.toDomain() }

    suspend fun save(value: WellbeingSettings): AppResult<WellbeingSettings> =
        apiCall(errorMapper) { api.updateWellbeing(value.toRequest()) }.map { it.toDomain() }

    /** Replaces the day's total; the caller sends the whole day each time. */
    suspend fun reportScreenTime(date: String, foregroundSecs: Long, sessions: Int): AppResult<ScreenTimeDay> =
        apiCall(errorMapper) {
            api.reportScreenTime(ScreenTimeReportRequest(date, foregroundSecs, sessions))
        }.map { it.toDomain() }

    suspend fun week(): AppResult<ScreenTimeWeek> =
        apiCall(errorMapper) { api.screenTime(range = "week") }.map { it.toDomain() }
}

private fun WellbeingDto.toDomain() = WellbeingSettings(
    dailyLimitMins = dailyLimitMins?.takeIf { it > 0 },
    bedtimeStart = bedtimeStart?.takeIf { it.isNotBlank() },
    bedtimeEnd = bedtimeEnd?.takeIf { it.isNotBlank() },
    focusModeEnabled = focusModeEnabled,
    nudgeIntervalMins = nudgeIntervalMins,
    hideLikeCounts = hideLikeCounts,
)

private fun WellbeingSettings.toRequest() = UpdateWellbeingRequest(
    dailyLimitMins = dailyLimitMins ?: 0,
    bedtimeStart = bedtimeStart?.let(::JsonPrimitive) ?: JsonNull,
    bedtimeEnd = bedtimeEnd?.let(::JsonPrimitive) ?: JsonNull,
    focusModeEnabled = focusModeEnabled,
    nudgeIntervalMins = nudgeIntervalMins,
    hideLikeCounts = hideLikeCounts,
)

private fun ScreenTimeDayDto.toDomain() = ScreenTimeDay(date, minutes, sessions)

private fun ScreenTimeWeekDto.toDomain() = ScreenTimeWeek(
    days = days.map { it.toDomain() },
    todayMinutes = todayMinutes,
    dailyLimitMins = dailyLimitMins?.takeIf { it > 0 },
)
