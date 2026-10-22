package com.us.android.feature.tube.ui.history

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.datastore.WatchHistoryPauseStore
import com.us.android.core.feed.data.WatchHistoryEntry
import com.us.android.core.feed.data.WatchHistorySource
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import com.us.android.core.network.ApiConfig
import com.us.android.core.network.Paged
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.tube.data.TubeQueue
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/**
 * History (2026-10-22): its states — loading, empty, error with retry,
 * success, paging — and its optimistic writes, Remove and Clear all, each
 * rolled back when the server refuses. Pause recording is the device
 * preference; search filters what is loaded.
 */
class HistoryViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private class FakePause : WatchHistoryPauseStore {
        override val paused = MutableStateFlow(false)
        override suspend fun setPaused(paused: Boolean) {
            this.paused.value = paused
        }
    }

    /** Pages from a queue; a write answers what the test set, after [gate] when one is given. */
    private class FakeSource : WatchHistorySource {
        val pages = ArrayDeque<AppResult<Paged<WatchHistoryEntry>>>()
        val cursors = mutableListOf<String?>()
        var removeAnswer: AppResult<Unit> = AppResult.Success(Unit)
        var clearAnswer: AppResult<Unit> = AppResult.Success(Unit)
        var gate: CompletableDeferred<Unit>? = null
        val removed = mutableListOf<String>()
        var clears = 0

        override suspend fun page(cursor: String?): AppResult<Paged<WatchHistoryEntry>> {
            cursors += cursor
            return pages.removeFirstOrNull() ?: AppResult.Failure(AppError.NoNetwork())
        }

        override suspend fun remove(postId: String): AppResult<Unit> {
            removed += postId
            gate?.await()
            return removeAnswer
        }

        override suspend fun clear(): AppResult<Unit> {
            clears++
            gate?.await()
            return clearAnswer
        }
    }

    private val resolver = MediaUrlResolver(
        ApiConfig(
            baseUrl = "http://127.0.0.1:8080",
            wsBaseUrl = "ws://127.0.0.1:8093",
            clientVersion = "test",
            environment = "test",
            isDebug = true,
        ),
    )

    private fun entry(id: String, title: String = "Video $id") = WatchHistoryEntry(
        item = FeedItem(
            id = id,
            authorId = "a1",
            author = FeedAuthor(id = "a1", displayName = "Clee"),
            text = "",
            title = title,
            visibility = "public",
            feedContentType = "long_video",
            postType = "video",
            createdAt = "",
            isPinned = false,
            media = emptyList(),
            counts = FeedCounts(likes = 0, comments = 0, reposts = 0, views = 0),
            viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
            isRepostable = false,
        ),
        positionMs = 60_000,
        durationMs = 600_000,
        completed = false,
        watchedAt = "2026-10-21T10:00:00Z",
    )

    private fun page(vararg ids: String, next: String? = null): AppResult<Paged<WatchHistoryEntry>> =
        AppResult.Success(Paged(ids.map { entry(it) }, next))

    private val source = FakeSource()
    private val pause = FakePause()
    private val queue = TubeQueue()

    private fun viewModel() = HistoryViewModel(source, pause, resolver, queue)

    @Test
    fun `a first read lists the rows most recent first`() = runTest {
        source.pages += page("a", "b", next = "c2")

        val state = viewModel().state.value

        assertThat(state.loading).isFalse()
        assertThat(state.error).isNull()
        assertThat(state.rows.map { it.postId }).containsExactly("a", "b").inOrder()
        assertThat(state.canLoadMore).isTrue()
        assertThat(source.cursors).containsExactly(null)
    }

    @Test
    fun `nothing watched is the empty state, not an error`() = runTest {
        source.pages += page()

        val state = viewModel().state.value

        assertThat(state.isEmpty).isTrue()
        assertThat(state.error).isNull()
    }

    @Test
    fun `a failed first read is the error, and Retry reads again`() = runTest {
        val vm = viewModel()
        assertThat(vm.state.value.error).isNotNull()
        assertThat(vm.state.value.isEmpty).isFalse()

        source.pages += page("a")
        vm.retry()

        assertThat(vm.state.value.error).isNull()
        assertThat(vm.state.value.rows.map { it.postId }).containsExactly("a")
    }

    @Test
    fun `a failed refresh keeps the rows already on screen`() = runTest {
        source.pages += page("a")
        val vm = viewModel()

        vm.refresh()

        assertThat(vm.state.value.rows.map { it.postId }).containsExactly("a")
        assertThat(vm.state.value.error).isNull()
        assertThat(vm.state.value.refreshing).isFalse()
        assertThat(vm.message.value).isNotNull()
    }

    @Test
    fun `the next page sends the cursor and appends, without repeating a row`() = runTest {
        source.pages += page("a", "b", next = "c2")
        source.pages += page("b", "c")
        val vm = viewModel()

        vm.loadMore()

        assertThat(source.cursors).containsExactly(null, "c2").inOrder()
        assertThat(vm.state.value.rows.map { it.postId }).containsExactly("a", "b", "c").inOrder()
        assertThat(vm.state.value.canLoadMore).isFalse()
    }

    @Test
    fun `a failed next page offers Try again and keeps the rows`() = runTest {
        source.pages += page("a", next = "c2")
        val vm = viewModel()

        vm.loadMore()

        assertThat(vm.state.value.appendFailed).isTrue()
        assertThat(vm.state.value.rows.map { it.postId }).containsExactly("a")
    }

    @Test
    fun `remove takes the row out at once`() = runTest {
        source.pages += page("a", "b", "c")
        source.gate = CompletableDeferred()
        val vm = viewModel()

        vm.remove(vm.state.value.rows[1])

        assertThat(vm.state.value.rows.map { it.postId }).containsExactly("a", "c").inOrder()
        source.gate!!.complete(Unit)
        assertThat(vm.state.value.rows.map { it.postId }).containsExactly("a", "c").inOrder()
        assertThat(source.removed).containsExactly("b")
    }

    @Test
    fun `a refused remove puts the row back where it was and says so`() = runTest {
        source.pages += page("a", "b", "c")
        source.removeAnswer = AppResult.Failure(AppError.Server(statusCode = 500, code = "INTERNAL_ERROR"))
        val vm = viewModel()

        vm.remove(vm.state.value.rows[1])

        assertThat(vm.state.value.rows.map { it.postId }).containsExactly("a", "b", "c").inOrder()
        assertThat(vm.message.value).isNotNull()
    }

    /** Already gone on the server is gone: no row comes back. */
    @Test
    fun `a remove answered not found stays removed`() = runTest {
        source.pages += page("a", "b")
        source.removeAnswer = AppResult.Failure(AppError.NotFound())
        val vm = viewModel()

        vm.remove(vm.state.value.rows[0])

        assertThat(vm.state.value.rows.map { it.postId }).containsExactly("b")
        assertThat(vm.message.value).isNull()
    }

    @Test
    fun `clear all asks first, then empties the page at once`() = runTest {
        source.pages += page("a", "b", next = "c2")
        source.gate = CompletableDeferred()
        val vm = viewModel()

        vm.askClear()
        assertThat(vm.state.value.confirmingClear).isTrue()
        vm.clearAll()

        assertThat(vm.state.value.confirmingClear).isFalse()
        assertThat(vm.state.value.rows).isEmpty()
        assertThat(vm.state.value.canLoadMore).isFalse()
        source.gate!!.complete(Unit)
        assertThat(vm.state.value.isEmpty).isTrue()
        assertThat(source.clears).isEqualTo(1)
    }

    @Test
    fun `a refused clear brings every row back`() = runTest {
        source.pages += page("a", "b", next = "c2")
        source.clearAnswer = AppResult.Failure(AppError.NoNetwork())
        val vm = viewModel()

        vm.clearAll()

        assertThat(vm.state.value.rows.map { it.postId }).containsExactly("a", "b").inOrder()
        assertThat(vm.state.value.nextCursor).isEqualTo("c2")
        assertThat(vm.message.value!!.text).contains("offline")
    }

    @Test
    fun `pause recording is the device preference, read and written`() = runTest {
        source.pages += page("a")
        pause.paused.value = true
        val vm = viewModel()
        assertThat(vm.state.value.paused).isTrue()

        vm.setPaused(false)

        assertThat(pause.paused.value).isFalse()
        assertThat(vm.state.value.paused).isFalse()
    }

    @Test
    fun `search filters the loaded rows by title`() = runTest {
        source.pages += AppResult.Success(Paged(listOf(entry("a", "Cooking pasta"), entry("b", "Bike repair")), null))
        val vm = viewModel()

        vm.setQuery("PASTA")

        assertThat(vm.state.value.shown.map { it.postId }).containsExactly("a")
        // The rows themselves are untouched: clearing the query shows them all again.
        vm.setQuery("")
        assertThat(vm.state.value.shown).hasSize(2)
    }

    @Test
    fun `opening a row makes the shown rows the queue, that one first`() = runTest {
        source.pages += page("a", "b", "c")
        val vm = viewModel()

        vm.onOpen(vm.state.value.rows[1])

        assertThat(queue.items.value.map { it.id }).containsExactly("b", "a", "c").inOrder()
    }

    @Test
    fun `the row's meta says how far and when`() {
        val half = entry("a")
        assertThat(historyMeta(half.copy(watchedAt = ""))).isEqualTo("1:00 of 10:00")
        assertThat(historyMeta(half.copy(completed = true, watchedAt = ""))).isEqualTo("Watched")
        assertThat(historyMeta(half.copy(durationMs = 0, watchedAt = ""))).isEmpty()
    }
}
