package com.us.android.core.feed.data.saved

import androidx.paging.PagingData
import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.model.FeedItem
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import javax.inject.Inject
import javax.inject.Singleton

/** A collection was deleted: its id, and how many posts went back to All Saved. */
data class DeletedCollection(val collectionId: String, val movedToDefault: Long)

/**
 * The viewer's saved collections (2026-10-24). Every route is the caller's
 * own; another viewer's id answers like one that does not exist.
 *
 * [changes] ticks after every write that moves a post or a count — a save
 * into a collection from the feed, a move, a create, a rename, a delete — so
 * the Saved tab, wherever it is, reads its collections again.
 */
@Singleton
class SavedCollectionsRepository @Inject constructor(
    private val api: SavedCollectionsApi,
    private val engagement: EngagementStore,
    private val errorMapper: ErrorMapper,
) {
    private val _changes = MutableStateFlow(0)
    val changes: StateFlow<Int> = _changes.asStateFlow()

    private fun changed() = _changes.update { it + 1 }

    suspend fun collections(): AppResult<List<SavedCollection>> =
        apiCall(errorMapper) { api.collections() }.map { it.toCollections() }

    /** [name] is sent as typed: the server trims and folds it, and answers the stored name. */
    suspend fun create(name: String): AppResult<SavedCollection> =
        apiCall(errorMapper) { api.create(CollectionNameRequest(name)) }
            .map { it.toCollection() }
            .also { if (it is AppResult.Success) changed() }

    suspend fun rename(collectionId: String, name: String): AppResult<SavedCollection> =
        apiCall(errorMapper) { api.rename(collectionId, CollectionNameRequest(name)) }
            .map { it.toCollection() }
            .also { if (it is AppResult.Success) changed() }

    suspend fun delete(collectionId: String): AppResult<DeletedCollection> =
        apiCall(errorMapper) { api.delete(collectionId) }
            .map { DeletedCollection(it.collectionId ?: collectionId, it.movedToDefault.coerceAtLeast(0)) }
            .also { if (it is AppResult.Success) changed() }

    /**
     * "Save to collection" from a post ([saveToCollectionCall]). On success
     * the post is saved on every surface: the bookmark glyph is told through
     * [EngagementStore.confirmBookmarked], with no second request.
     */
    suspend fun saveTo(postId: String, toCollectionId: String?, alreadySaved: Boolean): AppResult<Unit> {
        val result = when (val call = saveToCollectionCall(toCollectionId, alreadySaved)) {
            is SaveToCollectionCall.SaveInto -> apiCall(errorMapper) { api.saveInto(postId, call.body) }.map { }
            is SaveToCollectionCall.MoveTo -> apiCall(errorMapper) { api.move(postId, call.body) }.map { }
        }
        if (result is AppResult.Success) {
            engagement.confirmBookmarked(postId)
            changed()
        }
        return result
    }

    /** "Move to collection" on a saved post: `PUT /v1/posts/:id/bookmark/collection`. */
    suspend fun move(postId: String, toCollectionId: String?): AppResult<Unit> =
        apiCall(errorMapper) { api.move(postId, CollectionRefRequest(collectionRef(toCollectionId))) }
            .map { }
            .also { if (it is AppResult.Success) changed() }
}

/**
 * The Saved tab's pages, one collection at a time: [collection] is "default"
 * for All Saved or a collection's id. `VideoFeedRepository.savedPosts` in the
 * app (FeedModule); a fake in a test.
 */
fun interface SavedPostsSource {
    fun savedPosts(collection: String): Flow<PagingData<FeedItem>>
}
