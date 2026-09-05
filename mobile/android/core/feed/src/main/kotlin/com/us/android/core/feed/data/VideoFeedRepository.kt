package com.us.android.core.feed.data

import androidx.paging.PagingData
import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.dto.FeedItemDto
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedQuery
import com.us.android.core.model.FeedSurface
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.Flow
import javax.inject.Inject
import javax.inject.Singleton

/**
 * One request's worth of Tube: the whole ranked surface, only followed
 * authors, or one category. The three are exclusive — the chip rail is
 * single-select — so this is a small sealed vocabulary, not two flags.
 */
sealed interface VideoFeedQuery {
    /** `/v1/feed/videos` as the server ranks it — the "All" chip and Tube home. */
    data object All : VideoFeedQuery

    /** `/v1/feed/watch?following_only=true` — the Following chip and the Subscriptions page. */
    data object Following : VideoFeedQuery

    /** `/v1/feed/videos?category=<id>` — one category chip. */
    data class Category(val id: String) : VideoFeedQuery
}

/** A category chip: the server's slug and the label it wants shown. */
data class FeedCategory(val id: String, val label: String)

/** An unfinished video: the post, and the playhead the viewer left. */
data class ContinueWatching(val item: FeedItem, val positionMs: Long, val durationMs: Long) {
    /** 0..1, for the thin progress bar under the thumbnail. Zero when the length is unknown. */
    val fraction: Float
        get() = if (durationMs <= 0L) 0f else (positionMs.toFloat() / durationMs).coerceIn(0f, 1f)
}

/**
 * Tube's reads (2026-09-05): the video surfaces with their chips, the
 * Reels panel, continue-watching, the viewer's own videos and their saved
 * videos. Paged lists share [feedPager] with every other feed, so
 * de-duplication and the cursor rules are the same everywhere.
 *
 * Every non-paged read degrades to an empty answer rather than an error:
 * a shelf that cannot load is a shelf that is absent, never a crash and
 * never a screen-wide failure — the ranked list is the page, the shelves
 * are extras.
 */
@Singleton
class VideoFeedRepository @Inject constructor(
    private val api: VideoFeedApi,
    private val feeds: FeedRepository,
    private val errorMapper: ErrorMapper,
    private val hydrator: FeedItemHydrator,
) {

    /** The paged surface for one chip. */
    fun videos(query: VideoFeedQuery): Flow<PagingData<FeedItem>> = feedPager(errorMapper) {
        api.getFeed(
            surface = query.surface.path,
            limit = it.limit,
            cursor = it.cursor,
            followingOnly = (query is VideoFeedQuery.Following).takeIf { on -> on },
            category = (query as? VideoFeedQuery.Category)?.id,
        ).toFeedPage().let { page -> page.copy(items = hydrator.hydrate(page.items)) }
    }

    /**
     * The viewer's own long videos, newest first — bare post-service rows,
     * hydrated per page. The server filters by `content_type`; the client
     * filters again and walks on past a page the filter emptied, so a
     * server that ignored the parameter would still show only videos.
     */
    fun ownVideos(userId: String): Flow<PagingData<FeedItem>> = feedPager(errorMapper) { request ->
        videosOnly(request) { limit, cursor -> api.postsByAuthor(userId, LONG_VIDEO, limit, cursor) }
    }

    /**
     * The viewer's saved long videos: the bookmark list — every kind of
     * post — narrowed to videos, page by page. A page of nothing but saved
     * photos is skipped, up to [MAX_PAGE_HOPS] in one load, so the list is
     * not empty merely because the newest bookmarks were not videos.
     */
    fun savedVideos(): Flow<PagingData<FeedItem>> = feedPager(errorMapper) { request ->
        videosOnly(request) { limit, cursor -> api.bookmarks(limit, cursor) }
    }

    /**
     * The first page of long videos from followed authors, as a list — the
     * channels strip groups it by author. Empty when nothing is followed or
     * the read fails: the strip is then the viewer's own bubble alone.
     */
    suspend fun followingVideos(limit: Int): List<FeedItem> =
        when (
            val result = apiCall(errorMapper) {
                api.getFeed(FeedSurface.Watch.path, limit, followingOnly = true)
            }
        ) {
            is AppResult.Success ->
                hydrator.hydrate(result.data.filter { it.deletedAt.isBlank() }.map { it.toDomain() })
            is AppResult.Failure -> emptyList()
        }

    /** The first page of reels for the Reels panel — [FeedQuery.Reels], as a list, not a pager. */
    suspend fun reels(limit: Int): List<FeedItem> =
        when (val result = apiCall(errorMapper) { api.getFeed(FeedQuery.Reels.surface.path, limit) }) {
            is AppResult.Success ->
                hydrator.hydrate(result.data.filter { it.deletedAt.isBlank() }.map { it.toDomain() })
            is AppResult.Failure -> emptyList()
        }

    /**
     * One author's posts of one kind, newest first — bare post-service rows,
     * hydrated per page. The profile grid's three tabs (posts, reels, videos)
     * are three of these; [ownVideos] is the long-video one with the extra
     * client-side filter Tube wants.
     */
    fun authorPosts(userId: String, contentType: String): Flow<PagingData<FeedItem>> =
        feedPager(errorMapper) { request ->
            val page = api.postsByAuthor(userId, contentType, request.limit, request.cursor).toFeedPage()
            page.copy(items = hydrator.hydrate(page.items.filter { it.feedContentType == contentType }))
        }

    /**
     * The viewer's scheduled posts (2026-09-05), soonest first, hydrated;
     * empty — not failed — when the endpoint is unavailable or the server
     * predates it, so a grid without a scheduled tile is never an error.
     */
    suspend fun scheduledPosts(limit: Int): List<FeedItem> =
        when (val result = apiCall(errorMapper) { api.scheduled(limit) }) {
            is AppResult.Success ->
                hydrator.hydrate(result.data.filter { it.deletedAt.isBlank() }.map { it.toDomain() })
            is AppResult.Failure -> emptyList()
        }

    /**
     * The first page of one author's long videos, as a list — a channel's
     * strip bubble and the You header count read it without a pager.
     */
    suspend fun latestVideos(userId: String, limit: Int): List<FeedItem> =
        when (val result = apiCall(errorMapper) { api.postsByAuthor(userId, LONG_VIDEO, limit) }) {
            is AppResult.Success ->
                hydrator.hydrate(result.data.filter { it.deletedAt.isBlank() }.map { it.toDomain() })
            is AppResult.Failure -> emptyList()
        }

    /**
     * The unfinished videos, each with its post. A row that carries its post
     * is hydrated with the others as one batch — the author, the video's
     * delivery (its `thumb_150` still) and the chosen cover, each id once;
     * a row that does not is read back by id. Rows whose post is gone
     * (deleted, hidden, a 404) are dropped, and the shelf is empty — not
     * failed — when the endpoint is unavailable.
     */
    suspend fun continueWatching(limit: Int): List<ContinueWatching> {
        val rows = when (val result = apiCall(errorMapper) { api.continueWatching(limit) }) {
            is AppResult.Success -> result.data.filter { it.postId.isNotBlank() && !it.completed }
            is AppResult.Failure -> emptyList()
        }
        if (rows.isEmpty()) return emptyList()
        return coroutineScope {
            // Keyed by the row's own post id, in the row's order: hydrate keeps order and count.
            val live = rows.filter { row -> row.post?.deletedAt?.isBlank() == true }
            val batch = async {
                live.map { it.postId }.zip(hydrator.hydrate(live.mapNotNull { it.post?.toDomain() })).toMap()
            }
            val fetched = rows.filter { it.post == null }.map { row ->
                async { row.postId to (feeds.post(row.postId) as? AppResult.Success)?.data }
            }
            val posts = batch.await() + fetched.awaitAll().toMap()
            rows.mapNotNull { row -> posts[row.postId]?.let { ContinueWatching(it, row.positionMs, row.durationMs) } }
        }
    }

    /** The category taxonomy. Empty when the endpoint fails — the rail then shows All and Following alone. */
    suspend fun categories(): List<FeedCategory> =
        when (val result = apiCall(errorMapper) { api.categories() }) {
            is AppResult.Success ->
                result.data
                    .filter { it.id.isNotBlank() }
                    .map { FeedCategory(id = it.id, label = it.label.ifBlank { it.id }) }
            is AppResult.Failure -> emptyList()
        }

    /**
     * A page of long videos from a source that may hand back other kinds
     * too: hops past pages the filter empties (bounded), hydrates what is
     * left, and carries the cursor of the last page read.
     */
    private suspend fun videosOnly(
        request: FeedPageRequest,
        load: suspend (limit: Int, cursor: String?) -> ApiEnvelope<List<FeedItemDto>>,
    ): FeedPage {
        var cursor = request.cursor
        repeat(MAX_PAGE_HOPS) {
            val page = load(request.limit, cursor).toFeedPage()
            val videos = page.items.filter { it.feedContentType == LONG_VIDEO }
            if (page.errorCode != null || videos.isNotEmpty() || page.nextCursor == null) {
                return page.copy(items = hydrator.hydrate(videos))
            }
            cursor = page.nextCursor
        }
        // Hops exhausted with nothing to show: hand Paging the cursor to keep going from.
        return FeedPage(items = emptyList(), nextCursor = cursor)
    }

    private val VideoFeedQuery.surface: FeedSurface
        get() = if (this is VideoFeedQuery.Following) FeedSurface.Watch else FeedSurface.Videos

    private companion object {
        const val LONG_VIDEO = "long_video"

        /** Pages of non-video bookmarks skipped in one load before giving Paging the cursor back. */
        const val MAX_PAGE_HOPS = 4
    }
}
