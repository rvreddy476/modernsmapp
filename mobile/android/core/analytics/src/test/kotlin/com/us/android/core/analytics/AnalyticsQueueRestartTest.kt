package com.us.android.core.analytics

import android.content.Context
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import com.us.android.core.database.AnalyticsPendingEventEntity
import com.us.android.core.database.UsDatabase
import com.us.android.core.database.UsDatabaseCallbacks
import com.us.android.core.database.UsDatabaseMigrations
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * The queue survives process death.
 *
 * ## WHY A FILE DATABASE AND NOT AN IN-MEMORY ONE
 *
 * The claim being tested is "a view queued before the process died is still
 * there afterwards". An in-memory Room database cannot show that — it dies with
 * the test — so this writes to a real file, CLOSES the database the way a
 * killed process would, and opens a fresh one over the same file through the
 * production builder (real migration list, real callbacks). That last part
 * matters as much as the persistence: Room validates the on-disk schema against
 * the entities on open, so this is also what proves `MIGRATION_6_7` produced a
 * catalogue Room will actually accept.
 *
 * A view lost here is a view a creator is never paid for, and it would be lost
 * precisely in the situation this exists for: the user swipes away and Android
 * reclaims the process a moment later.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class AnalyticsQueueRestartTest {

    private lateinit var context: Context
    private var database: UsDatabase? = null

    @Before
    fun setUp() {
        context = ApplicationProvider.getApplicationContext()
        context.getDatabasePath(TEST_DB).delete()
    }

    @After
    fun tearDown() {
        database?.close()
        context.getDatabasePath(TEST_DB).delete()
    }

    private fun open(): UsDatabase = Room
        .databaseBuilder(context, UsDatabase::class.java, TEST_DB)
        .addMigrations(*UsDatabaseMigrations.toTypedArray())
        .also { builder -> UsDatabaseCallbacks.all.forEach { builder.addCallback(it) } }
        .build()
        .also { database = it }

    @Test
    fun `a queued event is still queued after the database is reopened`() = runTest {
        val row = row()
        open().analyticsDao().enqueue(row)
        // The process dies here.
        database?.close()
        database = null

        val recovered = open().analyticsDao().oldest(10)

        assertThat(recovered).hasSize(1)
        assertThat(recovered.single().eventId).isEqualTo(row.eventId)
        // The dedup key must survive verbatim — a retry that replayed a
        // DIFFERENT id would be counted as a second view.
        assertThat(recovered.single().payloadJson).isEqualTo(row.payloadJson)
        assertThat(recovered.single().attempts).isEqualTo(0)
    }

    @Test
    fun `the attempt counter survives a restart so retries stay bounded`() = runTest {
        val row = row()
        open().analyticsDao().let {
            it.enqueue(row)
            it.recordAttempt(listOf(row.eventId))
            it.recordAttempt(listOf(row.eventId))
        }
        database?.close()
        database = null

        assertThat(open().analyticsDao().oldest(10).single().attempts).isEqualTo(2)
    }

    /**
     * The unique index is what makes the double-emit races on the player
     * surfaces harmless — `ON_STOP` pausing playback and the composable's
     * `onDispose` can both close the same view.
     */
    @Test
    fun `two play_ends for one view collapse to a single row`() = runTest {
        val dao = open().analyticsDao()
        val first = row()
        dao.enqueue(first)
        dao.enqueue(first.copy(eventId = "event-0000000000000002"))

        assertThat(dao.count()).isEqualTo(1)
        assertThat(dao.oldest(10).single().eventId).isEqualTo(first.eventId)
    }

    /** A device offline for a week must not grow an unbounded table. */
    @Test
    fun `trimming keeps the newest rows and drops the oldest`() = runTest {
        val dao = open().analyticsDao()
        repeat(10) {
            dao.enqueue(
                row(eventId = "event-000000000000000$it", createdAt = it.toLong(), sessionId = session(it)),
            )
        }

        dao.trimTo(4)

        assertThat(dao.count()).isEqualTo(4)
        assertThat(dao.oldest(10).map { it.createdAtMillis }).containsExactly(6L, 7L, 8L, 9L).inOrder()
    }

    @Test
    fun `sign-out clears every queued row`() = runTest {
        val dao = open().analyticsDao()
        repeat(3) { dao.enqueue(row(eventId = "event-000000000000000$it", sessionId = session(it))) }

        dao.clear()

        assertThat(dao.count()).isEqualTo(0)
    }

    private companion object {
        const val TEST_DB = "analytics-restart-test.db"

        /**
         * [sessionId] defaults to a fixed value so two calls collide on the
         * unique index — which is exactly what the collapse test asserts. Tests
         * that need genuinely distinct rows pass their own.
         */
        fun row(
            eventId: String = "event-0000000000000001",
            createdAt: Long = 1_757_000_000_000L,
            sessionId: String = "11111111-1111-4111-8111-111111111111",
        ) = AnalyticsPendingEventEntity(
            eventId = eventId,
            type = AnalyticsEventType.PLAY_END,
            timestamp = "2026-09-07T10:00:00Z",
            payloadJson = """{"content_id":"c","watched_ms_total":9000}""",
            sessionId = sessionId,
            contentId = "22222222-2222-4222-8222-222222222222",
            dedupeKey = "end",
            createdAtMillis = createdAt,
        )

        /** A distinct playback session per index, so rows do not collapse. */
        fun session(index: Int): String = "1111111$index-1111-4111-8111-111111111111"
    }
}
