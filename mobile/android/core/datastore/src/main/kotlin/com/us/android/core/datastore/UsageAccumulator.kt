package com.us.android.core.datastore

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId
import javax.inject.Inject
import javax.inject.Singleton

/** Foreground time on one local date that the server has not yet confirmed. */
data class UsageRecord(
    /** `YYYY-MM-DD` in the device's zone. */
    val date: String,
    val foregroundMillis: Long,
    val sessions: Int,
) {
    val foregroundSecs: Long get() = foregroundMillis / MILLIS_PER_SECOND

    private companion object {
        const val MILLIS_PER_SECOND = 1000L
    }
}

/** Persistence seam for [UsageAccumulator]; the production one is [SettingsDataStore]. */
interface UsageStore {
    suspend fun read(): List<UsageRecord>
    suspend fun write(records: List<UsageRecord>)
}

class DataStoreUsageStore @Inject constructor(
    private val dataStore: SettingsDataStore,
) : UsageStore {
    override suspend fun read(): List<UsageRecord> =
        dataStore.usageLedger.first()?.let(UsageLedgerCodec::decode).orEmpty()

    override suspend fun write(records: List<UsageRecord>) =
        dataStore.setUsageLedger(UsageLedgerCodec.encode(records))
}

/** `date|millis|sessions;date|millis|sessions`. Tolerant of a corrupt entry. */
object UsageLedgerCodec {
    fun encode(records: List<UsageRecord>): String = records.joinToString(RECORD_SEPARATOR) {
        listOf(it.date, it.foregroundMillis.toString(), it.sessions.toString()).joinToString(FIELD_SEPARATOR)
    }

    fun decode(encoded: String): List<UsageRecord> = encoded.split(RECORD_SEPARATOR).mapNotNull { entry ->
        val parts = entry.split(FIELD_SEPARATOR)
        if (parts.size != FIELD_COUNT) return@mapNotNull null
        val millis = parts[1].toLongOrNull() ?: return@mapNotNull null
        val sessions = parts[2].toIntOrNull() ?: return@mapNotNull null
        UsageRecord(parts[0], millis, sessions).takeIf { it.date.isNotBlank() }
    }

    private const val RECORD_SEPARATOR = ";"
    private const val FIELD_SEPARATOR = "|"
    private const val FIELD_COUNT = 3
}

/**
 * Counts foreground time per local date.
 *
 * Fed by the application's process-lifecycle observer: [onForeground] opens a
 * session, [onBackground] closes it, and [snapshot] folds whatever has elapsed
 * into the ledger so a sync can report it. Time spanning midnight is split at
 * the boundary — the minutes before belong to yesterday — which is what keeps
 * "today" honest for a user who reads past 12am.
 *
 * Every day the server has not confirmed stays in the ledger until
 * [markFlushed]; today is kept regardless, because the day's total is
 * replaced (not added to) on every report and the count must keep growing.
 *
 * The clock and zone are injectable so the rollover is testable.
 */
@Singleton
class UsageAccumulator @Inject constructor(
    private val store: UsageStore,
) {
    private var zone: ZoneId = ZoneId.systemDefault()
    private var now: () -> Long = System::currentTimeMillis

    /** Test seam: a fixed zone and a controllable clock. */
    constructor(store: UsageStore, zone: ZoneId, now: () -> Long) : this(store) {
        this.zone = zone
        this.now = now
    }

    private val mutex = Mutex()
    private var records: MutableList<UsageRecord>? = null
    private var foregroundSince: Long? = null

    private val _todaySeconds = MutableStateFlow(0L)

    /** Foreground seconds on the current local date, as of the last fold. */
    val todaySeconds: StateFlow<Long> = _todaySeconds.asStateFlow()

    suspend fun onForeground() = mutex.withLock {
        val ledger = loaded()
        val at = now()
        if (foregroundSince == null) {
            foregroundSince = at
            val date = dateOf(at)
            val current = ledger.firstOrNull { it.date == date }
            ledger.replace(current?.copy(sessions = current.sessions + 1) ?: UsageRecord(date, 0L, 1))
        }
        persist(ledger)
    }

    suspend fun onBackground() = mutex.withLock {
        val ledger = loaded()
        fold(ledger, now())
        foregroundSince = null
        persist(ledger)
    }

    /** Every day with something to report, today last, after folding elapsed time. */
    suspend fun snapshot(): List<UsageRecord> = mutex.withLock {
        val ledger = loaded()
        fold(ledger, now())
        persist(ledger)
        ledger.sortedBy { it.date }
    }

    /** Drops a confirmed past day; today always stays so its total keeps growing. */
    suspend fun markFlushed(date: String) = mutex.withLock {
        val ledger = loaded()
        if (date != dateOf(now())) ledger.removeAll { it.date == date }
        persist(ledger)
    }

    private suspend fun loaded(): MutableList<UsageRecord> =
        records ?: store.read().toMutableList().also { records = it }

    /** Attributes the time since the session opened to each date it crossed. */
    private fun fold(ledger: MutableList<UsageRecord>, until: Long) {
        var cursor = foregroundSince ?: return
        while (cursor < until) {
            val date = dateOf(cursor)
            val boundary = startOfNextDay(cursor)
            val end = minOf(boundary, until)
            val current = ledger.firstOrNull { it.date == date }
            ledger.replace(
                current?.copy(foregroundMillis = current.foregroundMillis + (end - cursor))
                    ?: UsageRecord(date, end - cursor, sessions = 1),
            )
            cursor = end
        }
        foregroundSince = until
    }

    private suspend fun persist(ledger: MutableList<UsageRecord>) {
        _todaySeconds.value = ledger.firstOrNull { it.date == dateOf(now()) }?.foregroundSecs ?: 0L
        store.write(ledger)
    }

    private fun MutableList<UsageRecord>.replace(record: UsageRecord) {
        removeAll { it.date == record.date }
        add(record)
    }

    private fun dateOf(millis: Long): String = localDate(millis).toString()

    private fun localDate(millis: Long): LocalDate = Instant.ofEpochMilli(millis).atZone(zone).toLocalDate()

    private fun startOfNextDay(millis: Long): Long =
        localDate(millis).plusDays(1).atStartOfDay(zone).toInstant().toEpochMilli()
}
