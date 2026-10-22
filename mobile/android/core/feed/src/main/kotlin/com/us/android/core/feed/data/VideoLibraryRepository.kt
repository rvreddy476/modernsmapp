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

/** Who may open a collection: post-service's three values (`playlistVisibilities`). */
enum class CollectionVisibility(val wire: String, val label: String) {
    PUBLIC("public", "Public"),
    UNLISTED("unlisted", "Unlisted"),
    PRIVATE("private", "Private"),
    ;

    companion object {
        /** Anything unknown reads as Private: a label must never overstate reach. */
        fun fromWire(value: String?): CollectionVisibility = entries.firstOrNull { it.wire == value } ?: PRIVATE
    }
}

/** A collection of long videos: one the viewer made, or a list the server keeps for them (Watch later, Liked videos). */
data class VideoCollection(
    val id: String,
    val title: String,
    val itemCount: Int,
    val isPrivate: Boolean,
    /** The Watch later list: named by the app, never renamed or deleted, and written through its own routes. */
    val isWatchLater: Boolean,
    val description: String = "",
    val visibility: CollectionVisibility = if (isPrivate) CollectionVisibility.PRIVATE else CollectionVisibility.PUBLIC,
    /** Liked videos: the server's like toggle fills it; no generic write applies (409 SYSTEM_PLAYLIST). */
    val isLiked: Boolean = false,
) {
    /** A list the server keeps: never renamed, deleted or reordered from here. */
    val isSystem: Boolean get() = isWatchLater || isLiked
}

/**
 * A collection with what is in it, in the collection's own order. [order] is
 * EVERY row's post id by position, the ones the viewer may not see included:
 * the server's move route counts them, so a reorder is planned against it.
 */
data class VideoCollectionContents(
    val collection: VideoCollection,
    val videos: List<FeedItem>,
    val order: List<String> = videos.map { it.id },
)

/** An edit of a collection's details; null leaves a field as it is. */
data class CollectionEdit(
    val title: String? = null,
    val description: String? = null,
    val visibility: CollectionVisibility? = null,
) {
    val isEmpty: Boolean get() = title == null && description == null && visibility == null
}

/**
 * What a collection's page reads and writes. A seam so the page's ViewModel
 * is tested against answers a test controls; [VideoLibraryRepository] is
 * the real one.
 */
interface VideoCollectionSource {
    suspend fun watchLater(): AppResult<VideoCollectionContents>
    suspend fun liked(): AppResult<VideoCollectionContents>
    suspend fun collection(collectionId: String): AppResult<VideoCollectionContents>
    suspend fun removeFromCollection(collectionId: String, postId: String): AppResult<Unit>
    suspend fun updateCollection(collectionId: String, edit: CollectionEdit): AppResult<VideoCollection>
    suspend fun deleteCollection(collectionId: String): AppResult<Unit>

    /** Moves [postId] to the 0-based [position]; answers every row's post id in the server's new order. */
    suspend fun moveInCollection(collectionId: String, postId: String, position: Int): AppResult<List<String>>
}

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
) : VideoLibraryWrites, VideoCollectionSource {

    override suspend fun setWatchLater(postId: String, queued: Boolean): AppResult<Unit> =
        apiCall(errorMapper) {
            if (queued) api.addWatchLater(postId) else api.removeWatchLater(postId)
        }.map { }

    override suspend fun setDisliked(postId: String, disliked: Boolean): AppResult<Unit> =
        apiCall(errorMapper) {
            if (disliked) api.addDislike(postId) else api.removeDislike(postId)
        }.map { }

    /** The Watch later list and its videos: the system collection (made on first read), then its items. */
    override suspend fun watchLater(): AppResult<VideoCollectionContents> = system(VideoLibraryApi.KIND_WATCH_LATER)

    /**
     * Liked videos (web `/posttube/loved`, 2026-10-22 on Android): the `liked`
     * system collection, then its items. Read-only from here: the server's
     * like toggle is its only writer.
     */
    override suspend fun liked(): AppResult<VideoCollectionContents> = system(VideoLibraryApi.KIND_LIKED)

    private suspend fun system(kind: String): AppResult<VideoCollectionContents> =
        // The route is the authority on the kind even if the row omits it (the web's rule).
        when (val list = apiCall(errorMapper) { api.systemPlaylist(kind) }) {
            is AppResult.Success -> contentsOf(list.data.copy(kind = kind).toCollection())
            is AppResult.Failure -> list
        }

    /** One collection and its videos. */
    override suspend fun collection(collectionId: String): AppResult<VideoCollectionContents> =
        when (val list = apiCall(errorMapper) { api.playlist(collectionId) }) {
            is AppResult.Success -> contentsOf(list.data.toCollection())
            is AppResult.Failure -> list
        }

    private suspend fun contentsOf(collection: VideoCollection): AppResult<VideoCollectionContents> =
        when (val rows = listApiCall(errorMapper) { api.items(collection.id) }) {
            is AppResult.Success -> AppResult.Success(
                VideoCollectionContents(
                    collection = collection,
                    videos = hydrator.hydrate(rows.data.toVideos()),
                    order = rows.data.toOrder(),
                ),
            )
            is AppResult.Failure -> rows
        }

    /**
     * Title, description and/or visibility of a collection the viewer made
     * (`PATCH /v1/playlists/:id`). Only what [edit] names goes on the wire; a
     * title is trimmed and a blank one is refused here, as the server would.
     */
    override suspend fun updateCollection(collectionId: String, edit: CollectionEdit): AppResult<VideoCollection> {
        val title = edit.title?.trim()
        if (title != null && title.isEmpty()) {
            return AppResult.Failure(AppError.InvalidRequest(message = "A collection needs a title."))
        }
        return apiCall(errorMapper) {
            api.updatePlaylist(
                collectionId,
                UpdatePlaylistRequest(
                    title = title,
                    description = edit.description?.trim(),
                    visibility = edit.visibility?.wire,
                ),
            )
        }.map { it.toCollection() }
    }

    /** Deletes a collection the viewer made; its videos are not touched. 204. */
    override suspend fun deleteCollection(collectionId: String): AppResult<Unit> =
        noContentApiCall(errorMapper) { api.deletePlaylist(collectionId) }

    /** The reorder: [position] is the 0-based index [postId] lands on, in every row's terms (see [VideoCollectionContents.order]). */
    override suspend fun moveInCollection(collectionId: String, postId: String, position: Int): AppResult<List<String>> =
        listApiCall(errorMapper) {
            api.moveItem(collectionId, postId, MovePlaylistItemRequest(position.coerceAtLeast(0)))
        }.map { it.toOrder() }

    /** The collections [userId] made, the private ones included when it is the viewer. Never the system lists. */
    suspend fun collections(userId: String): AppResult<List<VideoCollection>> =
        listApiCall(errorMapper) { api.creatorPlaylists(userId, COLLECTIONS_LIMIT, 0) }.map { rows ->
            rows.filter { it.id.isNotBlank() }.map { it.toCollection() }.filterNot { it.isSystem }
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

    override suspend fun removeFromCollection(collectionId: String, postId: String): AppResult<Unit> =
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

/**
 * Every row's post id in position order, hidden rows included: what the
 * server's move route counts. A repeated id keeps its first place.
 */
internal fun List<PlaylistItemDto>.toOrder(): List<String> =
    sortedBy { it.position }.map { it.postId }.filter { it.isNotBlank() }.distinct()

/** A system list is named by the app's vocabulary, never by the server's row title (which says "Queue"). */
internal fun PlaylistDto.toCollection(): VideoCollection {
    val watchLater = kind == VideoLibraryApi.KIND_WATCH_LATER
    val liked = kind == VideoLibraryApi.KIND_LIKED
    return VideoCollection(
        id = id,
        title = when {
            watchLater -> WATCH_LATER_TITLE
            liked -> LIKED_VIDEOS_TITLE
            else -> title.trim().ifBlank { "Untitled" }
        },
        itemCount = itemCount.coerceAtLeast(0),
        // Anything that is not plainly public is treated as private: the label must never overstate reach.
        isPrivate = visibility != VideoLibraryRepository.PUBLIC,
        isWatchLater = watchLater,
        description = if (watchLater || liked) "" else description.trim(),
        visibility = CollectionVisibility.fromWire(visibility),
        isLiked = liked,
    )
}

/** RUTUBE's words (founder, 2026-09-28). */
const val WATCH_LATER_TITLE = "Watch later"

/** The web's title for the `liked` system list (`SYSTEM_TITLES.liked`). */
const val LIKED_VIDEOS_TITLE = "Liked videos"
