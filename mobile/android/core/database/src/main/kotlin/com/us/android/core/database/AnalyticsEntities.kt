package com.us.android.core.database

import androidx.room.Dao
import androidx.room.Entity
import androidx.room.Index
import androidx.room.Insert
import androidx.room.OnConflictStrategy
import androidx.room.PrimaryKey
import androidx.room.Query

/**
 * The durable analytics outbox (analytics client, 2026-09-07).
 *
 * ## WHY THIS IS ON DISK AND NOT IN MEMORY
 *
 * A view the user swiped away from is a view the creator is not paid for.
 * Analytics-service counts views from `play_end`, and `play_end` is emitted at
 * exactly the moment the app is most likely to be killed — the user leaves the
 * reel, backgrounds the app, and Android reclaims the process. An in-memory
 * buffer loses precisely the events that matter most.
 *
 * So the shape is the same one [ChatPendingSendEntity] already uses for sends:
 * a row plus a WorkManager job. The row lands before the request is attempted,
 * and it is deleted only when the server has acknowledged it.
 *
 * ## WHY [eventId] IS THE PRIMARY KEY
 *
 * `event_id` is the server's de-duplication key. A retry MUST replay the same
 * id — analytics-service counts a repeated id as `duplicate` rather than
 * inflating the creator's view count, and a client that minted a fresh uuid per
 * attempt would turn one flaky network into several paid views. Making it the
 * primary key means the id is generated once, at enqueue, and is structurally
 * incapable of changing on the way out.
 *
 * ## WHY THE UNIQUE INDEX ON (sessionId, contentId, type, dedupeKey)
 *
 * It mirrors the server's own receipts constraint
 * (`UNIQUE (actor_id, session_id, content_id, event_type, dedupe_key)`), so a
 * signal the server would collapse anyway never occupies a queue slot or a
 * request. It also makes the double-emit races on the player surfaces harmless:
 * `ON_STOP` pausing playback and the composable's `onDispose` can both end the
 * same view, and only the first insert survives.
 *
 * [dedupeKey] is empty rather than null for the events the server does not
 * collapse (`play_start`, `watch_heartbeat`, `play_end`), because SQLite treats
 * every NULL as distinct in a unique index — a nullable column here would
 * silently disable the constraint for exactly the high-volume events.
 * Heartbeats therefore carry their sequence number, which is what keeps
 * successive beats in one session distinct.
 *
 * No user id column: rows are wiped on sign-out by [AnalyticsDao.clear], and
 * attribution is rebuilt server-side from the gateway actor regardless of
 * anything this client claims.
 */
@Entity(
    tableName = "analytics_pending_event",
    indices = [
        Index(
            value = ["sessionId", "contentId", "type", "dedupeKey"],
            unique = true,
            name = "index_analytics_pending_event_dedupe",
        ),
    ],
)
data class AnalyticsPendingEventEntity(
    /** The server's de-duplication key. Generated once; never regenerated. */
    @PrimaryKey val eventId: String,
    /** One of the thirteen accepted `type` values. */
    val type: String,
    /** RFC-3339 UTC. The server rejects anything older than 24h. */
    val timestamp: String,
    /** The wire `payload` object, already serialized and already validated. */
    val payloadJson: String,
    /** Playback session uuid, or the empty string for engagement events. */
    val sessionId: String,
    val contentId: String,
    /** Narrows the local uniqueness rule the way the server narrows its own. */
    val dedupeKey: String,
    val createdAtMillis: Long,
    /** Upload attempts so far. A row that exhausts them is dropped, silently. */
    val attempts: Int = 0,
)

@Dao
interface AnalyticsDao {

    /**
     * IGNORE, not REPLACE: a duplicate signal must not overwrite the row that
     * already carries the id the server has possibly already seen.
     */
    @Insert(onConflict = OnConflictStrategy.IGNORE)
    suspend fun enqueue(row: AnalyticsPendingEventEntity): Long

    /** Oldest first — watch time is more useful in order, and it bounds age. */
    @Query("SELECT * FROM analytics_pending_event ORDER BY createdAtMillis ASC LIMIT :limit")
    suspend fun oldest(limit: Int): List<AnalyticsPendingEventEntity>

    @Query("SELECT COUNT(*) FROM analytics_pending_event")
    suspend fun count(): Int

    @Query("UPDATE analytics_pending_event SET attempts = attempts + 1 WHERE eventId IN (:eventIds)")
    suspend fun recordAttempt(eventIds: List<String>)

    @Query("DELETE FROM analytics_pending_event WHERE eventId IN (:eventIds)")
    suspend fun delete(eventIds: List<String>)

    /**
     * Drops the rows that have burned through their attempt budget.
     *
     * Telemetry is not worth unbounded retries, and it must never surface an
     * error to the person using the app, so an event the server keeps refusing
     * simply stops existing.
     */
    @Query("DELETE FROM analytics_pending_event WHERE attempts >= :maxAttempts")
    suspend fun dropExhausted(maxAttempts: Int)

    /**
     * Trims the queue to [keep] newest rows.
     *
     * A device that is offline for a week must not accumulate an unbounded
     * table. Oldest events go first: they are the ones most likely to be past
     * the server's 24-hour acceptance window anyway.
     */
    @Query(
        "DELETE FROM analytics_pending_event WHERE eventId NOT IN " +
            "(SELECT eventId FROM analytics_pending_event ORDER BY createdAtMillis DESC LIMIT :keep)",
    )
    suspend fun trimTo(keep: Int)

    /** Sign-out / account switch. */
    @Query("DELETE FROM analytics_pending_event")
    suspend fun clear()
}
