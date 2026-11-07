package com.us.android.feature.tube.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.datastore.WatchHistoryPauseStore
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.runTest
import org.junit.Test

/**
 * The watch queue is private to whoever wrote it (audit H2, 2026-11-07):
 * one account's History, Watch later or Liked list must never become the
 * next account's "Up next", and a video opened from somewhere that wrote
 * no list must not inherit the last one.
 */
class TubeQueueTest {

    private val session = SignedIn("ada")
    private val queue = TubeQueue { session }
    private val history = listOf("h1", "h2", "h3").map(::item)

    @Test
    fun `the viewer who wrote the list gets it for a video in it`() {
        queue.set(history)

        assertThat(queue.listFor("h2").map { it.id }).containsExactly("h1", "h2", "h3").inOrder()
    }

    @Test
    fun `another account signed in on the same process gets nothing`() {
        queue.set(history)

        session.signOut()
        assertThat(queue.listFor("h1")).isEmpty()

        session.signInAs("bob")
        assertThat(queue.listFor("h1")).isEmpty()
    }

    @Test
    fun `a video the list does not hold opens with no list`() {
        queue.set(history)

        // A notification, a push link or a profile: none of them wrote a list.
        assertThat(queue.listFor("from-a-notification")).isEmpty()
    }

    @Test
    fun `nothing is kept for a write with nobody signed in`() {
        session.signOut()
        queue.set(history)

        session.signInAs("ada")
        assertThat(queue.listFor("h1")).isEmpty()
    }

    @Test
    fun `sign-out empties the queue and turns History recording back on`() = runTest {
        val pause = Pause(paused = true)
        queue.set(history)

        TubeTeardown(queue, pause).onSignOut()

        assertThat(queue.listFor("h1")).isEmpty()
        assertThat(pause.paused.value).isFalse()
        // The same account signing back in finds nothing of it either.
        assertThat(queue.listFor("h2")).isEmpty()
    }

    private class Pause(paused: Boolean) : WatchHistoryPauseStore {
        override val paused = MutableStateFlow(paused)
        override suspend fun setPaused(paused: Boolean) {
            this.paused.value = paused
        }
    }

    private fun item(id: String) = FeedItem(
        id = id,
        authorId = "u",
        author = FeedAuthor(id = "u", displayName = "Ada"),
        text = "",
        visibility = "private",
        feedContentType = "long_video",
        postType = "video",
        createdAt = "",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(0, 0, 0, 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = false,
    )
}
