package com.us.android.feature.tube.data

import com.us.android.core.common.session.SessionTeardownTask
import com.us.android.core.datastore.WatchHistoryPauseStore
import javax.inject.Inject

/**
 * What Tube leaves behind at sign-out (2026-11-07), so the next person to
 * sign in on this phone starts clean:
 *
 *  - the watch queue (audit H2): it can hold the last viewer's History,
 *    Watch later, Liked videos or a private collection;
 *  - History's "Pause recording" (audit L6): a device switch, so one
 *    person's pause silently stopped the next person's History. Back to
 *    recording, the default, which the History page shows.
 *
 * Local only and idempotent, as the [SessionTeardownTask] contract asks.
 */
class TubeTeardown @Inject constructor(
    private val queue: TubeQueue,
    private val historyPause: WatchHistoryPauseStore,
) : SessionTeardownTask {
    override suspend fun onSignOut() {
        queue.clear()
        historyPause.setPaused(false)
    }
}
