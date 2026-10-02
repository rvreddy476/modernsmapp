package com.us.android.core.feed.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.model.FeedItem
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.network.listApiCall
import com.us.android.core.network.noContentApiCall
import javax.inject.Inject
import javax.inject.Singleton

/** A collection of long videos: one the viewer made, or the Watch later list the server keeps for them. */
data class VideoCollection(
    val id: String,
    val title: String,
    val itemCount: Int,
    val isPrivate: Boolean,
    /** The Watch later list: named by the app, never renamed or deleted, and written through its own routes. */
    val isWatchLater: Boolean,
)

/** A collection with what is in it, in the collection's own order. */
data class VideoCollectionContents(val collection: VideoCollection, val videos: List<FeedItem>)

/**
 * The two writes [VideoLibraryStore] performs. An interface so the store's
 * optimistic update and rollback can be tested against answers a test controls.
 */
interface VideoLibraryWrites {
    suspend fun setWatchLater(postId: String, queued: Boolean): AppResult<Unit>
    suspend fun setDisliked(postId: String, disliked: Boolean): AppResult<Unit>
}

/**
 * Network operations for the long-video library (2026-10-02), mapped into
 * [AppResult]. Thin on purpose, like `EngagementRepository`: the optimistic
 * state lives in [VideoLibraryStore].
 */
@Singleton
class VideoLibraryRepository @Inject constructor(
    private val api: VideoLibraryApi,
    private val errorMapper: ErrorMapper,
    private val hydrator: FeedItemHydrator,
) : VideoLibraryWrites {

    override suspend fun setWatchLater(postId: String, queued: Boolean): AppResult<Unit> =
        apiCall(errorMapper) {
            if (queued) api.addWatchLater(postId) else api.removeWatchLater(postId)
        }.map { }

    override suspend fun setDisliked(postId: String, disliked: Boolean): AppResult<Unit> =
        apiCall(errorMapper) {
            if (disliked) api.addDislike(postId) else api.removeDislike(postId)
        }.map { }

    /** The Watch later list and its videos: the system collection (made on first read), then its items. */
    suspend fun watchLater(): AppResult<VideoCollectionContents> =
        when (val list = apiCall(errorMapper) { api.systemPlaylist(VideoLibraryApi.KIND_WATCH_LATER) }) {
            is AppResult.Success -> contentsOf(list.data.toCollection())
            is AppResult.Failure -> list
        }

    /** One collection and its videos. */
    suspend fun collection(collectionId: String): AppResult<VideoCollectionContents> =
        when (val list = apiCall(errorMapper) { api.playlist(collectionId) }) {
            is AppResult.Success -> contentsOf(list.data.toCollection())
            is AppResult.Failure -> list
        }

    private suspend fun contentsOf(collection: VideoCollection): AppResult<VideoCollectionContents> =
        when (val rows = listApiCall(errorMapper) { api.items(collection.id) }) {
            is AppResult.Success -> AppResult.Success(
                VideoCollectionContents(collection, hydrator.hydrate(rows.data.toVideos())),
            )
            is AppResult.Failure -> rows
        }

    /** The collections [userId] made, the private ones included when it is the viewer. Never the system lists. */
    suspend fun collections(userId: String): AppResult<List<VideoCollection>> =
        listApiCall(errorMapper) { api.creatorPlaylists(userId, COLLECTIONS_LIMIT, 0) }.map { rows ->
            rows.filter { it.id.isNotBlank() }.map { it.toCollection() }.filterNot { it.isWatchLater }
        }

    /** A new, empty collection. Private unless [isPrivate] is false, as on the web's dialog. */
    suspend fun createCollection(title: String, isPrivate: Boolean): AppResult<VideoCollection> =
        apiCall(errorMapper) {
            api.createPlaylist(CreatePlaylistRequest(title.trim(), if (isPrivate) PRIVATE else PUBLIC))
        }.map { it.toCollection() }

    /**
     * Adds [postId] to a collection the viewer made.
     *
     * The position is NOT left to the server. `POST /v1/playlists/{id}/items`
     * upserts on `(playlist_id, position)`, so a position that is already
     * taken silently REPLACES the video sitting there; an absent position is
     * 0, which would overwrite the first video every time. The items are
     * read first and the video goes one past the highest position in use
     * ([nextPosition]). A video that is already in the collection is left
     * where it is and answered as a success: adding twice is not an error.
     */
    suspend fun addToCollection(collectionId: String, postId: String): AppResult<Unit> {
        val rows = when (val result = listApiCall(errorMapper) { api.items(collectionId) }) {
            is AppResult.Success -> result.data
            is AppResult.Failure -> return result
        }
        if (rows.any { it.postId == postId }) return AppResult.Success(Unit)
        return apiCall(errorMapper) {
            api.addItem(collectionId, AddPlaylistItemRequest(postId = postId, position = nextPosition(rows)))
        }.map { }
    }

    suspend fun removeFromCollection(collectionId: String, postId: String): AppResult<Unit> =
        noContentApiCall(errorMapper) { api.removeItem(collectionId, postId) }

    companion object {
        const val PRIVATE = "private"
        const val PUBLIC = "public"

        /** The web asks for 50; nobody scrolls a picker further than that. */
        private const val COLLECTIONS_LIMIT = 50

        /** Why a library write was refused, in words the screen can show. One wording for every surface. */
        fun errorMessage(error: AppError, fallback: String): String = when (error) {
            is AppError.NoNetwork -> "You're offline. Check your connection and try again."
            is AppError.Timeout -> "That took too long. Try again."
            is AppError.NotFound -> "This video is no longer available."
            else -> fallback
        }
    }
}

/** One past the highest position in use; 0 for an empty collection. Never a position a row already holds. */
internal fun nextPosition(rows: List<PlaylistItemDto>): Int = (rows.maxOfOrNull { it.position } ?: -1) + 1

/**
 * The rows as videos, in position order: a row whose post the viewer may not
 * see (`post` null), one that was deleted, and a repeated post id are dropped.
 */
internal fun List<PlaylistItemDto>.toVideos(): List<FeedItem> =
    sortedBy { it.position }
        .mapNotNull { row -> row.post?.takeIf { it.deletedAt.isBlank() && it.id.isNotBlank() } }
        .distinctBy { it.id }
        .map { it.toDomain() }

/** The system list is named by the app's vocabulary, never by the server's row title (which says "Queue"). */
internal fun PlaylistDto.toCollection(): VideoCollection {
    val watchLater = kind == VideoLibraryApi.KIND_WATCH_LATER
    return VideoCollection(
        id = id,
        title = if (watchLater) WATCH_LATER_TITLE else title.trim().ifBlank { "Untitled" },
        itemCount = itemCount.coerceAtLeast(0),
        // Anything that is not plainly public is treated as private: the label must never overstate reach.
        isPrivate = visibility != VideoLibraryRepository.PUBLIC,
        isWatchLater = watchLater,
    )
}

/** RUTUBE's words (founder, 2026-09-28). */
const val WATCH_LATER_TITLE = "Watch later"
