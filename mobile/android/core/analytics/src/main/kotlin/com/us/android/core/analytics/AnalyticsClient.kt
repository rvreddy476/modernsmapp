package com.us.android.core.analytics

import com.us.android.core.analytics.data.AnalyticsStore
import com.us.android.core.analytics.data.AnalyticsUploadScheduler
import com.us.android.core.common.di.ApplicationScope
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import javax.inject.Inject
import javax.inject.Singleton

/**
 * What records an event.
 *
 * An interface so [VideoWatchTracker] — which owns every watch-accounting rule
 * worth testing — can be exercised without a database, a network or
 * WorkManager. Same seam as `OutboxScheduler` and `PlayerFactory` elsewhere
 * here: the collaborator that needs Android is the one behind the interface.
 */
interface AnalyticsRecorder {
    fun record(event: AnalyticsEvent?): Job?

    suspend fun recordNow(event: AnalyticsEvent?)

    /** like / comment_create / share / save / follow_from_content. */
    fun recordEngagement(type: String, session: WatchSession)

    /** not_interested / report / block_creator. */
    fun recordNegativeSignal(
        type: String,
        session: WatchSession,
        reason: NegativeSignalReason = NegativeSignalReason.UNSPECIFIED,
    )

    fun flush()
}

/**
 * Records nothing.
 *
 * The same escape hatch `NoOpTelemetry` provides next door, and for the same
 * reason: a test or a preview that is not about analytics should not have to
 * assemble a database, an API and WorkManager to construct the thing it IS
 * about. Nothing here reaches disk or the network.
 */
object NoOpAnalyticsRecorder : AnalyticsRecorder {
    override fun record(event: AnalyticsEvent?): Job? = null
    override suspend fun recordNow(event: AnalyticsEvent?) = Unit
    override fun recordEngagement(type: String, session: WatchSession) = Unit
    override fun recordNegativeSignal(
        type: String,
        session: WatchSession,
        reason: NegativeSignalReason,
    ) = Unit

    override fun flush() = Unit
}

/**
 * What the rest of the app talks to.
 *
 * ## FIRE AND FORGET, ALWAYS
 *
 * Every method returns immediately and does its work on [ApplicationScope].
 * Nothing here can block a player, a tap handler or a recomposition, and
 * nothing here can fail in a way the person using the app will ever see. That
 * is not politeness — an analytics call that can throw on the playback path is
 * an analytics call that can break playback, and views are worth much less than
 * the video actually playing.
 *
 * The scope is the application's rather than a screen's on purpose: `play_end`
 * is emitted at exactly the moment a screen is going away, so a scope tied to
 * that screen would cancel the write that matters most.
 */
@Singleton
class AnalyticsClient @Inject constructor(
    private val store: AnalyticsStore,
    private val scheduler: AnalyticsUploadScheduler,
    @ApplicationScope private val scope: CoroutineScope,
) : AnalyticsRecorder {

    /**
     * Queues one event.
     *
     * A null is the normal way [AnalyticsEvents] reports "the server would have
     * rejected this", so it is accepted and ignored rather than being an error
     * the caller has to handle.
     */
    override fun record(event: AnalyticsEvent?): Job? {
        if (event == null) return null
        return scope.launch { recordNow(event) }
    }

    /**
     * [record] for callers that are already in a coroutine and need the write
     * to have HAPPENED before they continue.
     *
     * Sign-out is the case that needs it: teardown flushes while the token is
     * still valid, so a `play_end` that is merely *scheduled* at that moment is
     * a view that is wiped before it is ever sent.
     */
    override suspend fun recordNow(event: AnalyticsEvent?) {
        if (event == null) return
        store.enqueue(event)
        if (store.queueSize() >= FLUSH_THRESHOLD) scheduler.scheduleUpload()
    }

    /** like / comment_create / share / save / follow_from_content. */
    override fun recordEngagement(type: String, session: WatchSession) {
        record(AnalyticsEvents.engagement(type, session, System.currentTimeMillis()))
    }

    /** not_interested / report / block_creator. */
    override fun recordNegativeSignal(
        type: String,
        session: WatchSession,
        reason: NegativeSignalReason,
    ) {
        record(AnalyticsEvents.negativeSignal(type, session, reason, System.currentTimeMillis()))
    }

    /**
     * Sends what is queued.
     *
     * Called on app background and on a foreground cadence. Goes through
     * WorkManager rather than straight to [AnalyticsStore.drain] so that a
     * process killed seconds after backgrounding still delivers: the job
     * survives the process, an in-flight coroutine does not.
     */
    override fun flush() {
        scheduler.scheduleUpload()
    }

    companion object {
        /**
         * Queue depth that triggers an upload without waiting for the cadence.
         *
         * Well under [AnalyticsStore.MAX_BATCH] so a burst of activity is sent
         * as one request rather than accumulating into several.
         */
        const val FLUSH_THRESHOLD = 50
    }
}
