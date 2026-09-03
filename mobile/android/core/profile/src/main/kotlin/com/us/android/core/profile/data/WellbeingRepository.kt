package com.us.android.core.profile.data

import com.us.android.core.common.di.ApplicationScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.common.result.onSuccess
import com.us.android.core.datastore.SettingsDataStore
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.profile.data.dto.ScreenTimeDayDto
import com.us.android.core.profile.data.dto.ScreenTimeReportRequest
import com.us.android.core.profile.data.dto.ScreenTimeWeekDto
import com.us.android.core.profile.data.dto.UpdateWellbeingRequest
import com.us.android.core.profile.data.dto.WellbeingDto
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonPrimitive
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The last server-confirmed [WellbeingGuardSnapshot], kept locally so
 * [com.us.android.screentime.ScreenTimeGuardCoordinator] has a value on a
 * cold start, before its first per-session fetch answers. An interface so
 * [WellbeingRepository] is testable without a DataStore.
 */
interface WellbeingGuardCache {
    suspend fun read(): WellbeingGuardSnapshot?
    suspend fun write(snapshot: WellbeingGuardSnapshot)
}

class DataStoreWellbeingGuardCache @Inject constructor(
    private val dataStore: SettingsDataStore,
) : WellbeingGuardCache {
    override suspend fun read(): WellbeingGuardSnapshot? =
        dataStore.wellbeingGuardCache.first()?.let(WellbeingGuardCacheCodec::decode)

    override suspend fun write(snapshot: WellbeingGuardSnapshot) {
        dataStore.setWellbeingGuardCache(WellbeingGuardCacheCodec.encode(snapshot))
    }
}

/** `dailyLimitMins|bedtimeStart|bedtimeEnd`; an empty segment means null. Tolerant of a corrupt entry. */
object WellbeingGuardCacheCodec {
    fun encode(snapshot: WellbeingGuardSnapshot): String = listOf(
        snapshot.dailyLimitMins?.toString().orEmpty(),
        snapshot.bedtimeStart.orEmpty(),
        snapshot.bedtimeEnd.orEmpty(),
    ).joinToString(SEPARATOR)

    fun decode(encoded: String): WellbeingGuardSnapshot? {
        val parts = encoded.split(SEPARATOR)
        if (parts.size != FIELD_COUNT) return null
        return WellbeingGuardSnapshot(
            dailyLimitMins = parts[0].toIntOrNull(),
            bedtimeStart = parts[1].ifEmpty { null },
            bedtimeEnd = parts[2].ifEmpty { null },
        )
    }

    private const val SEPARATOR = "|"
    private const val FIELD_COUNT = 3
}

/**
 * Screen-time controls and the usage ledger, both owned by user-service.
 *
 * The server is authoritative for the limit and sleep window; the client only
 * reports foreground seconds per local date ([reportScreenTime]) and reads
 * back the week it has assembled.
 *
 * Also the one holder of [guardSnapshot] — the daily-limit / sleep-window pair
 * [com.us.android.screentime.ScreenTimeGuardCoordinator] resolves against.
 * Both [settings] and [save] adopt their answer into it, so a save on the
 * Screen time screen is visible to the guard immediately, without either side
 * polling the other. Seeded from [cache] at construction so a cold start has
 * a value before the first fetch of a session answers.
 */
@Singleton
class WellbeingRepository @Inject constructor(
    private val api: WellbeingApi,
    private val errorMapper: ErrorMapper,
    private val cache: WellbeingGuardCache,
    @ApplicationScope scope: CoroutineScope,
) {
    private val _guardSnapshot = MutableStateFlow<WellbeingGuardSnapshot?>(null)

    /** `null` until the first successful fetch/save of this process, or the cold-start cache read completes. */
    val guardSnapshot: StateFlow<WellbeingGuardSnapshot?> = _guardSnapshot.asStateFlow()

    init {
        scope.launch {
            val cached = cache.read() ?: return@launch
            // Never overwrites a value a real fetch already produced: the
            // cache read is async and must not clobber a fresher answer that
            // landed first.
            _guardSnapshot.update { current -> current ?: cached }
        }
    }

    suspend fun settings(): AppResult<WellbeingSettings> =
        apiCall(errorMapper) { api.wellbeing() }.map { it.toDomain() }.onSuccess { adoptGuardSnapshot(it) }

    suspend fun save(value: WellbeingSettings): AppResult<WellbeingSettings> =
        apiCall(errorMapper) { api.updateWellbeing(value.toRequest()) }
            .map { it.toDomain() }
            .onSuccess { adoptGuardSnapshot(it) }

    private suspend fun adoptGuardSnapshot(settings: WellbeingSettings) {
        val snapshot = settings.toGuardSnapshot()
        _guardSnapshot.value = snapshot
        cache.write(snapshot)
    }

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
