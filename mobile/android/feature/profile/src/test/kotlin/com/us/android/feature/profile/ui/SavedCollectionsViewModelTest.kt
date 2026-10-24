package com.us.android.feature.profile.ui

import androidx.paging.PagingData
import androidx.paging.testing.asSnapshot
import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.engagement.data.EngagementWrites
import com.us.android.core.feed.data.saved.BookmarkedDto
import com.us.android.core.feed.data.saved.CollectionNameRequest
import com.us.android.core.feed.data.saved.CollectionRefRequest
import com.us.android.core.feed.data.saved.DeletedCollectionDto
import com.us.android.core.feed.data.saved.SavedCollection
import com.us.android.core.feed.data.saved.SavedCollectionDto
import com.us.android.core.feed.data.saved.SavedCollectionsApi
import com.us.android.core.feed.data.saved.SavedCollectionsRepository
import com.us.android.core.feed.data.saved.SavedItemDto
import com.us.android.core.feed.data.saved.SavedPostsSource
import com.us.android.core.feed.ui.saved.CollectionOutcome
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiErrorBody
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/**
 * The Saved tab's collections (2026-10-24): which collection's pages are
 * asked for, what a move does to the pages on screen and to the counts, and
 * New / Rename / Delete with their refusals. The fake answers in the
 * handlers' shapes (saved_collections_handler.go:17-43).
 */
class SavedCollectionsViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private class FakeApi : SavedCollectionsApi {
        val calls = mutableListOf<String>()
        var refuse: String? = null
        var rows = mutableListOf(
            SavedCollectionDto(id = null, name = "All Saved", count = 3, isDefault = true),
            SavedCollectionDto(id = "c-1", name = "Recipes", count = 2),
            SavedCollectionDto(id = "c-2", name = "Trips", count = 0),
        )

        private fun <T> answer(call: String, value: () -> T): ApiEnvelope<T> {
            calls += call
            val code = refuse ?: return ApiEnvelope(value())
            refuse = null
            return ApiEnvelope(error = ApiErrorBody(code = code))
        }

        override suspend fun collections() = answer("list") { rows.toList() }
        override suspend fun create(body: CollectionNameRequest) = answer("create:${body.name}") {
            SavedCollectionDto(id = "c-new", name = body.name).also { rows += it }
        }
        override suspend fun rename(collectionId: String, body: CollectionNameRequest) =
            answer("rename:$collectionId:${body.name}") {
                val index = rows.indexOfFirst { it.id == collectionId }
                rows[index] = rows[index].copy(name = body.name)
                rows[index]
            }
        override suspend fun delete(collectionId: String) = answer("delete:$collectionId") {
            // The server's delete: the collection goes, its posts are counted in All Saved.
            val gone = rows.first { it.id == collectionId }
            rows.removeAll { it.id == collectionId }
            rows[0] = rows[0].copy(count = rows[0].count + gone.count)
            DeletedCollectionDto(deleted = true, collectionId = collectionId, movedToDefault = gone.count)
        }
        override suspend fun saveInto(postId: String, body: CollectionRefRequest) =
            answer("save:$postId:${body.collectionId}") { BookmarkedDto(true) }
        override suspend fun move(postId: String, body: CollectionRefRequest) =
            answer("move:$postId:${body.collectionId}") { SavedItemDto(id = "s", targetId = postId, collectionId = body.collectionId) }
    }

    private object NoWrites : EngagementWrites {
        override suspend fun react(postId: String, reaction: String) = AppResult.Success(Unit)
        override suspend fun unreact(postId: String) = AppResult.Success(Unit)
        override suspend fun setBookmarked(postId: String, bookmarked: Boolean) = AppResult.Success(Unit)
        override suspend fun repost(postId: String) = AppResult.Success(Unit)
        override suspend fun removeRepost(postId: String) = AppResult.Success(Unit)
    }

    /** Every collection asked for, and two posts in each. */
    private class FakeSource : SavedPostsSource {
        val asked = mutableListOf<String>()
        override fun savedPosts(collection: String) = flowOf(
            PagingData.from(listOf(post("$collection-a"), post("$collection-b"))),
        ).also { asked += collection }
    }

    private val api = FakeApi()
    private val source = FakeSource()
    private val repository = SavedCollectionsRepository(api, EngagementStore(NoWrites), ErrorMapper(NetworkModule.provideJson()))
    // Lazy: built inside the test body, after the Main dispatcher rule has swapped Main in.
    private val viewModel by lazy { SavedCollectionsViewModel(repository, source) }

    private val state get() = viewModel.state.value

    @Test
    fun `nothing is asked before the own grid activates the tab`() = runTest {
        assertThat(viewModel.posts.snapshot()).isEmpty()
        assertThat(api.calls).isEmpty()
        assertThat(source.asked).isEmpty()
    }

    @Test
    fun `the tab opens on All Saved and reads one collection at a time`() = runTest {
        viewModel.activate()

        assertThat(viewModel.posts.snapshot().map { it.id }).containsExactly("default-a", "default-b").inOrder()
        assertThat(state.collections.map { it.name }).containsExactly("All Saved", "Recipes", "Trips").inOrder()

        viewModel.select("c-1")
        assertThat(viewModel.posts.snapshot().map { it.id }).containsExactly("c-1-a", "c-1-b").inOrder()
        assertThat(source.asked).containsExactly("default", "c-1").inOrder()

        // A collection that is not there is All Saved.
        viewModel.select("c-9")
        assertThat(state.selected).isNull()
        viewModel.activate()
        assertThat(api.calls).containsExactly("list")
    }

    @Test
    fun `a post moved out of the collection on screen leaves it, and the counts follow`() = runTest {
        viewModel.activate()
        viewModel.select("c-1")

        viewModel.onMoved(outcome(postId = "c-1-a", from = "c-1", to = "c-2"))

        assertThat(viewModel.posts.snapshot().map { it.id }).containsExactly("c-1-b")
        assertThat(state.collections.map { it.count }).containsExactly(3L, 1L, 1L).inOrder()
        assertThat(state.notice).isEqualTo("Moved to Trips")

        // Another collection starts clean.
        viewModel.select(null)
        assertThat(viewModel.posts.snapshot().map { it.id }).containsExactly("default-a", "default-b").inOrder()
    }

    @Test
    fun `a post moved into the collection on screen stays`() = runTest {
        viewModel.activate()
        viewModel.select("c-2")

        viewModel.onMoved(outcome(postId = "c-2-a", from = "c-2", to = "c-2"))

        assertThat(viewModel.posts.snapshot().map { it.id }).containsExactly("c-2-a", "c-2-b").inOrder()
        assertThat(state.collections.map { it.count }).containsExactly(3L, 2L, 0L).inOrder()
    }

    @Test
    fun `a collection made in the picker joins the pills`() = runTest {
        viewModel.activate()
        val made = SavedCollection("c-new", "Weekend", 0)

        viewModel.onMoved(outcome(postId = "default-a", from = null, to = "c-new", created = made))

        assertThat(state.collections.last()).isEqualTo(made.copy(count = 1))
        assertThat(state.collections.first().count).isEqualTo(2)
    }

    @Test
    fun `new collection is checked here first, then created and named in the notice`() = runTest {
        viewModel.activate()
        viewModel.startCreate()

        viewModel.changeDialogName("TRIPS")
        viewModel.confirmDialog()
        assertThat((state.dialog as SavedDialog.Create).problem).isEqualTo("You already have a collection with that name.")
        assertThat(api.calls).containsExactly("list")

        viewModel.changeDialogName(" Weekend ")
        viewModel.confirmDialog()
        assertThat(api.calls).contains("create:Weekend")
        assertThat(state.dialog).isNull()
        assertThat(state.collections.map { it.name }).contains("Weekend")
        assertThat(state.notice).isEqualTo("Created Weekend")
    }

    @Test
    fun `rename keeps the collection's place and takes the stored name`() = runTest {
        viewModel.activate()
        viewModel.startRename(SavedCollection("c-1", "Recipes", 2))

        viewModel.changeDialogName("Dinners")
        viewModel.confirmDialog()

        assertThat(api.calls).contains("rename:c-1:Dinners")
        assertThat(state.collections.map { it.name }).containsExactly("All Saved", "Dinners", "Trips").inOrder()
        // All Saved cannot be renamed or deleted.
        viewModel.startRename(state.collections.first())
        viewModel.startDelete(state.collections.first())
        assertThat(state.dialog).isNull()
    }

    @Test
    fun `deleting the collection on screen falls back to All Saved with its posts counted there`() = runTest {
        viewModel.activate()
        viewModel.select("c-1")
        viewModel.startDelete(state.selectedCollection)
        assertThat(deleteQuestion(state.selectedCollection)).isEqualTo("The 2 posts in it move to All Saved. Nothing is unsaved.")

        viewModel.confirmDialog()

        assertThat(api.calls).contains("delete:c-1")
        assertThat(state.selected).isNull()
        assertThat(state.collections.map { it.name }).containsExactly("All Saved", "Trips").inOrder()
        assertThat(state.collections.first().count).isEqualTo(5)
        assertThat(state.notice).isEqualTo("Deleted Recipes. Its 2 posts are in All Saved now.")
    }

    @Test
    fun `a server refusal stays in the open dialog`() = runTest {
        viewModel.activate()
        viewModel.startDelete(SavedCollection("c-2", "Trips", 0))
        api.refuse = "COLLECTION_NOT_FOUND"

        viewModel.confirmDialog()

        val dialog = state.dialog as SavedDialog.ConfirmDelete
        assertThat(dialog.problem).isEqualTo("That collection no longer exists. It may have been deleted on another device.")
        assertThat(state.busy).isFalse()
    }

    @Test
    fun `a save into a collection anywhere reloads the counts`() = runTest {
        viewModel.activate()
        api.rows[1] = api.rows[1].copy(count = 9)

        repository.saveTo("p9", toCollectionId = "c-1", alreadySaved = false)

        assertThat(api.calls).containsExactly("list", "save:p9:c-1", "list").inOrder()
        assertThat(state.collections[1].count).isEqualTo(9)
    }

    @Test
    fun `the copy for a delete says where the posts went`() {
        assertThat(deletedNotice("Trips", 0)).isEqualTo("Deleted Trips")
        assertThat(deletedNotice("Trips", 1)).isEqualTo("Deleted Trips. Its 1 post is in All Saved now.")
        assertThat(deleteQuestion(SavedCollection("c", "x", 0))).isEqualTo("This collection is empty.")
        assertThat(deleteQuestion(SavedCollection("c", "x", 1))).isEqualTo("The 1 post in it moves to All Saved. Nothing is unsaved.")
        assertThat(savedEmptyTitle(SavedTabUiState())).isNull()
        assertThat(savedEmptyTitle(SavedTabUiState(collections = listOf(SavedCollection(null, "All Saved", 0), SavedCollection("c", "Trips", 0)), selected = "c")))
            .isEqualTo("Nothing in Trips yet")
    }

    private fun outcome(postId: String, from: String?, to: String?, created: SavedCollection? = null) = CollectionOutcome(
        postId = postId,
        fromCollectionId = from,
        toCollectionId = to,
        collectionName = created?.name ?: listOf("All Saved", "Recipes", "Trips")[listOf(null, "c-1", "c-2").indexOf(to).coerceAtLeast(0)],
        moved = true,
        created = created,
    )

    private companion object {
        fun post(id: String) = FeedItem(
            id = id,
            authorId = "a",
            author = FeedAuthor(id = "a", displayName = "A"),
            text = "t",
            visibility = "public",
            feedContentType = "post",
            postType = "text",
            createdAt = "2026-10-24T00:00:00Z",
            isPinned = false,
            media = emptyList(),
            counts = FeedCounts(likes = 0, comments = 0, reposts = 0, views = 0),
            viewer = FeedViewerState(isBookmarked = true, hasReacted = false, hasReposted = false),
            isRepostable = true,
        )
    }
}

/**
 * The PagingData on screen now, read once. The ViewModel's flow never
 * completes (the moved-out set is a StateFlow), so the latest emission is
 * taken and that single PagingData is snapshotted.
 */
private suspend fun Flow<PagingData<FeedItem>>.snapshot(): List<FeedItem> = flowOf(first()).asSnapshot()
