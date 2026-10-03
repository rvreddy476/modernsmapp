package com.us.android.feature.tube.ui.watch

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.engagement.data.EngagementWrites
import com.us.android.core.feed.data.VideoLibraryStore
import com.us.android.core.feed.data.VideoLibraryWrites
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Test

/**
 * The action row under a long video, end to end below the screen: the tap,
 * the write, what the control shows while the write is out, what it shows
 * and SAYS when the write is refused, and what it shows when the video is
 * opened again.
 *
 * One fake stands in for the server, holding its truth for the four things
 * the row changes, so "the app shows X" can be checked against "the server
 * has X" after every step.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class WatchEngagementTest {

    private class FakeServer : EngagementWrites, VideoLibraryWrites {
        var liked = false
        var saved = false
        var queued = false
        var disliked = false

        /** Every write in the order it reached the server. */
        val calls = mutableListOf<String>()

        /** Calls whose name starts with one of these fail instead of applying. */
        val failing = mutableSetOf<String>()
        var error: AppError = AppError.Server(statusCode = 500, code = "INTERNAL_ERROR")

        /** When set, every call waits here until the test completes it. */
        var gate: CompletableDeferred<Unit>? = null

        private suspend fun call(name: String, apply: () -> Unit): AppResult<Unit> {
            calls += name
            gate?.await()
            if (failing.any { name.startsWith(it) }) return AppResult.Failure(error)
            apply()
            return AppResult.Success(Unit)
        }

        override suspend fun react(postId: String, reaction: String) = call("POST reactions") { liked = true }
        override suspend fun unreact(postId: String) = call("DELETE reactions") { liked = false }
        override suspend fun setBookmarked(postId: String, bookmarked: Boolean) =
            call(if (bookmarked) "POST bookmark" else "DELETE bookmark") { saved = bookmarked }
        override suspend fun repost(postId: String) = call("POST repost") {}
        override suspend fun removeRepost(postId: String) = call("DELETE repost") {}
        override suspend fun setWatchLater(postId: String, queued: Boolean) =
            call(if (queued) "POST watch-later" else "DELETE watch-later") { this.queued = queued }
        override suspend fun setDisliked(postId: String, disliked: Boolean) =
            call(if (disliked) "POST tune" else "DELETE tune") { this.disliked = disliked }
    }

    private class Rig(val server: FakeServer = FakeServer()) {
        val engagement = EngagementStore(server)
        val library = VideoLibraryStore(server)
        val actions = WatchEngagement(engagement, library)

        /** What the row draws for [item] right now. */
        fun shown(item: FeedItem): WatchViewerState =
            watchViewerState(item, engagement.overlays.value[item.id] ?: EngagementOverlay(), library.state.value)
    }

    private fun video(
        saved: Boolean = false,
        liked: Boolean = false,
        queued: Boolean = false,
        disliked: Boolean = false,
    ) = FeedItem(
        id = "v1",
        authorId = "a1",
        author = FeedAuthor(id = "a1", displayName = "Clee"),
        text = "",
        visibility = "public",
        feedContentType = "long_video",
        postType = "video",
        createdAt = "2026-09-27T10:00:00Z",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(likes = 3, comments = 0, reposts = 0, views = 10),
        viewer = FeedViewerState(
            isBookmarked = saved,
            hasReacted = liked,
            hasReposted = false,
            isQueued = queued,
            hasDisliked = disliked,
        ),
        isRepostable = false,
    )

    // ── Save ────────────────────────────────────────────────────────────

    @Test
    fun `save shows at once, reaches the server, and a second tap removes it`() = runTest {
        val rig = Rig()
        val item = video()
        rig.server.gate = CompletableDeferred()

        backgroundScope.launch { rig.actions.toggleSave(item) }
        runCurrent()
        // Lit before the server has answered.
        assertThat(rig.shown(item).saved).isTrue()
        assertThat(rig.server.saved).isFalse()

        rig.server.gate!!.complete(Unit)
        runCurrent()
        assertThat(rig.shown(item).saved).isTrue()
        assertThat(rig.server.saved).isTrue()

        rig.actions.toggleSave(item)
        assertThat(rig.shown(item).saved).isFalse()
        assertThat(rig.server.saved).isFalse()
        assertThat(rig.server.calls).containsExactly("POST bookmark", "DELETE bookmark").inOrder()
        assertThat(rig.actions.message.value).isNull()
    }

    /** Before 2026-10-02 this rollback was silent on the watch screen: the control went back and nothing was said. */
    @Test
    fun `a refused save puts the control back and says so`() = runTest {
        val rig = Rig()
        val item = video()
        rig.server.failing += "POST bookmark"

        rig.actions.toggleSave(item)

        assertThat(rig.shown(item).saved).isFalse()
        val message = rig.actions.message.value
        assertThat(message?.text).isEqualTo("Couldn't save this video. Try again.")
        assertThat(message?.type).isEqualTo(UsMessageType.Error)
        // Said here, so it is not left on the shared list for the feed's failure bar to show later.
        assertThat(rig.engagement.failures.value).isEmpty()
    }

    @Test
    fun `a refused un-save leaves the video saved and says so`() = runTest {
        val rig = Rig()
        val item = video(saved = true)
        rig.server.saved = true
        rig.server.failing += "DELETE bookmark"

        rig.actions.toggleSave(item)

        assertThat(rig.shown(item).saved).isTrue()
        assertThat(rig.actions.message.value).isNotNull()
    }

    @Test
    fun `offline is said as offline`() = runTest {
        val rig = Rig()
        rig.server.failing += "POST"
        rig.server.error = AppError.NoNetwork()

        rig.actions.toggleSave(video())
        assertThat(rig.actions.message.value?.text).isEqualTo("You're offline. Check your connection and try again.")

        rig.actions.dismissMessage()
        assertThat(rig.actions.message.value).isNull()

        rig.actions.toggleWatchLater(video())
        assertThat(rig.actions.message.value?.text).isEqualTo("You're offline. Check your connection and try again.")
    }

    // ── Watch later ─────────────────────────────────────────────────────

    @Test
    fun `watch later shows at once, reaches the server, and a second tap removes it`() = runTest {
        val rig = Rig()
        val item = video()
        rig.server.gate = CompletableDeferred()

        backgroundScope.launch { rig.actions.toggleWatchLater(item) }
        runCurrent()
        assertThat(rig.shown(item).queued).isTrue()
        assertThat(rig.server.queued).isFalse()

        rig.server.gate!!.complete(Unit)
        runCurrent()
        assertThat(rig.server.queued).isTrue()

        rig.actions.toggleWatchLater(item)
        assertThat(rig.shown(item).queued).isFalse()
        assertThat(rig.server.queued).isFalse()
        assertThat(rig.server.calls).containsExactly("POST watch-later", "DELETE watch-later").inOrder()
        assertThat(rig.actions.message.value).isNull()
    }

    @Test
    fun `a refused watch later puts the control back and says so`() = runTest {
        val rig = Rig()
        val item = video()
        rig.server.failing += "POST watch-later"

        rig.actions.toggleWatchLater(item)

        assertThat(rig.shown(item).queued).isFalse()
        assertThat(rig.actions.message.value?.text).isEqualTo("Couldn't update Watch later. Try again.")
    }

    /** The row says "in Watch later" (the detail's `viewer_queued`): the first tap takes it OUT. */
    @Test
    fun `a video already in watch later is removed by the first tap`() = runTest {
        val rig = Rig()
        val item = video(queued = true)
        rig.server.queued = true

        rig.actions.toggleWatchLater(item)

        assertThat(rig.server.calls).containsExactly("DELETE watch-later")
        assertThat(rig.shown(item).queued).isFalse()
    }

    // ── Reopening ───────────────────────────────────────────────────────

    /**
     * Saved and queued, the screen left, the video opened again from a list
     * row that was loaded BEFORE either tap: the row still says "not saved,
     * not in Watch later", and the screen must not.
     */
    @Test
    fun `a video saved and queued shows both when reopened from a row that predates the taps`() = runTest {
        val rig = Rig()
        val staleRow = video()
        rig.actions.toggleSave(staleRow)
        rig.actions.toggleWatchLater(staleRow)

        // "Reopened": a new screen builds its own WatchEngagement over the same process-wide stores.
        val reopened = WatchEngagement(rig.engagement, rig.library)

        assertThat(rig.shown(staleRow)).isEqualTo(
            WatchViewerState(liked = false, disliked = false, queued = true, saved = true),
        )
        // And the next tap goes the right way: OUT, not in again.
        reopened.toggleWatchLater(staleRow)
        reopened.toggleSave(staleRow)
        assertThat(rig.server.queued).isFalse()
        assertThat(rig.server.saved).isFalse()
    }

    /** A cold start: nothing in the session, so the post detail's flags are what the row shows. */
    @Test
    fun `after a restart the post detail's flags light the controls`() = runTest {
        val rig = Rig()
        val detail = video(saved = true, queued = true, disliked = true)

        rig.actions.adopt(detail)

        assertThat(rig.shown(detail)).isEqualTo(
            WatchViewerState(liked = false, disliked = true, queued = true, saved = true),
        )
        // The list row says nothing about Watch later; after the detail was adopted the row alone shows it.
        assertThat(rig.shown(video()).queued).isTrue()
    }

    /** Un-saved on another device since this session saved it: the detail read on open wins. */
    @Test
    fun `the post detail read on open replaces a settled local value`() = runTest {
        val rig = Rig()
        val row = video()
        rig.actions.toggleSave(row)
        rig.actions.toggleWatchLater(row)

        rig.actions.adopt(video(saved = false, queued = false))

        assertThat(rig.shown(row).saved).isFalse()
        assertThat(rig.shown(row).queued).isFalse()
    }

    // ── Like and Dislike are never lit together ─────────────────────────

    /**
     * The app likes through `/reactions`, which does not clear a dislike on
     * the server, so the app removes the dislike itself.
     */
    @Test
    fun `liking a disliked video removes the dislike`() = runTest {
        val rig = Rig()
        val item = video(disliked = true)
        rig.server.disliked = true

        rig.actions.toggleLike(item)

        assertThat(rig.server.calls).containsExactly("DELETE tune", "POST reactions")
        assertThat(rig.server.liked).isTrue()
        assertThat(rig.server.disliked).isFalse()
        assertThat(rig.shown(item).liked).isTrue()
        assertThat(rig.shown(item).disliked).isFalse()
    }

    /** And a `/reactions` like is not in the place the server looks when a dislike arrives, so the app removes it. */
    @Test
    fun `disliking a liked video removes the like`() = runTest {
        val rig = Rig()
        val item = video(liked = true)
        rig.server.liked = true

        rig.actions.toggleDislike(item)

        assertThat(rig.server.calls).containsExactly("POST tune", "DELETE reactions")
        assertThat(rig.server.liked).isFalse()
        assertThat(rig.server.disliked).isTrue()
        assertThat(rig.shown(item).liked).isFalse()
        assertThat(rig.shown(item).disliked).isTrue()
    }

    /** Both halves move on the tap: the row never shows the two lit while one call waits on the other. */
    @Test
    fun `while both writes are out the row already shows the new pair`() = runTest {
        val rig = Rig()
        val item = video(liked = true)
        rig.server.liked = true
        rig.server.gate = CompletableDeferred()

        backgroundScope.launch { rig.actions.toggleDislike(item) }
        runCurrent()

        assertThat(rig.shown(item).disliked).isTrue()
        assertThat(rig.shown(item).liked).isFalse()
    }

    @Test
    fun `a plain like or dislike touches only its own state`() = runTest {
        val rig = Rig()
        val item = video()

        rig.actions.toggleLike(item)
        assertThat(rig.server.calls).containsExactly("POST reactions")

        val other = Rig()
        other.actions.toggleDislike(item)
        assertThat(other.server.calls).containsExactly("POST tune")
        // Undo: the dislike comes off and the like is not touched.
        other.actions.toggleDislike(item)
        assertThat(other.server.calls).containsExactly("POST tune", "DELETE tune").inOrder()
        assertThat(other.server.liked).isFalse()
    }

    @Test
    fun `a refused dislike puts the control back and says so`() = runTest {
        val rig = Rig()
        val item = video()
        rig.server.failing += "POST tune"

        rig.actions.toggleDislike(item)

        assertThat(rig.shown(item).disliked).isFalse()
        assertThat(rig.actions.message.value?.text).isEqualTo("Couldn't save that. Try again.")
    }

    @Test
    fun `a refused like puts the control back and says so`() = runTest {
        val rig = Rig()
        val item = video()
        rig.server.failing += "POST reactions"

        rig.actions.toggleLike(item)

        assertThat(rig.shown(item).liked).isFalse()
        assertThat(rig.actions.message.value?.text).isEqualTo("Couldn't save your like. Try again.")
        assertThat(rig.engagement.failures.value).isEmpty()
    }
}
