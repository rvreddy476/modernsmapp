package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Test

/**
 * Watch later and the dislike, optimistically: what the control shows the
 * instant it is tapped, what it shows when the server refuses, and what a
 * video shows when it is opened again.
 *
 * The fake keeps the server's truth and answers each call when the test
 * releases it, so a test can look at the state WHILE a write is on the wire,
 * which is where an optimistic store is right or wrong.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class VideoLibraryStoreTest {

    private val post = "p1"

    private class FakeServer : VideoLibraryWrites {
        val queued = mutableSetOf<String>()
        val disliked = mutableSetOf<String>()

        /** Every call in the order it reached the server. */
        val calls = mutableListOf<String>()

        /** Fail the next call instead of applying it. */
        var failNext: AppError? = null

        /** When set, each call waits here until the test completes it. */
        var gate: CompletableDeferred<Unit>? = null

        override suspend fun setWatchLater(postId: String, queued: Boolean): AppResult<Unit> =
            call("watch-later:$postId:$queued") { if (queued) this.queued += postId else this.queued -= postId }

        override suspend fun setDisliked(postId: String, disliked: Boolean): AppResult<Unit> =
            call("tune:$postId:$disliked") { if (disliked) this.disliked += postId else this.disliked -= postId }

        private suspend fun call(name: String, apply: () -> Unit): AppResult<Unit> {
            calls += name
            gate?.await()
            failNext?.let { error ->
                failNext = null
                return AppResult.Failure(error)
            }
            apply()
            return AppResult.Success(Unit)
        }
    }

    private fun shown(store: VideoLibraryStore, server: Boolean = false) = store.state.value.queuedOr(post, server)

    // ── The tap ─────────────────────────────────────────────────────────

    @Test
    fun `adding to watch later shows at once and reaches the server`() = runTest {
        val server = FakeServer().apply { gate = CompletableDeferred() }
        val store = VideoLibraryStore(server)

        backgroundScope.launch { store.setQueued(post, queued = true, serverQueued = false) }
        runCurrent()

        // Shown before the server has answered.
        assertThat(shown(store)).isTrue()
        assertThat(server.queued).isEmpty()

        server.gate!!.complete(Unit)
        runCurrent()

        assertThat(shown(store)).isTrue()
        assertThat(server.queued).containsExactly(post)
        assertThat(server.calls).containsExactly("watch-later:p1:true")
    }

    @Test
    fun `a second tap takes it out again, with a DELETE`() = runTest {
        val server = FakeServer()
        val store = VideoLibraryStore(server)

        store.setQueued(post, queued = true, serverQueued = false)
        store.setQueued(post, queued = false, serverQueued = false)

        assertThat(shown(store)).isFalse()
        assertThat(server.queued).isEmpty()
        assertThat(server.calls).containsExactly("watch-later:p1:true", "watch-later:p1:false").inOrder()
    }

    // ── The refusal ─────────────────────────────────────────────────────

    @Test
    fun `a refused add puts the control back to what the row said and answers a failure`() = runTest {
        val server = FakeServer().apply { failNext = AppError.NoNetwork() }
        val store = VideoLibraryStore(server)

        val result = store.setQueued(post, queued = true, serverQueued = false)

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
        assertThat(shown(store)).isFalse()
        assertThat(server.queued).isEmpty()
    }

    /** The row said "in Watch later"; removing it failed; it is still in. */
    @Test
    fun `a refused remove puts the control back to in watch later`() = runTest {
        val server = FakeServer().apply {
            queued += "p1"
            failNext = AppError.Timeout()
        }
        val store = VideoLibraryStore(server)

        val result = store.setQueued(post, queued = false, serverQueued = true)

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
        assertThat(shown(store, server = true)).isTrue()
    }

    /**
     * Added (acknowledged), then a remove is refused: the control goes back
     * to the value the SERVER last acknowledged, not to what the stale row
     * said before either tap.
     */
    @Test
    fun `a refusal rolls back to the last value the server acknowledged, not to the row`() = runTest {
        val server = FakeServer()
        val store = VideoLibraryStore(server)
        store.setQueued(post, queued = true, serverQueued = false)

        server.failNext = AppError.NoNetwork()
        store.setQueued(post, queued = false, serverQueued = false)

        assertThat(shown(store)).isTrue()
        assertThat(server.queued).containsExactly(post)
    }

    // ── Order ───────────────────────────────────────────────────────────

    /** Add then remove, tapped faster than the server answers: sent in that order, never both at once. */
    @Test
    fun `two quick taps are written one after the other, in the order they were made`() = runTest {
        val gate = CompletableDeferred<Unit>()
        val server = FakeServer().apply { this.gate = gate }
        val store = VideoLibraryStore(server)

        backgroundScope.launch { store.setQueued(post, queued = true, serverQueued = false) }
        backgroundScope.launch { store.setQueued(post, queued = false, serverQueued = false) }
        runCurrent()

        // The second write has not been sent while the first is on the wire.
        assertThat(server.calls).containsExactly("watch-later:p1:true")
        // The control already shows the latest wish.
        assertThat(shown(store)).isFalse()

        gate.complete(Unit)
        runCurrent()

        assertThat(server.calls).containsExactly("watch-later:p1:true", "watch-later:p1:false").inOrder()
        assertThat(server.queued).isEmpty()
        assertThat(shown(store)).isFalse()
    }

    // ── Reopening ───────────────────────────────────────────────────────

    /** The video is opened again from a list row that still says "not queued": the session's value wins. */
    @Test
    fun `a video put in watch later still shows it when reopened from a stale row`() = runTest {
        val store = VideoLibraryStore(FakeServer())
        store.setQueued(post, queued = true, serverQueued = false)

        assertThat(store.state.value.queuedOr(post, server = false)).isTrue()
        // Another video is untouched and reads its own row.
        assertThat(store.state.value.queuedOr("other", server = false)).isFalse()
        assertThat(store.state.value.queuedOr("other", server = true)).isTrue()
    }

    /** The post detail read on open is the truth for a video nothing is being written to. */
    @Test
    fun `the post detail's values are adopted on open`() = runTest {
        val store = VideoLibraryStore(FakeServer())

        store.adopt(post, serverQueued = true, serverDisliked = true)

        assertThat(store.state.value.queuedOr(post, server = false)).isTrue()
        assertThat(store.state.value.dislikedOr(post, server = false)).isTrue()

        // Changed on another device since: the next detail says so, and wins.
        store.adopt(post, serverQueued = false, serverDisliked = false)

        assertThat(store.state.value.queuedOr(post, server = true)).isFalse()
        assertThat(store.state.value.dislikedOr(post, server = true)).isFalse()
    }

    /**
     * A detail that was read BEFORE the write landed does not describe it:
     * adopting it would flick the control back to the pre-tap value.
     */
    @Test
    fun `a detail that arrives while a write is on the wire does not undo the tap`() = runTest {
        val gate = CompletableDeferred<Unit>()
        val server = FakeServer().apply { this.gate = gate }
        val store = VideoLibraryStore(server)
        backgroundScope.launch { store.setQueued(post, queued = true, serverQueued = false) }
        runCurrent()

        store.adopt(post, serverQueued = false, serverDisliked = true)

        assertThat(store.state.value.queuedOr(post, server = false)).isTrue()
        // The lane nothing is being written to IS adopted.
        assertThat(store.state.value.dislikedOr(post, server = false)).isTrue()

        gate.complete(Unit)
        runCurrent()
        assertThat(store.state.value.queuedOr(post, server = false)).isTrue()
    }

    // ── Dislike, the same rules ─────────────────────────────────────────

    @Test
    fun `a dislike shows at once, reaches the server, and a refusal puts it back`() = runTest {
        val server = FakeServer()
        val store = VideoLibraryStore(server)

        store.setDisliked(post, disliked = true, serverDisliked = false)
        assertThat(store.state.value.dislikedOr(post, server = false)).isTrue()
        assertThat(server.disliked).containsExactly(post)

        server.failNext = AppError.NoNetwork()
        val result = store.setDisliked(post, disliked = false, serverDisliked = false)

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
        assertThat(store.state.value.dislikedOr(post, server = false)).isTrue()
        // Watch later is its own lane: a dislike says nothing about it.
        assertThat(store.state.value.queued).isEmpty()
    }

    // ── Another account ─────────────────────────────────────────────────

    /** The state is private and keyed by post id alone: the next viewer on this process starts clean. */
    @Test
    fun `a different viewer does not inherit the last one's watch later or dislikes`() = runTest {
        val store = VideoLibraryStore(FakeServer())
        store.setViewer("viewer-a")
        store.setQueued(post, queued = true, serverQueued = false)
        store.setDisliked(post, disliked = true, serverDisliked = false)

        store.setViewer("viewer-b")

        assertThat(store.state.value).isEqualTo(VideoLibraryState())

        // The same viewer again is not a change: nothing is cleared by a repeat.
        store.setQueued(post, queued = true, serverQueued = false)
        store.setViewer("viewer-b")
        assertThat(store.state.value.queuedOr(post, server = false)).isTrue()
    }

    /** A write that was out when the account changed does not land on the next viewer. */
    @Test
    fun `a write in flight across a viewer change does not mark the next viewer's video`() = runTest {
        val gate = CompletableDeferred<Unit>()
        val server = FakeServer().apply { this.gate = gate }
        val store = VideoLibraryStore(server)
        store.setViewer("viewer-a")
        backgroundScope.launch { store.setQueued(post, queued = true, serverQueued = false) }
        runCurrent()

        store.setViewer("viewer-b")
        server.failNext = AppError.NoNetwork()
        gate.complete(Unit)
        runCurrent()

        assertThat(store.state.value).isEqualTo(VideoLibraryState())
    }
}
