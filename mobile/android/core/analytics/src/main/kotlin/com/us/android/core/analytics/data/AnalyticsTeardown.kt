package com.us.android.core.analytics.data

import com.us.android.core.analytics.PlayEndReason
import com.us.android.core.analytics.VideoWatchTracker
import com.us.android.core.common.session.SessionTeardownTask
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Sign-out: close open views, deliver what is owed, then wipe.
 *
 * [SessionTeardownTask] runs while the session is still VALID, which is the
 * only moment a queued view can still be delivered — once the token is gone the
 * gateway sends no actor and analytics-service refuses the batch. So the order
 * matters: end the views that are open (the user may have signed out from a
 * screen that was playing something), drain, and only then clear.
 *
 * The clear is unconditional. Whatever did not make it belongs to the account
 * that is leaving, and carrying it into the next session would attribute one
 * person's watching to another's actor id.
 *
 * Failure is not fatal here by contract — a user who taps "log out" is logged
 * out even with no network — so nothing below throws.
 */
@Singleton
class AnalyticsTeardown @Inject constructor(
    private val tracker: VideoWatchTracker,
    private val store: AnalyticsStore,
    private val scheduler: AnalyticsUploadScheduler,
) : SessionTeardownTask {

    override suspend fun onSignOut() {
        // endAll suspends until every play_end is on disk, so the drain below
        // actually carries them.
        runCatching {
            tracker.endAll(PlayEndReason.BACKGROUNDED)
            store.drain()
        }
        // A job left scheduled would wake up against the next account's token.
        runCatching { scheduler.cancelUpload() }
        runCatching { store.clear() }
    }
}
