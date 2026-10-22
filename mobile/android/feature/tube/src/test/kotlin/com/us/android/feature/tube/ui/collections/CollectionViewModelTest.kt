package com.us.android.feature.tube.ui.collections

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.CollectionEdit
import com.us.android.core.feed.data.CollectionVisibility
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.feed.data.VideoCollection
import com.us.android.core.feed.data.VideoCollectionContents
import com.us.android.core.feed.data.VideoCollectionSource
import com.us.android.core.feed.data.VideoLibraryStore
import com.us.android.core.feed.data.VideoLibraryWrites
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import com.us.android.core.network.ApiConfig
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.tube.data.TubeQueue
import io.mockk.every
import io.mockk.mockk
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * One collection's page (2026-10-22): the states, and the writes a list the
 * viewer made allows — remove, reorder (optimistic, rolled back on a
 * refusal), edit and delete (both wait for the server) — and what Liked
 * videos, which only the like writes, does not offer.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class CollectionViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private class FakeSource : VideoCollectionSource {
        var contents: AppResult<VideoCollectionContents> = AppResult.Failure(AppError.NoNetwork())
        var removeAnswer: AppResult<Unit> = AppResult.Success(Unit)
        var moveAnswer: AppResult<List<String>>? = null
        var updateAnswer: AppResult<VideoCollection>? = null
        var deleteAnswer: AppResult<Unit> = AppResult.Success(Unit)
        var gate: CompletableDeferred<Unit>? = null
        val calls = mutableListOf<String>()

        override suspend fun watchLater() = contents.also { calls += "watchLater" }
        override suspend fun liked() = contents.also { calls += "liked" }
        override suspend fun collection(collectionId: String) = contents.also { calls += "collection:$collectionId" }

        override suspend fun removeFromCollection(collectionId: String, postId: String): AppResult<Unit> {
            calls += "remove:$collectionId:$postId"
            gate?.await()
            return removeAnswer
        }

        override suspend fun updateCollection(collectionId: String, edit: CollectionEdit): AppResult<VideoCollection> {
            calls += "update:$collectionId:${edit.title}:${edit.description}:${edit.visibility}"
            gate?.await()
            return requireNotNull(updateAnswer)
        }

        override suspend fun deleteCollection(collectionId: String): AppResult<Unit> {
            calls += "delete:$collectionId"
            return deleteAnswer
        }

        override suspend fun moveInCollection(collectionId: String, postId: String, position: Int): AppResult<List<String>> {
            calls += "move:$collectionId:$postId@$position"
            gate?.await()
            return requireNotNull(moveAnswer)
        }
    }

    private class OkWrites : VideoLibraryWrites {
        override suspend fun setWatchLater(postId: String, queued: Boolean): AppResult<Unit> = AppResult.Success(Unit)
        override suspend fun setDisliked(postId: String, disliked: Boolean): AppResult<Unit> = AppResult.Success(Unit)
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
    private val follows = mockk<FollowGraph> { every { ownId } returns "me" }
    private val source = FakeSource()

    private fun video(id: String) = FeedItem(
        id = id,
        authorId = "a1",
        author = FeedAuthor(id = "a1", displayName = "Clee"),
        text = "",
        visibility = "public",
        feedContentType = "long_video",
        postType = "video",
        createdAt = "",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(likes = 0, comments = 0, reposts = 0, views = 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = false,
    )

    private val own = VideoCollection(
        id = "c1",
        title = "Build logs",
        itemCount = 3,
        isPrivate = true,
        isWatchLater = false,
        description = "Every Friday",
        visibility = CollectionVisibility.PRIVATE,
    )
    private val liked = VideoCollection("lk", "Liked videos", 2, isPrivate = true, isWatchLater = false, isLiked = true)

    private fun ownContents(order: List<String> = listOf("a", "b", "c")) =
        AppResult.Success(VideoCollectionContents(own, listOf(video("a"), video("b"), video("c")), order))

    /** The page's ViewModel, with its content flow collected as the screen would. */
    private fun TestScope.viewModel(id: String = "c1"): CollectionViewModel {
        val vm = CollectionViewModel(
            savedStateHandle = SavedStateHandle(mapOf("collectionId" to id)),
            source = source,
            library = VideoLibraryStore(OkWrites()),
            urlResolver = resolver,
            queue = TubeQueue(),
            follows = follows,
        )
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { vm.content.collect {} }
        return vm
    }

    private fun CollectionViewModel.ready() = content.value as CollectionContent.Ready

    @Test
    fun `a collection the viewer made reads by id and offers edit, reorder and remove`() = runTest {
        source.contents = ownContents()

        val vm = viewModel()

        assertThat(source.calls).containsExactly("collection:c1")
        assertThat(vm.ready().videos.map { it.id }).containsExactly("a", "b", "c").inOrder()
        assertThat(collectionPermissions(vm.ready().collection)).isEqualTo(
            CollectionPermissions(canEdit = true, canReorder = true, canRemove = true),
        )
    }

    @Test
    fun `liked videos reads the liked list and offers nothing to write`() = runTest {
        source.contents = AppResult.Success(VideoCollectionContents(liked, listOf(video("a"), video("b"))))

        val vm = viewModel(id = "liked")
        vm.remove(video("a"))
        vm.move(video("b"), 0)
        vm.openEdit()
        vm.askDelete()

        assertThat(source.calls).containsExactly("liked")
        assertThat(vm.ready().videos.map { it.id }).containsExactly("a", "b").inOrder()
        assertThat(vm.edit.value).isEqualTo(CollectionEditState.CLOSED)
        assertThat(vm.confirmingDelete.value).isFalse()
    }

    @Test
    fun `a list that cannot be read is the error, and Retry reads again`() = runTest {
        val vm = viewModel()
        assertThat(vm.content.value).isInstanceOf(CollectionContent.Failed::class.java)

        source.contents = ownContents()
        vm.retry()

        assertThat(vm.ready().videos).hasSize(3)
    }

    @Test
    fun `an empty collection is ready with no videos`() = runTest {
        source.contents = AppResult.Success(VideoCollectionContents(own, emptyList()))

        assertThat(viewModel().ready().videos).isEmpty()
    }

    @Test
    fun `remove takes the row out at once and a refusal puts it back`() = runTest {
        source.contents = ownContents()
        source.gate = CompletableDeferred()
        source.removeAnswer = AppResult.Failure(AppError.NoNetwork())
        val vm = viewModel()

        vm.remove(video("b"))
        assertThat(vm.ready().videos.map { it.id }).containsExactly("a", "c").inOrder()

        source.gate!!.complete(Unit)
        assertThat(vm.ready().videos.map { it.id }).containsExactly("a", "b", "c").inOrder()
        assertThat(vm.message.value).isNotNull()
    }

    @Test
    fun `a move reorders at once, sends the server position, and keeps the server's order`() = runTest {
        // A row the viewer may not see sits between a and b.
        source.contents = ownContents(order = listOf("a", "h", "b", "c"))
        source.gate = CompletableDeferred()
        source.moveAnswer = AppResult.Success(listOf("b", "a", "h", "c"))
        val vm = viewModel()

        vm.move(video("a"), 1)
        assertThat(vm.ready().videos.map { it.id }).containsExactly("b", "a", "c").inOrder()
        assertThat(source.calls.last()).isEqualTo("move:c1:a@2")

        source.gate!!.complete(Unit)
        assertThat(vm.ready().videos.map { it.id }).containsExactly("b", "a", "c").inOrder()
        assertThat(vm.ready().order).containsExactly("b", "a", "h", "c").inOrder()
    }

    @Test
    fun `a refused move puts the old order back and says so`() = runTest {
        source.contents = ownContents()
        source.moveAnswer = AppResult.Failure(AppError.Server(statusCode = 500, code = "INTERNAL_ERROR"))
        val vm = viewModel()

        vm.move(video("c"), 0)

        assertThat(vm.ready().videos.map { it.id }).containsExactly("a", "b", "c").inOrder()
        assertThat(vm.ready().order).containsExactly("a", "b", "c").inOrder()
        assertThat(vm.message.value).isNotNull()
    }

    @Test
    fun `a move that changes nothing sends nothing`() = runTest {
        source.contents = ownContents()
        val vm = viewModel()

        vm.move(video("a"), 0)

        assertThat(source.calls).containsExactly("collection:c1")
    }

    @Test
    fun `saving the details sends only what changed and shows the server's answer`() = runTest {
        source.contents = ownContents()
        source.updateAnswer = AppResult.Success(own.copy(title = "Friday builds", visibility = CollectionVisibility.PUBLIC))
        val vm = viewModel()

        vm.openEdit()
        vm.save(title = " Friday builds ", description = "Every Friday", visibility = CollectionVisibility.PUBLIC)

        assertThat(source.calls.last()).isEqualTo("update:c1:Friday builds:null:PUBLIC")
        assertThat(vm.edit.value).isEqualTo(CollectionEditState.CLOSED)
        assertThat(vm.ready().collection.title).isEqualTo("Friday builds")
    }

    @Test
    fun `nothing changed closes the sheet without a request`() = runTest {
        source.contents = ownContents()
        val vm = viewModel()

        vm.openEdit()
        vm.save(title = "Build logs", description = "Every Friday", visibility = CollectionVisibility.PRIVATE)

        assertThat(vm.edit.value).isEqualTo(CollectionEditState.CLOSED)
        assertThat(source.calls).containsExactly("collection:c1")
    }

    @Test
    fun `a refused save keeps the sheet open and the old details`() = runTest {
        source.contents = ownContents()
        source.updateAnswer = AppResult.Failure(AppError.NoNetwork())
        val vm = viewModel()

        vm.openEdit()
        vm.save(title = "New", description = "", visibility = CollectionVisibility.PRIVATE)

        assertThat(vm.edit.value).isEqualTo(CollectionEditState.OPEN)
        assertThat(vm.ready().collection.title).isEqualTo("Build logs")
        assertThat(vm.message.value).isNotNull()
    }

    @Test
    fun `delete asks first, then leaves the page once the server agrees`() = runTest {
        source.contents = ownContents()
        val vm = viewModel()

        vm.askDelete()
        assertThat(vm.confirmingDelete.value).isTrue()
        vm.delete()

        assertThat(source.calls.last()).isEqualTo("delete:c1")
        assertThat(vm.deleted.value).isTrue()
    }

    @Test
    fun `a refused delete stays on the page and says so`() = runTest {
        source.contents = ownContents()
        source.deleteAnswer = AppResult.Failure(AppError.Server(statusCode = 500, code = "INTERNAL_ERROR"))
        val vm = viewModel()

        vm.delete()

        assertThat(vm.deleted.value).isFalse()
        assertThat(vm.deleting.value).isFalse()
        assertThat(vm.message.value).isNotNull()
    }

    // ── Pure rules ──────────────────────────────────────────────────────

    @Test
    fun `permissions follow whose list it is`() {
        val watchLater = VideoCollection("wl", "Watch later", 0, isPrivate = true, isWatchLater = true)
        assertThat(collectionPermissions(watchLater)).isEqualTo(CollectionPermissions(false, false, true))
        assertThat(collectionPermissions(liked)).isEqualTo(CollectionPermissions(false, false, false))
        assertThat(collectionPermissions(own)).isEqualTo(CollectionPermissions(true, true, true))
    }

    @Test
    fun `the edit carries a trimmed title that changed, never a blank one`() {
        val same = collectionEditOf(own, "  Build logs ", "Every Friday", CollectionVisibility.PRIVATE)
        assertThat(same.isEmpty).isTrue()

        val blank = collectionEditOf(own, "   ", "Every Friday", CollectionVisibility.PRIVATE)
        assertThat(blank.title).isNull()

        val cleared = collectionEditOf(own, "Build logs", "", CollectionVisibility.UNLISTED)
        assertThat(cleared).isEqualTo(CollectionEdit(description = "", visibility = CollectionVisibility.UNLISTED))
    }

    @Test
    fun `a row is offered only the moves that move it`() {
        assertThat(collectionRowMoves(index = 0, count = 3))
            .containsExactly(CollectionRowMove.DOWN, CollectionRowMove.BOTTOM).inOrder()
        assertThat(collectionRowMoves(index = 1, count = 3))
            .containsExactly(CollectionRowMove.UP, CollectionRowMove.DOWN).inOrder()
        assertThat(collectionRowMoves(index = 2, count = 3))
            .containsExactly(CollectionRowMove.TOP, CollectionRowMove.UP).inOrder()
        assertThat(collectionRowMoves(index = 0, count = 1)).isEmpty()
        assertThat(CollectionRowMove.BOTTOM.target(index = 0, count = 3)).isEqualTo(2)
        assertThat(CollectionRowMove.UP.target(index = 2, count = 3)).isEqualTo(1)
    }

    @Test
    fun `the meta line names the visibility`() {
        assertThat(collectionMeta(3, CollectionVisibility.UNLISTED)).isEqualTo("3 videos · Unlisted")
        assertThat(collectionMeta(1, CollectionVisibility.PUBLIC)).isEqualTo("1 video · Public")
    }
}
