package com.us.android.core.feed.ui.saved

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
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiErrorBody
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/** The collection picker: what a pick sends, what it answers, and a refusal kept on the sheet. */
class SaveToCollectionViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    /** Records every call; [refuse] answers the next write with that code. */
    private class FakeApi : SavedCollectionsApi {
        val calls = mutableListOf<String>()
        var refuse: String? = null
        var rows = listOf(
            SavedCollectionDto(id = null, name = "All Saved", count = 3, isDefault = true),
            SavedCollectionDto(id = "c-1", name = "Recipes", count = 1),
        )

        private fun <T> answer(call: String, value: T): ApiEnvelope<T> {
            calls += call
            val code = refuse ?: return ApiEnvelope(value)
            refuse = null
            return ApiEnvelope(error = ApiErrorBody(code = code))
        }

        override suspend fun collections() = ApiEnvelope(rows).also { calls += "list" }
        override suspend fun create(body: CollectionNameRequest) =
            answer("create:${body.name}", SavedCollectionDto(id = "c-new", name = body.name))
        override suspend fun rename(collectionId: String, body: CollectionNameRequest) =
            answer("rename:$collectionId", SavedCollectionDto(id = collectionId, name = body.name))
        override suspend fun delete(collectionId: String) = answer("delete:$collectionId", DeletedCollectionDto(true, collectionId, 0))
        override suspend fun saveInto(postId: String, body: CollectionRefRequest) =
            answer("save:$postId:${body.collectionId}", BookmarkedDto(true))
        override suspend fun move(postId: String, body: CollectionRefRequest) =
            answer("move:$postId:${body.collectionId}", SavedItemDto(id = "s1", targetId = postId, collectionId = body.collectionId))
    }

    private object NoWrites : EngagementWrites {
        override suspend fun react(postId: String, reaction: String) = AppResult.Success(Unit)
        override suspend fun unreact(postId: String) = AppResult.Success(Unit)
        override suspend fun setBookmarked(postId: String, bookmarked: Boolean) = AppResult.Success(Unit)
        override suspend fun repost(postId: String) = AppResult.Success(Unit)
        override suspend fun removeRepost(postId: String) = AppResult.Success(Unit)
    }

    private val api = FakeApi()
    private val engagement = EngagementStore(NoWrites)
    private val viewModel = SaveToCollectionViewModel(
        SavedCollectionsRepository(api, engagement, ErrorMapper(NetworkModule.provideJson())),
    )

    private val recipes = SavedCollection("c-1", "Recipes", 1)
    private val all = SavedCollection(null, "All Saved", 3)

    @Test
    fun `a save into a collection sends it and reports where the post went`() = runTest {
        viewModel.bind(CollectionTarget.Save("p1", alreadySaved = false))

        viewModel.pick(recipes)

        assertThat(api.calls).containsExactly("list", "save:p1:c-1").inOrder()
        val outcome = requireNotNull(viewModel.state.value.outcome)
        assertThat(outcome.message).isEqualTo("Saved to Recipes")
        assertThat(outcome.toCollectionId).isEqualTo("c-1")
        assertThat(engagement.overlayFor("p1").bookmarked).isTrue()
    }

    @Test
    fun `All Saved for a post already saved is the move back`() = runTest {
        viewModel.bind(CollectionTarget.Save("p1", alreadySaved = true))

        viewModel.pick(all)

        assertThat(api.calls.last()).isEqualTo("move:p1:default")
    }

    @Test
    fun `a move names where it came from, and the current collection is not a choice`() = runTest {
        viewModel.bind(CollectionTarget.Move("p1", fromCollectionId = "c-1"))

        viewModel.pick(recipes)
        assertThat(api.calls).containsExactly("list")
        assertThat(viewModel.state.value.isCurrent(recipes)).isTrue()

        viewModel.pick(all)
        assertThat(api.calls.last()).isEqualTo("move:p1:default")
        val outcome = requireNotNull(viewModel.state.value.outcome)
        assertThat(outcome.message).isEqualTo("Moved to All Saved")
        assertThat(outcome.fromCollectionId).isEqualTo("c-1")
        assertThat(outcome.toCollectionId).isNull()
    }

    @Test
    fun `a refusal stays on the sheet as a sentence and nothing is reported`() = runTest {
        viewModel.bind(CollectionTarget.Move("p1", fromCollectionId = null))
        api.refuse = "NOT_SAVED"

        viewModel.pick(recipes)

        assertThat(viewModel.state.value.outcome).isNull()
        assertThat(viewModel.state.value.error).isEqualTo("This post isn't in your saved items any more.")
        assertThat(viewModel.state.value.busy).isFalse()
    }

    @Test
    fun `a new collection is checked here, created, then picked`() = runTest {
        viewModel.bind(CollectionTarget.Save("p1", alreadySaved = false))
        viewModel.startNaming()

        viewModel.changeName("recipes")
        viewModel.createAndPick()
        assertThat(viewModel.state.value.nameProblem).isEqualTo("You already have a collection with that name.")
        assertThat(api.calls).containsExactly("list")

        viewModel.changeName("  Weekend   trips ")
        viewModel.createAndPick()
        assertThat(api.calls).containsExactly("list", "create:Weekend trips", "save:p1:c-new").inOrder()
        val outcome = requireNotNull(viewModel.state.value.outcome)
        assertThat(outcome.created).isEqualTo(SavedCollection("c-new", "Weekend trips", 0))
        assertThat(outcome.message).isEqualTo("Saved to Weekend trips")
    }

    @Test
    fun `a server refusal of the name is said under the field`() = runTest {
        viewModel.bind(CollectionTarget.Save("p1", alreadySaved = false))
        viewModel.startNaming()
        viewModel.changeName("Fresh")
        api.refuse = "COLLECTION_LIMIT_REACHED"

        viewModel.createAndPick()

        assertThat(viewModel.state.value.nameProblem).isEqualTo("You can have up to 50 collections. Delete one to make another.")
        assertThat(api.calls).containsExactly("list", "create:Fresh").inOrder()
    }
}
