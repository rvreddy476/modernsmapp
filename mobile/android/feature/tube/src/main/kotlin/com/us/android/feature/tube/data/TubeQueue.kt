package com.us.android.feature.tube.data

import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.model.FeedItem
import com.us.android.core.model.SessionState
import javax.inject.Inject
import javax.inject.Provider
import javax.inject.Singleton

/**
 * The videos the watch screen plays through: the page a Tube list had
 * loaded when a card was tapped, in its order.
 *
 * "Up next" is the rest of THAT list, so the watch screen needs the list,
 * and a route argument is the wrong place for a page of hydrated posts. A
 * list writes it as the viewer leaves for a video; the watch screen reads
 * it once, as it opens ([listFor]). The same holder shape as `ReelsEntry`,
 * scoped to this feature.
 *
 * ## WHOSE LIST IT IS (audit H2, 2026-11-07)
 *
 * The rows can be private — History, Watch later, Liked videos, a private
 * collection — and this holder outlives the screens that wrote it. It used
 * to outlive the ACCOUNT too: after a sign-out and a sign-in as someone
 * else, a video opened from a notification offered the first person's
 * History as "Up next". So a list is kept with the viewer who wrote it and
 * is handed only to that viewer; sign-out empties it ([TubeTeardown]); and
 * a watch screen takes it only when the video it opens is in it, so a
 * video opened from somewhere that wrote no list (a notification, a push
 * link, a profile) does not inherit whatever list was written last.
 */
@Singleton
class TubeQueue @Inject constructor(
    // A Provider: sign-out runs [TubeTeardown], which holds this queue, and
    // the session provider is the auth repository that runs the teardowns.
    private val session: Provider<SessionStateProvider>,
) {

    private class Written(val viewerId: String, val items: List<FeedItem>)

    @Volatile
    private var written: Written? = null

    /** The list the next watch screen plays through, written by the signed-in viewer. */
    fun set(items: List<FeedItem>) {
        val viewer = viewerId()
        written = if (viewer.isBlank()) null else Written(viewer, items)
    }

    /**
     * The list for a watch screen opening [postId]: the one written last,
     * when the signed-in viewer wrote it and [postId] is in it; else empty.
     */
    fun listFor(postId: String): List<FeedItem> {
        val list = written ?: return emptyList()
        val viewer = viewerId()
        if (viewer.isBlank() || list.viewerId != viewer) return emptyList()
        return if (list.items.any { it.id == postId }) list.items else emptyList()
    }

    /** Forget the list: the viewer signed out. */
    fun clear() {
        written = null
    }

    private fun viewerId(): String =
        (session.get().sessionState.value as? SessionState.Authenticated)?.userId.orEmpty()
}
